package herdrapi

import "encoding/json"

type Snapshot struct {
	Protocol           int         `json:"protocol"`
	Version            string      `json:"version"`
	Workspaces         []Workspace `json:"workspaces"`
	Tabs               []Tab       `json:"tabs"`
	Panes              []Pane      `json:"panes"`
	Layouts            []Layout    `json:"layouts"`
	Agents             []Agent     `json:"agents"`
	FocusedWorkspaceID string      `json:"focused_workspace_id"`
	FocusedTabID       string      `json:"focused_tab_id"`
	FocusedPaneID      string      `json:"focused_pane_id"`
}

type Workspace struct {
	WorkspaceID string         `json:"workspace_id"`
	Number      int            `json:"number"`
	Label       string         `json:"label"`
	Focused     bool           `json:"focused"`
	TabCount    int            `json:"tab_count"`
	PaneCount   int            `json:"pane_count"`
	ActiveTabID string         `json:"active_tab_id"`
	AgentStatus string         `json:"agent_status"`
	Tokens      map[string]any `json:"tokens"`
	Worktree    *Worktree      `json:"worktree"`
}

type Worktree struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	PaneCount   int    `json:"pane_count"`
	AgentStatus string `json:"agent_status"`
}

type Pane struct {
	PaneID                string          `json:"pane_id"`
	TabID                 string          `json:"tab_id"`
	WorkspaceID           string          `json:"workspace_id"`
	Label                 string          `json:"label"`
	Title                 string          `json:"title"`
	Focused               bool            `json:"focused"`
	Cwd                   string          `json:"cwd"`
	ForegroundCwd         string          `json:"foreground_cwd"`
	TerminalID            string          `json:"terminal_id"`
	TerminalTitle         string          `json:"terminal_title"`
	TerminalTitleStripped string          `json:"terminal_title_stripped"`
	Agent                 string          `json:"agent"`
	DisplayAgent          string          `json:"display_agent"`
	AgentStatus           string          `json:"agent_status"`
	AgentSession          json.RawMessage `json:"agent_session"`
	Revision              int             `json:"revision"`
	Scroll                Scroll          `json:"scroll"`
	StateLabels           []string        `json:"state_labels"`
	Tokens                map[string]any  `json:"tokens"`
}

type Layout struct {
	TabID         string       `json:"tab_id"`
	WorkspaceID   string       `json:"workspace_id"`
	FocusedPaneID string       `json:"focused_pane_id"`
	Area          Rect         `json:"area"`
	Panes         []LayoutPane `json:"panes"`
	Splits        []Split      `json:"splits"`
	Zoomed        bool         `json:"zoomed"`
}

type LayoutPane struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
	Rect    Rect   `json:"rect"`
}

type Rect struct {
	Height int `json:"height"`
	Width  int `json:"width"`
	X      int `json:"x"`
	Y      int `json:"y"`
}

type Split struct {
	Direction string  `json:"direction"`
	ID        string  `json:"id"`
	Ratio     float64 `json:"ratio"`
	Rect      Rect    `json:"rect"`
}

type Scroll struct {
	MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
	OffsetFromBottom    int `json:"offset_from_bottom"`
	ViewportRows        int `json:"viewport_rows"`
}

type Agent struct {
	PaneID                string          `json:"pane_id"`
	TabID                 string          `json:"tab_id"`
	WorkspaceID           string          `json:"workspace_id"`
	Agent                 string          `json:"agent"`
	Name                  string          `json:"name"`
	DisplayAgent          string          `json:"display_agent"`
	AgentStatus           string          `json:"agent_status"`
	Focused               bool            `json:"focused"`
	Cwd                   string          `json:"cwd"`
	ForegroundCwd         string          `json:"foreground_cwd"`
	TerminalID            string          `json:"terminal_id"`
	Revision              int             `json:"revision"`
	InteractiveReady      bool            `json:"interactive_ready"`
	LaunchPending         bool            `json:"launch_pending"`
	StateChangeSeq        int             `json:"state_change_seq"`
	Title                 string          `json:"title"`
	TerminalTitle         string          `json:"terminal_title"`
	TerminalTitleStripped string          `json:"terminal_title_stripped"`
	StateLabels           []string        `json:"state_labels"`
	Tokens                map[string]any  `json:"tokens"`
	AgentSession          json.RawMessage `json:"agent_session"`
}

type ProcessInfo struct {
	PaneID                   string    `json:"pane_id"`
	ForegroundProcessGroupID *int      `json:"foreground_process_group_id"`
	ForegroundProcesses      []Process `json:"foreground_processes"`
	ShellPID                 *int      `json:"shell_pid"`
	TTY                      string    `json:"tty"`
}

type Process struct {
	PID         int      `json:"pid"`
	Name        string   `json:"name"`
	Argv0       string   `json:"argv0"`
	Argv        []string `json:"argv"`
	CommandLine string   `json:"cmdline"`
	Cwd         string   `json:"cwd"`
}
