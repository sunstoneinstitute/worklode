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
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/indexer"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

type Options struct{ DSN, Listen, AdminListen, DocDepthLimit string }

const shutdownTimeout = 10 * time.Second

func Run(ctx context.Context, opts Options) error {
	depthLimit, err := parseDocDepthLimit(opts.DocDepthLimit)
	if err != nil {
		return err
	}
	store.SetDocDepthLimit(depthLimit)
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
	disableSkillMatching := false
	if v := os.Getenv("LODE_DISABLE_SKILL_MATCHING"); v != "" {
		disableSkillMatching, err = strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("LODE_DISABLE_SKILL_MATCHING: %q is not a boolean", v)
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
	storeOpts := []store.Option{store.WithMetrics(reg)}
	if v := os.Getenv("LODE_DOC_STALENESS_DAYS"); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 1 {
			return fmt.Errorf("LODE_DOC_STALENESS_DAYS: want an integer >= 1, got %q", v)
		}
		storeOpts = append(storeOpts, store.WithDocStalenessDays(days))
	}
	st, err := store.Open(opts.DSN, storeOpts...)
	if err != nil {
		return err
	}
	defer st.Close()
	gc, err := graphClientFromEnv()
	if err != nil {
		return err
	}
	cfg := api.Config{
		BackgroundCtx:        ctx,
		ClusterEnvMap:        clusterEnv,
		InstanceEnv:          instanceEnv,
		WebOpen:              webOpen,
		DisableSkillMatching: disableSkillMatching,
		IndexInterval:        indexInterval,
		Graph:                gc,
		Metrics:              reg,
	}
	setEnvTaggedFields(&cfg)
	handler, adminHandler, err := api.NewServer(st, cfg)
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

// parseDocDepthLimit reads the 025 §6.1 anchor depth limit (--doc-depth-limit
// or LODE_DOC_DEPTH_LIMIT), defaulting to designdoc.DepthLimit. Below 1 no
// document could hold an addressable section at all, so it fails the boot
// rather than making every accept impossible.
func parseDocDepthLimit(v string) (int, error) {
	if v == "" {
		return designdoc.DepthLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("LODE_DOC_DEPTH_LIMIT (--doc-depth-limit): want an integer >= 1, got %q", v)
	}
	return n, nil
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

// setEnvTaggedFields fills every string field of cfg carrying an `env:"NAME"`
// struct tag from os.Getenv(NAME). Fields needing parsing or defaulting
// (bools, durations, maps) are set by the caller instead and carry no tag.
func setEnvTaggedFields(cfg *api.Config) {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if name := t.Field(i).Tag.Get("env"); name != "" {
			v.Field(i).SetString(os.Getenv(name))
		}
	}
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
