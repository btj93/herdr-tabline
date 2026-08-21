// Command herdr-tabline renders Herdr tab labels from templates and profiles.
package main

import (
	"os"

	"github.com/btj93/herdr-tabline/internal/cli"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		// Fall back to argv[0]; only `start` needs this, and it reports its own diagnostic.
		executable = os.Args[0]
	}
	env := cli.Env{
		SocketPath:      os.Getenv("HERDR_SOCKET_PATH"),
		PluginConfigDir: os.Getenv("HERDR_PLUGIN_CONFIG_DIR"),
		PluginStateDir:  os.Getenv("HERDR_PLUGIN_STATE_DIR"),
		TabID:           os.Getenv("HERDR_TAB_ID"),
		ContextJSON:     os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"),
		Executable:      executable,
	}
	os.Exit(cli.Run(os.Args[1:], env, os.Stdin, os.Stdout, os.Stderr, cli.Deps{
		NewClient: herdrapi.NewUnixClient,
		Spawn:     cli.SpawnDetached,
	}))
}
