package cmd

import (
	"context"
	"errors"
	"fmt"
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
//
// Values must be dev or prod. Anything else is rejected at boot rather than
// accepted: env_deploys only holds those two stages, so a cluster mapped to
// e.g. "staging" would record deployments rows and then silently never
// advance a task.
func parseClusterEnvMap(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || k == "" {
			continue
		}
		if v != "dev" && v != "prod" {
			return nil, fmt.Errorf(
				"LODE_CLUSTER_ENV_MAP: cluster %q maps to %q, want dev or prod", k, v)
		}
		m[k] = v
	}
	return m, nil
}

func newServeCmd() *cobra.Command {
	var dsn, listen, adminListen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the worklode HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				return errors.New("no DSN: set --dsn or LODE_DSN")
			}
			clusterEnv, err := parseClusterEnvMap(os.Getenv("LODE_CLUSTER_ENV_MAP"))
			if err != nil {
				return err
			}
			st, err := store.Open(dsn)
			if err != nil {
				return err
			}
			defer st.Close()

			handler, adminHandler, err := api.NewServer(st, api.Config{
				BootstrapToken:      os.Getenv("LODE_BOOTSTRAP_TOKEN"),
				GitHubWebhookSecret: os.Getenv("LODE_GITHUB_WEBHOOK_SECRET"),
				FluxWebhookSecret:   os.Getenv("LODE_FLUX_WEBHOOK_SECRET"),
				ClusterEnvMap:       clusterEnv,
				BranchPrefix:        os.Getenv("LODE_BRANCH_PREFIX"),
				OIDCIssuer:          os.Getenv("LODE_OIDC_ISSUER"),
				OIDCClientID:        os.Getenv("LODE_OIDC_CLIENT_ID"),
				PublicURL:           os.Getenv("LODE_PUBLIC_URL"),
				SessionSecret:       os.Getenv("LODE_SESSION_SECRET"),
				GitHubClientID:      os.Getenv("LODE_GITHUB_APP_CLIENT_ID"),
				GitHubClientSecret:  os.Getenv("LODE_GITHUB_APP_CLIENT_SECRET"),
				GitHubOrg:           os.Getenv("LODE_GITHUB_ORG"),
				GitHubAdminTeam:     os.Getenv("LODE_GITHUB_ADMIN_TEAM"),
				TokenEncKey:         os.Getenv("LODE_TOKEN_ENC_KEY"),
				GitHubAppID:         os.Getenv("LODE_GITHUB_APP_ID"),
				GitHubAppPrivateKey: os.Getenv("LODE_GITHUB_APP_PRIVATE_KEY"),
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
			// Admin server (/healthz, /metrics) on a separate port so they are
			// never reachable through the public ingress, which routes only the
			// app port. Probes and in-cluster scraping hit this port directly.
			adminSrv := &http.Server{Addr: adminListen, Handler: adminHandler}
			errCh := make(chan error, 2)
			go func() {
				slog.Info("listening", "addr", listen)
				errCh <- srv.ListenAndServe()
			}()
			go func() {
				slog.Info("admin listening", "addr", adminListen)
				errCh <- adminSrv.ListenAndServe()
			}()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				slog.Info("shutting down")
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				mainErr := srv.Shutdown(shutdownCtx)
				adminErr := adminSrv.Shutdown(shutdownCtx)
				if mainErr != nil && !errors.Is(mainErr, http.ErrServerClosed) {
					return mainErr
				}
				if adminErr != nil && !errors.Is(adminErr, http.ErrServerClosed) {
					return adminErr
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	cmd.Flags().StringVar(&listen, "listen", ":8080", "address for the public app server (web UI, API, webhooks)")
	cmd.Flags().StringVar(&adminListen, "admin-listen", ":9090", "address for the admin server (/healthz, /metrics)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newServeCmd())
}
