package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/btj93/herdr-tabline/internal/cli"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/state"
)

const validConfig = `schema_version = 1
mode = "auto"
template = ' {{ .Tab.Number }}: {{ .Pane.Directory }} > {{ .Process.Name }} '
`

func TestVersionPrintsTheExactReleaseLine(t *testing.T) {
	h := newHarness(t)
	if code := h.run("version"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if got := h.stdout.String(); got != "herdr-tabline 0.1.0 schema 1\n" {
		t.Fatalf("stdout = %q, want the exact release line", got)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "unknown command", args: []string{"frobnicate"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			if code := h.run(test.args...); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if h.stderr.Len() == 0 {
				t.Fatal("no diagnostic was written to stderr")
			}
			if h.stdout.Len() != 0 {
				t.Fatalf("usage error wrote to stdout: %q", h.stdout.String())
			}
		})
	}
}

func TestValidateConfigSucceedsWithoutASocket(t *testing.T) {
	h := newHarness(t)
	h.env.SocketPath = ""
	if code := h.run("validate-config"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "ok") {
		t.Fatalf("stdout = %q, want a success line", h.stdout.String())
	}
}

func TestInvalidConfigExitsTwo(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("schema_version = 1\npoll_interval = \"not-a-duration\"\n")
	code := h.run("validate-config")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for an invalid configuration", code)
	}
	if h.stderr.Len() == 0 {
		t.Fatal("invalid config produced no diagnostic")
	}
}

// TestInvalidConfigExitsTwoForEveryConfigConsumer covers the commands that load config on
// a path other than validate-config; a config fault must stay a usage error there too.
func TestInvalidConfigExitsTwoForEveryConfigConsumer(t *testing.T) {
	for _, command := range []string{"validate-config", "preview", "rename-current"} {
		t.Run(command, func(t *testing.T) {
			h := newHarness(t)
			h.writeConfig("schema_version = 1\npoll_interval = \"not-a-duration\"\n")
			if code := h.run(command); code != 2 {
				t.Fatalf("%s exit = %d, want 2 for an invalid configuration (stderr: %s)", command, code, h.stderr.String())
			}
		})
	}
}

func TestRuntimeTransportFailureExitsOne(t *testing.T) {
	h := newHarness(t)
	h.client.snapshotErr = errors.New("socket is gone")
	code := h.run("rename-current")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for a transport failure", code)
	}
	if !strings.Contains(h.stderr.String(), "socket is gone") {
		t.Fatalf("stderr = %q, want the transport failure", h.stderr.String())
	}
}

func TestPreviewPrintsProfileContextAndQuotedLabelWithoutRenaming(t *testing.T) {
	h := newHarness(t)
	if code := h.run("preview"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"profile:", "default", "tab:", "w1:t1", `" 1: work > nvim "`} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview output %q is missing %q", out, want)
		}
	}
	if len(h.client.renames) != 0 {
		t.Fatalf("preview renamed tabs: %#v", h.client.renames)
	}
}

func TestRenameCurrentWritesOnlyTheResolvedTab(t *testing.T) {
	h := newHarness(t)
	if code := h.run("rename-current"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if len(h.client.renames) != 1 || h.client.renames[0].tabID != "w1:t1" {
		t.Fatalf("renames = %#v, want exactly the resolved tab", h.client.renames)
	}
}

// ---- the four documented tab-resolution states ----------------------------------------

func TestTabResolutionPrefersTheEnvironmentVariable(t *testing.T) {
	h := newHarness(t)
	h.env.TabID = "w1:t2"
	h.env.ContextJSON = `{"tab_id":"w1:t3"}`
	h.client.snapshot.FocusedTabID = "w1:t1"
	if code := h.run("rename-current"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if h.client.renames[0].tabID != "w1:t2" {
		t.Fatalf("resolved %q, want HERDR_TAB_ID to win", h.client.renames[0].tabID)
	}
}

func TestTabResolutionFallsBackToContextJSON(t *testing.T) {
	h := newHarness(t)
	h.env.TabID = ""
	h.env.ContextJSON = `{"tab_id":"w1:t3"}`
	h.client.snapshot.FocusedTabID = "w1:t1"
	if code := h.run("rename-current"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if h.client.renames[0].tabID != "w1:t3" {
		t.Fatalf("resolved %q, want the context JSON tab", h.client.renames[0].tabID)
	}
}

// TestTabResolutionFallsBackToFocusedTab covers an invocation that did not originate from a
// tab. PluginInvocationContext.tab_id is nullable upstream, so failing here would make
// keybind mode unusable from a global binding.
func TestTabResolutionFallsBackToFocusedTab(t *testing.T) {
	h := newHarness(t)
	h.env.TabID = ""
	h.env.ContextJSON = ""
	h.client.snapshot.FocusedTabID = "w1:t1"
	if code := h.run("rename-current"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if h.client.renames[0].tabID != "w1:t1" {
		t.Fatalf("resolved %q, want the snapshot's focused tab", h.client.renames[0].tabID)
	}
}

func TestTabResolutionFailureNamesTheVariables(t *testing.T) {
	h := newHarness(t)
	h.env.TabID = ""
	h.env.ContextJSON = ""
	h.client.snapshot.FocusedTabID = ""
	code := h.run("rename-current")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 when no tab can be resolved", code)
	}
	if !strings.Contains(h.stderr.String(), "HERDR_TAB_ID") {
		t.Fatalf("stderr = %q, want it to name HERDR_TAB_ID", h.stderr.String())
	}
}

// TestUnparseableContextJSONIsSkippedNotFatal protects against a future Herdr schema
// addition breaking the command outright.
func TestUnparseableContextJSONIsSkippedNotFatal(t *testing.T) {
	h := newHarness(t)
	h.env.TabID = ""
	h.env.ContextJSON = "{not json"
	h.client.snapshot.FocusedTabID = "w1:t1"
	if code := h.run("rename-current"); code != 0 {
		t.Fatalf("exit = %d, want the snapshot fallback to be used (stderr: %s)", code, h.stderr.String())
	}
	if h.client.renames[0].tabID != "w1:t1" {
		t.Fatalf("resolved %q, want the focused tab", h.client.renames[0].tabID)
	}
}

// ---- missing environment ---------------------------------------------------------------

func TestCommandsNameTheMissingEnvironmentVariable(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blank   func(*cli.Env)
		wants   string
	}{
		{name: "preview without socket", command: "preview", blank: func(e *cli.Env) { e.SocketPath = "" }, wants: "HERDR_SOCKET_PATH"},
		{name: "rename without socket", command: "rename-current", blank: func(e *cli.Env) { e.SocketPath = "" }, wants: "HERDR_SOCKET_PATH"},
		{name: "stop without state dir", command: "stop", blank: func(e *cli.Env) { e.PluginStateDir = "" }, wants: "HERDR_PLUGIN_STATE_DIR"},
		{name: "start without state dir", command: "start", blank: func(e *cli.Env) { e.PluginStateDir = "" }, wants: "HERDR_PLUGIN_STATE_DIR"},
		{name: "validate without config dir", command: "validate-config", blank: func(e *cli.Env) { e.PluginConfigDir = "" }, wants: "HERDR_PLUGIN_CONFIG_DIR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			test.blank(&h.env)
			if code := h.run(test.command); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(h.stderr.String(), test.wants) {
				t.Fatalf("stderr = %q, want it to name %s", h.stderr.String(), test.wants)
			}
		})
	}
}

// ---- lifecycle ---------------------------------------------------------------------------

func TestStartSpawnsDetachedDaemonAgainstTheSessionLog(t *testing.T) {
	h := newHarness(t)
	if code := h.run("start"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if len(h.spawns) != 1 {
		t.Fatalf("spawns = %#v, want exactly one", h.spawns)
	}
	spawn := h.spawns[0]
	if spawn.executable != h.env.Executable {
		t.Fatalf("spawned %q, want the plugin's own executable", spawn.executable)
	}
	if len(spawn.args) != 1 || spawn.args[0] != "daemon" {
		t.Fatalf("spawn args = %#v, want [daemon]", spawn.args)
	}
	wantLog := state.NewPaths(h.env.PluginStateDir, h.env.SocketPath).Log
	if spawn.logPath != wantLog {
		t.Fatalf("log = %q, want the session log %q", spawn.logPath, wantLog)
	}
}

func TestStopReportsWhenNoDaemonIsRunning(t *testing.T) {
	h := newHarness(t)
	if code := h.run("stop"); code != 0 {
		t.Fatalf("exit = %d, want 0 when there is nothing to stop (stderr: %s)", code, h.stderr.String())
	}
	if h.stdout.Len() == 0 {
		t.Fatal("stop said nothing about the absent daemon")
	}
}

// ---- harness -------------------------------------------------------------------------

type recordedRename struct{ tabID, label string }

type recordedSpawn struct {
	executable string
	args       []string
	logPath    string
}

type harness struct {
	t      *testing.T
	env    cli.Env
	client *stubClient
	spawns []recordedSpawn
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	stateDir := filepath.Join(root, "state")
	for _, dir := range []string{configDir, stateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{
		t: t,
		env: cli.Env{
			SocketPath:      filepath.Join(root, "herdr.sock"),
			PluginConfigDir: configDir,
			PluginStateDir:  stateDir,
			TabID:           "w1:t1",
			Executable:      filepath.Join(root, "herdr-tabline"),
		},
		client: newStubClient(),
	}
	h.writeConfig(validConfig)
	return h
}

func (h *harness) writeConfig(body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.env.PluginConfigDir, "config.toml"), []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) run(args ...string) int {
	h.t.Helper()
	return cli.Run(args, h.env, strings.NewReader(""), &h.stdout, &h.stderr, cli.Deps{
		NewClient: func(string) herdrapi.Client { return h.client },
		Spawn: func(executable string, args []string, logPath string) error {
			h.spawns = append(h.spawns, recordedSpawn{executable: executable, args: args, logPath: logPath})
			return nil
		},
	})
}

type stubClient struct {
	mu          sync.Mutex
	snapshot    herdrapi.Snapshot
	snapshotErr error
	renames     []recordedRename
}

func newStubClient() *stubClient {
	// Three tabs, so the resolution-precedence tests can name a tab that is not the
	// focused one and still find it in the snapshot.
	snapshot := herdrapi.Snapshot{
		Workspaces:         []herdrapi.Workspace{{WorkspaceID: "w1", Number: 1, Label: "main", Focused: true, TabCount: 3}},
		FocusedWorkspaceID: "w1",
		FocusedTabID:       "w1:t1",
	}
	for _, tab := range []struct{ id, pane, cwd string }{
		{"w1:t1", "w1:p1", "/home/me/work"},
		{"w1:t2", "w1:p2", "/home/me/second"},
		{"w1:t3", "w1:p3", "/home/me/third"},
	} {
		snapshot.Tabs = append(snapshot.Tabs, herdrapi.Tab{TabID: tab.id, WorkspaceID: "w1", Number: len(snapshot.Tabs) + 1, PaneCount: 1})
		snapshot.Panes = append(snapshot.Panes, herdrapi.Pane{PaneID: tab.pane, TabID: tab.id, WorkspaceID: "w1", Cwd: tab.cwd})
		snapshot.Layouts = append(snapshot.Layouts, herdrapi.Layout{TabID: tab.id, WorkspaceID: "w1", FocusedPaneID: tab.pane})
	}
	return &stubClient{snapshot: snapshot}
}

func (c *stubClient) Snapshot(context.Context) (herdrapi.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshotErr != nil {
		return herdrapi.Snapshot{}, c.snapshotErr
	}
	return c.snapshot, nil
}

func (c *stubClient) ProcessInfo(_ context.Context, paneID string) (herdrapi.ProcessInfo, error) {
	shell := 100
	return herdrapi.ProcessInfo{
		PaneID:              paneID,
		ForegroundProcesses: []herdrapi.Process{{PID: 200, Name: "nvim", Argv0: "nvim", Cwd: "/home/me/work"}},
		ShellPID:            &shell,
	}, nil
}

func (c *stubClient) RenameTab(_ context.Context, tabID, label string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renames = append(c.renames, recordedRename{tabID: tabID, label: label})
	return nil
}

func (c *stubClient) Subscribe(context.Context, []string) (<-chan herdrapi.Event, error) {
	return nil, errors.New("not used by CLI tests")
}

func (c *stubClient) Close() error { return nil }

// ---- manifest ---------------------------------------------------------------------------

// TestManifestDeclaresOnlyValidContexts guards against a typo shipping as a silently
// ignored context. Herdr's PluginActionContext values are the five below; anything else is
// dropped without warning, so the action would simply never appear in that menu.
func TestManifestDeclaresOnlyValidContexts(t *testing.T) {
	manifest := loadManifest(t)
	valid := map[string]bool{"global": true, "workspace": true, "tab": true, "pane": true, "selection": true}
	if len(manifest.Actions) == 0 {
		t.Fatal("manifest declares no actions")
	}
	for _, action := range manifest.Actions {
		if len(action.Contexts) == 0 {
			t.Fatalf("action %q declares no contexts", action.ID)
		}
		for _, context := range action.Contexts {
			if !valid[context] {
				t.Fatalf("action %q declares invalid context %q", action.ID, context)
			}
		}
	}
}

// TestManifestExposesExactlyTheUserFacingCommands pins the boundary between the CLI and the
// manifest: daemon and version are internal and must never appear as invocable actions.
func TestManifestExposesExactlyTheUserFacingCommands(t *testing.T) {
	manifest := loadManifest(t)
	got := map[string]bool{}
	for _, action := range manifest.Actions {
		got[action.ID] = true
	}
	for _, want := range []string{"start", "stop", "rename-current", "validate-config", "preview"} {
		if !got[want] {
			t.Fatalf("manifest is missing the %q action", want)
		}
		delete(got, want)
	}
	for leftover := range got {
		t.Fatalf("manifest exposes unexpected action %q", leftover)
	}
	for _, internal := range []string{"daemon", "version"} {
		for _, action := range manifest.Actions {
			if action.ID == internal {
				t.Fatalf("internal command %q is exposed as a manifest action", internal)
			}
		}
	}
}

func TestManifestActionsInvokeTheBuiltBinary(t *testing.T) {
	manifest := loadManifest(t)
	for _, action := range manifest.Actions {
		if len(action.Command) < 2 {
			t.Fatalf("action %q has command %#v", action.ID, action.Command)
		}
		if action.Command[0] != "./bin/herdr-tabline" {
			t.Fatalf("action %q invokes %q, want the built binary", action.ID, action.Command[0])
		}
		if action.Command[1] != action.ID {
			t.Fatalf("action %q invokes subcommand %q; the ids must match", action.ID, action.Command[1])
		}
	}
}

// TestExampleConfigIsValid keeps the shipped example honest: it is the first thing a user
// copies, so it must compile through the same loader the daemon uses.
func TestExampleConfigIsValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.writeConfig(string(raw))
	if code := h.run("validate-config"); code != 0 {
		t.Fatalf("config.example.toml is invalid (exit %d): %s", code, h.stderr.String())
	}
}

type pluginManifest struct {
	ID      string `toml:"id"`
	Version string `toml:"version"`
	Actions []struct {
		ID       string   `toml:"id"`
		Title    string   `toml:"title"`
		Contexts []string `toml:"contexts"`
		Command  []string `toml:"command"`
	} `toml:"actions"`
}

func loadManifest(t *testing.T) pluginManifest {
	t.Helper()
	var manifest pluginManifest
	if _, err := toml.DecodeFile(filepath.Join("..", "..", "herdr-plugin.toml"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "herdr-tabline" {
		t.Fatalf("manifest id = %q", manifest.ID)
	}
	if manifest.Version != cli.Version {
		t.Fatalf("manifest version %q does not match cli.Version %q", manifest.Version, cli.Version)
	}
	return manifest
}
