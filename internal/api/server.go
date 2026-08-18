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

	// BranchTemplate (LODE_BRANCH_TEMPLATE) renders the task-branch names the
	// server hands out and correlates pushes by; empty means
	// store.DefaultBranchTemplate. An invalid template fails NewServer.
	BranchTemplate string

	// OIDC/SSO. The feature is off unless OIDCIssuer and OIDCClientID are both
	// set; unset behaves exactly as before. SessionSecret is required when OIDC
	// is enabled (Plan 2's web sessions sign cookies with it). PublicURL is the
	// external base URL used to build the web callback redirect URI.
	OIDCIssuer    string // LODE_OIDC_ISSUER
	OIDCClientID  string // LODE_OIDC_CLIENT_ID
	PublicURL     string // LODE_PUBLIC_URL
	SessionSecret string // LODE_SESSION_SECRET

	// WebOpen (LODE_WEB_OPEN) permits the web UI to serve anonymous callers on
	// an instance with *no* login provider configured — the local stack and
	// CI. It is ignored when OIDC is configured: the opt-in is about the
	// absence of a provider, never about weakening one that exists. Without
	// it, an instance with no provider refuses to serve any web page rather
	// than serving the whole cockpit to anyone who can reach the port.
	WebOpen bool // LODE_WEB_OPEN

	// GitHub App auth. Enabled only when GitHubClientID and GitHubClientSecret
	// are both set; independent of the OIDC feature. PublicURL and
	// SessionSecret (above) are shared and required when this is enabled.
	GitHubClientID     string // LODE_GITHUB_APP_CLIENT_ID
	GitHubClientSecret string // LODE_GITHUB_APP_CLIENT_SECRET
	TokenEncKey        string // LODE_TOKEN_ENC_KEY (hex-encoded 32 bytes)

	// GitHub App installation auth, used to discover a newly mapped repo's
	// delivery profile (see discoverDoneState). Independent of the login flow
	// above and optional: with either field empty, discovery is skipped and a
	// repo mapping keeps its default done_state. GitHubAppPrivateKey is secret
	// PEM — like the other secrets here it is only ever consumed, never logged
	// and never served.
	GitHubAppID         string // LODE_GITHUB_APP_ID
	GitHubAppPrivateKey string // LODE_GITHUB_APP_PRIVATE_KEY

	// SecretsCatalogPath (LODE_SECRETS_CATALOG_PATH) points at the org
	// secrets catalog TOML (a mounted ConfigMap in the deployment). Empty
	// disables the catalog endpoint (404). The file maps names to op:// refs
	// — it holds no values, but vault/item names are mildly sensitive, so it
	// is only ever served authenticated.
	SecretsCatalogPath string

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

	// Metrics is the registry the server registers its instruments on and
	// serves at /metrics on the admin handler. Nil (tests) gets a private
	// empty registry; serve.go passes the process-wide one, which also
	// carries the Go/process collectors and the store's instruments.
	Metrics *prometheus.Registry
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

	// oidc is nil unless OIDC is configured (issuer + client id set). All SSO
	// routes 404 when it is nil.
	oidc *oidc.Verifier

	// gh and tokenCipher are nil unless the GitHub App OAuth client is
	// configured; reserved for the future account-link flow (spec 001 §9.3).
	// Login never touches them — Keycloak is worklode's sole interactive
	// login provider (spec 001 §3).
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

	syncRuns     *prometheus.CounterVec
	syncDuration prometheus.Histogram
	syncItems    *prometheus.CounterVec

	// assignments counts task assignment actions (assign, unassign, start,
	// stop); see assign.go.
	assignments *prometheus.CounterVec

	// cockpitProjections counts attempted project cockpit projection
	// assemblies, by surface (api, web) and outcome; see cockpit.go.
	cockpitProjections *prometheus.CounterVec

	// navigations counts web UI page requests, by destination and outcome;
	// see web.go's navWrap and metrics.go's observeNavigation.
	navigations *prometheus.CounterVec

	// formSubmissions counts the web UI's creation-form POSTs, by form (task,
	// deliverable) and outcome; see webform.go and observeFormSubmission.
	// These are the only web routes that write, so this is where a rejected
	// or refused cockpit write becomes visible.
	formSubmissions *prometheus.CounterVec

	// authzDecisions counts policy decisions by permission and outcome; see
	// authz.go and observeAuthz.
	authzDecisions *prometheus.CounterVec

	// doc sync (spec 025 §15.7): runs by result, request duration, docs synced
	// by kind/outcome, and forced (--force) syncs accepted.

	// localMerges counts tasks named in a local merge report, by result
	// (advanced, duplicate, unknown_task); see merges.go. In a repo where
	// both a webhook and a developer's clone report merges, plenty of
	// "duplicate" is the healthy signal — its disappearance means one of the
	// two reporters has stopped.
	localMerges *prometheus.CounterVec

	// eventSubscriberSeeks counts admin seeks of a subscriber's offsets, by
	// subscriber; see events.go and observeEventSubscriberSeek.
	eventSubscriberSeeks *prometheus.CounterVec

	// eventStreamsActive is the number of open SSE follows and
	// eventStreamEventsSent the events pushed over them; see eventstream.go.
	// Both are unlabelled: a stream is a whole-instance resource, and the
	// only cardinality worth adding (the client) is exactly the unbounded
	// one.
	eventStreamsActive    prometheus.Gauge
	eventStreamEventsSent prometheus.Counter

	// listExpansions counts list endpoint requests that asked for an
	// expansion, by endpoint (tasks, docs) and expansion (detail, body); see
	// observeListExpansion.
	listExpansions *prometheus.CounterVec
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
	}
	s.cliCodes = newCLICodeStore(st.Now)
	s.bgCtx = cfg.BackgroundCtx
	if s.bgCtx == nil {
		s.bgCtx = context.Background()
	}

	reg := cfg.Metrics
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	if err := store.SetBranchTemplate(cfg.BranchTemplate); err != nil {
		return nil, nil, err
	}

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
		key, err := hex.DecodeString(cfg.TokenEncKey)
		if err != nil || len(key) != 32 {
			return nil, nil, fmt.Errorf("LODE_TOKEN_ENC_KEY must be 64 hex chars (32 bytes)")
		}
		tc, err := tokencrypt.New(key)
		if err != nil {
			return nil, nil, fmt.Errorf("configure token cipher: %w", err)
		}
		s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret)
		s.tokenCipher = tc
	}

	if s.oidc == nil && cfg.WebOpen {
		s.log.Warn("web UI is serving unauthenticated: no login provider is " +
			"configured and LODE_WEB_OPEN is set; every page and every " +
			"creation form is reachable by anyone who can reach this port")
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
		s.embedder = &embed.OpenAI{URL: cfg.EmbeddingURL, Model: cfg.EmbeddingModel, Key: cfg.EmbeddingAPIKey, Metrics: embed.NewMetrics(reg)}
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

	s.initMetrics(reg)

	mux := http.NewServeMux()
	// Every route below is registered through r, which looks its guard up in
	// routeGuards (router.go) and panics on a pattern the table does not
	// name. The permission a route requires is therefore stated as data, in
	// one reviewable place, rather than implied by how its handler is
	// wrapped; see authz.go for the policy it is checked against.
	r := newRouter(s, mux)

	// Read-mostly web UI. These resolve a session cookie to a subject and
	// apply the policy (webGuard); an unauthenticated visitor is sent to
	// /auth/login, and when no login provider is configured the UI stays open
	// as in v1 — as one named decision rather than a silent passthrough (see
	// authOpen, and the open-UI follow-up in docs/follow-ups.md). The seven
	// global destinations (spec 032 §2) and the project-local destinations
	// below each record one worklode_web_navigation_requests_total
	// observation via navWrap.
	r.web("GET /{$}", s.navWrap("home", s.homePage))
	r.web("GET /intake", s.navWrap("intake", s.globalPlaceholder("intake", "Intake",
		"Intake capture and the Discovery-to-Editorial-Evaluation pipeline arrive with spec 032 §5 and spec 029 §8.")))
	r.web("GET /projects", s.navWrap("projects", s.projectsPage))
	r.web("GET /projects/{id}", s.navWrap("projects", s.projectPage))
	// The literal-segment patterns win over the {section} wildcard below for
	// the destinations that are built; everything else still lands on the
	// honest placeholder.
	r.web("GET /projects/{id}/deliverables", s.navWrap("deliverables", s.deliverablesPage))
	r.web("GET /projects/{id}/deliverables/new", s.navWrap("deliverable_new", s.newDeliverablePage))
	r.web("POST /projects/{id}/deliverables", s.navWrap("deliverable_new", s.createDeliverableFromForm))
	r.web("GET /projects/{id}/tasks/new", s.navWrap("task_new", s.newTaskPage))
	r.web("POST /projects/{id}/tasks", s.navWrap("task_new", s.createTaskFromForm))
	r.web("GET /projects/{id}/{section}", s.navWrap("project_section", s.projectSectionPage))
	r.web("GET /work", s.navWrap("work", s.workPage))
	r.web("GET /reviews", s.navWrap("reviews", s.globalPlaceholder("reviews", "Reviews",
		"Decisions awaiting the current actor arrive with spec 029 §7 and spec 032 §7.")))
	r.web("GET /deliveries", s.navWrap("deliveries", s.globalPlaceholder("deliveries", "Deliveries",
		"Publication, deployment, and operational delivery evidence arrive with spec 029 §3 and spec 004 §5.")))
	r.web("GET /knowledge", s.navWrap("knowledge", s.globalPlaceholder("knowledge", "Knowledge",
		"Documents and graph-backed expert views arrive with specs 025, 026, and 006.")))
	r.web("GET /tasks/{id}", s.taskPage)
	r.public("GET /assets/", s.assetHandler())
	r.publicFunc("GET /auth/login", s.authLogin)
	r.publicFunc("GET /auth/callback", s.authCallback)

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
	hookMetrics := hooks.NewMetrics(reg)
	r.public("POST /hooks/github", hooks.NewGitHubHandler(st, cfg.GitHubWebhookSecret, s.log, onSkillPush, s.appAuth, hookMetrics))
	r.public("POST /hooks/flux", hooks.NewFluxHandler(st, cfg.FluxWebhookSecret, cfg.ClusterEnvMap, s.log, hookMetrics))

	// SSO token exchange + config discovery for the CLI login flow. Registered
	// outside the /api/v1 bearer-auth middleware, like /healthz and /hooks/*.
	// Both 404 when OIDC is unconfigured (s.oidc == nil).
	r.publicFunc("GET /auth/oidc/config", s.oidcConfig)
	r.publicFunc("POST /auth/oidc/token", s.oidcTokenExchange)

	// Provider-neutral, server-mediated CLI login (see cliauth.go).
	r.publicFunc("GET /.well-known/lode-login", s.wellKnownLogin)
	r.publicFunc("GET /auth/cli/login", s.cliLogin)
	r.publicFunc("POST /auth/cli/token", s.cliToken)

	r.api("POST /api/v1/tasks", s.createTask)
	r.api("GET /api/v1/tasks", s.listTasks)
	r.api("GET /api/v1/tasks/{id}", s.getTask)
	r.api("GET /api/v1/tasks/{id}/brief", s.taskBrief)
	r.api("PATCH /api/v1/tasks/{id}", s.patchTask)
	r.api("PUT /api/v1/tasks/{id}/skills", s.setTaskSkills)
	r.api("POST /api/v1/tasks/{id}/edges", s.addEdge)
	r.api("DELETE /api/v1/tasks/{id}/edges", s.removeEdge)
	r.api("POST /api/v1/tasks/{id}/decompose", s.decomposeTask)
	r.api("POST /api/v1/tasks/claim-next", s.claimNext)
	r.api("POST /api/v1/tasks/{id}/claim", s.claimTask)
	r.api("POST /api/v1/tasks/{id}/renew", s.renewLease)
	r.api("POST /api/v1/tasks/{id}/release", s.releaseLease)
	r.api("POST /api/v1/tasks/{id}/assign", s.assignTask)
	r.api("POST /api/v1/tasks/{id}/unassign", s.unassignTask)
	r.api("POST /api/v1/tasks/{id}/start", s.startTask)
	r.api("POST /api/v1/tasks/{id}/stop", s.stopTask)
	r.api("POST /api/v1/tasks/{id}/lease/worktree", s.rebindWorktree)
	r.api("POST /api/v1/tasks/{id}/agent-session", s.touchAgentSession)
	r.api("POST /api/v1/tasks/{id}/agent-session/end", s.endAgentSession)
	r.api("POST /api/v1/tasks/{id}/done", s.doneTask)
	r.api("POST /api/v1/tasks/{id}/abandon", s.abandonTask)
	r.api("POST /api/v1/tasks/{id}/reopen", s.reopenTask)
	r.api("GET /api/v1/tasks/{id}/timeline", s.taskTimeline)

	r.api("GET /api/v1/skills", s.listSkills)
	r.api("GET /api/v1/skills/{name}", s.getSkill)
	r.api("GET /api/v1/skills/{name}/archive/{hash}", s.skillArchive)
	r.api("POST /api/v1/skills/recommend", s.recommendSkills)
	r.api("POST /api/v1/skills/sync", s.syncSkills)

	r.api("POST /api/v1/runtime-events", s.createRuntimeEvent)

	r.api("GET /api/v1/secrets/catalog", s.secretsCatalog)
	r.api("POST /api/v1/merges", s.reportMerge)

	// Project, actor, and token management is admin-only (permProjectAdmin /
	// permActorAdmin in routeGuards): any bearer token may otherwise mint
	// further tokens, which is privilege escalation.
	r.api("POST /api/v1/projects", s.createProject)
	r.api("GET /api/v1/projects", s.listProjects)
	// Literal segment, so Go's mux prefers it over the wildcard route below.
	r.api("GET /api/v1/projects/resolve", s.resolveProjectByRemote)
	r.api("GET /api/v1/projects/{id}", s.getProject)
	r.api("GET /api/v1/projects/{id}/cockpit", s.projectCockpit)
	r.api("GET /api/v1/projects/{id}/deliverables", s.listProjectDeliverables)
	r.api("POST /api/v1/projects/{id}/deliverables", s.createDeliverable)
	r.api("PATCH /api/v1/projects/{id}", s.patchProject)
	r.api("POST /api/v1/projects/{id}/repos", s.addRepo)
	r.api("PATCH /api/v1/repos/{owner}/{name}", s.patchRepo)

	r.api("POST /api/v1/actors", s.createActor)
	r.api("POST /api/v1/actors/{id}/tokens", s.createToken)
	r.api("DELETE /api/v1/tokens", s.revokeToken)

	// The repo half of an inbox item contains a slash ("owner/name"), so
	// promote/dismiss take it as a body field instead of a path segment.
	r.api("GET /api/v1/inbox", s.listInbox)
	r.api("POST /api/v1/inbox/promote", s.promoteInbox)
	r.api("POST /api/v1/inbox/dismiss", s.dismissInbox)
	r.api("POST /api/v1/inbox/link", s.linkInbox)
	r.api("POST /api/v1/inbox/import", s.importInbox)

	r.api("GET /api/v1/board", s.board)

	r.api("GET /api/v1/events", s.listEvents)
	r.api("GET /api/v1/events/stream", s.streamEvents)
	r.api("GET /api/v1/event-subscribers", s.listEventSubscribers)
	r.api("POST /api/v1/event-subscribers/{name}/seek", s.seekEventSubscriber)

	// The table describes exactly the routes above: an entry nothing
	// registered is dead policy that reads like a guard, so it fails the boot
	// rather than sitting in the file looking enforced.
	if err := r.checkComplete(); err != nil {
		return nil, nil, err
	}

	// Admin handler: health and metrics on a dedicated listener, never routed
	// through the public ingress. No auth or request-metrics middleware — the
	// metrics endpoint must not count its own scrapes.
	admin := http.NewServeMux()
	admin.HandleFunc("GET /healthz", s.healthz)
	// ContinueOnError: one failing collector (e.g. the store's lease query
	// timing out) must not 500 the whole scrape and take every other family
	// with it. Registry exposes promhttp_metric_handler_errors_total so a
	// persistently failing collector is still visible, and ErrorLog reports
	// what actually failed.
	admin.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      reg,
		ErrorLog:      slog.NewLogLogger(s.log.Handler(), slog.LevelError),
	}))

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

// syncOnce runs a single bounded sync pass, records its metrics, and logs
// its outcome.
func (s *server) syncOnce(ctx context.Context, reason string) {
	ctx, cancel := context.WithTimeout(ctx, skillSyncTimeout)
	defer cancel()
	start := time.Now()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	s.observeSkillSync(sum, err, time.Since(start))
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

// Unwrap exposes the wrapped writer to http.ResponseController, which walks
// the Unwrap chain to find the optional interfaces net/http's own writer
// implements.
//
// Without it, nothing served by this server can stream. Embedding an
// interface promotes only that interface's methods, so *statusWriter is not
// an http.Flusher no matter what it wraps — and logging and metrics wrap
// every single request in one, twice over. A handler that flushed would
// silently buffer instead, which for a server-sent-event stream means the
// client sees nothing until the connection closes.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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

// auth wraps an /api/v1 handler with bearer-token authentication: it is the
// authentication half only, and puts both the actor (for handlers that
// attribute a write) and the derived Subject (for the policy check) into the
// request context. Authorization is requirePerm's job, which the router
// always composes inside this — see authz.go.
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
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, actor))
		next(w, withSubject(r, subjectFromActor(actor, authToken)))
	})
}

// actorFrom returns the authenticated actor, or nil outside the auth
// middleware.
func actorFrom(r *http.Request) *store.Actor {
	a, _ := r.Context().Value(actorKey{}).(*store.Actor)
	return a
}

// requireAdmin is gone: "admin only" is now a property of a permission in
// authz.go's grants table (permProjectAdmin, permActorAdmin, permSkillAdmin,
// permInboxAdmin), applied by requirePerm to whichever routes routeGuards
// names. The refusal is unchanged, message included — see denialMessage.

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
