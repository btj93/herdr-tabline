package model

import "time"

type Context struct {
	Workspace     Workspace
	Tab           Tab
	Pane          Pane
	Panes         []Pane
	Process       Process
	Processes     []Process
	ProcessByPGID Process
	Shell         Shell
	Agent         Agent
	Profile       string
	Now           time.Time
}

type Workspace struct {
	ID, Label, ActiveTabID, AgentStatus       string
	Number, NativeNumber, TabCount, PaneCount int
	Focused                                   bool
	Tokens                                    map[string]any
	Worktree                                  Worktree
}

type Worktree struct {
	Present                                   bool
	RepoKey, RepoName, RepoRoot, CheckoutPath string
	IsLinkedWorktree                          bool
}

type Tab struct {
	ID, Label, CurrentLabel, RenderedLabel string
	Index, Number, NativeNumber, PaneCount int
	Focused                                bool
}

type Pane struct {
	ID, Label, Title                                 string
	Focused, FocusedInTab                            bool
	Cwd, ForegroundCwd, EffectiveCwd, Directory      string
	TerminalID, TerminalTitle, TerminalTitleStripped string
	Agent, DisplayAgent, AgentStatus                 string
	StateLabels                                      []string
	Tokens                                           map[string]any
}

type Process struct {
	PID         int
	Name        string
	Argv0       string
	Argv        []string
	CommandLine string
	Cwd         string
}

type Shell struct {
	PID int
	TTY string
}

type Agent struct {
	Active                                      bool
	Kind, Name, DisplayName, Status             string
	InteractiveReady, LaunchPending             bool
	StateChangeSeq                              int
	Title, TerminalTitle, TerminalTitleStripped string
	StateLabels                                 []string
	Tokens                                      map[string]any
	HasSession                                  bool
}

type LabelHistory struct {
	SourceLabel   string `json:"source_label"`
	RenderedLabel string `json:"rendered_label"`
}

// Session is the whole-session context used by the `status` command, which renders one
// line for Herdr's tab-bar right edge rather than a per-tab label.
type Session struct {
	Workspace  Workspace
	Workspaces int

	// Agent counts across every workspace.
	Agents                                int
	Blocked, Working, Done, Idle, Unknown int

	// Attention is the number of agents actively waiting on the user. It is currently the
	// blocked count, named separately so the meaning survives if Herdr adds another state
	// that needs a person.
	Attention int

	Now time.Time
}
