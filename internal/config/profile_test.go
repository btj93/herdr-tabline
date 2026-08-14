package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btj93/herdr-tabline/internal/config"
	"github.com/btj93/herdr-tabline/internal/model"
)

func TestResolveAppliesMatchingProfilesInDeclarationOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
template = "base"
mode = "off"
[[profiles]]
name = "template-first"
template = "first"
[profiles.match]
process_regex = ["^go$"]
[[profiles]]
name = "mode-second"
mode = "keybind"
[profiles.match]
cwd_glob = ["/work/**"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	effective := cfg.Resolve(model.Context{Process: model.Process{Name: "go"}, Pane: model.Pane{EffectiveCwd: "/work/project"}})
	if got := effective.Template.Source(); got != "first" {
		t.Fatalf("template = %q", got)
	}
	if effective.Mode != config.ModeKeybind || effective.ProfileName != "mode-second" {
		t.Fatalf("effective = %#v", effective)
	}
}

func TestResolveUsesMatcherCategoriesAsAndAndPatternsAsOr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[[profiles]]
name = "matched"
mode = "off"
[profiles.match]
workspace_label_regex = ["^one$", "^two$"]
agent_status = ["working", "idle"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := model.Context{Workspace: model.Workspace{Label: "two"}, Agent: model.Agent{Status: "idle"}}
	if got := cfg.Resolve(ctx); got.Mode != config.ModeOff || got.ProfileName != "matched" {
		t.Fatalf("matched = %#v", got)
	}
	ctx.Agent.Status = "blocked"
	if got := cfg.Resolve(ctx); got.Mode != config.ModeAuto || got.ProfileName != "default" {
		t.Fatalf("unmatched = %#v", got)
	}
}
