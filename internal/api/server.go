// Package api implements the lode HTTP server: bearer-token auth, JSON task
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
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/oidc"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/tokencrypt"
)

// Config carries server configuration. The webhook secrets and cluster/env
// map are consumed by the /hooks/github and /hooks/flux endpoints.
type Config struct {
	BootstrapToken      string            // LODE_BOOTSTRAP_TOKEN: create the first admin actor if the store is empty
	GitHubWebhookSecret string            // LODE_GITHUB_WEBHOOK_SECRET
	FluxWebhookSecret   string            // LODE_FLUX_WEBHOOK_SECRET
	ClusterEnvMap       map[string]string // LODE_CLUSTER_ENV_MAP: cluster name -> environment

	// BranchPrefix (LODE_BRANCH_PREFIX) is the task-branch prefix the server
	// hands out and correlates pushes by; empty means "lode/". The legacy
	// "wl/" prefix stays recognized for correlation regardless.
	BranchPrefix string

	// OIDC/SSO. The feature is off unless OIDCIssuer and OIDCClientID are both
	// set; unset behaves exactly as before. SessionSecret is required when OIDC
	// is enabled (Plan 2's web sessions sign cookies with it). PublicURL is the
	// external base URL used to build the web callback redirect URI.
	OIDCIssuer    string // LODE_OIDC_ISSUER
	OIDCClientID  string // LODE_OIDC_CLIENT_ID
	PublicURL     string // LODE_PUBLIC_URL
	SessionSecret string // LODE_SESSION_SECRET

	// GitHub App auth. Enabled only when GitHubClientID and GitHubClientSecret
	// are both set; independent of the OIDC feature. PublicURL and
	// SessionSecret (above) are shared and required when this is enabled.
	GitHubClientID     string // LODE_GITHUB_APP_CLIENT_ID
	GitHubClientSecret string // LODE_GITHUB_APP_CLIENT_SECRET
	GitHubOrg          string // LODE_GITHUB_ORG
	// GitHubAdminTeam (LODE_GITHUB_ADMIN_TEAM) is optional; when empty no user is
	// granted admin via GitHub (the team-membership check simply 404s).
	GitHubAdminTeam string
	TokenEncKey     string // LODE_TOKEN_ENC_KEY (hex-encoded 32 bytes)

	// GitHub App installation auth, used to discover a newly mapped repo's
	// delivery profile (see discoverDoneState). Independent of the login flow
	// above and optional: with either field empty, discovery is skipped and a
	// repo mapping keeps its default done_state. GitHubAppPrivateKey is secret
	// PEM — like the other secrets here it is only ever consumed, never logged
	// and never served.
	GitHubAppID         string // LODE_GITHUB_APP_ID
	GitHubAppPrivateKey string // LODE_GITHUB_APP_PRIVATE_KEY

	// SkillSources configures org skill source repos, comma-separated
	// "owner/repo@ref:glob" entries. LODE_SKILL_SOURCES. Requires the GitHub
	// App to be configured. Unset: skill sync off.
	SkillSources string
	// EmbeddingURL is a full OpenAI-compatible embeddings endpoint URL.
	// LODE_EMBEDDING_URL. Unset: recommendations run pins-only.
	EmbeddingURL string
	// EmbeddingModel names the model sent to EmbeddingURL. LODE_EMBEDDING_MODEL.
	EmbeddingModel string
	// EmbeddingAPIKey authenticates against EmbeddingURL. LODE_EMBEDDING_API_KEY.
	EmbeddingAPIKey string
	// SkillScoreFloor is the minimum cosine similarity for a recommendation,
	// default 0.35. LODE_SKILL_SCORE_FLOOR.
	SkillScoreFloor string

	// BackgroundCtx governs goroutines NewServer starts on its own (boot
	// skill sync, webhook-triggered skill syncs) — not any HTTP request.
	// Defaults to context.Background() when nil, so background syncs run
	// unbounded and are never cancelled; pass the process's shutdown context
	// (e.g. the one built around signal.NotifyContext in cmd/serve.go) to
	// have them abort on shutdown instead of outliving the server.
	BackgroundCtx context.Context
}

// githubAPIBase is the public GitHub REST endpoint. A var, not a const, so
// tests can redirect it at a local server instead of reaching out to the
// real GitHub API (e.g. a boot-time skill sync triggered by NewServer).
var githubAPIBase = "https://api.github.com"

// newAppAuth builds the GitHub App client used for repo discovery, or nil when
// the app id and key are not both configured. An unusable key is a startup
// error, matching how NewServer treats every other secret: unset means the
// feature is off, malformed means the operator meant to enable it and the
// server must not start half-configured (cf. LODE_TOKEN_ENC_KEY, LODE_OIDC_*).
// Degrading here would look like a repo with no environments.
func newAppAuth(cfg Config) (*githubauth.AppAuth, error) {
	if cfg.GitHubAppID == "" || cfg.GitHubAppPrivateKey == "" {
		return nil, nil
	}
	key, err := githubauth.ParseAppPrivateKey([]byte(cfg.GitHubAppPrivateKey))
	if err != nil {
		return nil, fmt.Errorf("LODE_GITHUB_APP_PRIVATE_KEY: %w", err)
	}
	return &githubauth.AppAuth{AppID: cfg.GitHubAppID, Key: key, BaseURL: githubAPIBase}, nil
}

type server struct {
	st  *store.Store
	cfg Config
	log *slog.Logger

	// branchPrefix is cfg.BranchPrefix normalized to its default; the server
	// is the authority on branch names handed to clients.
	branchPrefix string

	// oidc is nil unless OIDC is configured (issuer + client id set). All SSO
	// routes 404 when it is nil.
	oidc *oidc.Verifier

	// gh and tokenCipher are nil unless GitHub App auth is configured. All
	// /auth/github/* routes 404 when gh is nil.
	gh          *githubauth.Client
	tokenCipher *tokencrypt.Cipher

	// appAuth is nil unless the GitHub App id and private key are configured;
	// when nil, addRepo skips done_state discovery.
	appAuth *githubauth.AppAuth

	// embedder is nil unless an embedding provider is configured; recommend
	// then runs pins-only. skillSyncer is nil unless skill sources are
	// configured; sync then 422s. skillSyncMu serializes concurrent sync
	// requests against the same source set. skillSyncPending records a
	// trigger that arrived while a sync was already running, so runSkillSync
	// re-runs once more instead of silently dropping it (see runSkillSync).
	embedder         embed.Provider
	skillSyncer      *skillsync.Syncer
	skillSources     []skillsync.Source
	skillFloor       float64
	skillSyncMu      sync.Mutex
	skillSyncPending atomic.Bool

	// bgCtx governs background goroutines started by NewServer (boot sync,
	// webhook-triggered syncs) — cfg.BackgroundCtx, or context.Background()
	// when unset. It is not the request context of any HTTP call.
	bgCtx context.Context

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
		return fmt.Errorf("LODE_PUBLIC_URL must be an absolute http(s) URL (e.g. https://lode.example.com)")
	}
	return nil
}

// NewServer builds the worklode HTTP handlers. It returns two handlers: the
// public app handler (web UI, API, webhooks) and a separate admin handler
// (/healthz, /metrics). The admin handler is served on its own listener so
// health and metrics are never exposed through the public ingress; probes and
// in-cluster scraping hit the admin port directly.
//
// If cfg.BootstrapToken is set and the actors table is empty, it creates the
// initial admin actor (idempotent). A bootstrap failure is fatal: the server
// must not start half-configured.
func NewServer(st *store.Store, cfg Config) (http.Handler, http.Handler, error) {
	s := &server{
		st: st, cfg: cfg, log: slog.Default(),
		tmplBoard:   parseWebTemplates("board.html"),
		tmplTask:    parseWebTemplates("task.html"),
		tmplProject: parseWebTemplates("project.html"),
	}
	s.cliCodes = newCLICodeStore(st.Now)
	s.bgCtx = cfg.BackgroundCtx
	if s.bgCtx == nil {
		s.bgCtx = context.Background()
	}

	store.SetBranchPrefix(cfg.BranchPrefix)
	s.branchPrefix = store.BranchPrefix()

	if cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" {
		if cfg.SessionSecret == "" {
			return nil, nil, fmt.Errorf("LODE_SESSION_SECRET is required when OIDC is enabled")
		}
		if cfg.PublicURL == "" {
			return nil, nil, fmt.Errorf("LODE_PUBLIC_URL is required when OIDC is enabled")
		}
		if err := validatePublicURL(cfg.PublicURL); err != nil {
			return nil, nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		v, err := oidc.New(ctx, cfg.OIDCIssuer, cfg.OIDCClientID)
		if err != nil {
			return nil, nil, fmt.Errorf("configure oidc: %w", err)
		}
		s.oidc = v
	}

	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		if cfg.SessionSecret == "" {
			return nil, nil, fmt.Errorf("LODE_SESSION_SECRET is required when GitHub auth is enabled")
		}
		if cfg.PublicURL == "" {
			return nil, nil, fmt.Errorf("LODE_PUBLIC_URL is required when GitHub auth is enabled")
		}
		if err := validatePublicURL(cfg.PublicURL); err != nil {
			return nil, nil, err
		}
		if cfg.GitHubOrg == "" {
			return nil, nil, fmt.Errorf("LODE_GITHUB_ORG is required when GitHub auth is enabled")
		}
		key, err := hex.DecodeString(cfg.TokenEncKey)
		if err != nil || len(key) != 32 {
			return nil, nil, fmt.Errorf("LODE_TOKEN_ENC_KEY must be 64 hex chars (32 bytes)")
		}
		tc, err := tokencrypt.New(key)
		if err != nil {
			return nil, nil, fmt.Errorf("configure token cipher: %w", err)
		}
		s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.GitHubOrg, cfg.GitHubAdminTeam)
		s.tokenCipher = tc
	}

	appAuth, err := newAppAuth(cfg)
	if err != nil {
		return nil, nil, err
	}
	s.appAuth = appAuth

	s.skillFloor = 0.35
	if cfg.SkillScoreFloor != "" {
		f, err := strconv.ParseFloat(cfg.SkillScoreFloor, 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
			return nil, nil, fmt.Errorf("LODE_SKILL_SCORE_FLOOR: want a float in [0,1], got %q", cfg.SkillScoreFloor)
		}
		s.skillFloor = f
	}
	if cfg.EmbeddingURL != "" {
		if cfg.EmbeddingModel == "" {
			return nil, nil, fmt.Errorf("LODE_EMBEDDING_MODEL is required when LODE_EMBEDDING_URL is set")
		}
		s.embedder = &embed.OpenAI{URL: cfg.EmbeddingURL, Model: cfg.EmbeddingModel, Key: cfg.EmbeddingAPIKey}
		// At boot, before the first request and regardless of whether skill
		// sources are configured. Vectors from a previous provider are not
		// comparable with this one's, and dropping the sources (or swapping the
		// model on an instance that has none) leaves no sync to notice. Fatal
		// like the bootstrap below: refusing to start beats serving matches
		// from the wrong embedding space.
		if err := skillsync.InvalidateOnProviderChange(context.Background(), st, s.embedder, s.log); err != nil {
			return nil, nil, fmt.Errorf("invalidate skill embeddings: %w", err)
		}
	}
	skillSources, err := skillsync.ParseSources(cfg.SkillSources)
	if err != nil {
		return nil, nil, fmt.Errorf("LODE_SKILL_SOURCES: %w", err)
	}
	if len(skillSources) > 0 {
		if appAuth == nil {
			return nil, nil, fmt.Errorf("LODE_SKILL_SOURCES requires the GitHub App (LODE_GITHUB_APP_ID/LODE_GITHUB_APP_PRIVATE_KEY)")
		}
		s.skillSources = skillSources
		s.skillSyncer = &skillsync.Syncer{Store: st, Fetch: appAuth.Tarball, Embed: s.embedder, Log: s.log}
	}

	if cfg.BootstrapToken != "" {
		if err := st.BootstrapAdmin(context.Background(), cfg.BootstrapToken); err != nil {
			return nil, nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests served, by method, route pattern, and status code.",
	}, []string{"method", "route", "code"})
	s.durations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration, by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	reg.MustRegister(s.requests, s.durations)

	mux := http.NewServeMux()

	// Read-only web UI. When OIDC is enabled these require a valid session
	// cookie (webAuth 302s to /auth/login otherwise); when OIDC is
	// unconfigured webAuth is a passthrough and the UI stays open as in v1.
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
	// onSkillPush triggers an async sync on a push matching a configured
	// skill source; skillSources is nil when none are configured, so
	// MatchesPush always reports false and the closure is a no-op.
	onSkillPush := func(repo, branch string) bool {
		if !skillsync.MatchesPush(s.skillSources, repo, branch) {
			return false
		}
		go s.runSkillSync(s.bgCtx, "webhook push")
		return true
	}
	mux.Handle("POST /hooks/github", hooks.NewGitHubHandler(st, cfg.GitHubWebhookSecret, s.log, onSkillPush))
	mux.Handle("POST /hooks/flux", hooks.NewFluxHandler(st, cfg.FluxWebhookSecret, cfg.ClusterEnvMap, s.log))

	// SSO token exchange + config discovery for the CLI login flow. Registered
	// outside the /api/v1 bearer-auth middleware, like /healthz and /hooks/*.
	// Both 404 when OIDC is unconfigured (s.oidc == nil).
	mux.HandleFunc("GET /auth/oidc/config", s.oidcConfig)
	mux.HandleFunc("POST /auth/oidc/token", s.oidcTokenExchange)

	// Provider-neutral, server-mediated CLI login (see cliauth.go).
	mux.HandleFunc("GET /.well-known/lode-login", s.wellKnownLogin)
	mux.HandleFunc("GET /auth/cli/login", s.cliLogin)
	mux.HandleFunc("POST /auth/cli/token", s.cliToken)

	mux.Handle("POST /api/v1/tasks", s.auth(s.createTask))
	mux.Handle("GET /api/v1/tasks", s.auth(s.listTasks))
	mux.Handle("GET /api/v1/tasks/{id}", s.auth(s.getTask))
	mux.Handle("GET /api/v1/tasks/{id}/brief", s.auth(s.taskBrief))
	mux.Handle("PATCH /api/v1/tasks/{id}", s.auth(s.patchTask))
	mux.Handle("PUT /api/v1/tasks/{id}/skills", s.auth(s.setTaskSkills))
	mux.Handle("POST /api/v1/tasks/{id}/edges", s.auth(s.addEdge))
	mux.Handle("DELETE /api/v1/tasks/{id}/edges", s.auth(s.removeEdge))
	mux.Handle("POST /api/v1/tasks/{id}/decompose", s.auth(s.decomposeTask))
	mux.Handle("POST /api/v1/tasks/claim-next", s.auth(s.claimNext))
	mux.Handle("POST /api/v1/tasks/{id}/claim", s.auth(s.claimTask))
	mux.Handle("POST /api/v1/tasks/{id}/renew", s.auth(s.renewLease))
	mux.Handle("POST /api/v1/tasks/{id}/release", s.auth(s.releaseLease))
	mux.Handle("POST /api/v1/tasks/{id}/lease/worktree", s.auth(s.rebindWorktree))
	mux.Handle("POST /api/v1/tasks/{id}/agent-session", s.auth(s.touchAgentSession))
	mux.Handle("POST /api/v1/tasks/{id}/agent-session/end", s.auth(s.endAgentSession))
	mux.Handle("POST /api/v1/tasks/{id}/done", s.auth(s.doneTask))
	mux.Handle("POST /api/v1/tasks/{id}/abandon", s.auth(s.abandonTask))
	mux.Handle("POST /api/v1/tasks/{id}/reopen", s.auth(s.reopenTask))
	mux.Handle("GET /api/v1/tasks/{id}/timeline", s.auth(s.taskTimeline))

	mux.Handle("GET /api/v1/skills", s.auth(s.listSkills))
	mux.Handle("GET /api/v1/skills/{name}", s.auth(s.getSkill))
	mux.Handle("GET /api/v1/skills/{name}/archive/{hash}", s.auth(s.skillArchive))
	mux.Handle("POST /api/v1/skills/recommend", s.auth(s.recommendSkills))
	mux.Handle("POST /api/v1/skills/sync", s.auth(requireAdmin(s.syncSkills)))

	mux.Handle("POST /api/v1/runtime-events", s.auth(s.createRuntimeEvent))

	// Project, actor, and token management is admin-only: any bearer token
	// may otherwise mint further tokens (verified privilege escalation).
	mux.Handle("POST /api/v1/projects", s.auth(requireAdmin(s.createProject)))
	mux.Handle("GET /api/v1/projects", s.auth(s.listProjects))
	// Literal segment, so Go's mux prefers it over any future
	// GET /api/v1/projects/{id}. Read-only: no requireAdmin.
	mux.Handle("GET /api/v1/projects/resolve", s.auth(s.resolveProjectByRemote))
	mux.Handle("PATCH /api/v1/projects/{id}", s.auth(requireAdmin(s.patchProject)))
	mux.Handle("POST /api/v1/projects/{id}/repos", s.auth(requireAdmin(s.addRepo)))
	mux.Handle("PATCH /api/v1/repos/{owner}/{name}", s.auth(requireAdmin(s.patchRepo)))

	mux.Handle("POST /api/v1/actors", s.auth(requireAdmin(s.createActor)))
	mux.Handle("POST /api/v1/actors/{id}/tokens", s.auth(requireAdmin(s.createToken)))
	mux.Handle("DELETE /api/v1/tokens", s.auth(requireAdmin(s.revokeToken)))

	// The repo half of an inbox item contains a slash ("owner/name"), so
	// promote/dismiss take it as a body field instead of a path segment.
	mux.Handle("GET /api/v1/inbox", s.auth(s.listInbox))
	mux.Handle("POST /api/v1/inbox/promote", s.auth(s.promoteInbox))
	mux.Handle("POST /api/v1/inbox/dismiss", s.auth(s.dismissInbox))

	mux.Handle("GET /api/v1/board", s.auth(s.board))

	// Admin handler: health and metrics on a dedicated listener, never routed
	// through the public ingress. No auth or request-metrics middleware — the
	// metrics endpoint must not count its own scrapes.
	admin := http.NewServeMux()
	admin.HandleFunc("GET /healthz", s.healthz)
	admin.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// One sync at boot keeps the registry current with whatever landed on
	// skill-source branches while the server was down; webhook pushes cover
	// it live from here. Guarded on skillSyncer so a server with no skill
	// sources configured starts no background goroutine.
	if s.skillSyncer != nil {
		go s.runSkillSync(s.bgCtx, "boot")
	}

	return s.logging(s.metrics(mux)), admin, nil
}

// runSkillSync serializes full syncs via skillSyncMu. A trigger that arrives
// while one is already running does not queue behind it (that could pile up
// unboundedly under a busy source); it instead sets skillSyncPending so the
// in-flight run does one more pass once it finishes. Without this, a push
// landing mid-sync would be silently dropped — its content never re-checked
// until the next trigger or a restart, which on a quiet repo can be never.
func (s *server) runSkillSync(ctx context.Context, reason string) {
	if s.skillSyncer == nil {
		return
	}
	if !s.skillSyncMu.TryLock() {
		s.skillSyncPending.Store(true)
		return
	}
	defer s.skillSyncMu.Unlock()
	for {
		s.skillSyncPending.Store(false)
		s.syncOnce(ctx, reason)
		if !s.skillSyncPending.CompareAndSwap(true, false) {
			return
		}
		reason += " (coalesced)"
	}
}

// syncOnce runs a single bounded sync pass and logs its outcome.
func (s *server) syncOnce(ctx context.Context, reason string) {
	ctx, cancel := context.WithTimeout(ctx, skillSyncTimeout)
	defer cancel()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	if err != nil {
		// Error, matching the HTTP path: a background failure has no caller
		// watching a response, so the log is the only signal there is.
		s.log.Error("skill sync failed", "reason", reason, "err", err)
		return
	}
	s.log.Info("skill sync", "reason", reason, "synced", sum.Synced,
		"changed", sum.Changed, "embedded", sum.Embedded, "deleted", sum.Deleted)
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
		errors.Is(err, store.ErrKeyTaken),
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
