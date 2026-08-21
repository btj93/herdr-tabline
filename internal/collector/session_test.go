package collector_test

import (
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/collector"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

func TestBuildSessionCountsAgentsByStatus(t *testing.T) {
	snapshot := herdrapi.Snapshot{
		Workspaces: []herdrapi.Workspace{
			{WorkspaceID: "w1", Label: "config", Focused: true, TabCount: 2},
			{WorkspaceID: "w2", Label: "projects", TabCount: 5},
		},
		FocusedWorkspaceID: "w1",
		Agents: []herdrapi.Agent{
			{PaneID: "p1", WorkspaceID: "w1", Agent: "claude", AgentStatus: "blocked"},
			{PaneID: "p2", WorkspaceID: "w1", Agent: "claude", AgentStatus: "working"},
			{PaneID: "p3", WorkspaceID: "w2", Agent: "codex", AgentStatus: "working"},
			{PaneID: "p4", WorkspaceID: "w2", Agent: "claude", AgentStatus: "done"},
			{PaneID: "p5", WorkspaceID: "w2", Agent: "claude", AgentStatus: "idle"},
			{PaneID: "p6", WorkspaceID: "w2", Agent: "claude", AgentStatus: "unknown"},
		},
	}
	session := collector.BuildSession(snapshot, time.Unix(0, 0))

	if session.Blocked != 1 || session.Working != 2 || session.Done != 1 ||
		session.Idle != 1 || session.Unknown != 1 {
		t.Fatalf("counts = %#v", session)
	}
	if session.Agents != 6 {
		t.Fatalf("total agents = %d, want 6", session.Agents)
	}
	if session.Attention != 1 {
		t.Fatalf("attention = %d, want the blocked count", session.Attention)
	}
}

func TestBuildSessionReportsTheFocusedWorkspace(t *testing.T) {
	snapshot := herdrapi.Snapshot{
		Workspaces: []herdrapi.Workspace{
			{WorkspaceID: "w1", Label: "config", TabCount: 2},
			{WorkspaceID: "w2", Label: "projects", Focused: true, TabCount: 5},
		},
		FocusedWorkspaceID: "w2",
	}
	session := collector.BuildSession(snapshot, time.Unix(0, 0))
	if session.Workspace.Label != "projects" {
		t.Fatalf("focused workspace = %q, want projects", session.Workspace.Label)
	}
	if session.Workspace.Number != 2 {
		t.Fatalf("focused workspace number = %d, want its position", session.Workspace.Number)
	}
	if session.Workspaces != 2 {
		t.Fatalf("workspace count = %d, want 2", session.Workspaces)
	}
}

// TestBuildSessionToleratesNoFocusedWorkspace keeps the status line renderable when the
// snapshot reports no focus, rather than failing the whole command.
func TestBuildSessionToleratesNoFocusedWorkspace(t *testing.T) {
	session := collector.BuildSession(herdrapi.Snapshot{
		Workspaces: []herdrapi.Workspace{{WorkspaceID: "w1", Label: "only"}},
	}, time.Unix(0, 0))
	if session.Workspace.Label != "" {
		t.Fatalf("unfocused snapshot invented a workspace: %q", session.Workspace.Label)
	}
	if session.Workspaces != 1 {
		t.Fatalf("workspace count = %d, want 1", session.Workspaces)
	}
}

// TestBuildSessionIgnoresPanesWithoutAnAgent guards the count against plain shells, which
// carry an empty agent status rather than a missing record.
func TestBuildSessionIgnoresPanesWithoutAnAgent(t *testing.T) {
	session := collector.BuildSession(herdrapi.Snapshot{
		Agents: []herdrapi.Agent{
			{PaneID: "p1", Agent: "claude", AgentStatus: "idle"},
			{PaneID: "p2", Agent: "", AgentStatus: ""},
		},
	}, time.Unix(0, 0))
	if session.Agents != 1 {
		t.Fatalf("agents = %d, want only the real agent counted", session.Agents)
	}
}
