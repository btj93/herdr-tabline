package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/btj93/herdr-tabline/internal/model"
)

// Resolve applies all matching profiles in declaration order.
func (c *Compiled) Resolve(context model.Context) Effective {
	effective := Effective{Mode: c.Mode, Template: c.Template, MaxWidth: c.MaxWidth, ProfileName: "default"}
	for _, profile := range c.profiles {
		if !profile.match.matches(context) {
			continue
		}
		overrode := false
		if profile.mode != nil {
			effective.Mode, overrode = *profile.mode, true
		}
		if profile.template != nil {
			effective.Template, overrode = profile.template, true
		}
		if profile.maxWidth != nil {
			effective.MaxWidth, overrode = *profile.maxWidth, true
		}
		if overrode {
			effective.ProfileName = profile.name
		}
	}
	return effective
}

func (m compiledMatch) matches(context model.Context) bool {
	return regexMatches(m.workspaceLabel, context.Workspace.Label) &&
		regexMatches(m.tabLabel, context.Tab.Label) &&
		globMatches(m.cwdGlob, paneCWD(context.Pane)) &&
		regexMatches(m.process, context.Process.Name) &&
		regexMatches(m.agent, context.Agent.Kind) &&
		stringMatches(m.agentStatus, context.Agent.Status)
}

func regexMatches(patterns []*regexp.Regexp, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func stringMatches(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func globMatches(patterns []string, cwd string) bool {
	if len(patterns) == 0 {
		return true
	}
	normalized, err := normalizePath(cwd)
	if err != nil {
		return false
	}
	for _, pattern := range patterns {
		matched, err := doublestar.Match(pattern, normalized)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func paneCWD(pane model.Pane) string {
	if pane.EffectiveCwd != "" {
		return pane.EffectiveCwd
	}
	if pane.ForegroundCwd != "" {
		return pane.ForegroundCwd
	}
	return pane.Cwd
}

func normalizeGlob(pattern string) (string, error) {
	if strings.HasPrefix(pattern, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand home: %w", err)
		}
		pattern = filepath.Join(home, pattern[2:])
	}
	if !filepath.IsAbs(pattern) {
		absolute, err := filepath.Abs(pattern)
		if err != nil {
			return "", fmt.Errorf("normalize cwd_glob: %w", err)
		}
		pattern = absolute
	}
	return filepath.ToSlash(filepath.Clean(pattern)), nil
}

func normalizePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		path = absolute
	}
	return filepath.ToSlash(filepath.Clean(path)), nil
}
