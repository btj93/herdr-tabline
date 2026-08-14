package collector

import (
	"path/filepath"
	"time"

	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/model"
)

func Build(snapshot herdrapi.Snapshot, history map[string]model.LabelHistory, now time.Time) []model.Context {
	workspaces := make(map[string]model.Workspace, len(snapshot.Workspaces))
	for index, raw := range snapshot.Workspaces {
		workspaces[raw.WorkspaceID] = normalizeWorkspace(raw, index)
	}
	layouts := make(map[string]*herdrapi.Layout, len(snapshot.Layouts))
	for index := range snapshot.Layouts {
		layouts[snapshot.Layouts[index].TabID] = &snapshot.Layouts[index]
	}
	agents := make(map[string]*herdrapi.Agent, len(snapshot.Agents))
	for index := range snapshot.Agents {
		agents[snapshot.Agents[index].PaneID] = &snapshot.Agents[index]
	}
	panesByTab := make(map[string][]herdrapi.Pane)
	for _, raw := range snapshot.Panes {
		panesByTab[raw.TabID] = append(panesByTab[raw.TabID], raw)
	}

	positions := map[string]int{}
	contexts := make([]model.Context, 0, len(snapshot.Tabs))
	for _, rawTab := range snapshot.Tabs {
		index := positions[rawTab.WorkspaceID]
		positions[rawTab.WorkspaceID] = index + 1
		tabPanes := panesByTab[rawTab.TabID]
		selected, foundByLayout := selectPane(layouts[rawTab.TabID], tabPanes)
		panes := make([]model.Pane, 0, len(tabPanes))
		for _, rawPane := range tabPanes {
			panes = append(panes, normalizePane(rawPane, foundByLayout && rawPane.PaneID == selected.PaneID))
		}
		var selectedPane model.Pane
		for _, pane := range panes {
			if pane.ID == selected.PaneID {
				selectedPane = pane
				break
			}
		}
		label := rawTab.Label
		rendered := ""
		if prior, ok := history[rawTab.TabID]; ok {
			rendered = prior.RenderedLabel
			if rawTab.Label == prior.RenderedLabel {
				label = prior.SourceLabel
			}
		}
		ctx := model.Context{
			Workspace: workspaces[rawTab.WorkspaceID],
			Tab:       model.Tab{ID: rawTab.TabID, Label: label, CurrentLabel: rawTab.Label, RenderedLabel: rendered, Index: index, Number: index + 1, NativeNumber: rawTab.Number, PaneCount: rawTab.PaneCount, Focused: rawTab.Focused},
			Pane:      selectedPane,
			Panes:     panes,
			Agent:     normalizeAgent(agents[selected.PaneID], selected),
			Profile:   "default",
			Now:       now,
		}
		contexts = append(contexts, ctx)
	}
	return contexts
}

func AttachProcess(ctx *model.Context, info herdrapi.ProcessInfo) {
	ctx.Processes = make([]model.Process, 0, len(info.ForegroundProcesses))
	for _, raw := range info.ForegroundProcesses {
		process := normalizeProcess(raw)
		ctx.Processes = append(ctx.Processes, process)
		if info.ForegroundProcessGroupID != nil && raw.PID == *info.ForegroundProcessGroupID {
			ctx.ProcessByPGID = process
		}
	}
	if len(ctx.Processes) > 0 {
		ctx.Process = ctx.Processes[0]
	}
	if info.ShellPID != nil {
		ctx.Shell.PID = *info.ShellPID
	}
	ctx.Shell.TTY = info.TTY
}

func selectPane(layout *herdrapi.Layout, tabPanes []herdrapi.Pane) (herdrapi.Pane, bool) {
	if layout != nil {
		if layout.FocusedPaneID != "" {
			if pane, ok := findPane(tabPanes, layout.FocusedPaneID); ok {
				return pane, true
			}
		}
		for _, entry := range layout.Panes {
			if entry.Focused {
				if pane, ok := findPane(tabPanes, entry.PaneID); ok {
					return pane, true
				}
			}
		}
	}
	if len(tabPanes) == 0 {
		return herdrapi.Pane{}, false
	}
	return tabPanes[0], false
}

func findPane(panes []herdrapi.Pane, id string) (herdrapi.Pane, bool) {
	for _, pane := range panes {
		if pane.PaneID == id {
			return pane, true
		}
	}
	return herdrapi.Pane{}, false
}

func normalizeWorkspace(raw herdrapi.Workspace, index int) model.Workspace {
	workspace := model.Workspace{ID: raw.WorkspaceID, Number: index + 1, NativeNumber: raw.Number, Label: raw.Label, Focused: raw.Focused, TabCount: raw.TabCount, PaneCount: raw.PaneCount, ActiveTabID: raw.ActiveTabID, AgentStatus: raw.AgentStatus, Tokens: raw.Tokens}
	if raw.Worktree != nil {
		workspace.Worktree = model.Worktree{Present: true, RepoKey: raw.Worktree.RepoKey, RepoName: raw.Worktree.RepoName, RepoRoot: raw.Worktree.RepoRoot, CheckoutPath: raw.Worktree.CheckoutPath, IsLinkedWorktree: raw.Worktree.IsLinkedWorktree}
	}
	return workspace
}

func normalizePane(raw herdrapi.Pane, focusedInTab bool) model.Pane {
	effectiveCwd := raw.Cwd
	if raw.ForegroundCwd != "" {
		effectiveCwd = raw.ForegroundCwd
	}
	directory := ""
	if effectiveCwd != "" {
		directory = filepath.Base(effectiveCwd)
	}
	return model.Pane{ID: raw.PaneID, Label: raw.Label, Title: raw.Title, Focused: raw.Focused, FocusedInTab: focusedInTab, Cwd: raw.Cwd, ForegroundCwd: raw.ForegroundCwd, EffectiveCwd: effectiveCwd, Directory: directory, TerminalID: raw.TerminalID, TerminalTitle: raw.TerminalTitle, TerminalTitleStripped: raw.TerminalTitleStripped, Agent: raw.Agent, DisplayAgent: raw.DisplayAgent, AgentStatus: raw.AgentStatus, StateLabels: raw.StateLabels, Tokens: raw.Tokens}
}

func normalizeAgent(raw *herdrapi.Agent, pane herdrapi.Pane) model.Agent {
	if raw == nil {
		return model.Agent{Kind: pane.Agent, DisplayName: pane.DisplayAgent, Status: pane.AgentStatus, StateLabels: pane.StateLabels, Tokens: pane.Tokens}
	}
	return model.Agent{Active: true, Kind: raw.Agent, Name: raw.Name, DisplayName: raw.DisplayAgent, Status: raw.AgentStatus, InteractiveReady: raw.InteractiveReady, LaunchPending: raw.LaunchPending, StateChangeSeq: raw.StateChangeSeq, Title: raw.Title, TerminalTitle: raw.TerminalTitle, TerminalTitleStripped: raw.TerminalTitleStripped, StateLabels: raw.StateLabels, Tokens: raw.Tokens, HasSession: len(raw.AgentSession) > 0 && string(raw.AgentSession) != "null"}
}

func normalizeProcess(raw herdrapi.Process) model.Process {
	return model.Process{PID: raw.PID, Name: raw.Name, Argv0: raw.Argv0, Argv: raw.Argv, CommandLine: raw.CommandLine, Cwd: raw.Cwd}
}
