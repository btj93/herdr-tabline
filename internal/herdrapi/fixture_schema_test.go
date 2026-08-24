package herdrapi_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file is a release guard, not a behaviour test. It asserts what a recorded fixture
// may contain, rather than scanning for what it must not.
//
// Two properties carry the weight, and both were learned the hard way.
//
// CLOSED. A key present in a fixture but absent from fieldRules is a failure, not an
// ignore. Every earlier guard could be evaded by data arriving in a field the guard did
// not know to look at — which is exactly how live terminal ids survived a sweep for
// usernames and paths. A future Herdr field now breaks the build until a human classifies
// it. That is the correct failure, and it is annoying precisely when it should be.
//
// DERIVED. permittedKeys is computed from fieldRules, so a key cannot be permitted without
// a value rule. A permitted-key list maintained separately from a value-rule list is the
// shape of the bug it is meant to prevent: adding to one and forgetting the other leaves a
// key the schema has explicitly looked at and waved through. Both this project and the
// companion herdr-tokens plugin shipped that gap independently — labels here, token values
// there — and neither audit found its own.

// rule checks one scalar value. A nil check marks a field that is structural (a container
// the walker recurses into) or deliberately unconstrained, and every such entry states why.
type rule struct {
	check func(value any) error
	why   string
	// leaf marks a container whose rule validates the WHOLE value, so the walker must not
	// descend into it. Without this, tokens is validated correctly and then re-walked, and
	// its dynamic keys are reported as unrecognised — rejecting valid data. A guard that
	// false-positives on correct input is one people switch off.
	leaf bool
}

func pattern(expression string) rule {
	compiled := regexp.MustCompile(expression)
	return rule{check: func(value any) error {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string, got %T", value)
		}
		if !compiled.MatchString(text) {
			return fmt.Errorf("%q does not match %s", text, expression)
		}
		return nil
	}}
}

func oneOf(allowed ...string) rule {
	permitted := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		permitted[value] = true
	}
	return rule{check: func(value any) error {
		if value == nil {
			return nil // Herdr sends null for absent optional strings.
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string, got %T", value)
		}
		if !permitted[text] {
			return fmt.Errorf("%q is not one of %s", text, strings.Join(allowed, ", "))
		}
		return nil
	}}
}

func numeric() rule {
	return rule{check: func(value any) error {
		if value == nil {
			return nil
		}
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("expected a number, got %T", value)
		}
		return nil
	}}
}

func boolean() rule {
	return rule{check: func(value any) error {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected a bool, got %T", value)
		}
		return nil
	}}
}

// structural marks a container the walker descends into; its children carry the rules.
func structural(why string) rule { return rule{why: why} }

// tokenValues constrains metadata token values, not just their keys.
//
// st_* values carry the workspace label by the herdr-tokens contract, so type-checking
// them would let a real workspace name through a schema that pattern-checks `label`
// elsewhere — the same data by a different route. Found in the companion plugin by
// mutation and fixed there; this is the mirror.
func tokenValues() rule {
	key := regexp.MustCompile(`^(st_(working|blocked|done|idle|unknown|none)|att_blocked|n_agents)$`)
	count := regexp.MustCompile(`^\d+$`)
	return rule{check: func(value any) error {
		if value == nil {
			return nil
		}
		entries, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected null or an object, got %T", value)
		}
		for name, raw := range entries {
			if !key.MatchString(name) {
				return fmt.Errorf("token key %q is not in the frozen vocabulary", name)
			}
			text, ok := raw.(string)
			if !ok {
				return fmt.Errorf("token %q must be a string, got %T", name, raw)
			}
			if strings.HasPrefix(name, "st_") {
				if !permittedLabels[text] {
					return fmt.Errorf("token %s = %q is a workspace label and is not on the "+
						"label allowlist", name, text)
				}
				continue
			}
			if !count.MatchString(text) {
				return fmt.Errorf("token %s = %q is not a decimal count", name, text)
			}
		}
		return nil
	}, leaf: true}
}

// permittedLabels is an allowlist of exact label values. Tab labels embed directory and
// process names verbatim, so re-capturing a fixture would otherwise restore real project
// names into a field the schema was permitting anything in.
var permittedLabels = map[string]bool{
	"": true, "config": true, "projects": true,
	" 1: backend > nvim ": true, " 3: api > zsh ": true, " 3: herdr > codex ": true,
	" 4: tmux > zsh ": true, " 5: nvim > codex ": true, " 6: notes > nvim ": true,
	" 7: editor-plugin > node ": true, " 7: mobile-app > zsh ": true,
	" 8: desktop-app > Reel ": true, " 8: herdr > zsh ": true, " 9: dashboard > zsh ": true,
	" 10: website > zsh ": true,
}

func labelRule() rule {
	return rule{check: func(value any) error {
		if value == nil {
			return nil
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string, got %T", value)
		}
		if !permittedLabels[text] {
			return fmt.Errorf("label %q is not on the allowlist", text)
		}
		return nil
	}}
}

// fieldRules is the single source of truth: permitted keys AND their value rules.
var fieldRules = map[string]rule{
	// Identifiers.
	"workspace_id":         pattern(`^w[0-9]+$`),
	"tab_id":               pattern(`^w[0-9]+:t[0-9A-Za-z]+$`),
	"pane_id":              pattern(`^w[0-9]+:p[0-9A-Za-z]+$`),
	"focused_workspace_id": pattern(`^(w[0-9]+)?$`),
	"focused_tab_id":       pattern(`^(w[0-9]+:t[0-9A-Za-z]+)?$`),
	"focused_pane_id":      pattern(`^(w[0-9]+:p[0-9A-Za-z]+)?$`),
	"active_tab_id":        pattern(`^(w[0-9]+:t[0-9A-Za-z]+)?$`),
	"terminal_id":          pattern(`^term_[0-9]{12}$`),
	"value":                pattern(`^fixture-session-[0-9]{2}$`),
	"id":                   pattern(`^split_[0-9_]+[a-z0-9_]*$`),

	// Free text that can carry user data.
	"label":                   labelRule(),
	"title":                   oneOf("", "shell", "agent", "nvim", "Claude Code"),
	"terminal_title":          oneOf("", "herdr", "nvim", "backend", "Claude Code", "✳ Claude Code", "⠋ herdr", "⠦ herdr", "⠼ nvim", "notes: nvim", "backend: nvim", "herdr: nvim", "desktop-app: make run-debug", "✳ Add a health check endpoint", "✳ Review the retry policy", "✳ Review the API spec", "~/.config/tmux", "~/projects/api", "~/projects/dashboard", "~/projects/editor-plugin", "~/projects/mobile-app", "~/projects/website"),
	"terminal_title_stripped": oneOf("", "herdr", "nvim", "backend", "Claude Code", "notes: nvim", "backend: nvim", "herdr: nvim", "desktop-app: make run-debug", "Add a health check endpoint", "Review the retry policy", "Review the API spec", "~/.config/tmux", "~/projects/api", "~/projects/dashboard", "~/projects/editor-plugin", "~/projects/mobile-app", "~/projects/website"),

	// Paths and command lines. cmdline is the sharpest of these: it embeds absolute paths
	// and was previously unchecked entirely.
	"cwd":            pattern(`^(/Users/user(/[A-Za-z0-9._-]+)*|/opt/[^"]*|/usr/[^"]*|~/[^"]*)?$`),
	"foreground_cwd": pattern(`^(/Users/user(/[A-Za-z0-9._-]+)*|/opt/[^"]*|/usr/[^"]*|~/[^"]*)?$`),
	// A bare command name, or an absolute path under a permitted root, optionally
	// prefixed by an interpreter. The point is that no /Users path other than the
	// placeholder home can appear anywhere in the line.
	"cmdline": pattern(`^([a-z0-9._-]+|((/opt|/usr|/bin|/Users/user)/[A-Za-z0-9@._/-]*)|[a-z0-9._-]+ ((/opt|/usr|/bin|/Users/user)/[A-Za-z0-9@._/-]*))?$`),
	"argv":    structural("array of argv strings; each element checked as a scalar"),
	"argv0":   oneOf("", "node", "claude", "codex", "node codex", "context7-mcp", "mcp@latest", "nvim", "zsh", "Reel", "caffeinate"),
	"name":    oneOf("", "node", "claude", "codex", "node codex", "nvim", "zsh", "Reel", "2.1.232", "caffeinate", "htop"),
	"tty":     pattern(`^(/dev/ttys[0-9]+)?$`),

	// Enumerations Herdr defines.
	"agent":               oneOf("", "claude", "codex", "gemini", "cursor", "pi", "amp", "droid"),
	"display_agent":       oneOf("", "claude", "codex", "Claude Code"),
	"agent_status":        oneOf("", "idle", "working", "blocked", "done", "unknown"),
	"focused_pane_status": oneOf("", "idle", "working", "blocked", "done", "unknown"),
	"kind":                oneOf("id"),
	"source":              oneOf("herdr:claude", "herdr:codex"),
	"direction":           oneOf("down", "right"),
	"type":                oneOf("", "session_snapshot", "pane_process_info"),
	"version":             pattern(`^[0-9]+\.[0-9]+\.[0-9]+$`),

	// Metadata tokens: keys AND values.
	"tokens": tokenValues(),

	// Numbers and booleans.
	"number": numeric(), "pane_count": numeric(), "tab_count": numeric(),
	"protocol": numeric(), "pid": numeric(), "shell_pid": numeric(),
	"foreground_process_group_id": numeric(), "revision": numeric(),
	"state_change_seq": numeric(), "ratio": numeric(),
	"height": numeric(), "width": numeric(), "x": numeric(), "y": numeric(),
	"max_offset_from_bottom": numeric(), "offset_from_bottom": numeric(),
	"viewport_rows": numeric(),
	"focused":       boolean(), "zoomed": boolean(),
	"interactive_ready": boolean(), "launch_pending": boolean(),

	// Containers the walker descends into.
	"snapshot": structural("top-level wrapper"), "process_info": structural("wrapper"),
	"workspaces": structural("array"), "tabs": structural("array"),
	"panes": structural("array"), "layouts": structural("array"),
	"agents": structural("array"), "splits": structural("array"),
	"foreground_processes": structural("array"), "focused_processes": structural("array"),
	"state_labels":  structural("array of label strings"),
	"agent_session": structural("object"), "worktree": structural("object"),
	"rect": structural("object"), "area": structural("object"), "scroll": structural("object"),
}

// TestFixturesConformToAPositiveSchema is closed: an unrecognised key fails.
func TestFixturesConformToAPositiveSchema(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		walkFixture(t, entry.Name(), "$", document)
	}
}

// TestEveryPermittedKeyHasAValueRule is the guard on the guard. permittedKeys is derived
// from fieldRules, so this cannot fail by construction — it exists to fail loudly if
// someone reintroduces a separate key list, which is the drift that caused both this
// project's label gap and the companion plugin's token-value gap.
func TestEveryPermittedKeyHasAValueRule(t *testing.T) {
	for _, key := range permittedKeys() {
		entry, present := fieldRules[key]
		if !present {
			t.Errorf("key %q is permitted with no rule", key)
			continue
		}
		if entry.check == nil && entry.why == "" {
			t.Errorf("key %q has neither a value check nor a stated reason for having none", key)
		}
	}
}

func permittedKeys() []string {
	keys := make([]string, 0, len(fieldRules))
	for key := range fieldRules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func walkFixture(t *testing.T, file, path string, node any) {
	t.Helper()
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			entry, permitted := fieldRules[key]
			if !permitted {
				t.Errorf("%s %s.%s: unrecognised key — a new field may carry personal data; "+
					"classify it and add it to fieldRules", file, path, key)
				continue
			}
			if entry.check != nil {
				if err := entry.check(value); err != nil {
					t.Errorf("%s %s.%s: %v", file, path, key, err)
				}
			}
			if entry.leaf {
				continue
			}
			walkFixture(t, file, path+"."+key, value)
		}
	case []any:
		for index, item := range typed {
			walkFixture(t, file, fmt.Sprintf("%s[%d]", path, index), item)
		}
	}
}
