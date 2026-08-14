package collector_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/collector"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/model"
)

func TestBuildDerivesTabNumberFromCurrentWorkspaceOrder(t *testing.T) {
	shot := herdrapi.Snapshot{
		Workspaces: []herdrapi.Workspace{{WorkspaceID: "w1", Number: 4, Label: "repo"}},
		Tabs: []herdrapi.Tab{
			{TabID: "w1:t9", WorkspaceID: "w1", Number: 9, Label: "second"},
			{TabID: "w1:t3", WorkspaceID: "w1", Number: 3, Label: "first"},
		},
		Panes: []herdrapi.Pane{
			{PaneID: "w1:p9", TabID: "w1:t9", Cwd: "/repo/two"},
			{PaneID: "w1:p3", TabID: "w1:t3", Cwd: "/repo/one"},
		},
		Layouts: []herdrapi.Layout{
			{TabID: "w1:t9", WorkspaceID: "w1", FocusedPaneID: "w1:p9"},
			{TabID: "w1:t3", WorkspaceID: "w1", FocusedPaneID: "w1:p3"},
		},
	}

	got := collector.Build(shot, nil, time.Unix(1, 0))
	if got[0].Tab.Index != 0 || got[0].Tab.Number != 1 || got[0].Tab.NativeNumber != 9 {
		t.Fatalf("first tab numbering = %#v", got[0].Tab)
	}
	if got[1].Tab.Index != 1 || got[1].Tab.Number != 2 || got[1].Tab.NativeNumber != 3 {
		t.Fatalf("second tab numbering = %#v", got[1].Tab)
	}
	if got[0].Workspace.Number != 1 || got[0].Workspace.NativeNumber != 4 {
		t.Fatalf("workspace numbering = %#v", got[0].Workspace)
	}
}

func TestBuildSelectsLayoutFocusedPaneWhenNoPaneIsGloballyFocused(t *testing.T) {
	shot := herdrapi.Snapshot{
		Workspaces: []herdrapi.Workspace{{WorkspaceID: "w1"}},
		Tabs:       []herdrapi.Tab{{TabID: "w1:t1", WorkspaceID: "w1"}},
		Panes: []herdrapi.Pane{
			{PaneID: "w1:p1", TabID: "w1:t1", Cwd: "/repo/first", Focused: false},
			{PaneID: "w1:p2", TabID: "w1:t1", Cwd: "/repo/second", Focused: false},
		},
		Layouts: []herdrapi.Layout{{
			TabID: "w1:t1", WorkspaceID: "w1", FocusedPaneID: "w1:p2",
			Panes: []herdrapi.LayoutPane{
				{PaneID: "w1:p1", Focused: false},
				{PaneID: "w1:p2", Focused: true},
			},
		}},
	}

	got := collector.Build(shot, nil, time.Unix(1, 0))
	if got[0].Pane.ID != "w1:p2" {
		t.Fatalf("selected pane = %q, want the tab's layout-focused pane", got[0].Pane.ID)
	}
	if !got[0].Pane.FocusedInTab || got[0].Pane.Focused {
		t.Fatalf("focus flags = %#v", got[0].Pane)
	}
	if got[0].Panes[0].ID != "w1:p1" {
		t.Fatalf(".Panes must stay in snapshot order, got %q first", got[0].Panes[0].ID)
	}
}

func TestBuildUsesLayoutFocusForEveryRecordedTab(t *testing.T) {
	shot := readSnapshot(t, "snapshot.json")
	contexts := collector.Build(shot, nil, time.Unix(1, 0))
	if len(contexts) != len(shot.Tabs) {
		t.Fatalf("context count = %d, want one for each of %d snapshot tabs", len(contexts), len(shot.Tabs))
	}
	byTabID := make(map[string]model.Context, len(contexts))
	for _, ctx := range contexts {
		byTabID[ctx.Tab.ID] = ctx
	}
	for _, layout := range shot.Layouts {
		ctx, ok := byTabID[layout.TabID]
		if !ok {
			t.Fatalf("layout tab %s has no context", layout.TabID)
		}
		if ctx.Pane.ID != layout.FocusedPaneID {
			t.Fatalf("tab %s selected pane %s, want layout focused pane %s", layout.TabID, ctx.Pane.ID, layout.FocusedPaneID)
		}
	}
}

func TestBuildRestartsNumbersAndFallsBackToFirstPaneWithoutLayout(t *testing.T) {
	shot := herdrapi.Snapshot{
		Workspaces: []herdrapi.Workspace{{WorkspaceID: "one", Number: 8}, {WorkspaceID: "two", Number: 2}},
		Tabs:       []herdrapi.Tab{{TabID: "one:t1", WorkspaceID: "one"}, {TabID: "one:t2", WorkspaceID: "one"}, {TabID: "two:t1", WorkspaceID: "two"}},
		Panes:      []herdrapi.Pane{{PaneID: "one:p1", TabID: "one:t1"}, {PaneID: "one:p2", TabID: "one:t1"}, {PaneID: "one:p3", TabID: "one:t2"}, {PaneID: "two:p1", TabID: "two:t1"}},
	}
	got := collector.Build(shot, nil, time.Unix(1, 0))
	if got[0].Tab.Number != 1 || got[1].Tab.Number != 2 || got[2].Tab.Number != 1 {
		t.Fatalf("tab numbers = %d, %d, %d", got[0].Tab.Number, got[1].Tab.Number, got[2].Tab.Number)
	}
	if got[0].Workspace.Number != 1 || got[2].Workspace.Number != 2 {
		t.Fatalf("workspace numbers = %d, %d", got[0].Workspace.Number, got[2].Workspace.Number)
	}
	if got[0].Pane.ID != "one:p1" || got[0].Pane.FocusedInTab {
		t.Fatalf("no-layout selection = %#v", got[0].Pane)
	}
}

func TestBuildNormalizesFullContextAndHistory(t *testing.T) {
	shot := snapshotFromJSON(t, `{
		"workspaces":[{"workspace_id":"w1","number":9,"label":"workspace","focused":true,"tab_count":1,"pane_count":1,"active_tab_id":"w1:t1","agent_status":"working","tokens":{"input":42},"worktree":{"repo_key":"key","repo_name":"repo","repo_root":"/repo","checkout_path":"/checkout","is_linked_worktree":true}}],
		"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","number":7,"label":"rendered","focused":true,"pane_count":1}],
		"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","label":"pane label","title":"pane title","focused":true,"cwd":"/fallback","foreground_cwd":"/preferred/path","terminal_id":"terminal","terminal_title":"terminal title","terminal_title_stripped":"stripped title","agent":"codex","display_agent":"Codex","agent_status":"working","state_labels":["busy"],"tokens":{"output":8}}],
		"layouts":[{"tab_id":"w1:t1","workspace_id":"w1","focused_pane_id":"w1:p1"}],
		"agents":[{"pane_id":"w1:p1","agent":"codex","name":"named","display_agent":"Codex","agent_status":"working","interactive_ready":true,"launch_pending":true,"state_change_seq":12,"title":"agent title","terminal_title":"agent terminal","terminal_title_stripped":"agent stripped","state_labels":["busy"],"tokens":{"output":8},"agent_session":{"kind":"id","value":"secret"}}]
	}`)
	history := map[string]model.LabelHistory{"w1:t1": {SourceLabel: "source", RenderedLabel: "rendered"}}
	now := time.Unix(123, 0)
	got := collector.Build(shot, history, now)
	if len(got) != 1 {
		t.Fatalf("context count = %d", len(got))
	}
	ctx := got[0]
	if ctx.Workspace.ID != "w1" || ctx.Workspace.Number != 1 || ctx.Workspace.NativeNumber != 9 || ctx.Workspace.Label != "workspace" || !ctx.Workspace.Focused || ctx.Workspace.TabCount != 1 || ctx.Workspace.PaneCount != 1 || ctx.Workspace.ActiveTabID != "w1:t1" || ctx.Workspace.AgentStatus != "working" || ctx.Workspace.Tokens["input"] != float64(42) {
		t.Fatalf("workspace = %#v", ctx.Workspace)
	}
	if !ctx.Workspace.Worktree.Present || ctx.Workspace.Worktree.RepoKey != "key" || ctx.Workspace.Worktree.RepoName != "repo" || ctx.Workspace.Worktree.RepoRoot != "/repo" || ctx.Workspace.Worktree.CheckoutPath != "/checkout" || !ctx.Workspace.Worktree.IsLinkedWorktree {
		t.Fatalf("worktree = %#v", ctx.Workspace.Worktree)
	}
	if ctx.Tab.ID != "w1:t1" || ctx.Tab.Index != 0 || ctx.Tab.Number != 1 || ctx.Tab.NativeNumber != 7 || ctx.Tab.Label != "source" || ctx.Tab.CurrentLabel != "rendered" || ctx.Tab.RenderedLabel != "rendered" || !ctx.Tab.Focused || ctx.Tab.PaneCount != 1 {
		t.Fatalf("tab = %#v", ctx.Tab)
	}
	if ctx.Pane.ID != "w1:p1" || ctx.Pane.Label != "pane label" || ctx.Pane.Title != "pane title" || !ctx.Pane.Focused || !ctx.Pane.FocusedInTab || ctx.Pane.Cwd != "/fallback" || ctx.Pane.ForegroundCwd != "/preferred/path" || ctx.Pane.EffectiveCwd != "/preferred/path" || ctx.Pane.Directory != "path" || ctx.Pane.TerminalID != "terminal" || ctx.Pane.TerminalTitle != "terminal title" || ctx.Pane.TerminalTitleStripped != "stripped title" || ctx.Pane.Agent != "codex" || ctx.Pane.DisplayAgent != "Codex" || ctx.Pane.AgentStatus != "working" || ctx.Pane.StateLabels[0] != "busy" || ctx.Pane.Tokens["output"] != float64(8) {
		t.Fatalf("pane = %#v", ctx.Pane)
	}
	if !ctx.Agent.Active || ctx.Agent.Kind != "codex" || ctx.Agent.Name != "named" || ctx.Agent.DisplayName != "Codex" || ctx.Agent.Status != "working" || !ctx.Agent.InteractiveReady || !ctx.Agent.LaunchPending || ctx.Agent.StateChangeSeq != 12 || ctx.Agent.Title != "agent title" || ctx.Agent.TerminalTitle != "agent terminal" || ctx.Agent.TerminalTitleStripped != "agent stripped" || ctx.Agent.StateLabels[0] != "busy" || ctx.Agent.Tokens["output"] != float64(8) || !ctx.Agent.HasSession {
		t.Fatalf("agent = %#v", ctx.Agent)
	}
	var processInfo herdrapi.ProcessInfo
	decodeJSON(t, `{"foreground_process_group_id":11,"foreground_processes":[{"pid":10,"name":"codex","argv0":"codex","argv":["codex","--full"],"cmdline":"codex --full","cwd":"/process"},{"pid":11,"name":"node","argv0":"node"}],"shell_pid":9,"tty":"/dev/ttys1"}`, &processInfo)
	collector.AttachProcess(&ctx, processInfo)
	if ctx.Process.PID != 10 || ctx.Process.Name != "codex" || ctx.Process.Argv0 != "codex" || ctx.Process.Argv[1] != "--full" || ctx.Process.CommandLine != "codex --full" || ctx.Process.Cwd != "/process" || ctx.Processes[1].PID != 11 || ctx.ProcessByPGID.PID != 11 || ctx.Shell.PID != 9 || ctx.Shell.TTY != "/dev/ttys1" {
		t.Fatalf("process/shell = %#v / %#v / %#v / %#v", ctx.Process, ctx.Processes, ctx.ProcessByPGID, ctx.Shell)
	}
	if ctx.Profile != "default" || !ctx.Now.Equal(now) {
		t.Fatalf("profile/time = %q %s", ctx.Profile, ctx.Now)
	}
}

func TestBuildMarksNullWorktreeAbsentAndKeepsNewSourceLabel(t *testing.T) {
	shot := snapshotFromJSON(t, `{"workspaces":[{"workspace_id":"w1","worktree":null}],"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","label":"manual"}],"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1"}]}`)
	got := collector.Build(shot, map[string]model.LabelHistory{"w1:t1": {SourceLabel: "old", RenderedLabel: "rendered"}}, time.Unix(1, 0))
	if got[0].Workspace.Worktree.Present || got[0].Tab.Label != "manual" {
		t.Fatalf("worktree/source label = %#v / %q", got[0].Workspace.Worktree, got[0].Tab.Label)
	}
}

func TestAttachProcessUsesFirstForegroundProcessAndSeparatesPGIDLeader(t *testing.T) {
	var info herdrapi.ProcessInfo
	decodeJSON(t, `{"foreground_process_group_id":20,"foreground_processes":[{"pid":10,"name":"codex","argv0":"codex","argv":["codex"],"cmdline":"codex","cwd":"/repo"},{"pid":20,"name":"node","argv0":"node"}],"shell_pid":9,"tty":"/dev/ttys1"}`, &info)
	ctx := model.Context{}
	collector.AttachProcess(&ctx, info)
	if ctx.Process.PID != 10 || ctx.Process.Name != "codex" || ctx.Process.Argv0 != "codex" || ctx.Processes[1].PID != 20 || ctx.ProcessByPGID.Name != "node" || ctx.Shell.PID != 9 || ctx.Shell.TTY != "/dev/ttys1" {
		t.Fatalf("process context = %#v", ctx)
	}
}

func TestAttachProcessRecordedCodexUsesCodexRatherThanGroupLeaderNode(t *testing.T) {
	var info herdrapi.ProcessInfo
	decodeJSON(t, readFile(t, "process_info_codex.json"), &info)
	ctx := model.Context{}
	collector.AttachProcess(&ctx, info)
	if ctx.Process.Name != "codex" || ctx.ProcessByPGID.Name != "node" {
		t.Fatalf("selected/group process = %#v / %#v", ctx.Process, ctx.ProcessByPGID)
	}
}

func readSnapshot(t *testing.T, name string) herdrapi.Snapshot {
	t.Helper()
	var shot herdrapi.Snapshot
	decodeJSON(t, readFile(t, name), &shot)
	return shot
}

func snapshotFromJSON(t *testing.T, value string) herdrapi.Snapshot {
	t.Helper()
	var shot herdrapi.Snapshot
	decodeJSON(t, value, &shot)
	return shot
}

func decodeJSON(t *testing.T, value string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(value), target); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "herdrapi", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
