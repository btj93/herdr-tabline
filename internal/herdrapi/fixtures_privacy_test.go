package herdrapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFixturesCarryNoPersonalData is a release guard, not a behaviour test.
//
// These fixtures were recorded from a live session. A recording carries the operator's
// username, absolute home paths, real project directory names, and — the one that is
// easiest to miss — terminal titles, which coding agents set to a summary of whatever the
// user is actually working on. Re-recording a fixture without sanitizing it would publish
// all of that. This test fails loudly when that happens.
func TestFixturesCarryNoPersonalData(t *testing.T) {
	// Only this home directory may appear. Any other /Users/<name> is a real account.
	homePath := regexp.MustCompile(`/Users/(?:[^u/"]|u(?:[^s/"]|s(?:[^e/"]|e(?:[^r/"]|r[^/"]))))[^"]*`)

	// Terminal titles are the highest-risk field. Anything not on this list is either new
	// personal data or a deliberate addition that belongs on the list.
	allowedTitles := map[string]bool{
		"": true, "herdr": true, "nvim": true, "backend": true, "Claude Code": true,
		"✳ Claude Code": true, "⠋ herdr": true, "⠦ herdr": true, "⠼ nvim": true,
		"notes: nvim": true, "backend: nvim": true, "herdr: nvim": true,
		"desktop-app: make run-debug":   true,
		"✳ Add a health check endpoint": true, "Add a health check endpoint": true,
		"✳ Review the retry policy": true, "Review the retry policy": true,
		"✳ Review the API spec": true, "Review the API spec": true,
		// Sanitized directory titles; the project names here are already placeholders.
		"~/.config/tmux": true, "~/projects/api": true, "~/projects/dashboard": true,
		"~/projects/editor-plugin": true, "~/projects/mobile-app": true,
		"~/projects/website": true,
	}

	liveUUID := regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	terminalID := regexp.MustCompile(`term_[0-9a-f]+`)
	placeholderTerminalID := regexp.MustCompile(`^term_0{6}\d{6}$`)

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
		if match := homePath.Find(raw); match != nil {
			t.Errorf("%s contains a real home path: %s", entry.Name(), match)
		}
		// Opaque live identifiers: session UUIDs and terminal handles. These carry no
		// username, path, or task text, so the rules above do not see them — which is
		// exactly how they survived two earlier sweeps.
		if match := liveUUID.Find(raw); match != nil {
			t.Errorf("%s contains a live session UUID: %s", entry.Name(), match)
		}
		for _, id := range terminalID.FindAllString(string(raw), -1) {
			if !placeholderTerminalID.MatchString(id) {
				t.Errorf("%s contains a live terminal id %q; use the term_00000000000N form",
					entry.Name(), id)
			}
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		for _, title := range collectTitles(document) {
			if !allowedTitles[title] {
				t.Errorf("%s has an unapproved terminal title %q — terminal titles carry "+
					"in-flight task descriptions; sanitize it or add it to the allowlist",
					entry.Name(), title)
			}
		}
	}
}

func collectTitles(node any) []string {
	var found []string
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if key == "title" || key == "terminal_title" || key == "terminal_title_stripped" {
				if text, ok := value.(string); ok {
					found = append(found, text)
				}
				continue
			}
			found = append(found, collectTitles(value)...)
		}
	case []any:
		for _, item := range typed {
			found = append(found, collectTitles(item)...)
		}
	}
	return found
}
