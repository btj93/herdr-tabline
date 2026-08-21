// Package config loads and resolves herdr-tabline configuration.
package config

import (
	"regexp"
	"time"

	"github.com/btj93/herdr-tabline/internal/render"
)

// defaultTemplate names the detected agent when there is one, and the foreground process
// otherwise.
//
// Herdr's agent detection is the only source that is correct for every agent. The first
// foreground record reads "codex" correctly but "node" or "caffeinate" for Claude, while
// the process-group leader reads "claude" correctly but "node" for Codex. Neither process
// heuristic works for both, so neither is used for a pane Herdr has already identified.
const defaultTemplate = ` {{ .Tab.Number }}: {{ .Pane.Directory }} > ` +
	`{{ if .Agent.Active }}{{ statusIcon .Agent.Status }} ` +
	`{{ coalesce .Agent.DisplayName .Agent.Kind }}` +
	`{{ else }}{{ .Process.Name }}{{ end }} `

// Mode controls when tab labels are written.
type Mode string

const (
	ModeAuto    Mode = "auto"
	ModeKeybind Mode = "keybind"
	ModeOff     Mode = "off"
)

// Match contains the optional conditions for a profile.
type Match struct {
	WorkspaceLabelRegex []string `toml:"workspace_label_regex"`
	TabLabelRegex       []string `toml:"tab_label_regex"`
	CwdGlob             []string `toml:"cwd_glob"`
	ProcessRegex        []string `toml:"process_regex"`
	AgentRegex          []string `toml:"agent_regex"`
	AgentStatus         []string `toml:"agent_status"`
}

// Profile selectively overrides the top-level configuration when Match succeeds.
type Profile struct {
	Name     string  `toml:"name"`
	Mode     *Mode   `toml:"mode"`
	Template *string `toml:"template"`
	MaxWidth *int    `toml:"max_width"`
	Match    Match   `toml:"match"`
}

// defaultStatusTemplate renders the tab-bar right edge: attention-worthy counts first,
// each omitted when zero, then the focused space name. It stays quiet on a calm session.
const defaultStatusTemplate = `{{ if .Blocked }}{{ statusIcon "blocked" }}{{ .Blocked }} {{ end }}` +
	`{{ if .Working }}{{ statusIcon "working" }}{{ .Working }} {{ end }}` +
	`{{ if .Done }}{{ statusIcon "done" }}{{ .Done }} {{ end }}` +
	`{{ .Workspace.Label }}`

type statusSource struct {
	Template string `toml:"template"`
}

type icons struct {
	AgentStatus map[string]string `toml:"agent_status"`
}

type source struct {
	SchemaVersion   *int                         `toml:"schema_version"`
	Mode            *Mode                        `toml:"mode"`
	Template        string                       `toml:"template"`
	PollInterval    string                       `toml:"poll_interval"`
	RefreshDebounce string                       `toml:"refresh_debounce"`
	MaxWidth        int                          `toml:"max_width"`
	Aliases         map[string]map[string]string `toml:"aliases"`
	Icons           icons                        `toml:"icons"`
	Profiles        []Profile                    `toml:"profiles"`
	Status          statusSource                 `toml:"status"`
}

// Effective is the resolved rendering configuration for one tab context.
type Effective struct {
	Mode        Mode
	Template    *render.Template
	MaxWidth    int
	ProfileName string
}

// Compiled is a fully validated configuration ready for use by the controller.
type Compiled struct {
	Mode            Mode
	Template        *render.Template
	MaxWidth        int
	PollInterval    time.Duration
	RefreshDebounce time.Duration
	Aliases         map[string]map[string]string
	Icons           map[string]map[string]string
	// Status is the compiled tab-bar right-edge template used by the status command.
	Status   *render.Template
	profiles []compiledProfile
}

type compiledProfile struct {
	name     string
	mode     *Mode
	template *render.Template
	maxWidth *int
	match    compiledMatch
}

type compiledMatch struct {
	workspaceLabel []*regexp.Regexp
	tabLabel       []*regexp.Regexp
	cwdGlob        []string
	process        []*regexp.Regexp
	agent          []*regexp.Regexp
	agentStatus    []string
}
