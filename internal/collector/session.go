package collector

import (
	"time"

	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/model"
)

// BuildSession summarizes a whole snapshot for the status line: agent counts by state and
// the focused workspace. It reads only the snapshot, so it costs no extra API calls.
func BuildSession(snapshot herdrapi.Snapshot, now time.Time) model.Session {
	session := model.Session{
		Workspaces: len(snapshot.Workspaces),
		Now:        now,
	}
	for index, raw := range snapshot.Workspaces {
		if raw.WorkspaceID == snapshot.FocusedWorkspaceID && snapshot.FocusedWorkspaceID != "" {
			session.Workspace = normalizeWorkspace(raw, index)
		}
	}
	for _, agent := range snapshot.Agents {
		// An empty agent kind is a pane Herdr has not identified as an agent at all, which
		// is an ordinary shell rather than an agent in an unknown state.
		if agent.Agent == "" {
			continue
		}
		session.Agents++
		switch agent.AgentStatus {
		case "blocked":
			session.Blocked++
		case "working":
			session.Working++
		case "done":
			session.Done++
		case "idle":
			session.Idle++
		default:
			session.Unknown++
		}
	}
	session.Attention = session.Blocked
	return session
}
