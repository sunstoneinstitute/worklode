package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newLoginCmd() *cobra.Command {
	var server string
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to worklode and store a token",
		Long: "Open a browser to sign in with Keycloak, then store the resulting\n" +
			"30-day token in the OS keychain. Re-run after it expires.\n\n" +
			"When no browser can be opened here — no opener installed, or no\n" +
			"display, as over SSH — lode prints a URL to open anywhere instead and\n" +
			"waits for the one-time code that page shows. --no-browser asks for that\n" +
			"directly.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := cli.LoadConfig()
			if err != nil {
				return err
			}
			if server != "" {
				cfg.ServerURL = server
			}
			if cfg.ServerURL == "" {
				return errors.New(`server URL not set: pass --server, set LODE_SERVER, or add server = "https://..." to ~/.config/worklode/config.toml`)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			res, err := cli.RunLogin(ctx, cli.LoginOptions{
				Server:    cfg.ServerURL,
				NoBrowser: noBrowser,
				Stdin:     cmd.InOrStdin(),
				Stdout:    cmd.OutOrStdout(),
			})
			if err != nil {
				return err
			}
			if err := cli.SaveConfig(cli.Config{ServerURL: cfg.ServerURL, Token: res.Token}); err != nil {
				// The login itself worked, so the token exists and is the user's;
				// a machine with no keychain is now an expected outcome rather
				// than a corner case (spec 001 §8.5). Hand it over instead of
				// making them log in again to reach the same dead end.
				return fmt.Errorf("%w\n\nThe login succeeded. To use the token without a keychain:\n\n  export LODE_TOKEN=%s\n", err, res.Token)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged in as %s (token expires %s)\n", res.ActorID, res.ExpiresAt)
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "worklode server URL (overrides LODE_SERVER / config file)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print a URL to open elsewhere and wait for the code, instead of launching a browser")
	return cmd
}

func init() {
	rootCmd.AddCommand(newLoginCmd())
}
