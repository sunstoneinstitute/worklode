package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
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

const (
	// shutdownGrace is how long in-flight requests get to finish on their own
	// before their context is cancelled; shutdownTimeout is the whole budget.
	shutdownGrace   = 2 * time.Second
	shutdownTimeout = 10 * time.Second
)

// shutdownServers stops every server gracefully and returns the first real
// error, treating http.ErrServerClosed as success.
//
// cancelRequests cancels the context every in-flight request derives from
// (http.Server.BaseContext). Shutdown waits for handlers and never cancels
// them, and the event-log SSE stream returns only when its request context is
// done — so without this a single open `lode event tail --follow` would hold
// shutdown until the deadline and turn every SIGTERM into a non-zero exit.
// Ordinary requests still get grace to finish untouched; the servers are shut
// down concurrently so a slow one cannot spend another's budget.
func shutdownServers(cancelRequests context.CancelFunc, grace, timeout time.Duration, srvs ...*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	each := make(chan error, len(srvs))
	for _, srv := range srvs {
		go func(srv *http.Server) { each <- srv.Shutdown(ctx) }(srv)
	}
	all := make(chan error, 1)
	go func() {
		var firstErr error
		for range srvs {
			if err := <-each; err != nil && !errors.Is(err, http.ErrServerClosed) && firstErr == nil {
				firstErr = err
			}
		}
		all <- firstErr
	}()

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-all:
		return err // everything drained inside the grace window
	case <-timer.C:
	case <-ctx.Done():
	}
	cancelRequests()
	return <-all
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
			reg := prometheus.NewRegistry()
			reg.MustRegister(collectors.NewGoCollector())
			reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

			st, err := store.Open(dsn, store.WithMetrics(reg))
			if err != nil {
				return err
			}
			defer st.Close()

			// Built before NewServer so its boot-time skill sync (a background
			// goroutine NewServer starts internally) shares the same shutdown
			// signal as the lease sweeper below, instead of running uncancellable.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			handler, adminHandler, err := api.NewServer(st, api.Config{
				BackgroundCtx:       ctx,
				BootstrapToken:      os.Getenv("LODE_BOOTSTRAP_TOKEN"),
				GitHubWebhookSecret: os.Getenv("LODE_GITHUB_WEBHOOK_SECRET"),
				FluxWebhookSecret:   os.Getenv("LODE_FLUX_WEBHOOK_SECRET"),
				ClusterEnvMap:       clusterEnv,
				BranchTemplate:      os.Getenv("LODE_BRANCH_TEMPLATE"),
				OIDCIssuer:          os.Getenv("LODE_OIDC_ISSUER"),
				OIDCClientID:        os.Getenv("LODE_OIDC_CLIENT_ID"),
				PublicURL:           os.Getenv("LODE_PUBLIC_URL"),
				SessionSecret:       os.Getenv("LODE_SESSION_SECRET"),
				GitHubClientID:      os.Getenv("LODE_GITHUB_APP_CLIENT_ID"),
				GitHubClientSecret:  os.Getenv("LODE_GITHUB_APP_CLIENT_SECRET"),
				TokenEncKey:         os.Getenv("LODE_TOKEN_ENC_KEY"),
				GitHubAppID:         os.Getenv("LODE_GITHUB_APP_ID"),
				GitHubAppPrivateKey: os.Getenv("LODE_GITHUB_APP_PRIVATE_KEY"),
				SkillSources:        os.Getenv("LODE_SKILL_SOURCES"),
				EmbeddingURL:        os.Getenv("LODE_EMBEDDING_URL"),
				EmbeddingModel:      os.Getenv("LODE_EMBEDDING_MODEL"),
				EmbeddingAPIKey:     os.Getenv("LODE_EMBEDDING_API_KEY"),
				SkillScoreFloor:     os.Getenv("LODE_SKILL_SCORE_FLOOR"),
				Metrics:             reg,
			})
			if err != nil {
				return err
			}

			sweeperRuns := prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "worklode_lease_sweeper_runs_total",
				Help: "Lease sweeper runs by result.",
			}, []string{"result"})
			reg.MustRegister(sweeperRuns)
			// Pre-initialise both series so alert expressions see 0, not no-data.
			sweeperRuns.WithLabelValues("ok")
			sweeperRuns.WithLabelValues("error")

			// Background sweeper: expire stale leases every 60s until shutdown.
			go func() {
				ticker := time.NewTicker(60 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if n, err := st.ExpireLeases(ctx, time.Now().UTC()); errors.Is(err, context.Canceled) {
							return
						} else if err != nil {
							sweeperRuns.WithLabelValues("error").Inc()
							slog.Error("expire leases", "err", err)
						} else {
							sweeperRuns.WithLabelValues("ok").Inc()
							if n > 0 {
								slog.Info("expired leases", "count", n)
							}
						}
					}
				}
			}()

			// Every in-flight request derives from reqCtx, which shutdown
			// cancels — see shutdownServers.
			reqCtx, cancelRequests := context.WithCancel(context.Background())
			defer cancelRequests()
			baseCtx := func(net.Listener) context.Context { return reqCtx }

			srv := &http.Server{Addr: listen, Handler: handler, BaseContext: baseCtx}
			// Admin server (/healthz, /metrics) on a separate port so they are
			// never reachable through the public ingress, which routes only the
			// app port. Probes and in-cluster scraping hit this port directly.
			adminSrv := &http.Server{Addr: adminListen, Handler: adminHandler, BaseContext: baseCtx}
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
				return shutdownServers(cancelRequests, shutdownGrace, shutdownTimeout, srv, adminSrv)
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
