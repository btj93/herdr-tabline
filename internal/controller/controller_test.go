package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/config"
	"github.com/btj93/herdr-tabline/internal/controller"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/model"
	"github.com/btj93/herdr-tabline/internal/state"
)

const orderTemplate = ` {{ .Tab.Number }}: {{ .Pane.Directory }} > {{ .Process.Name }} `

type rename struct {
	tabID string
	label string
}

// TestAutoReconcileRenumbersAfterTabMove is the reorder regression guard. Native tab
// numbers stay fixed while array positions swap, so a controller that trusted
// Tab.NativeNumber would produce no second pair of writes at all.
func TestAutoReconcileRenumbersAfterTabMove(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.snapshot = snapshotInOrder("w1:t2", "w1:t1")
	client.reflectSuccessfulLabelsIntoSnapshot()
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	want := []rename{
		{"w1:t1", " 1: one > nvim "},
		{"w1:t2", " 2: two > zsh "},
		{"w1:t2", " 1: two > zsh "},
		{"w1:t1", " 2: one > nvim "},
	}
	if !reflect.DeepEqual(want, client.renames) {
		t.Fatalf("renames = %#v, want %#v", client.renames, want)
	}
}

func TestAutoReconcileSkipsTabsAlreadyCarryingTheDesiredLabel(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.reflectSuccessfulLabelsIntoSnapshot()
	before := len(client.renames)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.renames) != before {
		t.Fatalf("a settled session rewrote labels: %#v", client.renames[before:])
	}
}

// TestAutoReconcileOverwritesAManualRename pins the authoritative-auto rule: a skip is
// only allowed when the desired label equals the tab's CURRENT label. Skipping because it
// merely equals what the plugin wrote last time would let a manual rename stick.
func TestAutoReconcileOverwritesAManualRename(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.reflectSuccessfulLabelsIntoSnapshot()
	client.setLabel("w1:t1", "hand written")
	before := len(client.renames)

	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	got := client.renames[before:]
	want := []rename{{"w1:t1", " 1: one > nvim "}}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("renames = %#v, want %#v", got, want)
	}
}

// TestManualRenameBecomesTheSourceLabel proves the observed label is captured as the new
// source before the authoritative overwrite, which is what tab_label_regex matches on.
func TestManualRenameBecomesTheSourceLabel(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	store, c := newTestControllerWithStore(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.reflectSuccessfulLabelsIntoSnapshot()
	client.setLabel("w1:t1", "hand written")
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := history["w1:t1"]
	if entry.SourceLabel != "hand written" {
		t.Fatalf("source label = %q, want the manual rename", entry.SourceLabel)
	}
	if entry.RenderedLabel != " 1: one > nvim " {
		t.Fatalf("rendered label = %q, want the authoritative write", entry.RenderedLabel)
	}
}

// TestSourceLabelIsObservedWithoutWriting covers the modes where the plugin never renames.
// Observation is then the only way tab_label_regex ever sees a manual rename, so it cannot
// be a side effect of a successful write.
func TestSourceLabelIsObservedWithoutWriting(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	client.setLabel("w1:t1", "named by hand")
	store, c := newTestControllerWithStore(t, client, orderTemplate, config.ModeOff)

	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.renames) != 0 {
		t.Fatalf("off mode wrote while observing: %#v", client.renames)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if history["w1:t1"].SourceLabel != "named by hand" {
		t.Fatalf("source label = %#v, want the manual rename recorded without a write", history["w1:t1"])
	}
}

func TestKeybindModeWritesOnlyTheTargetTab(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeKeybind)

	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.renames) != 0 {
		t.Fatalf("keybind mode wrote on an auto trigger: %#v", client.renames)
	}

	if err := c.Reconcile(context.Background(), controller.TriggerAction, "w1:t2"); err != nil {
		t.Fatal(err)
	}
	want := []rename{{"w1:t2", " 2: two > zsh "}}
	if !reflect.DeepEqual(want, client.renames) {
		t.Fatalf("renames = %#v, want %#v", client.renames, want)
	}
}

func TestActionTriggerWritesTheTargetTabEvenInAutoMode(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAction, "w1:t2"); err != nil {
		t.Fatal(err)
	}
	want := []rename{{"w1:t2", " 2: two > zsh "}}
	if !reflect.DeepEqual(want, client.renames) {
		t.Fatalf("renames = %#v, want %#v", client.renames, want)
	}
}

func TestActionTriggerRequiresATargetTab(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	err := c.Reconcile(context.Background(), controller.TriggerAction, "")
	if err == nil {
		t.Fatal("an action reconciliation without a target tab succeeded")
	}
	if !strings.Contains(err.Error(), "requires a target tab id") {
		t.Fatalf("error = %v, want the explicit missing-target-tab guard", err)
	}
	if len(client.renames) != 0 {
		t.Fatalf("a targetless action still wrote: %#v", client.renames)
	}
}

func TestOffModeNeverWrites(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	c := newTestController(t, client, orderTemplate, config.ModeOff)
	for _, call := range []func() error{
		func() error { return c.Reconcile(context.Background(), controller.TriggerAuto, "") },
		func() error { return c.Reconcile(context.Background(), controller.TriggerAction, "w1:t1") },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.renames) != 0 {
		t.Fatalf("off mode wrote labels: %#v", client.renames)
	}
}

// TestPerTabProcessFailureIsIsolated proves one unreachable pane does not cost the other
// tabs their refresh — the daemon must degrade per tab, not per reconciliation.
func TestPerTabProcessFailureIsIsolated(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	client.processErrors["w1:p1"] = errors.New("pane went away")
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	want := []rename{{"w1:t2", " 2: two > zsh "}}
	if !reflect.DeepEqual(want, client.renames) {
		t.Fatalf("renames = %#v, want only the healthy tab %#v", client.renames, want)
	}
}

func TestRenameFailureIsIsolatedPerTab(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	client.renameErrors["w1:t1"] = errors.New("rename refused")
	store, c := newTestControllerWithStore(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	// The fake records only accepted renames, so the healthy tab appearing at all proves
	// the pass continued past the refused one rather than aborting on it.
	want := []rename{{"w1:t2", " 2: two > zsh "}}
	if !reflect.DeepEqual(want, client.renames) {
		t.Fatalf("renames = %#v, want the pass to continue past the failure with %#v", client.renames, want)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := history["w1:t1"]; recorded {
		t.Fatalf("a failed rename was recorded as written: %#v", history)
	}
	if history["w1:t2"].RenderedLabel != " 2: two > zsh " {
		t.Fatalf("the successful rename was not recorded: %#v", history)
	}
}

func TestRenderFailureIsIsolatedPerTab(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	// Ranging over a string is not permitted by text/template, so this template compiles
	// but fails at execution time for every tab that reaches it.
	c := newTestController(t, client, `{{ range .Tab.ID }}{{ . }}{{ end }}`, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.renames) != 0 {
		t.Fatalf("a failing render still wrote: %#v", client.renames)
	}
}

func TestEmptyRenderedLabelIsRejected(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	// The tabs must already carry a label, otherwise an empty render equals the current
	// empty label and would be skipped for that reason rather than rejected as empty.
	client.setLabel("w1:t1", "existing one")
	client.setLabel("w1:t2", "existing two")
	c := newTestController(t, client, `{{ if false }}never{{ end }}`, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.renames) != 0 {
		t.Fatalf("an empty label was written: %#v", client.renames)
	}
}

func TestClosedTabsArePurgedFromState(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	store, c := newTestControllerWithStore(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.reflectSuccessfulLabelsIntoSnapshot()
	if history, err := store.Load(); err != nil {
		t.Fatal(err)
	} else if len(history) != 2 {
		t.Fatalf("expected two recorded tabs, got %#v", history)
	}

	client.snapshot = snapshotInOrder("w1:t2")
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := history["w1:t1"]; present {
		t.Fatalf("closed tab survived in state: %#v", history)
	}
	if _, present := history["w1:t2"]; !present {
		t.Fatalf("open tab was purged: %#v", history)
	}
}

// TestReconcileReloadsHistoryEveryPass guards against a startup-only cache: state written
// by another process between passes must be visible to the next one.
func TestReconcileReloadsHistoryEveryPass(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	store, c := newTestControllerWithStore(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.reflectSuccessfulLabelsIntoSnapshot()

	// Another process (rename-current) records a different source label for w1:t1.
	if _, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		entry := tabs["w1:t1"]
		entry.SourceLabel = "from another process"
		tabs["w1:t1"] = entry
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if history["w1:t1"].SourceLabel != "from another process" {
		t.Fatalf("out-of-process source label was clobbered: %#v", history["w1:t1"])
	}
}

func TestUnchangedHistoryPerformsNoStateWrite(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	store, c := newTestControllerWithStore(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	client.reflectSuccessfulLabelsIntoSnapshot()
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a settled reconciliation rewrote the state file")
	}
}

// TestSelectedProcessIsTheFirstForegroundRecord is the first-record regression guard. The
// recorded Codex fixture's process-group leader is `node`; selecting by PGID would render
// "> node " and silently regress every agent label.
func TestSelectedProcessIsTheFirstForegroundRecord(t *testing.T) {
	info := loadProcessFixture(t, "process_info_codex.json")
	if len(info.ForegroundProcesses) < 2 {
		t.Fatalf("fixture needs at least two foreground processes, got %d", len(info.ForegroundProcesses))
	}
	if info.ForegroundProcessGroupID == nil {
		t.Fatal("fixture needs a foreground process group id to make this test meaningful")
	}
	first := info.ForegroundProcesses[0]
	if first.Name != "codex" {
		t.Fatalf("fixture's first foreground process = %q, want codex", first.Name)
	}

	client := newFakeClient(snapshotFocusingPane("w5:t5", info.PaneID, first.Cwd))
	client.processInfo[info.PaneID] = info
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.renames) != 1 {
		t.Fatalf("renames = %#v, want exactly one", client.renames)
	}
	label := client.renames[0].label
	if !strings.HasSuffix(label, "> codex ") {
		t.Fatalf("label = %q, want it to end in \"> codex \"", label)
	}
	if strings.Contains(label, "> node ") {
		t.Fatalf("label = %q selected the process-group leader", label)
	}
	if want := " 1: " + filepath.Base(first.Cwd) + " > codex "; label != want {
		t.Fatalf("label = %q, want %q", label, want)
	}
}

// TestSplitTabDescribesTheLayoutFocusedPane covers the common case of a tab whose panes
// are all globally unfocused: the layout, not Pane.Focused, decides which pane is described.
func TestSplitTabDescribesTheLayoutFocusedPane(t *testing.T) {
	snapshot := snapshotInOrder("w1:t1")
	snapshot.Panes = append(snapshot.Panes, herdrapi.Pane{
		PaneID: "w1:p1b", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/work/second", Focused: false,
	})
	snapshot.Tabs[0].PaneCount = 2
	snapshot.Layouts[0].FocusedPaneID = "w1:p1b"
	snapshot.Layouts[0].Panes = []herdrapi.LayoutPane{
		{PaneID: "w1:p1", Focused: false},
		{PaneID: "w1:p1b", Focused: true},
	}

	client := newFakeClient(snapshot)
	client.processInfo["w1:p1b"] = processInfoNamed("w1:p1b", "htop", "/work/second")
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	if err := c.Reconcile(context.Background(), controller.TriggerAuto, ""); err != nil {
		t.Fatal(err)
	}
	want := []rename{{"w1:t1", " 1: second > htop "}}
	if !reflect.DeepEqual(want, client.renames) {
		t.Fatalf("renames = %#v, want the layout-focused pane %#v", client.renames, want)
	}
}

func TestSnapshotFailureAbortsTheWholePass(t *testing.T) {
	client := newFakeClient(snapshotInOrder("w1:t1", "w1:t2"))
	client.snapshotError = errors.New("socket is gone")
	c := newTestController(t, client, orderTemplate, config.ModeAuto)
	err := c.Reconcile(context.Background(), controller.TriggerAuto, "")
	if err == nil {
		t.Fatal("a failed snapshot was treated as success")
	}
	if !strings.Contains(err.Error(), "socket is gone") {
		t.Fatalf("error = %v, want the transport failure", err)
	}
	if len(client.renames) != 0 {
		t.Fatalf("labels were written from a failed snapshot: %#v", client.renames)
	}
}

// ---- fakes -------------------------------------------------------------------------

type fakeClient struct {
	mu sync.Mutex

	snapshot      herdrapi.Snapshot
	snapshotError error
	processInfo   map[string]herdrapi.ProcessInfo
	processErrors map[string]error
	renameErrors  map[string]error

	renames       []rename
	snapshotCalls int
	events        chan herdrapi.Event
	subscribeErr  error
	subscriptions [][]string
}

func newFakeClient(snapshot herdrapi.Snapshot) *fakeClient {
	client := &fakeClient{
		snapshot:      snapshot,
		processInfo:   map[string]herdrapi.ProcessInfo{},
		processErrors: map[string]error{},
		renameErrors:  map[string]error{},
	}
	for _, pane := range snapshot.Panes {
		client.processInfo[pane.PaneID] = processInfoNamed(pane.PaneID, defaultProcessFor(pane.PaneID), pane.Cwd)
	}
	return client
}

func (c *fakeClient) Snapshot(context.Context) (herdrapi.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshotCalls++
	if c.snapshotError != nil {
		return herdrapi.Snapshot{}, c.snapshotError
	}
	return c.snapshot, nil
}

func (c *fakeClient) ProcessInfo(_ context.Context, paneID string) (herdrapi.ProcessInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.processErrors[paneID]; err != nil {
		return herdrapi.ProcessInfo{}, err
	}
	info, ok := c.processInfo[paneID]
	if !ok {
		return herdrapi.ProcessInfo{}, errors.New("no process info for " + paneID)
	}
	return info, nil
}

func (c *fakeClient) RenameTab(_ context.Context, tabID, label string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.renameErrors[tabID]; err != nil {
		return err
	}
	c.renames = append(c.renames, rename{tabID: tabID, label: label})
	return nil
}

func (c *fakeClient) Subscribe(context.Context, []string) (<-chan herdrapi.Event, error) {
	return nil, errors.New("not used by controller tests")
}

func (c *fakeClient) Close() error { return nil }

// reflectSuccessfulLabelsIntoSnapshot makes the fake server report the labels it accepted,
// which is what a real snapshot would show on the next pass.
func (c *fakeClient) reflectSuccessfulLabelsIntoSnapshot() {
	c.mu.Lock()
	defer c.mu.Unlock()
	latest := map[string]string{}
	for _, entry := range c.renames {
		latest[entry.tabID] = entry.label
	}
	for index := range c.snapshot.Tabs {
		if label, ok := latest[c.snapshot.Tabs[index].TabID]; ok {
			c.snapshot.Tabs[index].Label = label
		}
	}
}

func (c *fakeClient) setLabel(tabID, label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := range c.snapshot.Tabs {
		if c.snapshot.Tabs[index].TabID == tabID {
			c.snapshot.Tabs[index].Label = label
		}
	}
}

// ---- snapshot builders -------------------------------------------------------------

// snapshotInOrder builds tabs whose native numbers stay bound to their identity while
// their array positions follow the argument order, so a reorder changes position without
// changing Tab.Number as reported by the server.
func snapshotInOrder(tabIDs ...string) herdrapi.Snapshot {
	native := map[string]int{"w1:t1": 1, "w1:t2": 2, "w1:t3": 3}
	snapshot := herdrapi.Snapshot{
		Workspaces:         []herdrapi.Workspace{{WorkspaceID: "w1", Number: 1, Label: "main", Focused: true, TabCount: len(tabIDs)}},
		FocusedWorkspaceID: "w1",
	}
	for _, tabID := range tabIDs {
		paneID := paneFor(tabID)
		snapshot.Tabs = append(snapshot.Tabs, herdrapi.Tab{
			TabID: tabID, WorkspaceID: "w1", Number: native[tabID], Label: "", PaneCount: 1,
		})
		snapshot.Panes = append(snapshot.Panes, herdrapi.Pane{
			PaneID: paneID, TabID: tabID, WorkspaceID: "w1", Cwd: cwdFor(tabID), Focused: false,
		})
		snapshot.Layouts = append(snapshot.Layouts, herdrapi.Layout{
			TabID: tabID, WorkspaceID: "w1", FocusedPaneID: paneID,
			Panes: []herdrapi.LayoutPane{{PaneID: paneID, Focused: false}},
		})
	}
	return snapshot
}

func snapshotFocusingPane(tabID, paneID, cwd string) herdrapi.Snapshot {
	return herdrapi.Snapshot{
		Workspaces:         []herdrapi.Workspace{{WorkspaceID: "w5", Number: 1, Label: "main", Focused: true, TabCount: 1}},
		FocusedWorkspaceID: "w5",
		Tabs:               []herdrapi.Tab{{TabID: tabID, WorkspaceID: "w5", Number: 1, PaneCount: 1}},
		Panes:              []herdrapi.Pane{{PaneID: paneID, TabID: tabID, WorkspaceID: "w5", Cwd: cwd}},
		Layouts: []herdrapi.Layout{{
			TabID: tabID, WorkspaceID: "w5", FocusedPaneID: paneID,
			Panes: []herdrapi.LayoutPane{{PaneID: paneID, Focused: false}},
		}},
	}
}

func paneFor(tabID string) string { return strings.Replace(tabID, ":t", ":p", 1) }
func cwdFor(tabID string) string  { return "/work/" + nameFor(tabID) }
func nameFor(tabID string) string {
	switch tabID {
	case "w1:t1":
		return "one"
	case "w1:t2":
		return "two"
	default:
		return "three"
	}
}

func defaultProcessFor(paneID string) string {
	switch paneID {
	case "w1:p1":
		return "nvim"
	case "w1:p2":
		return "zsh"
	default:
		return "bash"
	}
}

func processInfoNamed(paneID, name, cwd string) herdrapi.ProcessInfo {
	shell := 100
	return herdrapi.ProcessInfo{
		PaneID:              paneID,
		ForegroundProcesses: []herdrapi.Process{{PID: 200, Name: name, Argv0: name, Cwd: cwd}},
		ShellPID:            &shell,
		TTY:                 "/dev/ttys000",
	}
}

func loadProcessFixture(t *testing.T, name string) herdrapi.ProcessInfo {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "herdrapi", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var info herdrapi.ProcessInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

// ---- controller construction --------------------------------------------------------

func newTestController(t *testing.T, client *fakeClient, template string, mode config.Mode) *controller.Controller {
	t.Helper()
	_, c := newTestControllerWithStore(t, client, template, mode)
	return c
}

func newTestControllerWithStore(t *testing.T, client *fakeClient, template string, mode config.Mode) (*state.Store, *controller.Controller) {
	t.Helper()
	root := t.TempDir()
	store := state.NewStore(root, filepath.Join(root, "herdr.sock"))
	return store, controller.New(controller.Options{
		Client: client,
		Config: newStaticConfig(t, template, mode),
		Store:  store,
		Now:    func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) },
		Logger: testLogger(t),
	})
}

// newStaticConfig writes a real configuration file and loads it through the production
// manager, so these tests exercise the same compile and resolve path the daemon uses.
func newStaticConfig(t *testing.T, template string, mode config.Mode) *config.Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "schema_version = 1\nmode = '" + string(mode) + "'\ntemplate = '" + template + "'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := config.NewManager(path)
	if _, err := manager.ReloadIfChanged(); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Current(); !ok {
		t.Fatal("test configuration did not compile")
	}
	return manager
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}
