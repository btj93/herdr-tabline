// Package cli implements the herdr-tabline command line and daemon lifecycle.
//
// Exit codes are part of the contract: 0 on success, 2 for usage and configuration
// problems the caller can fix, and 1 for runtime failures such as a dead socket. Herdr
// surfaces these to the user, so a misconfiguration must never look like a crash.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/btj93/herdr-tabline/internal/collector"
	"github.com/btj93/herdr-tabline/internal/config"
	"github.com/btj93/herdr-tabline/internal/controller"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/render"
	"github.com/btj93/herdr-tabline/internal/state"
)

const (
	// Version and SchemaVersion appear in the exact line the manifest and docs promise.
	Version       = "1.0.0"
	SchemaVersion = 1

	exitOK      = 0
	exitRuntime = 1
	exitUsage   = 2

	configFileName = "config.toml"
)

// Env is the narrow slice of the Herdr plugin environment this binary consumes.
type Env struct {
	SocketPath      string // HERDR_SOCKET_PATH
	PluginConfigDir string // HERDR_PLUGIN_CONFIG_DIR
	PluginStateDir  string // HERDR_PLUGIN_STATE_DIR
	TabID           string // HERDR_TAB_ID; nullable upstream, may be empty
	ContextJSON     string // HERDR_PLUGIN_CONTEXT_JSON
	Executable      string
}

// Deps are the injected seams. Tests substitute both; main wires the real ones.
type Deps struct {
	NewClient func(socket string) herdrapi.Client
	Spawn     func(executable string, args []string, logPath string) error
}

// usageError is a caller-fixable problem and maps to exit code 2.
type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

func errUsage(format string, args ...any) error {
	return usageError{message: fmt.Sprintf(format, args...)}
}

// Run dispatches one command and returns the process exit code.
func Run(args []string, env Env, stdin io.Reader, stdout, stderr io.Writer, deps Deps) int {
	_ = stdin
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText())
		return exitUsage
	}
	command, rest := args[0], args[1:]
	if len(rest) > 0 {
		fmt.Fprintf(stderr, "herdr-tabline: %s takes no arguments\n", command)
		return exitUsage
	}

	app := &application{env: env, deps: deps, stdout: stdout, stderr: stderr}
	var err error
	switch command {
	case "version":
		fmt.Fprintf(stdout, "herdr-tabline %s schema %d\n", Version, SchemaVersion)
		return exitOK
	case "validate-config":
		err = app.validateConfig()
	case "preview":
		err = app.preview()
	case "rename-current":
		err = app.renameCurrent()
	case "start":
		err = app.start()
	case "stop":
		err = app.stop()
	case "daemon":
		err = app.daemon()
	default:
		fmt.Fprintf(stderr, "herdr-tabline: unknown command %q\n\n%s", command, usageText())
		return exitUsage
	}
	if err != nil {
		fmt.Fprintf(stderr, "herdr-tabline: %v\n", err)
		var usage usageError
		if errors.As(err, &usage) {
			return exitUsage
		}
		return exitRuntime
	}
	return exitOK
}

func usageText() string {
	return "usage: herdr-tabline <start|stop|rename-current|validate-config|preview|version>\n"
}

type application struct {
	env    Env
	deps   Deps
	stdout io.Writer
	stderr io.Writer
}

// require reports the first missing environment variable by name, so a diagnostic tells
// the user exactly which one to set rather than that "something" is unset.
func (a *application) require(names ...string) error {
	for _, name := range names {
		var value string
		switch name {
		case "HERDR_SOCKET_PATH":
			value = a.env.SocketPath
		case "HERDR_PLUGIN_CONFIG_DIR":
			value = a.env.PluginConfigDir
		case "HERDR_PLUGIN_STATE_DIR":
			value = a.env.PluginStateDir
		}
		if value == "" {
			return errUsage("%s is not set", name)
		}
	}
	return nil
}

func (a *application) configPath() string {
	return filepath.Join(a.env.PluginConfigDir, configFileName)
}

func (a *application) validateConfig() error {
	if err := a.require("HERDR_PLUGIN_CONFIG_DIR"); err != nil {
		return err
	}
	compiled, found, err := config.Load(a.configPath())
	if err != nil {
		return errUsage("configuration %s is invalid: %v", a.configPath(), err)
	}
	source := a.configPath()
	if !found {
		source = "built-in defaults (no file at " + a.configPath() + ")"
	}
	fmt.Fprintf(a.stdout, "ok: %s\nmode: %s\npoll_interval: %s\nrefresh_debounce: %s\n",
		source, compiled.Mode, compiled.PollInterval, compiled.RefreshDebounce)
	return nil
}

func (a *application) preview() error {
	if err := a.require("HERDR_SOCKET_PATH", "HERDR_PLUGIN_CONFIG_DIR", "HERDR_PLUGIN_STATE_DIR"); err != nil {
		return err
	}
	compiled, err := a.load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	client := a.deps.NewClient(a.env.SocketPath)
	defer client.Close()

	tabID, err := a.resolveTabID(ctx, client)
	if err != nil {
		return err
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	history, err := a.store().Load()
	if err != nil {
		return err
	}
	contexts := collector.Build(snapshot, history, time.Now())
	for index := range contexts {
		if contexts[index].Tab.ID != tabID {
			continue
		}
		tabContext := contexts[index]
		if tabContext.Pane.ID != "" {
			info, err := client.ProcessInfo(ctx, tabContext.Pane.ID)
			if err != nil {
				return fmt.Errorf("process info for pane %s: %w", tabContext.Pane.ID, err)
			}
			collector.AttachProcess(&tabContext, info)
		}
		effective := compiled.Resolve(tabContext)
		tabContext.Profile = effective.ProfileName
		label, err := render.New(compiled.Aliases, compiled.Icons).
			Execute(effective.Template, tabContext, effective.MaxWidth)
		if err != nil {
			return fmt.Errorf("render: %w", err)
		}
		fmt.Fprintf(a.stdout, "profile: %s\nmode: %s\ntab: %s\nworkspace: %s\npane: %s\nlabel: %q\n",
			effective.ProfileName, effective.Mode, tabContext.Tab.ID,
			tabContext.Workspace.Label, tabContext.Pane.ID, label)
		return nil
	}
	return errUsage("tab %s is not present in the current snapshot", tabID)
}

func (a *application) renameCurrent() error {
	if err := a.require("HERDR_SOCKET_PATH", "HERDR_PLUGIN_CONFIG_DIR", "HERDR_PLUGIN_STATE_DIR"); err != nil {
		return err
	}
	ctx := context.Background()
	client := a.deps.NewClient(a.env.SocketPath)
	defer client.Close()

	tabID, err := a.resolveTabID(ctx, client)
	if err != nil {
		return err
	}
	manager, err := a.manager()
	if err != nil {
		return err
	}
	reconciler := controller.New(controller.Options{
		Client: client,
		Config: manager,
		Store:  a.store(),
		Logger: a.logger(slog.LevelWarn),
	})
	// The action path is the same reconciliation the daemon runs, scoped to one tab.
	return reconciler.Reconcile(ctx, controller.TriggerAction, tabID)
}

func (a *application) start() error {
	if err := a.require("HERDR_SOCKET_PATH", "HERDR_PLUGIN_STATE_DIR"); err != nil {
		return err
	}
	if a.env.Executable == "" {
		return errUsage("cannot locate this executable to start the daemon")
	}
	paths := state.NewPaths(a.env.PluginStateDir, a.env.SocketPath)
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return fmt.Errorf("create state directory %q: %w", paths.Root, err)
	}
	// A daemon already holding the session lock is success, not an error: Herdr invokes
	// start from both the startup hook and a pane.created event.
	if pid, err := state.NewDaemonLock(a.env.PluginStateDir, a.env.SocketPath).PID(); err == nil && pid != 0 {
		if running, _ := daemonIsRunning(a.env.PluginStateDir, a.env.SocketPath); running {
			fmt.Fprintf(a.stdout, "already running (pid %d)\n", pid)
			return nil
		}
	}
	if err := a.deps.Spawn(a.env.Executable, []string{"daemon"}, paths.Log); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	fmt.Fprintf(a.stdout, "started; logging to %s\n", paths.Log)
	return nil
}

func (a *application) stop() error {
	if err := a.require("HERDR_SOCKET_PATH", "HERDR_PLUGIN_STATE_DIR"); err != nil {
		return err
	}
	lock := state.NewDaemonLock(a.env.PluginStateDir, a.env.SocketPath)
	signaled, err := lock.SignalStop()
	if err != nil {
		return err
	}
	if !signaled {
		fmt.Fprintln(a.stdout, "no running daemon for this session")
		return nil
	}
	fmt.Fprintln(a.stdout, "stop signal sent")
	return nil
}

func (a *application) daemon() error {
	if err := a.require("HERDR_SOCKET_PATH", "HERDR_PLUGIN_CONFIG_DIR", "HERDR_PLUGIN_STATE_DIR"); err != nil {
		return err
	}
	lock := state.NewDaemonLock(a.env.PluginStateDir, a.env.SocketPath)
	if err := lock.Acquire(); err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	manager, err := a.manager()
	if err != nil {
		return err
	}
	client := a.deps.NewClient(a.env.SocketPath)
	defer client.Close()

	logger := a.logger(slog.LevelInfo)
	reconciler := controller.New(controller.Options{
		Client: client,
		Config: manager,
		Store:  a.store(),
		Logger: logger,
	})
	runner := controller.NewRunner(controller.RunnerOptions{
		Reconciler: reconciler,
		Client:     client,
		Config:     manager,
		Logger:     logger,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	logger.Info("herdr-tabline daemon started", "pid", os.Getpid(), "socket", a.env.SocketPath)
	return runner.Run(ctx)
}

// resolveTabID walks the documented chain and only fails when every source is empty.
func (a *application) resolveTabID(ctx context.Context, client herdrapi.Client) (string, error) {
	if a.env.TabID != "" {
		return a.env.TabID, nil
	}
	if id := tabIDFromContextJSON(a.env.ContextJSON); id != "" {
		return id, nil
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	if snapshot.FocusedTabID != "" {
		return snapshot.FocusedTabID, nil
	}
	return "", errUsage("no tab: set HERDR_TAB_ID, or invoke from a tab context (HERDR_PLUGIN_CONTEXT_JSON)")
}

// tabIDFromContextJSON is deliberately forgiving. A malformed or extended payload is
// skipped so a future Herdr schema addition cannot break the command.
func tabIDFromContextJSON(raw string) string {
	if raw == "" {
		return ""
	}
	var payload struct {
		TabID *string `json:"tab_id"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.TabID == nil {
		return ""
	}
	return *payload.TabID
}

func (a *application) load() (*config.Compiled, error) {
	compiled, _, err := config.Load(a.configPath())
	if err != nil {
		return nil, errUsage("configuration %s is invalid: %v", a.configPath(), err)
	}
	return compiled, nil
}

func (a *application) manager() (*config.Manager, error) {
	manager := config.NewManager(a.configPath())
	if _, err := manager.ReloadIfChanged(); err != nil {
		return nil, errUsage("configuration %s is invalid: %v", a.configPath(), err)
	}
	if _, ok := manager.Current(); !ok {
		return nil, errUsage("configuration %s could not be loaded", a.configPath())
	}
	return manager, nil
}

func (a *application) store() *state.Store {
	return state.NewStore(a.env.PluginStateDir, a.env.SocketPath)
}

func (a *application) logger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))
}

// daemonIsRunning reports whether this session's lock is held, without disturbing it.
func daemonIsRunning(stateDir, socket string) (bool, error) {
	lock := state.NewDaemonLock(stateDir, socket)
	pid, err := lock.PID()
	if err != nil || pid == 0 {
		return false, err
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false, nil
	}
	return true, nil
}
