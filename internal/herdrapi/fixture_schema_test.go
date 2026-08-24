package herdrapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// permittedKeys is every JSON key a fixture may contain. This is the load-bearing half of
// the guard: an unrecognised key fails, so a future Herdr field arrives as a test failure
// demanding review rather than as data nobody looked at.
var permittedKeys = map[string]bool{
	"active_tab_id": true, "agent": true, "agent_session": true, "agent_status": true,
	"agents": true, "area": true, "argv": true, "argv0": true, "cmdline": true, "cwd": true,
	"direction": true, "display_agent": true, "focused": true, "focused_pane_id": true,
	"focused_pane_status": true, "focused_processes": true, "focused_tab_id": true,
	"focused_workspace_id": true, "foreground_cwd": true, "foreground_process_group_id": true,
	"foreground_processes": true, "height": true, "id": true, "interactive_ready": true,
	"kind": true, "label": true, "launch_pending": true, "layouts": true,
	"max_offset_from_bottom": true, "name": true, "number": true, "offset_from_bottom": true,
	"pane_count": true, "pane_id": true, "panes": true, "pid": true, "process_info": true,
	"protocol": true, "ratio": true, "rect": true, "revision": true, "scroll": true,
	"shell_pid": true, "snapshot": true, "source": true, "splits": true,
	"state_change_seq": true, "state_labels": true, "tab_count": true, "tab_id": true,
	"tabs": true, "terminal_id": true, "terminal_title": true, "terminal_title_stripped": true,
	"title": true, "tokens": true, "tty": true, "type": true, "value": true, "version": true,
	"viewport_rows": true, "width": true, "workspace_id": true, "workspaces": true,
	"worktree": true, "x": true, "y": true, "zoomed": true,
}

// identifierForms is a POSITIVE schema: for each identifier-bearing key, the shapes its
// value is permitted to take. Anything else fails.
//
// This replaces an earlier scan for known-bad shapes. That approach cannot work, and we
// proved it twice: a known-bad scan cannot distinguish a leak from its own fix, and two
// independently written scanners each flagged the other project's placeholders as leaks
// while missing shapes they had not anticipated. Enumerating what is ALLOWED has no such
// blind spot — an identifier in a form nobody predicted simply is not on the list.
var identifierForms = map[string]*regexp.Regexp{
	"workspace_id":         regexp.MustCompile(`^w[0-9]+$`),
	"tab_id":               regexp.MustCompile(`^w[0-9]+:t[0-9A-Za-z]+$`),
	"pane_id":              regexp.MustCompile(`^w[0-9]+:p[0-9A-Za-z]+$`),
	"focused_tab_id":       regexp.MustCompile(`^(w[0-9]+:t[0-9A-Za-z]+)?$`),
	"focused_pane_id":      regexp.MustCompile(`^(w[0-9]+:p[0-9A-Za-z]+)?$`),
	"focused_workspace_id": regexp.MustCompile(`^(w[0-9]+)?$`),
	"active_tab_id":        regexp.MustCompile(`^(w[0-9]+:t[0-9A-Za-z]+)?$`),
	"terminal_id":          regexp.MustCompile(`^term_[0-9]{12}$`),
	// agent_session.value: the sanitized placeholder form only.
	"value": regexp.MustCompile(`^fixture-session-[0-9]{2}$`),
}

// permittedLabels is an allowlist of exact label values.
//
// A label is a permitted KEY with an unconstrained VALUE until this exists, which is the
// same gap in miniature: the guard knew the field and did not check it. Tab labels embed
// directory and process names verbatim, so re-capturing a fixture would restore real
// project names here and nothing would object. Exact values force a review on every change.
var permittedLabels = map[string]bool{
	"": true, "config": true, "projects": true,
	" 1: backend > nvim ": true, " 3: api > zsh ": true, " 3: herdr > codex ": true,
	" 4: tmux > zsh ": true, " 5: nvim > codex ": true, " 6: notes > nvim ": true,
	" 7: editor-plugin > node ": true, " 7: mobile-app > zsh ": true,
	" 8: desktop-app > Reel ": true, " 8: herdr > zsh ": true, " 9: dashboard > zsh ": true,
	" 10: website > zsh ": true,
}

// pathForms constrains every filesystem path to the placeholder home.
var pathForms = regexp.MustCompile(`^(/Users/user(/|$)|/opt/|/usr/|/bin/|/dev/|~/|$)`)

var pathKeys = map[string]bool{"cwd": true, "foreground_cwd": true}

// TestFixturesConformToAPositiveSchema asserts what fixtures may contain rather than
// scanning for what they must not.
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
		walkFixture(t, entry.Name(), "", document)
	}
}

func walkFixture(t *testing.T, file, path string, node any) {
	t.Helper()
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if !permittedKeys[key] {
				t.Errorf("%s%s: unrecognised key %q — a new field may carry personal data; "+
					"review it and add it to permittedKeys", file, path, key)
				continue
			}
			if text, ok := value.(string); ok {
				if form, guarded := identifierForms[key]; guarded && !form.MatchString(text) {
					t.Errorf("%s%s: %s = %q does not match its permitted form %s",
						file, path, key, text, form)
				}
				if key == "label" && !permittedLabels[text] {
					t.Errorf("%s%s: label %q is not on the allowlist — labels embed directory "+
						"and process names; sanitize it or add it", file, path, text)
				}
				if pathKeys[key] && !pathForms.MatchString(text) {
					t.Errorf("%s%s: %s = %q is not under the placeholder home",
						file, path, key, text)
				}
			}
			walkFixture(t, file, path+"."+key, value)
		}
	case []any:
		for index, item := range typed {
			walkFixture(t, file, path+"[]", item)
			_ = index
		}
	}
}
