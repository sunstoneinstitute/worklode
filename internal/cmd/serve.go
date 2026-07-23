package cmd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// parseClusterEnvMap parses "cluster1=dev,cluster2=prod" into a map. Entries
// without '=' are ignored. Empty input returns nil.
func parseClusterEnvMap(s string) map[string]string {
	if s == "" {
		return nil
	}
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || k == "" {
			continue
		}
		m[k] = v
	}
	return m
}

func newServeCmd() *cobra.Command {
	var dbPath, listen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the worklode HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			handler, err := api.NewServer(st, api.Config{
				BootstrapToken:      os.Getenv("WL_BOOTSTRAP_TOKEN"),
				GitHubWebhookSecret: os.Getenv("WL_GITHUB_WEBHOOK_SECRET"),
				FluxWebhookSecret:   os.Getenv("WL_FLUX_WEBHOOK_SECRET"),
				ClusterEnvMap:       parseClusterEnvMap(os.Getenv("WL_CLUSTER_ENV_MAP")),
				OIDCIssuer:          os.Getenv("WL_OIDC_ISSUER"),
				OIDCClientID:        os.Getenv("WL_OIDC_CLIENT_ID"),
				PublicURL:           os.Getenv("WL_PUBLIC_URL"),
				SessionSecret:       os.Getenv("WL_SESSION_SECRET"),
				GitHubClientID:      os.Getenv("WL_GITHUB_APP_CLIENT_ID"),
				GitHubClientSecret:  os.Getenv("WL_GITHUB_APP_CLIENT_SECRET"),
				GitHubOrg:           os.Getenv("WL_GITHUB_ORG"),
				GitHubAdminTeam:     os.Getenv("WL_GITHUB_ADMIN_TEAM"),
				TokenEncKey:         os.Getenv("WL_TOKEN_ENC_KEY"),
			})
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Background sweeper: expire stale leases every 60s until shutdown.
			go func() {
				ticker := time.NewTicker(60 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if n, err := st.ExpireLeases(ctx, time.Now().UTC()); err != nil {
							slog.Error("expire leases", "err", err)
						} else if n > 0 {
							slog.Info("expired leases", "count", n)
						}
					}
				}
			}()

			srv := &http.Server{Addr: listen, Handler: handler}
			errCh := make(chan error, 1)
			go func() {
				slog.Info("listening", "addr", listen, "db", dbPath)
				errCh <- srv.ListenAndServe()
			}()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				slog.Info("shutting down")
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite database file")
	cmd.Flags().StringVar(&listen, "listen", ":8080", "address to listen on")
	cmd.MarkFlagRequired("db")
	return cmd
}

func init() {
	rootCmd.AddCommand(newServeCmd())
}
