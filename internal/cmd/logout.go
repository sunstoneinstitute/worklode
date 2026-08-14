package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// runLogout deletes the stored token for server from every store the CLI
// writes to — the keychain, and the fallback token file on a machine with no
// keychain. A missing entry is not an error.
func runLogout(server string) error {
	if server == "" {
		return errors.New(`server URL not set: pass --server or set LODE_SERVER`)
	}
	err := cli.DeleteToken(server)
	if err != nil && !errors.Is(err, cli.ErrTokenNotFound) {
		return err
	}
	return nil
}

func newLogoutCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored token for a server from the OS keychain or token file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := cli.LoadConfig()
			if err != nil {
				return err
			}
			if server != "" {
				cfg.ServerURL = server
			}
			if err := runLogout(cfg.ServerURL); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged out of %s\n", cfg.ServerURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "worklode server URL (overrides LODE_SERVER / config file)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newLogoutCmd())
}
