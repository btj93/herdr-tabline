package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/config"
	"github.com/btj93/herdr-tabline/internal/model"
	"github.com/btj93/herdr-tabline/internal/render"
)

func TestMissingConfigUsesCompatibilityDefaults(t *testing.T) {
	cfg, found, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	effective := cfg.Resolve(model.Context{})
	// The default template names the detected agent when there is one and the foreground
	// process otherwise; see TestDefaultTemplateNamesTheAgentNotItsRuntime for why the
	// process alone is not sufficient.
	source := effective.Template.Source()
	if effective.Mode != config.ModeAuto {
		t.Fatalf("default mode = %q", effective.Mode)
	}
	for _, fragment := range []string{".Tab.Number", ".Pane.Directory", ".Agent.Active", ".Process.Name"} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("default template %q is missing %s", source, fragment)
		}
	}
	if cfg.PollInterval != 2*time.Second || cfg.RefreshDebounce != 80*time.Millisecond {
		t.Fatalf("timings = %s, %s", cfg.PollInterval, cfg.RefreshDebounce)
	}
}

func TestMissingConfigSeedsDocumentedAgentStatusIcons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`template = "{{ statusIcon .Agent.Status }}"`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	label, err := render.New(nil, nil).Execute(cfg.Resolve(model.Context{Agent: model.Agent{Status: "working"}}).Template, model.Context{Agent: model.Agent{Status: "working"}}, 0)
	// Herdr's "distinct symbols" glyph for working; see TestDefaultStatusIconsMatchHerdrSymbols.
	if err != nil || label != "◐" {
		t.Fatalf("label=%q err=%v", label, err)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{"unknown key", "unrecognized = true"},
		{"unsupported colors", "[colors]\\nforeground = \\\"red\\\""},
		{"schema", "schema_version = 2"},
		{"explicit schema zero", "schema_version = 0"},
		{"mode", `mode = "manual"`},
		{"explicit empty mode", `mode = ""`},
		{"short poll", `poll_interval = "50ms"`},
		{"long poll", `poll_interval = "2m"`},
		{"long debounce", `refresh_debounce = "3s"`},
		{"large width", "max_width = 1025"},
		{"duplicate profiles", "[[profiles]]\\nname = \\\"same\\\"\\n[[profiles]]\\nname = \\\"same\\\""},
		{"invalid regex", "[[profiles]]\\nname = \\\"bad\\\"\\n[profiles.match]\\nprocess_regex = [\\\"[\\\"]"},
		{"invalid glob", "[[profiles]]\\nname = \\\"bad\\\"\\n[profiles.match]\\ncwd_glob = [\\\"[\\\"]"},
		{"invalid template", `template = "{{ if }}"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := config.Load(path)
			if err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestManagerRetainsLastKnownGoodConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	m := config.NewManager(path)

	changed, err := m.ReloadIfChanged()
	if err != nil || !changed {
		t.Fatalf("missing reload changed=%v err=%v", changed, err)
	}
	defaults, ok := m.Current()
	if !ok {
		t.Fatal("missing defaults")
	}

	writeConfig(t, path, `template = "valid"`)
	changed, err = m.ReloadIfChanged()
	if err != nil || !changed {
		t.Fatalf("valid reload changed=%v err=%v", changed, err)
	}
	valid, ok := m.Current()
	if !ok || valid == defaults {
		t.Fatal("valid replacement missing")
	}

	writeConfig(t, path, `template = "{{ if }}"`)
	changed, err = m.ReloadIfChanged()
	if changed || err == nil {
		t.Fatalf("invalid reload changed=%v err=%v", changed, err)
	}
	current, ok := m.Current()
	if !ok || current != valid {
		t.Fatal("invalid reload replaced good config")
	}
	changed, err = m.ReloadIfChanged()
	if changed || err != nil {
		t.Fatalf("duplicate invalid reload changed=%v err=%v", changed, err)
	}

	writeConfig(t, path, `template = "corrected"`)
	changed, err = m.ReloadIfChanged()
	if err != nil || !changed {
		t.Fatalf("corrected reload changed=%v err=%v", changed, err)
	}
	corrected, ok := m.Current()
	if !ok || corrected == valid {
		t.Fatal("corrected config not installed")
	}
}

func TestManagerHasNoCurrentConfigAfterInvalidInitialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, path, `mode = "manual"`)
	m := config.NewManager(path)
	changed, err := m.ReloadIfChanged()
	if changed || err == nil {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, ok := m.Current(); ok {
		t.Fatal("invalid initial config became current")
	}
}

func TestManagerSupportsConcurrentReadsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, path, `template = "initial"`)
	m := config.NewManager(path)
	if changed, err := m.ReloadIfChanged(); err != nil || !changed {
		t.Fatalf("initial reload changed=%v err=%v", changed, err)
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if cfg, ok := m.Current(); ok {
						_ = cfg.Resolve(model.Context{})
					}
				}
			}
		}()
	}

	for index := 0; index < 20; index++ {
		writeConfig(t, path, `template = "valid"`)
		if changed, err := m.ReloadIfChanged(); err != nil || !changed {
			t.Fatalf("valid reload changed=%v err=%v", changed, err)
		}
		writeConfig(t, path, `template = "{{ if }}"`)
		if changed, err := m.ReloadIfChanged(); err == nil || changed {
			t.Fatalf("invalid reload changed=%v err=%v", changed, err)
		}
		writeConfig(t, path, `template = "corrected"`)
		if changed, err := m.ReloadIfChanged(); err != nil || !changed {
			t.Fatalf("corrected reload changed=%v err=%v", changed, err)
		}
	}
	close(stop)
	readers.Wait()
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)), 0o600); err != nil {
		t.Fatal(err)
	}
}
