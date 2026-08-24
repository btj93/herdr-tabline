package collector_test

import (
	"testing"

	"github.com/btj93/herdr-tabline/internal/collector"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/model"
)

// TestAttachProcessClearsStaleScalars covers reusing one Context across two panes.
// No current caller does that — the controller builds fresh contexts every pass — but the
// function reads as a setter, and a caller who reuses a Context would silently inherit the
// previous pane's process, shell, and PGID leader wherever the new pane supplies none.
func TestAttachProcessClearsStaleScalars(t *testing.T) {
	shell := 4242
	pgid := 100
	context := model.Context{}
	collector.AttachProcess(&context, herdrapi.ProcessInfo{
		ForegroundProcessGroupID: &pgid,
		ForegroundProcesses: []herdrapi.Process{
			{PID: 100, Name: "node", Argv0: "claude"},
			{PID: 101, Name: "helper"},
		},
		ShellPID: &shell,
		TTY:      "/dev/ttys001",
	})
	if context.Process.Name != "node" || context.ProcessByPGID.Argv0 != "claude" {
		t.Fatalf("first attach did not populate: %#v", context)
	}

	// Second pane: no foreground processes, no PGID, no shell.
	collector.AttachProcess(&context, herdrapi.ProcessInfo{PaneID: "empty"})

	if context.Process.Name != "" {
		t.Errorf("Process retained %q from the previous pane", context.Process.Name)
	}
	if context.ProcessByPGID.Argv0 != "" {
		t.Errorf("ProcessByPGID retained %q from the previous pane", context.ProcessByPGID.Argv0)
	}
	if context.Shell.PID != 0 {
		t.Errorf("Shell.PID retained %d from the previous pane", context.Shell.PID)
	}
	if context.Shell.TTY != "" {
		t.Errorf("Shell.TTY retained %q from the previous pane", context.Shell.TTY)
	}
	if len(context.Processes) != 0 {
		t.Errorf("Processes retained %d entries", len(context.Processes))
	}
}
