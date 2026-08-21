package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusRendersTheAttentionSummary(t *testing.T) {
	h := newHarness(t)
	h.client.withAgents(
		agentSpec{"p1", "w1", "claude", "blocked"},
		agentSpec{"p2", "w1", "claude", "working"},
		agentSpec{"p3", "w1", "codex", "working"},
		agentSpec{"p4", "w1", "claude", "done"},
	)
	if code := h.run("status"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	out := strings.TrimRight(h.stdout.String(), "\n")
	for _, want := range []string{"×1", "◐2", "✓1", "main"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "\n") {
		t.Fatalf("status printed multiple lines: %q", out)
	}
}

// TestStatusOmitsEmptyCounts keeps the right edge quiet when nothing needs attention.
func TestStatusOmitsEmptyCounts(t *testing.T) {
	h := newHarness(t)
	h.client.withAgents(agentSpec{"p1", "w1", "claude", "idle"})
	if code := h.run("status"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	out := h.stdout.String()
	for _, absent := range []string{"×", "◐", "✓"} {
		if strings.Contains(out, absent) {
			t.Fatalf("status = %q, should omit the empty %q count", out, absent)
		}
	}
	if !strings.Contains(out, "main") {
		t.Fatalf("status = %q, want the workspace name", out)
	}
}

// TestStatusResolvesTheSocketWithoutEnvironment matters because ui.tab_bar_right commands
// are UI config rather than plugin actions, so they may run with a bare environment.
func TestStatusResolvesTheSocketWithoutEnvironment(t *testing.T) {
	h := newHarness(t)
	socket := h.env.SocketPath
	h.env.SocketPath = "" // simulate Herdr not injecting it
	if code := h.run("status", "--socket", socket); code != 0 {
		t.Fatalf("exit = %d with an explicit --socket, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "main") {
		t.Fatalf("status = %q", h.stdout.String())
	}
}

func TestStatusAcceptsAnExplicitConfigDirectory(t *testing.T) {
	h := newHarness(t)
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "config.toml"),
		"schema_version = 1\n[status]\ntemplate = 'AGENTS={{ .Agents }}'\n")
	h.env.PluginConfigDir = ""
	h.client.withAgents(agentSpec{"p1", "w1", "claude", "idle"})
	if code := h.run("status", "--config", other); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if got := strings.TrimSpace(h.stdout.String()); got != "AGENTS=1" {
		t.Fatalf("status = %q, want the configured template", got)
	}
}

// TestStatusFailsQuietlyWhenTheSocketIsDown keeps a dead daemon from writing noise into the
// user's tab bar on every poll.
func TestStatusFailsQuietlyWhenTheSocketIsDown(t *testing.T) {
	h := newHarness(t)
	h.client.snapshotErr = errSocketGone
	code := h.run("status")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if h.stdout.Len() != 0 {
		t.Fatalf("status wrote %q to stdout on failure; the tab bar would render it", h.stdout.String())
	}
}
