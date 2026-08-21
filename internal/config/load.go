package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/btj93/herdr-tabline/internal/render"
)

const (
	defaultPollInterval    = 2 * time.Second
	defaultRefreshDebounce = 80 * time.Millisecond
)

// Load reads, validates, and compiles path. A missing file returns compatibility defaults.
func Load(path string) (*Compiled, bool, error) {
	var input source
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		compiled, compileErr := compile(source{})
		return compiled, false, compileErr
	}
	if err != nil {
		return nil, false, fmt.Errorf("read config: %w", err)
	}
	metadata, err := toml.Decode(string(data), &input)
	if err != nil {
		return nil, true, fmt.Errorf("decode config: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		return nil, true, fmt.Errorf("unknown configuration key %q", undecoded[0].String())
	}
	compiled, err := compile(input)
	if err != nil {
		return nil, true, err
	}
	return compiled, true, nil
}

func compile(input source) (*Compiled, error) {
	schemaVersion := 1
	if input.SchemaVersion != nil {
		schemaVersion = *input.SchemaVersion
	}
	if schemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d", schemaVersion)
	}
	mode := ModeAuto
	if input.Mode != nil {
		mode = *input.Mode
	}
	if !validMode(mode) {
		return nil, fmt.Errorf("invalid mode %q", mode)
	}
	if input.Template == "" {
		input.Template = defaultTemplate
	}
	if input.PollInterval == "" {
		input.PollInterval = defaultPollInterval.String()
	}
	pollInterval, err := time.ParseDuration(input.PollInterval)
	if err != nil || pollInterval < 100*time.Millisecond || pollInterval > time.Minute {
		return nil, fmt.Errorf("poll_interval must be between 100ms and 1m")
	}
	if input.RefreshDebounce == "" {
		input.RefreshDebounce = defaultRefreshDebounce.String()
	}
	refreshDebounce, err := time.ParseDuration(input.RefreshDebounce)
	if err != nil || refreshDebounce < 0 || refreshDebounce > 2*time.Second {
		return nil, fmt.Errorf("refresh_debounce must be between 0s and 2s")
	}
	if input.MaxWidth < 0 || input.MaxWidth > 1024 {
		return nil, fmt.Errorf("max_width must be 0 or between 1 and 1024")
	}
	if input.Aliases == nil {
		input.Aliases = map[string]map[string]string{}
	}
	// Herdr's own "distinct symbols" indicator set, so labels match the rest of its UI.
	// Herdr's other mode, "color dots", renders blocked, working, and done as one glyph
	// separated only by colour — which a tab label cannot express, since tab.rename takes
	// plain text. The symbols set is therefore used unconditionally, and every entry stays
	// individually overridable through [icons.agent_status].
	agentStatusIcons := map[string]string{
		"working": "◐",
		"blocked": "×",
		"done":    "✓",
		"idle":    "○",
		"unknown": "·",
	}
	for status, icon := range input.Icons.AgentStatus {
		agentStatusIcons[status] = icon
	}
	engine := render.New(input.Aliases, map[string]map[string]string{"agent_status": agentStatusIcons})
	template, err := engine.Compile("default", input.Template)
	if err != nil {
		return nil, err
	}
	compiled := &Compiled{
		Mode: mode, Template: template, MaxWidth: input.MaxWidth,
		PollInterval: pollInterval, RefreshDebounce: refreshDebounce,
		Aliases: input.Aliases, Icons: map[string]map[string]string{"agent_status": agentStatusIcons},
	}
	names := make(map[string]struct{}, len(input.Profiles))
	for index, profile := range input.Profiles {
		if profile.Name == "" {
			return nil, fmt.Errorf("profile %d has no name", index+1)
		}
		if _, exists := names[profile.Name]; exists {
			return nil, fmt.Errorf("duplicate profile name %q", profile.Name)
		}
		names[profile.Name] = struct{}{}
		if profile.Mode != nil && !validMode(*profile.Mode) {
			return nil, fmt.Errorf("profile %q has invalid mode %q", profile.Name, *profile.Mode)
		}
		if profile.MaxWidth != nil && (*profile.MaxWidth < 0 || *profile.MaxWidth > 1024) {
			return nil, fmt.Errorf("profile %q has invalid max_width", profile.Name)
		}
		matcher, err := compileMatch(profile.Match)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", profile.Name, err)
		}
		item := compiledProfile{name: profile.Name, mode: profile.Mode, maxWidth: profile.MaxWidth, match: matcher}
		if profile.Template != nil {
			item.template, err = engine.Compile(profile.Name, *profile.Template)
			if err != nil {
				return nil, err
			}
		}
		compiled.profiles = append(compiled.profiles, item)
	}
	return compiled, nil
}

func validMode(mode Mode) bool { return mode == ModeAuto || mode == ModeKeybind || mode == ModeOff }

func compileMatch(input Match) (compiledMatch, error) {
	compileRegexes := func(kind string, values []string) ([]*regexp.Regexp, error) {
		compiled := make([]*regexp.Regexp, 0, len(values))
		for _, value := range values {
			re, err := regexp.Compile(value)
			if err != nil {
				return nil, fmt.Errorf("invalid %s %q: %w", kind, value, err)
			}
			compiled = append(compiled, re)
		}
		return compiled, nil
	}
	workspace, err := compileRegexes("workspace_label_regex", input.WorkspaceLabelRegex)
	if err != nil {
		return compiledMatch{}, err
	}
	tab, err := compileRegexes("tab_label_regex", input.TabLabelRegex)
	if err != nil {
		return compiledMatch{}, err
	}
	process, err := compileRegexes("process_regex", input.ProcessRegex)
	if err != nil {
		return compiledMatch{}, err
	}
	agent, err := compileRegexes("agent_regex", input.AgentRegex)
	if err != nil {
		return compiledMatch{}, err
	}
	globs := make([]string, 0, len(input.CwdGlob))
	for _, glob := range input.CwdGlob {
		normalized, err := normalizeGlob(glob)
		if err != nil {
			return compiledMatch{}, err
		}
		if !doublestar.ValidatePattern(normalized) {
			return compiledMatch{}, fmt.Errorf("invalid cwd_glob %q", glob)
		}
		globs = append(globs, normalized)
	}
	return compiledMatch{workspaceLabel: workspace, tabLabel: tab, cwdGlob: globs, process: process, agent: agent, agentStatus: append([]string(nil), input.AgentStatus...)}, nil
}

type signature struct {
	exists  bool
	modTime time.Time
	size    int64
	hash    [sha256.Size]byte
}

// Manager hot-reloads a configuration while retaining the last valid value.
type Manager struct {
	mu        sync.RWMutex
	reloadMu  sync.Mutex
	path      string
	seen      bool
	signature signature
	current   *Compiled
}

// NewManager constructs an initially empty configuration manager.
func NewManager(path string) *Manager { return &Manager{path: path} }

// Current returns the last valid compiled configuration, if any.
func (m *Manager) Current() (*Compiled, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current, m.current != nil
}

// ReloadIfChanged installs a valid changed file. Invalid versions report one error and retain current.
func (m *Manager) ReloadIfChanged() (bool, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	sig, err := fileSignature(m.path)
	if err != nil {
		return false, err
	}
	m.mu.RLock()
	unchanged := m.seen && sig == m.signature
	m.mu.RUnlock()
	if unchanged {
		return false, nil
	}

	compiled, _, err := Load(m.path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen && sig == m.signature {
		return false, nil
	}
	m.seen, m.signature = true, sig
	if err != nil {
		return false, err
	}
	m.current = compiled
	return true, nil
}

func fileSignature(path string) (signature, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return signature{}, nil
	}
	if err != nil {
		return signature{}, fmt.Errorf("stat config: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return signature{}, fmt.Errorf("read config: %w", err)
	}
	return signature{exists: true, modTime: info.ModTime(), size: info.Size(), hash: sha256.Sum256(data)}, nil
}
