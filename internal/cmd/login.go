package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func newLoginCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to work-tracker and store a token",
		Long: "Open a browser to sign in with whatever identity provider the server\n" +
			"is configured for (Keycloak, GitHub, or a choice of both), then store the\n" +
			"resulting 30-day token in the OS keychain. Re-run after it expires.",
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
				return errors.New(`server URL not set: pass --server, set WT_SERVER, or add server = "https://..." to ~/.config/wt/config.toml`)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			res, err := cli.RunLogin(ctx, cli.LoginOptions{Server: cfg.ServerURL})
			if err != nil {
				return err
			}
			if err := cli.SaveConfig(cli.Config{ServerURL: cfg.ServerURL, Token: res.Token}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged in as %s (token expires %s)\n", res.ActorID, res.ExpiresAt)
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "work-tracker server URL (overrides WT_SERVER / config file)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newLoginCmd())
}
