// Package config loads and resolves herdr-tabline configuration.
package config

import (
	"regexp"
	"time"

	"github.com/btj93/herdr-tabline/internal/render"
)

const defaultTemplate = ` {{ .Tab.Number }}: {{ .Pane.Directory }} > {{ .Process.Name }} `

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
	profiles        []compiledProfile
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
