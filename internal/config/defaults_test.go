package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btj93/herdr-tabline/internal/config"
	"github.com/btj93/herdr-tabline/internal/model"
	"github.com/btj93/herdr-tabline/internal/render"
)

// TestDefaultStatusIconsMatchHerdrSymbols pins the icons to Herdr's own "distinct symbols"
// set, extracted from the herdr binary. Herdr's other indicator mode, "color dots", renders
// blocked, working, and done as the same glyph distinguished only by colour — which a tab
// label cannot express, since tab.rename takes plain text. So the symbols set is used
// unconditionally rather than mirroring the user's status_indicators setting.
func TestDefaultStatusIconsMatchHerdrSymbols(t *testing.T) {
	compiled := loadDefaults(t)
	want := map[string]string{
		"blocked": "×", // ×
		"working": "◐", // ◐
		"done":    "✓", // ✓
		"idle":    "○", // ○
		"unknown": "·", // ·
	}
	got := compiled.Icons["agent_status"]
	for status, icon := range want {
		if got[status] != icon {
			t.Errorf("icon for %q = %q, want %q", status, got[status], icon)
		}
	}
}

func TestStatusIconsRemainIndividuallyOverridable(t *testing.T) {
	compiled := loadConfig(t, "schema_version = 1\n[icons.agent_status]\nworking = \"🔥\"\n")
	icons := compiled.Icons["agent_status"]
	if icons["working"] != "🔥" {
		t.Fatalf("override ignored: working = %q", icons["working"])
	}
	// Unlisted statuses keep the Herdr-matching defaults rather than being blanked.
	if icons["blocked"] != "×" {
		t.Fatalf("overriding one icon disturbed another: blocked = %q", icons["blocked"])
	}
}

// TestDefaultTemplateNamesTheAgentNotItsRuntime is the reason this change exists.
// Herdr's own detection is the only source correct for every agent: the first foreground
// record reads "codex" correctly but "node"/"caffeinate" for Claude, while the
// process-group leader reads "claude" correctly but "node" for Codex.
func TestDefaultTemplateNamesTheAgentNotItsRuntime(t *testing.T) {
	compiled := loadDefaults(t)
	engine := render.New(compiled.Aliases, compiled.Icons)

	claude := model.Context{
		Tab:     model.Tab{Number: 1},
		Pane:    model.Pane{Directory: "herdr"},
		Process: model.Process{Name: "caffeinate"},
		Agent:   model.Agent{Active: true, Kind: "claude", Status: "working"},
	}
	label, err := engine.Execute(compiled.Template, claude, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := " 1: herdr > ◐ claude "; label != want {
		t.Fatalf("claude label = %q, want %q", label, want)
	}

	codex := model.Context{
		Tab:     model.Tab{Number: 2},
		Pane:    model.Pane{Directory: "api"},
		Process: model.Process{Name: "node"},
		Agent:   model.Agent{Active: true, Kind: "codex", Status: "idle"},
	}
	label, err = engine.Execute(compiled.Template, codex, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := " 2: api > ○ codex "; label != want {
		t.Fatalf("codex label = %q, want %q", label, want)
	}
}

// TestDefaultTemplateFallsBackToTheProcessWithoutAnAgent keeps every non-agent tab
// rendering exactly as it did before this change.
func TestDefaultTemplateFallsBackToTheProcessWithoutAnAgent(t *testing.T) {
	compiled := loadDefaults(t)
	engine := render.New(compiled.Aliases, compiled.Icons)
	shell := model.Context{
		Tab:     model.Tab{Number: 3},
		Pane:    model.Pane{Directory: "tmux"},
		Process: model.Process{Name: "zsh"},
	}
	label, err := engine.Execute(compiled.Template, shell, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := " 3: tmux > zsh "; label != want {
		t.Fatalf("non-agent label = %q, want %q", label, want)
	}
}

// TestDefaultTemplatePrefersDisplayAgentWhenHerdrSuppliesOne lets Herdr's own naming win
// over the bare kind when it has something friendlier.
func TestDefaultTemplatePrefersDisplayAgentWhenHerdrSuppliesOne(t *testing.T) {
	compiled := loadDefaults(t)
	engine := render.New(compiled.Aliases, compiled.Icons)
	ctx := model.Context{
		Tab:   model.Tab{Number: 1},
		Pane:  model.Pane{Directory: "web"},
		Agent: model.Agent{Active: true, Kind: "claude", DisplayName: "Claude Code", Status: "done"},
	}
	label, err := engine.Execute(compiled.Template, ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(label, "Claude Code") {
		t.Fatalf("label = %q, want the display name", label)
	}
}

func loadDefaults(t *testing.T) *config.Compiled {
	t.Helper()
	return loadConfig(t, "schema_version = 1\n")
}

func loadConfig(t *testing.T, body string) *config.Compiled {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	compiled, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}
