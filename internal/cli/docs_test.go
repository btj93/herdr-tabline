package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/btj93/herdr-tabline/internal/model"
)

// TestDocumentationSurface keeps README.md honest against the code rather than against a
// hand-maintained list. The context fields are discovered by reflection, so adding a field
// to model.Context fails this test until the README documents it.
func TestDocumentationSurface(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	t.Run("every context field is documented", func(t *testing.T) {
		for _, path := range contextFieldPaths() {
			if !strings.Contains(readme, path) {
				t.Errorf("README does not document context field %s", path)
			}
		}
	})

	t.Run("fields Herdr cannot supply stay undocumented", func(t *testing.T) {
		// These were inventions in an early draft of the spec. Documenting them again would
		// promise data the server never sends.
		for _, absent := range []string{".Workspace.Cwd", ".Shell.Name", ".Agent.Seen"} {
			if strings.Contains(readme, absent) {
				t.Errorf("README documents %s, which Herdr does not supply", absent)
			}
		}
		if strings.Contains(readme, "Worktree.Branch") || strings.Contains(readme, "worktree branch") {
			t.Error("README documents a worktree branch field, which does not exist")
		}
	})

	t.Run("every command is documented", func(t *testing.T) {
		// Require the command table row, not a bare substring: words like "start" and
		// "preview" appear throughout the prose, so a substring check passes even when the
		// command itself is undocumented.
		for _, command := range []string{"start", "stop", "rename-current", "validate-config", "preview", "version"} {
			if !strings.Contains(readme, "| `"+command+"` |") {
				t.Errorf("README has no command-table entry for %q", command)
			}
		}
	})

	t.Run("every mode is documented", func(t *testing.T) {
		for _, mode := range []string{"auto", "keybind", "off"} {
			if !strings.Contains(readme, "`"+mode+"`") {
				t.Errorf("README does not document the %q mode", mode)
			}
		}
	})

	t.Run("every helper is documented", func(t *testing.T) {
		for _, helper := range templateHelpers {
			if !strings.Contains(readme, "`"+helper+"`") {
				t.Errorf("README does not document the %q helper", helper)
			}
		}
	})

	t.Run("required facts are stated", func(t *testing.T) {
		for _, fact := range []string{
			"herdr-tabline.rename-current",
			"HERDR_PLUGIN_CONFIG_DIR",
			"HERDR_PLUGIN_STATE_DIR",
			"HERDR_SOCKET_PATH",
			"poll_interval",
			"refresh_debounce",
			"pane.updated",
			"events.subscribe",
			"macOS",
			"Linux",
			"make check",
		} {
			if !strings.Contains(readme, fact) {
				t.Errorf("README does not mention %q", fact)
			}
		}
	})

	t.Run("the styling limitation is stated", func(t *testing.T) {
		lowered := strings.ToLower(readme)
		if !strings.Contains(lowered, "color") {
			t.Error("README does not state the no-color limitation")
		}
	})

	t.Run("windows is not claimed", func(t *testing.T) {
		if strings.Contains(readme, "Windows support") {
			t.Error("README claims Windows support")
		}
	})

	t.Run("required sections are present", func(t *testing.T) {
		for _, heading := range []string{
			"## Requirements", "## Install", "## Quick start", "## Configuration",
			"## Profile precedence", "## Modes", "## Keybinding", "## Template variables",
			"## Helpers", "## Aliases and icons", "## Validation and preview", "## Hot reload",
			"## Refresh model", "## Tab reorder behavior", "## Session restore",
			"## Styling limitation", "## Troubleshooting", "## Live smoke test",
			"## Platform support", "## Development", "## Publication",
		} {
			if !strings.Contains(readme, heading) {
				t.Errorf("README is missing the %q section", heading)
			}
		}
	})

	t.Run("the owner appears only in the module path", func(t *testing.T) {
		// The module path is the single place the owner is allowed to appear, so a transfer
		// is one deliberate operation rather than a hunt through prose and badges.
		const owner = "btj93"
		for _, line := range strings.Split(readme, "\n") {
			if !strings.Contains(line, owner) {
				continue
			}
			if !strings.Contains(line, "github.com/btj93/herdr-tabline") {
				t.Errorf("owner appears outside a module path: %q", line)
			}
			if strings.Contains(line, "git clone") || strings.Contains(strings.ToLower(line), "badge") {
				t.Errorf("owner appears in a clone URL or badge: %q", line)
			}
		}
	})
}

func TestContributingDocumentsTheVerificationContract(t *testing.T) {
	contributing := readRepoFile(t, "CONTRIBUTING.md")
	for _, fact := range []string{"make check", "fake", "socket", "smoke test"} {
		if !strings.Contains(strings.ToLower(contributing), strings.ToLower(fact)) {
			t.Errorf("CONTRIBUTING.md does not cover %q", fact)
		}
	}
	lowered := strings.ToLower(contributing)
	if !strings.Contains(lowered, "must not") && !strings.Contains(lowered, "never") {
		t.Error("CONTRIBUTING.md does not prohibit live-session mutation in normal tests")
	}
}

func TestReleaseMetadata(t *testing.T) {
	license := readRepoFile(t, "LICENSE")
	for _, fact := range []string{"MIT", "2026", "Herdr Tabline contributors"} {
		if !strings.Contains(license, fact) {
			t.Errorf("LICENSE does not contain %q", fact)
		}
	}
	changelog := readRepoFile(t, "CHANGELOG.md")
	for _, fact := range []string{"0.1.0", "2026-08-21"} {
		if !strings.Contains(changelog, fact) {
			t.Errorf("CHANGELOG.md does not contain %q", fact)
		}
	}
}

func TestCIDoesNotClaimWindows(t *testing.T) {
	workflow := readRepoFile(t, filepath.Join(".github", "workflows", "ci.yml"))
	if strings.Contains(strings.ToLower(workflow), "windows") {
		t.Error("CI references Windows, which is not a supported platform")
	}
	// Assert the matrix covers each target rather than a literal `GOOS=linux` string: the
	// workflow sets GOOS/GOARCH from a matrix, and pinning the shell form would fail on a
	// refactor that still builds exactly the same four binaries.
	if !strings.Contains(workflow, "make check") {
		t.Error("CI does not run make check")
	}
	for _, key := range []string{"GOOS:", "GOARCH:"} {
		if !strings.Contains(workflow, key) {
			t.Errorf("CI does not set %s for the build job", key)
		}
	}
	for _, target := range []string{"linux", "darwin", "amd64", "arm64"} {
		if !strings.Contains(workflow, target) {
			t.Errorf("CI build matrix does not cover %q", target)
		}
	}
	if !strings.Contains(workflow, "refs/tags/v") {
		t.Error("CI does not gate artifact upload on version tags")
	}
}

// templateHelpers is the fixed helper set the renderer exposes.
var templateHelpers = []string{
	"alias", "basename", "cleanPath", "coalesce", "contains", "default", "dirname", "first",
	"formatTime", "hasPrefix", "hasSuffix", "homeRelative", "join", "last", "lower", "matches",
	"padLeft", "padRight", "replace", "statusIcon", "title", "trim", "truncate",
	"truncateMiddle", "upper",
}

// contextFieldPaths walks model.Context by reflection and returns every dotted template
// path a user can write, so the README cannot silently fall behind the model.
//
// Slices are leaves: a user ranges over `.Panes`, they cannot write `.Panes.ID`. Structs
// from outside the model package are leaves too — recursing into time.Time would yield
// only unexported fields and silently drop `.Now` from the required list.
func contextFieldPaths() []string {
	modelPkg := reflect.TypeOf(model.Context{}).PkgPath()
	var paths []string
	var walk func(prefix string, t reflect.Type)
	walk = func(prefix string, t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			path := prefix + "." + field.Name
			fieldType := field.Type
			for fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}
			if fieldType.Kind() == reflect.Struct && fieldType.PkgPath() == modelPkg {
				walk(path, fieldType)
				continue
			}
			paths = append(paths, path)
		}
	}
	walk("", reflect.TypeOf(model.Context{}))
	return paths
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(raw)
}
