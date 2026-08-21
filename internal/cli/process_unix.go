//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// SpawnDetached starts the daemon in its own session with stdout and stderr appended to
// the session log. Setsid matters: without it the child stays in Herdr's process group and
// dies with the invoking action, which is the opposite of starting a daemon.
func SpawnDetached(executable string, args []string, logPath string) error {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log %q: %w", logPath, err)
	}
	defer log.Close()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()

	command := exec.Command(executable, args...)
	command.Stdin = devNull
	command.Stdout = log
	command.Stderr = log
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s: %w", executable, err)
	}
	// Release rather than Wait: the daemon outlives this process by design, and leaving a
	// zombie would misreport the daemon as dead to anything scanning the process table.
	return command.Process.Release()
}
