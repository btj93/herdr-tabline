package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/btj93/herdr-tabline/internal/collector"
	"github.com/btj93/herdr-tabline/internal/render"
)

// status prints one line summarising the session, for Herdr's ui.tab_bar_right to poll.
//
// It takes flags where the other commands take none, because tab_bar_right entries are UI
// configuration rather than plugin actions: Herdr may invoke them with a bare environment,
// so the socket and config directory cannot be assumed to arrive through it.
func (a *application) status(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "path to the Herdr control socket")
	configDir := flags.String("config", "", "directory holding config.toml")
	if err := flags.Parse(args); err != nil {
		return errUsage("status: %v", err)
	}
	if flags.NArg() > 0 {
		return errUsage("status takes no positional arguments")
	}

	resolvedSocket := firstNonEmpty(a.env.SocketPath, *socket, defaultSocketPath())
	if resolvedSocket == "" {
		return errUsage("cannot locate the Herdr socket: set HERDR_SOCKET_PATH or pass --socket")
	}
	if *configDir != "" {
		a.env.PluginConfigDir = *configDir
	}

	compiled, err := a.load()
	if err != nil {
		return err
	}
	client := a.deps.NewClient(resolvedSocket)
	defer client.Close()

	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		// Nothing goes to stdout on failure: whatever this prints lands in the user's tab
		// bar on every poll, so a dead socket must be silent there rather than noisy.
		return fmt.Errorf("snapshot: %w", err)
	}
	line, err := render.New(compiled.Aliases(), compiled.Icons()).
		ExecuteSession(compiled.Status, collector.BuildSession(snapshot, time.Now()), compiled.MaxWidth)
	if err != nil {
		return fmt.Errorf("render status: %w", err)
	}
	fmt.Fprintln(a.stdout, line)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// defaultSocketPath mirrors Herdr's own default location, used only when neither the
// environment nor a flag supplied one.
func defaultSocketPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "herdr", "herdr.sock")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}
