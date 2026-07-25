// Package cmd defines the lode command-line interface.
package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// version is stamped at build time with
// -ldflags "-X github.com/sunstoneinstitute/worklode/internal/cmd.version=X.Y.Z"
// (see the Homebrew formula). It must stay a package-level var: the linker can
// only rewrite a symbol, not a struct literal field.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "lode",
	Short:   "lode is the Sunstone Institute work tracker",
	Version: version,
	// SilenceUsage/SilenceErrors: main.go already prints the error returned
	// by Execute() and exits 1. Without these, cobra additionally prints
	// "Error: ..." itself and dumps a full usage block for every runtime
	// error (e.g. a 404 from the server), which drowns the one line that
	// actually matters.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "print the raw JSON response instead of a table")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// jsonOut reports whether --json was passed to cmd (or an ancestor of it).
func jsonOut(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// newAPIClient loads the client config (LODE_SERVER/LODE_TOKEN env vars override
// a repo-local .worklode/config.toml, which overrides
// ~/.config/worklode/config.toml) and returns a ready-to-use Client, or an error
// telling the user how to configure the server URL.
func newAPIClient() (*cli.Client, error) {
	c, _, err := newAPIClientWithConfig()
	return c, err
}

// newAPIClientWithConfig is newAPIClient plus the config it was built from,
// for commands that also read config values such as current_project.
func newAPIClientWithConfig() (*cli.Client, cli.Config, error) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return nil, cli.Config{}, err
	}
	if cfg.ServerURL == "" {
		return nil, cli.Config{}, errors.New(`server URL not set: set LODE_SERVER, or add server = "https://..." to ~/.config/worklode/config.toml`)
	}
	return cli.NewClient(cfg), cfg, nil
}

// resolveProject returns the project a command should act on: the --project
// flag when it was passed (even as an empty string, which means "all
// projects"), otherwise the configured current_project.
func resolveProject(cmd *cobra.Command, flag, currentProject string) string {
	if cmd.Flags().Changed("project") {
		return flag
	}
	return currentProject
}

// projectFlagUsage suffixes a --project flag's help with where its default
// comes from.
const projectFlagUsage = " (default: current_project from config)"

// printRaw writes a raw JSON response body to cmd's stdout, adding a
// trailing newline if the body doesn't already end with one. Used by every
// command's --json path. A nil/empty raw (e.g. a 204 response) prints nothing.
func printRaw(cmd *cobra.Command, raw []byte) {
	if len(raw) == 0 {
		return
	}
	out := cmd.OutOrStdout()
	out.Write(raw)
	if raw[len(raw)-1] != '\n' {
		fmt.Fprintln(out)
	}
}
