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
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sunstoneinstitute/work-tracker/internal/githubauth"
	"github.com/sunstoneinstitute/work-tracker/internal/hooks"
	"github.com/sunstoneinstitute/work-tracker/internal/oidc"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
	"github.com/sunstoneinstitute/work-tracker/internal/tokencrypt"
)

// Config carries server configuration. The webhook secrets and cluster/env
// map are consumed by the /hooks/github and /hooks/flux endpoints.
type Config struct {
	BootstrapToken      string            // WT_BOOTSTRAP_TOKEN: create the first admin actor if the store is empty
	GitHubWebhookSecret string            // WT_GITHUB_WEBHOOK_SECRET
	FluxWebhookSecret   string            // WT_FLUX_WEBHOOK_SECRET
	ClusterEnvMap       map[string]string // WT_CLUSTER_ENV_MAP: cluster name -> environment

	// OIDC/SSO. The feature is off unless OIDCIssuer and OIDCClientID are both
	// set; unset behaves exactly as before. SessionSecret is required when OIDC
	// is enabled (Plan 2's web sessions sign cookies with it). PublicURL is the
	// external base URL used to build the web callback redirect URI.
	OIDCIssuer    string // WT_OIDC_ISSUER
	OIDCClientID  string // WT_OIDC_CLIENT_ID
	PublicURL     string // WT_PUBLIC_URL
	SessionSecret string // WT_SESSION_SECRET

	// GitHub App auth. Enabled only when GitHubClientID and GitHubClientSecret
	// are both set; independent of the OIDC feature. PublicURL and
	// SessionSecret (above) are shared and required when this is enabled.
	GitHubClientID     string // WT_GITHUB_APP_CLIENT_ID
	GitHubClientSecret string // WT_GITHUB_APP_CLIENT_SECRET
	GitHubOrg          string // WT_GITHUB_ORG
	// GitHubAdminTeam (WT_GITHUB_ADMIN_TEAM) is optional; when empty no user is
	// granted admin via GitHub (the team-membership check simply 404s).
	GitHubAdminTeam string
	TokenEncKey     string // WT_TOKEN_ENC_KEY (hex-encoded 32 bytes)
}

type server struct {
	st  *store.Store
	cfg Config
	log *slog.Logger

	// oidc is nil unless OIDC is configured (issuer + client id set). All SSO
	// routes 404 when it is nil.
	oidc *oidc.Verifier

	// gh and tokenCipher are nil unless GitHub App auth is configured. All
	// /auth/github/* routes 404 when gh is nil.
	gh          *githubauth.Client
	tokenCipher *tokencrypt.Cipher

	// cliCodes holds pending one-time codes for the server-mediated CLI login.
	cliCodes *cliCodeStore

	requests  *prometheus.CounterVec
	durations *prometheus.HistogramVec

	// Web UI templates, parsed once at startup (template.Must panics on a
	// parse error, so a broken template fails fast at boot, not on first
	// request). One *template.Template per page — see parseWebTemplates.
	tmplBoard   *template.Template
	tmplTask    *template.Template
	tmplProject *template.Template
}

// validatePublicURL ensures PublicURL is an absolute http(s) URL with a host,
// so the derived web callback redirect URIs are well-formed.
func validatePublicURL(publicURL string) error {
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("WT_PUBLIC_URL must be an absolute http(s) URL (e.g. https://wt.example.com)")
	}
	return nil
}

// NewServer builds the wt HTTP handler. If cfg.BootstrapToken is set and the
// actors table is empty, it creates the initial admin actor (idempotent). A
// bootstrap failure is fatal: the server must not start half-configured.
func NewServer(st *store.Store, cfg Config) (http.Handler, error) {
	s := &server{
		st: st, cfg: cfg, log: slog.Default(),
		tmplBoard:   parseWebTemplates("board.html"),
		tmplTask:    parseWebTemplates("task.html"),
		tmplProject: parseWebTemplates("project.html"),
	}
	s.cliCodes = newCLICodeStore(st.Now)

	if cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" {
		if cfg.SessionSecret == "" {
			return nil, fmt.Errorf("WT_SESSION_SECRET is required when OIDC is enabled")
		}
		if cfg.PublicURL == "" {
			return nil, fmt.Errorf("WT_PUBLIC_URL is required when OIDC is enabled")
		}
		if err := validatePublicURL(cfg.PublicURL); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		v, err := oidc.New(ctx, cfg.OIDCIssuer, cfg.OIDCClientID)
		if err != nil {
			return nil, fmt.Errorf("configure oidc: %w", err)
		}
		s.oidc = v
	}

	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		if cfg.SessionSecret == "" {
			return nil, fmt.Errorf("WT_SESSION_SECRET is required when GitHub auth is enabled")
		}
		if cfg.PublicURL == "" {
			return nil, fmt.Errorf("WT_PUBLIC_URL is required when GitHub auth is enabled")
		}
		if err := validatePublicURL(cfg.PublicURL); err != nil {
			return nil, err
		}
		if cfg.GitHubOrg == "" {
			return nil, fmt.Errorf("WT_GITHUB_ORG is required when GitHub auth is enabled")
		}
		key, err := hex.DecodeString(cfg.TokenEncKey)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("WT_TOKEN_ENC_KEY must be 64 hex chars (32 bytes)")
		}
		tc, err := tokencrypt.New(key)
		if err != nil {
			return nil, fmt.Errorf("configure token cipher: %w", err)
		}
		s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.GitHubOrg, cfg.GitHubAdminTeam)
		s.tokenCipher = tc
	}

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

	// Read-only web UI. When OIDC is enabled these require a valid session
	// cookie (webAuth 302s to /auth/login otherwise); when OIDC is
	// unconfigured webAuth is a passthrough and the UI stays open as in v1.
	// /healthz and /metrics (above) always stay open.
	mux.HandleFunc("GET /{$}", s.webAuth(s.boardPage))
	mux.HandleFunc("GET /tasks/{id}", s.webAuth(s.taskPage))
	mux.HandleFunc("GET /projects/{id}", s.webAuth(s.projectPage))
	mux.HandleFunc("GET /auth/login", s.authLogin)
	mux.HandleFunc("GET /auth/callback", s.authCallback)
	mux.HandleFunc("GET /auth/github/login", s.githubLogin)
	mux.HandleFunc("GET /auth/github/callback", s.githubCallback)
	mux.HandleFunc("GET /auth/choose", s.authChoose)

	// Webhooks authenticate with HMAC signatures, not bearer tokens. The
	// handler itself rejects all requests with 503 when its secret is empty.
	mux.Handle("POST /hooks/github", hooks.NewGitHubHandler(st, cfg.GitHubWebhookSecret, s.log))
	mux.Handle("POST /hooks/flux", hooks.NewFluxHandler(st, cfg.FluxWebhookSecret, cfg.ClusterEnvMap, s.log))

	// SSO token exchange + config discovery for the CLI login flow. Registered
	// outside the /api/v1 bearer-auth middleware, like /healthz and /hooks/*.
	// Both 404 when OIDC is unconfigured (s.oidc == nil).
	mux.HandleFunc("GET /auth/oidc/config", s.oidcConfig)
	mux.HandleFunc("POST /auth/oidc/token", s.oidcTokenExchange)

	// Provider-neutral, server-mediated CLI login (see cliauth.go).
	mux.HandleFunc("GET /.well-known/wt-login", s.wellKnownLogin)
	mux.HandleFunc("GET /auth/cli/login", s.cliLogin)
	mux.HandleFunc("POST /auth/cli/token", s.cliToken)

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

	// Project, actor, and token management is admin-only: any bearer token
	// may otherwise mint further tokens (verified privilege escalation).
	mux.Handle("POST /api/v1/projects", s.auth(requireAdmin(s.createProject)))
	mux.Handle("GET /api/v1/projects", s.auth(s.listProjects))
	mux.Handle("POST /api/v1/projects/{id}/repos", s.auth(requireAdmin(s.addRepo)))

	mux.Handle("POST /api/v1/actors", s.auth(requireAdmin(s.createActor)))
	mux.Handle("POST /api/v1/actors/{id}/tokens", s.auth(requireAdmin(s.createToken)))
	mux.Handle("DELETE /api/v1/tokens", s.auth(requireAdmin(s.revokeToken)))

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

// requireAdmin wraps a handler that must only be reachable by admin actors.
// It runs inside s.auth, which put the actor into the request context.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a := actorFrom(r); a == nil || !a.Admin {
			writeErr(w, http.StatusForbidden, "admin required")
			return
		}
		next(w, r)
	}
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
