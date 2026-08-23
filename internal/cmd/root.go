// Package cmd defines the lode command-line interface.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/cli"
)

var rootCmd = &cobra.Command{
	Use:     "lode",
	Short:   "lode is the Sunstone Institute work tracker",
	Version: buildinfo.Version,
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

// printJSON writes v as the command's --json output. Used by the commands
// whose JSON shape is assembled client-side rather than passed through from
// the server.
func printJSON(cmd *cobra.Command, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	printRaw(cmd, b)
	return nil
}

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
