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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/projector"
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

// graphProjector builds the knowledge-graph projector when
// LODE_GRAPHSERVER_URL is set (spec 006 §11): the same LODE_GRAPHSERVER_*
// variables graphserver.FromEnv documents, so serve and every other caller
// share one configuration surface. Unset means projection is disabled;
// set-but-broken fails the boot.
func graphProjector(reg prometheus.Registerer, st *store.Store) (*projector.Projector, error) {
	if os.Getenv("LODE_GRAPHSERVER_URL") == "" {
		return nil, nil
	}
	gc, err := graphserver.FromEnv()
	if err != nil {
		return nil, err
	}
	return projector.New(st, gc, projector.NewMetrics(reg), 200), nil
}

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
			return runServe(cmd, dsn, listen, adminListen)
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	cmd.Flags().StringVar(&listen, "listen", ":8080", "address for the public app server (web UI, API, webhooks)")
	cmd.Flags().StringVar(&adminListen, "admin-listen", ":9090", "address for the admin server (/healthz, /metrics)")
	return cmd
}

// runServe boots the store, the API server and the background loops, then
// serves until an interrupt or a listener error.
func runServe(cmd *cobra.Command, dsn, listen, adminListen string) error {
	if dsn == "" {
		return errors.New("no DSN: set --dsn or LODE_DSN")
	}
	clusterEnv, err := parseClusterEnvMap(os.Getenv("LODE_CLUSTER_ENV_MAP"))
	if err != nil {
		return err
	}
	// LODE_WEB_OPEN serves the web UI unauthenticated on an instance
	// with no login provider (the local stack, CI). An unparseable
	// value is a typo in a security-relevant setting, so it fails the
	// boot rather than defaulting quietly to either answer — and it
	// fails here, before the store is opened, so the operator sees the
	// typo and not a connection error.
	webOpen := false
	if v := os.Getenv("LODE_WEB_OPEN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("LODE_WEB_OPEN: %q is not a boolean", v)
		}
		webOpen = b
	}
	// LODE_INSTANCE_ENV says whether this is a dev or a prod instance (039
	// §3); today it decides whether a delete must carry a justification (044
	// §3). Validated here, before the store is opened, for the same reason
	// LODE_WEB_OPEN is: an unrecognised value is a typo in a setting that
	// changes what the server demands, and the operator should see the typo
	// rather than a connection error. api.NewServer re-checks it, so a
	// programmatic embedder cannot skip this.
	instanceEnv, err := api.ParseInstanceEnv(os.Getenv("LODE_INSTANCE_ENV"))
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
	// Passing it is also what starts the doc-lifecycle subscriber —
	// see the BackgroundCtx block at the end of NewServer.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler, adminHandler, err := api.NewServer(st, api.Config{
		BackgroundCtx:       ctx,
		BootstrapToken:      os.Getenv("LODE_BOOTSTRAP_TOKEN"),
		GitHubWebhookSecret: os.Getenv("LODE_GITHUB_WEBHOOK_SECRET"),
		FluxWebhookSecret:   os.Getenv("LODE_FLUX_WEBHOOK_SECRET"),
		ClusterEnvMap:       clusterEnv,
		InstanceEnv:         instanceEnv,
		BranchTemplate:      os.Getenv("LODE_BRANCH_TEMPLATE"),
		OIDCIssuer:          os.Getenv("LODE_OIDC_ISSUER"),
		OIDCClientID:        os.Getenv("LODE_OIDC_CLIENT_ID"),
		PublicURL:           os.Getenv("LODE_PUBLIC_URL"),
		SessionSecret:       os.Getenv("LODE_SESSION_SECRET"),
		WebOpen:             webOpen,
		GitHubClientID:      os.Getenv("LODE_GITHUB_APP_CLIENT_ID"),
		GitHubClientSecret:  os.Getenv("LODE_GITHUB_APP_CLIENT_SECRET"),
		TokenEncKey:         os.Getenv("LODE_TOKEN_ENC_KEY"),
		GitHubAppID:         os.Getenv("LODE_GITHUB_APP_ID"),
		GitHubAppPrivateKey: os.Getenv("LODE_GITHUB_APP_PRIVATE_KEY"),
		SecretsCatalogPath:  os.Getenv("LODE_SECRETS_CATALOG_PATH"),
		SkillSources:        os.Getenv("LODE_SKILL_SOURCES"),
		EmbeddingURL:        os.Getenv("LODE_EMBEDDING_URL"),
		EmbeddingModel:      os.Getenv("LODE_EMBEDDING_MODEL"),
		EmbeddingAPIKey:     os.Getenv("LODE_EMBEDDING_API_KEY"),
		SkillScoreFloor:     os.Getenv("LODE_SKILL_SCORE_FLOOR"),
		BlobEndpoint:        os.Getenv("LODE_BLOB_ENDPOINT"),
		BlobBucket:          os.Getenv("LODE_BLOB_BUCKET"),
		BlobRegion:          os.Getenv("LODE_BLOB_REGION"),
		BlobAccessKey:       os.Getenv("LODE_BLOB_ACCESS_KEY"),
		BlobSecretKey:       os.Getenv("LODE_BLOB_SECRET_KEY"),
		BlobSpoolDir:        os.Getenv("LODE_BLOB_SPOOL_DIR"),
		Metrics:             reg,
	})
	if err != nil {
		return err
	}

	// The lease sweeper's loop and counter live in internal/store (022 §4);
	// serve.go supplies only the registry (via store.WithMetrics above) and
	// the shutdown context.
	st.StartLeaseSweeper(ctx)

	proj, err := graphProjector(reg, st)
	if err != nil {
		return err
	}
	if proj != nil {
		// Knowledge-graph projection (spec 006 §11): follow the
		// state_log outbox and replace each dirty project's graph on
		// graph-server every 10s until shutdown. Single projector by
		// construction — one lode serve wired to LODE_GRAPHSERVER_URL
		// — so graph-server's per-branch lock covers it for now;
		// If-Match CAS (006 §13.3 item 6) is wanted before any second
		// writer.
		go everyUntilDone(ctx, 10*time.Second, proj.RunOnce, func(n int, err error) {
			if err != nil {
				slog.Error("graph projection", "err", err)
			} else if n > 0 {
				slog.Info("projected project graphs", "count", n)
			}
		})
	}

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
}

// everyUntilDone runs step on an interval until ctx is cancelled. A step that
// fails because of that cancellation ends the loop; every other outcome goes
// to report and the loop continues.
func everyUntilDone(ctx context.Context, every time.Duration, step func(context.Context) (int, error), report func(n int, err error)) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := step(ctx)
			if errors.Is(err, context.Canceled) {
				return
			}
			report(n, err)
		}
	}
}

func init() {
	rootCmd.AddCommand(newServeCmd())
}
