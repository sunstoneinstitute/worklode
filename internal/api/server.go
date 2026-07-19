// Package api implements the wt HTTP server: bearer-token auth, JSON task
// endpoints, and Prometheus metrics. Handlers stay thin — parse/validate,
// call store functions through RecordEvent, map errors, respond.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sunstoneinstitute/work-tracker/internal/hooks"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

// Config carries server configuration. The webhook secrets and cluster/env
// map are consumed by the /hooks/github and /hooks/flux endpoints.
type Config struct {
	BootstrapToken      string            // WT_BOOTSTRAP_TOKEN: create the first admin actor if the store is empty
	GitHubWebhookSecret string            // WT_GITHUB_WEBHOOK_SECRET
	FluxWebhookSecret   string            // WT_FLUX_WEBHOOK_SECRET
	ClusterEnvMap       map[string]string // WT_CLUSTER_ENV_MAP: cluster name -> environment
}

type server struct {
	st  *store.Store
	cfg Config
	log *slog.Logger

	requests  *prometheus.CounterVec
	durations *prometheus.HistogramVec
}

// NewServer builds the wt HTTP handler. If cfg.BootstrapToken is set and the
// actors table is empty, it creates the initial admin actor (idempotent). A
// bootstrap failure is fatal: the server must not start half-configured.
func NewServer(st *store.Store, cfg Config) (http.Handler, error) {
	s := &server{st: st, cfg: cfg, log: slog.Default()}

	if cfg.BootstrapToken != "" {
		if err := st.BootstrapAdmin(context.Background(), cfg.BootstrapToken); err != nil {
			return nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wt_http_requests_total",
		Help: "HTTP requests served, by method, route pattern, and status code.",
	}, []string{"method", "route", "code"})
	s.durations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "wt_http_request_duration_seconds",
		Help:    "HTTP request duration, by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	reg.MustRegister(s.requests, s.durations)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// Webhooks authenticate with HMAC signatures, not bearer tokens. The
	// handler itself rejects all requests with 503 when its secret is empty.
	mux.Handle("POST /hooks/github", hooks.NewGitHubHandler(st, cfg.GitHubWebhookSecret, s.log))
	mux.Handle("POST /hooks/flux", hooks.NewFluxHandler(st, cfg.FluxWebhookSecret, cfg.ClusterEnvMap, s.log))

	mux.Handle("POST /api/v1/tasks", s.auth(s.createTask))
	mux.Handle("GET /api/v1/tasks", s.auth(s.listTasks))
	mux.Handle("GET /api/v1/tasks/{id}", s.auth(s.getTask))
	mux.Handle("PATCH /api/v1/tasks/{id}", s.auth(s.patchTask))
	mux.Handle("POST /api/v1/tasks/{id}/edges", s.auth(s.addEdge))
	mux.Handle("DELETE /api/v1/tasks/{id}/edges", s.auth(s.removeEdge))
	mux.Handle("POST /api/v1/tasks/{id}/claim", s.auth(s.claimTask))
	mux.Handle("POST /api/v1/tasks/{id}/renew", s.auth(s.renewLease))
	mux.Handle("POST /api/v1/tasks/{id}/release", s.auth(s.releaseLease))
	mux.Handle("POST /api/v1/tasks/{id}/done", s.auth(s.doneTask))
	mux.Handle("POST /api/v1/tasks/{id}/abandon", s.auth(s.abandonTask))
	mux.Handle("GET /api/v1/tasks/{id}/timeline", s.auth(s.taskTimeline))

	mux.Handle("POST /api/v1/runtime-events", s.auth(s.createRuntimeEvent))

	mux.Handle("POST /api/v1/projects", s.auth(s.createProject))
	mux.Handle("GET /api/v1/projects", s.auth(s.listProjects))
	mux.Handle("POST /api/v1/projects/{id}/repos", s.auth(s.addRepo))

	mux.Handle("POST /api/v1/actors", s.auth(s.createActor))
	mux.Handle("POST /api/v1/actors/{id}/tokens", s.auth(s.createToken))
	mux.Handle("DELETE /api/v1/tokens", s.auth(s.revokeToken))

	// The repo half of an inbox item contains a slash ("owner/name"), so
	// promote/dismiss take it as a body field instead of a path segment.
	mux.Handle("GET /api/v1/inbox", s.auth(s.listInbox))
	mux.Handle("POST /api/v1/inbox/promote", s.auth(s.promoteInbox))
	mux.Handle("POST /api/v1/inbox/dismiss", s.auth(s.dismissInbox))

	mux.Handle("GET /api/v1/board", s.auth(s.board))

	return s.logging(s.metrics(mux)), nil
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// statusWriter captures the response status for logging and metrics.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// routeLabel returns the matched mux pattern without its method prefix
// (r.Pattern is set by ServeMux during routing), or "unmatched".
func routeLabel(r *http.Request) string {
	p := r.Pattern
	if p == "" {
		return "unmatched"
	}
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func (s *server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start),
		)
	})
}

func (s *server) metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		route := routeLabel(r)
		s.requests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
		s.durations.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// actorKey is the context key for the authenticated actor.
type actorKey struct{}

// auth wraps an /api/v1 handler with bearer-token authentication and puts
// the actor into the request context.
func (s *server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		actor, err := s.st.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			s.mapStoreErr(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
	})
}

// actorFrom returns the authenticated actor, or nil outside the auth
// middleware.
func actorFrom(r *http.Request) *store.Actor {
	a, _ := r.Context().Value(actorKey{}).(*store.Actor)
	return a
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("encode response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// maxAPIBody caps /api/v1 request bodies at 1 MiB; larger bodies get 413.
const maxAPIBody = 1 << 20

// readJSON decodes the request body into v, rejecting unknown fields and
// capping the body at maxAPIBody. Write its error with writeBodyErr so an
// over-limit body maps to 413.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAPIBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// writeBodyErr writes the response for a readJSON error: 413 for an
// over-limit body, 400 for anything else.
func writeBodyErr(w http.ResponseWriter, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeErr(w, http.StatusBadRequest, err.Error())
}

// mapStoreErr writes the HTTP response for a store error: ErrNotFound → 404,
// ErrBadTransition/ErrCycle/ErrInvalidInput → 422,
// ErrLeased/ErrBlocked/ErrRepoTaken/ErrEdgeExists → 409, anything else → 500
// with a generic body (the detail is logged, not leaked).
func (s *server) mapStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrBadTransition),
		errors.Is(err, store.ErrCycle),
		errors.Is(err, store.ErrInvalidInput):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, store.ErrLeased),
		errors.Is(err, store.ErrBlocked),
		errors.Is(err, store.ErrRepoTaken),
		errors.Is(err, store.ErrEdgeExists):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		s.log.Error("internal error", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

// randomExternalID returns a random hex string used as the (source,
// external_id) identity for server-originated events.
func randomExternalID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate external id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
