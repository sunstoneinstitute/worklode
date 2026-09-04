// Package serverapp runs Worklode's HTTP servers and background work.
package serverapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/indexer"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

type Options struct{ DSN, Listen, AdminListen string }

const shutdownTimeout = 10 * time.Second

func Run(ctx context.Context, opts Options) error {
	if opts.DSN == "" {
		return errors.New("no DSN: set --dsn or LODE_DSN")
	}
	clusterEnv, err := parseClusterEnvMap(os.Getenv("LODE_CLUSTER_ENV_MAP"))
	if err != nil {
		return err
	}
	webOpen := false
	if v := os.Getenv("LODE_WEB_OPEN"); v != "" {
		webOpen, err = strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("LODE_WEB_OPEN: %q is not a boolean", v)
		}
	}
	instanceEnv, err := api.ParseInstanceEnv(os.Getenv("LODE_INSTANCE_ENV"))
	if err != nil {
		return err
	}
	indexInterval, err := indexIntervalFromEnv()
	if err != nil {
		return err
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	st, err := store.Open(opts.DSN, store.WithMetrics(reg))
	if err != nil {
		return err
	}
	defer st.Close()
	gc, err := graphClientFromEnv()
	if err != nil {
		return err
	}
	handler, adminHandler, err := api.NewServer(st, api.Config{
		BackgroundCtx: ctx, BootstrapToken: os.Getenv("LODE_BOOTSTRAP_TOKEN"), GitHubWebhookSecret: os.Getenv("LODE_GITHUB_WEBHOOK_SECRET"), FluxWebhookSecret: os.Getenv("LODE_FLUX_WEBHOOK_SECRET"), CatalogWebhookSecret: os.Getenv("LODE_CATALOG_WEBHOOK_SECRET"), ClusterEnvMap: clusterEnv, InstanceEnv: instanceEnv, BranchTemplate: os.Getenv("LODE_BRANCH_TEMPLATE"), OIDCIssuer: os.Getenv("LODE_OIDC_ISSUER"), OIDCClientID: os.Getenv("LODE_OIDC_CLIENT_ID"), PublicURL: os.Getenv("LODE_PUBLIC_URL"), SessionSecret: os.Getenv("LODE_SESSION_SECRET"), WebOpen: webOpen, GitHubClientID: os.Getenv("LODE_GITHUB_APP_CLIENT_ID"), GitHubClientSecret: os.Getenv("LODE_GITHUB_APP_CLIENT_SECRET"), TokenEncKey: os.Getenv("LODE_TOKEN_ENC_KEY"), GitHubAppID: os.Getenv("LODE_GITHUB_APP_ID"), GitHubAppPrivateKey: os.Getenv("LODE_GITHUB_APP_PRIVATE_KEY"), SecretsCatalogPath: os.Getenv("LODE_SECRETS_CATALOG_PATH"), ApprovalFlowsDir: os.Getenv("LODE_APPROVAL_FLOWS_DIR"), SkillSources: os.Getenv("LODE_SKILL_SOURCES"), EmbeddingURL: os.Getenv("LODE_EMBEDDING_URL"), EmbeddingModel: os.Getenv("LODE_EMBEDDING_MODEL"), EmbeddingAPIKey: os.Getenv("LODE_EMBEDDING_API_KEY"), IndexInterval: indexInterval, SpeechToTextAPIKey: os.Getenv("LODE_ELEVENLABS_API_KEY"), SpeechToTextURL: os.Getenv("LODE_ELEVENLABS_URL"), BlobEndpoint: os.Getenv("LODE_BLOB_ENDPOINT"), BlobBucket: os.Getenv("LODE_BLOB_BUCKET"), BlobRegion: os.Getenv("LODE_BLOB_REGION"), BlobAccessKey: os.Getenv("LODE_BLOB_ACCESS_KEY"), BlobSecretKey: os.Getenv("LODE_BLOB_SECRET_KEY"), BlobSpoolDir: os.Getenv("LODE_BLOB_SPOOL_DIR"), Graph: gc, Metrics: reg,
	})
	if err != nil {
		return err
	}
	st.StartLeaseSweeper(ctx)
	if p := graphProjector(reg, st, gc); p != nil {
		go everyUntilDone(ctx, 10*time.Second, p.RunOnce, func(n int, err error) {
			if err != nil {
				slog.Error("graph projection", "projected", n, "err", err)
			} else if n > 0 {
				slog.Info("projected project graphs", "count", n)
			}
		})
	}
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	baseCtx := func(net.Listener) context.Context { return reqCtx }
	srv := &http.Server{Addr: opts.Listen, Handler: handler, BaseContext: baseCtx}
	adminSrv := &http.Server{Addr: opts.AdminListen, Handler: adminHandler, BaseContext: baseCtx}
	errCh := make(chan error, 2)
	go func() { slog.Info("listening", "addr", opts.Listen); errCh <- srv.ListenAndServe() }()
	go func() { slog.Info("admin listening", "addr", opts.AdminListen); errCh <- adminSrv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		return shutdownServers(cancelRequests, shutdownTimeout, srv, adminSrv)
	}
}

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
			return nil, fmt.Errorf("LODE_CLUSTER_ENV_MAP: cluster %q maps to %q, want dev or prod", k, v)
		}
		m[k] = v
	}
	return m, nil
}

// indexIntervalFromEnv reads LODE_INDEX_INTERVAL as a Go duration, defaulting
// to the convergence loop's own 5 minutes (040 §7). A typo fails the boot
// rather than silently converging on the default.
func indexIntervalFromEnv() (time.Duration, error) {
	v := os.Getenv("LODE_INDEX_INTERVAL")
	if v == "" {
		return indexer.DefaultInterval, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("LODE_INDEX_INTERVAL: want a positive Go duration (e.g. 5m), got %q", v)
	}
	return d, nil
}

func graphClientFromEnv() (*graphserver.Client, error) {
	if os.Getenv("LODE_GRAPHSERVER_URL") == "" {
		return nil, nil
	}
	return graphserver.FromEnv()
}
func graphProjector(reg prometheus.Registerer, st *store.Store, gc *graphserver.Client) *projector.Projector {
	if gc == nil {
		return nil
	}
	return projector.New(st, gc, projector.NewMetrics(reg), 200)
}
func shutdownServers(cancelRequests context.CancelFunc, timeout time.Duration, srvs ...*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	each := make(chan error, len(srvs))
	for _, srv := range srvs {
		go func(srv *http.Server) { each <- srv.Shutdown(ctx) }(srv)
	}
	var first error
	for range srvs {
		if err := <-each; err != nil && !errors.Is(err, http.ErrServerClosed) && first == nil {
			first = err
		}
	}
	cancelRequests()
	return first
}
func everyUntilDone(ctx context.Context, every time.Duration, step func(context.Context) (int, error), report func(int, error)) {
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
