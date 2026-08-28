// Package api implements the lode HTTP server: bearer-token auth, JSON task
// endpoints, and Prometheus metrics. Handlers stay thin — parse/validate,
// call store functions through RecordEvent, map errors, respond.
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
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

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/oidc"
	"github.com/sunstoneinstitute/worklode/internal/overview"
	"github.com/sunstoneinstitute/worklode/internal/reconcile"
	"github.com/sunstoneinstitute/worklode/internal/safefetch"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/storederive"
	"github.com/sunstoneinstitute/worklode/internal/tokencrypt"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// Config carries server configuration. The webhook secrets and cluster/env
// map are consumed by the /hooks/github, /hooks/flux and /hooks/catalog
// endpoints.
type Config struct {
	BootstrapToken       string            // LODE_BOOTSTRAP_TOKEN: create the first admin actor if the store is empty
	GitHubWebhookSecret  string            // LODE_GITHUB_WEBHOOK_SECRET
	FluxWebhookSecret    string            // LODE_FLUX_WEBHOOK_SECRET
	CatalogWebhookSecret string            // LODE_CATALOG_WEBHOOK_SECRET
	ClusterEnvMap        map[string]string // LODE_CLUSTER_ENV_MAP: cluster name -> environment

	// InstanceEnv (LODE_INSTANCE_ENV) is which kind of instance this is: "dev"
	// or "prod", nothing else, empty meaning prod (039 §3). It is not
	// ClusterEnvMap, which describes the deployments worklode observes. Today
	// it decides only whether a delete must carry a justification (044 §3).
	// NewServer normalises and validates it, so an unrecognised value fails
	// the boot rather than being read as either answer.
	InstanceEnv string

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
	// secrets catalog TOML (a mounted Secret, projected per environment by an
	// ExternalSecret and mounted optional). Empty — or an absent file, in an
	// environment with no catalog — disables the endpoint (404). It maps
	// names to op:// refs and holds no values, but vault/item names are
	// mildly sensitive, so it is only ever served authenticated.
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

	// Speech-to-text for the cockpit's dictation button (WL-299). Off unless
	// SpeechToTextAPIKey is set: the forms then render no microphone and
	// POST /dictate answers 503, with everything else unchanged.
	// SpeechToTextURL overrides the ElevenLabs API base for tests and
	// self-hosted gateways; empty means https://api.elevenlabs.io.
	SpeechToTextAPIKey string // LODE_ELEVENLABS_API_KEY
	SpeechToTextURL    string // LODE_ELEVENLABS_URL

	// Blob storage (spec 021). Off unless BlobEndpoint and BlobBucket are
	// both set: uploads then return 501 and every other surface behaves
	// exactly as before, so a local docker-compose stack needs no bucket.
	// Once they are set the access and secret keys are required too, and a
	// missing one fails the boot rather than 502ing every blob operation.
	BlobEndpoint  string // LODE_BLOB_ENDPOINT, e.g. https://hel1.your-objectstorage.com
	BlobBucket    string // LODE_BLOB_BUCKET
	BlobRegion    string // LODE_BLOB_REGION
	BlobAccessKey string // LODE_BLOB_ACCESS_KEY
	BlobSecretKey string // LODE_BLOB_SECRET_KEY
	BlobSpoolDir  string // LODE_BLOB_SPOOL_DIR; empty means os.TempDir()

	// BlobStoreForTest injects a blobstore.Store directly, bypassing the
	// S3 construction above. Tests only; production sets BlobEndpoint.
	BlobStoreForTest blobstore.Store

	// MaxBlobBytesForTest lowers the upload cap from maxBlobBytes. Tests
	// only, and deliberately not an operator knob: 100 MiB is spec 021 §5's
	// number, and a test that wants to see the 413 should not have to spool
	// 100 MiB to prove it. Zero means the real cap.
	MaxBlobBytesForTest int64

	// BackgroundCtx governs goroutines NewServer starts on its own (boot
	// skill sync, webhook-triggered skill syncs) — not any HTTP request.
	// Defaults to context.Background() when nil, so background syncs run
	// unbounded and are never cancelled; pass the process's shutdown context
	// (e.g. the one built around signal.NotifyContext in cmd/serve.go) to
	// have them abort on shutdown instead of outliving the server.
	//
	// Passing it non-nil is additionally what starts the doc-lifecycle
	// subscriber, which unlike the skill sync has no configuration of its own
	// to gate on.
	BackgroundCtx context.Context

	// EventPoll is how often the doc-lifecycle subscriber polls the log. Zero
	// takes eventbus's 1s default; e2e sets it low so the test does not wait
	// a second per step.
	EventPoll time.Duration

	// Graph is the knowledge-graph client, nil when LODE_GRAPHSERVER_URL is
	// unset; shared with the projector so one process has one client.
	//
	// A live client rather than a URL because serve.go already builds one for
	// the projector: the endpoint, its OAuth client credentials and their
	// token source are graphserver.FromEnv's business (spec 006 §11), and
	// handing the server a URL would mean a second client with a second token
	// source against the same endpoint. Nil disables the graph-backed reads
	// (drift, gaps) and POST /api/v1/derive; the frontier and the critical
	// path are backbone-authoritative and answer either way.
	Graph *graphserver.Client

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

	// overview is spec 007's read surface (see overview.go). Always non-nil:
	// the frontier and the critical path are computed from the backbone, so
	// they answer on an instance with no graph configured. cfg.Graph being
	// nil is what disables the graph-backed reads, inside the service.
	overview *overview.Service

	// repoReader is the pr-affects deriver's GitHub reader, nil when the
	// GitHub App is not configured (postDerive then refuses with 503). Built
	// once and shared: it caches one installation token per repo, so a run
	// over many PRs in one repo mints one token rather than one per read.
	repoReader *storederive.GitHubReader

	// hookMetrics is the webhook/replay instrument set (internal/hooks),
	// shared by the webhook handlers and POST /api/v1/reconcile's replay
	// call. Set in registerRoutes, right after hooks.NewMetrics(reg); every
	// *hooks.Metrics method is nil-safe (internal/hooks/metrics.go), so
	// callers don't need it non-nil.
	hookMetrics *hooks.Metrics

	// pollMetrics is engine 2's instrument set (internal/reconcile), used by
	// POST /api/v1/reconcile's poll call. Set in registerRoutes alongside
	// hookMetrics; every *reconcile.Metrics method is nil-safe.
	pollMetrics *reconcile.Metrics

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

	// navDestinations are the web navigation destinations the registered
	// routes wrap, collected by navWrap and pre-initialised by
	// initNavMetrics once registration is complete.
	navDestinations []string

	// watcherMetrics counts doc-lifecycle rule outcomes (docwatch.go). Nil
	// in every server that starts no subscriber; *watcher.Metrics is
	// nil-safe, so the handler is still callable directly in tests.
	watcherMetrics *watcher.Metrics

	// cliCodes holds pending one-time codes for the server-mediated CLI login.
	cliCodes *cliCodeStore

	// blobs is the object-storage backend for spec 021's content-addressed
	// blobs, or nil when no bucket is configured — the blob endpoints then
	// answer 501 and nothing else changes.
	blobs blobstore.Store

	// mirrorFetcherForTest overrides the SSRF-guarded fetcher used by
	// mirrorRemoteImages. Tests only; production leaves it nil so the host
	// allowlist and IP checks apply.
	mirrorFetcherForTest *safefetch.Fetcher

	// mdcache memoises rendered task- and document-body HTML on the body's
	// content hash, so a hostile body costs one render per edit rather than
	// one per view (WL-222). Nil in every test that builds a *server directly;
	// *mdrender.Cache is nil-safe and renders each call when nil.
	mdcache *mdrender.Cache

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

	// homeRenders counts Home page renders, by mode (actor, open, empty);
	// see web.go's homePage and metrics.go's observeHomeRender. navWrap
	// already counts the "home" destination by outcome — this one says which
	// Home the viewer got.
	homeRenders *prometheus.CounterVec

	// formSubmissions counts the web UI's creation-form POSTs, by form (task,
	// deliverable, crew_add, crew_remove) and outcome; see webform.go and
	// observeFormSubmission.
	// These are the only web routes that write, so this is where a rejected
	// or refused cockpit write becomes visible.
	formSubmissions *prometheus.CounterVec
	// dictations counts POST /dictate outcomes (WL-299); see metrics.go.
	dictations *prometheus.CounterVec
	// taskTokens counts task-scoped token mints (WL-306); see metrics.go.
	taskTokens *prometheus.CounterVec

	// crewChanges counts Crew membership changes, by surface (api, web),
	// action (add, remove), and outcome; see crew.go and observeCrewChange. Labels
	// are bounded on purpose: a project id, an actor id or a role label
	// would each be unbounded cardinality, and none of the three belongs in
	// a metric.
	crewChanges *prometheus.CounterVec

	// authzDecisions counts policy decisions by permission and outcome; see
	// authz.go and observeAuthz.
	authzDecisions *prometheus.CounterVec

	// approvalDecisions counts decisions submitted to POST
	// /approvals/{id}/decide, by decision and outcome; see webform.go and
	// observeApprovalDecision. The session refusal in front of the route is
	// not counted here — worklode_authz_decisions_total already carries it.
	approvalDecisions *prometheus.CounterVec

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

	// blobUploads and blobServes count the two spec 021 blob endpoints by
	// outcome; see blobs.go and observeBlobUpload/observeBlobServe. The
	// generic http_requests_total cannot stand in: a dedup and a fresh store
	// are both 200, and a "too large" and a rejected body are both 4xx, yet
	// each pair says something different about whether the bucket is filling
	// up or clients are misbehaving.
	blobUploads *prometheus.CounterVec
	blobServes  *prometheus.CounterVec

	// posterExtractions counts the ffmpeg run every uploaded video gets, by
	// outcome; see storePoster and observePosterExtraction. It is the only
	// signal that this deployment's image actually carries ffmpeg: a poster
	// that cannot be extracted is not an upload failure, so blobUploads reads
	// "stored" either way and nothing else would notice.
	posterExtractions *prometheus.CounterVec

	// blobGCRuns counts POST /api/v1/blobs/gc invocations by mode (dry_run,
	// apply) and outcome; blobGCObjects counts what each run found or acted
	// on, by action. See blobgc.go and observeBlobGC.
	blobGCRuns    *prometheus.CounterVec
	blobGCObjects *prometheus.CounterVec

	// imageMirrors counts remote issue images mirroring attempted to turn
	// into blobs on promote, by outcome; see inbox_mirror.go and
	// observeImageMirror. Distinct from blobUploads because this is the one
	// blob path driven by an outbound fetch of an attacker-chosen URL:
	// sustained fetch_failed is GitHub or the SSRF guard refusing, and every
	// such failure leaves a remote reference in a body that §8 then renders
	// as nothing.
	imageMirrors *prometheus.CounterVec

	// mirrorTokens counts the installation tokens a mirroring pass minted for
	// the images it was about to fetch, by outcome; see inbox_mirror.go and
	// observeMirrorToken. Counted apart from imageMirrors because one token
	// covers a whole pass, and because a failure here is silent by design:
	// mirroring falls back to unauthenticated fetches, so a private repo's
	// images turn into fetch_failed with nothing on that series saying why.
	mirrorTokens *prometheus.CounterVec

	// taskBlobRefs counts the explicit half of a task's blob reference graph
	// changed by the attach/detach endpoints, by action; see blobs.go and
	// observeTaskBlobRef. The embedded half follows the task body via
	// ReconcileEmbedded and is not counted here — it is not a distinct
	// caller action.
	taskBlobRefs *prometheus.CounterVec

	// deletes counts spec 044's tombstone operations by entity, op and
	// outcome; see softdelete.go and observeDelete. http_requests_total cannot
	// stand in for the outcome that matters: a prod instance refusing a
	// justification-less delete and a delete of a row that does not exist are
	// both 4xx, and only the first says a client has not learned the rule.
	deletes *prometheus.CounterVec

	// kindAliasUses counts requests naming a deprecated task kind that was
	// normalised to its current name, by alias and surface; see
	// kindalias.go. A sustained zero across these surfaces is not the whole
	// picture: plan-document bodies (internal/designdoc/plantasks.go) are a
	// stored input normalised the same way but not counted here, since they
	// are discoverable by querying the documents themselves rather than by
	// watching a request-shaped metric (WL-138).
	kindAliasUses *prometheus.CounterVec

	// overviewReads counts spec 007's five read endpoints by read and
	// outcome, and deriveRuns counts each server-side deriver a POST
	// /api/v1/derive ran, by source and outcome. See overview.go and
	// observeOverviewRead/observeDeriveRun. http_requests_total cannot stand
	// in for either: a graph-less instance answering 503 and a broken SPARQL
	// endpoint answering 500 are both "not 200", and one POST runs two
	// derivers whose outcomes differ — a skipped no-op and a replaced graph
	// are the same 200.
	overviewReads *prometheus.CounterVec
	deriveRuns    *prometheus.CounterVec
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

// requireWebSecrets checks the two settings every browser-facing login
// provider needs, naming the feature in the refusal. Both providers stated
// these checks identically before, which also meant parsing PublicURL twice
// when both were configured.
func (cfg Config) requireWebSecrets(feature string) error {
	if cfg.SessionSecret == "" {
		return fmt.Errorf("LODE_SESSION_SECRET is required when %s is enabled", feature)
	}
	if cfg.PublicURL == "" {
		return fmt.Errorf("LODE_PUBLIC_URL is required when %s is enabled", feature)
	}
	return validatePublicURL(cfg.PublicURL)
}

// registerRoutes builds the public mux: the whole route table in one place,
// separate from NewServer's config validation and background startup.
//
// Every route is registered through r, which looks its guard up in
// routeGuards (router.go) and panics on a pattern the table does not name.
// The permission a route requires is therefore stated as data, in one
// reviewable place, rather than implied by how its handler is wrapped; see
// authz.go for the policy it is checked against.
func (s *server) registerRoutes(reg prometheus.Registerer) (*http.ServeMux, error) {
	mux := http.NewServeMux()
	r := newRouter(s, mux)

	// Read-mostly web UI. These resolve a session cookie to a subject and
	// apply the policy (webGuard); an unauthenticated visitor is sent to
	// /auth/login, and when no login provider is configured the UI stays open
	// as in v1 — as one named decision rather than a silent passthrough (see
	// authOpen, and the open-UI follow-up in docs/follow-ups.md). The eight
	// global destinations (spec 032 §2) and the project-local destinations
	// below each record one worklode_web_navigation_requests_total
	// observation via navWrap.
	r.web("GET /{$}", s.navWrap("home", s.homePage))
	r.web("GET /ideas", s.navWrap("ideas", s.globalPlaceholder("ideas", "Ideas",
		"Low-friction idea capture, looser than and promotable into Intake, arrives with spec 032 §5.")))
	r.web("GET /intake", s.navWrap("intake", s.globalPlaceholder("intake", "Intake",
		"Intake capture and the Discovery-to-Editorial-Evaluation pipeline arrive with spec 032 §5 and spec 029 §8.")))
	r.web("GET /projects", s.navWrap("projects", s.projectsPage))
	r.web("GET /projects/{id}", s.navWrap("projects", s.projectPage))
	// The literal-segment patterns win over the {section} wildcard below for
	// the destinations that are built; everything else still lands on the
	// honest placeholder.
	r.web("GET /projects/{id}/crew", s.navWrap("crew", s.crewPage))
	r.web("POST /projects/{id}/crew", s.navWrap("crew", s.addCrewMemberFromForm))
	r.web("POST /projects/{id}/crew/remove", s.navWrap("crew", s.removeCrewMemberFromForm))
	r.web("GET /projects/{id}/deliverables", s.navWrap("deliverables", s.deliverablesPage))
	r.web("GET /projects/{id}/deliverables/new", s.navWrap("deliverable_new", s.newDeliverablePage))
	r.web("POST /projects/{id}/deliverables", s.navWrap("deliverable_new", s.createDeliverableFromForm))
	r.web("GET /projects/{id}/tasks/new", s.navWrap("task_new", s.newTaskPage))
	r.web("POST /projects/{id}/tasks", s.navWrap("task_new", s.createTaskFromForm))
	r.web("GET /projects/{id}/deleted", s.navWrap("deleted", s.deletedPage))
	r.web("POST /projects/{id}/deleted/tasks/restore", s.navWrap("deleted", s.restoreTaskFromForm))
	r.web("POST /projects/{id}/deleted/docs/restore", s.navWrap("deleted", s.restoreDocFromForm))
	// The MarkdownInput component's fragment endpoints (WL-299): not
	// navWrapped — they answer a fragment or JSON to the component's fetch,
	// never a navigated page.
	r.web("POST /preview", s.previewMarkdown)
	r.web("POST /dictate", s.dictate)
	r.web("GET /projects/{id}/{section}", s.navWrap("project_section", s.projectSectionPage))
	r.web("GET /work", s.navWrap("work", s.workPage))
	r.web("GET /reviews", s.navWrap("reviews", s.reviewsPage))
	r.web("GET /deliveries", s.navWrap("deliveries", s.globalPlaceholder("deliveries", "Deliveries",
		"Publication, deployment, and operational delivery evidence arrive with spec 029 §3 and spec 004 §5.")))
	// Knowledge is the document corpus: spec 032 §2 defines the destination
	// as "documents and graph-backed expert views", and /docs is the half of
	// it that exists. /knowledge stays as a redirect so the spec's own
	// spelling of the URL resolves; it renders no page, so it records no
	// navigation.
	r.web("GET /knowledge", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs", http.StatusFound)
	})
	r.web("GET /tasks/{id}", s.taskPage)
	// The document corpus (spec 025 §5) is read-only in the cockpit: writing
	// a document is an authoring act performed through the API and the CLI,
	// where the body — the artifact itself — comes from a file.
	r.web("GET /docs", s.navWrap("knowledge", s.docsPage))
	r.web("GET /docs/{id}", s.navWrap("knowledge", s.docPage))
	// /docs/versions/{id}/{n}, not /docs/{id}/versions/{n}: see routeGuards'
	// comment on this route in router.go.
	r.web("GET /docs/versions/{id}/{n}", s.navWrap("knowledge", s.docVersionPage))
	// The reference redirect (WL-301): not navWrapped — it answers a 302,
	// never a page.
	r.web("GET /docs/ref/{ref...}", s.docRefRedirect)
	// The drift board (spec 007) is the graph-backed half of Knowledge, so it
	// marks that destination current rather than taking an eighth nav entry
	// (see primaryNav's doc comment). Read-only: it renders no act.
	r.web("GET /drift", s.navWrap("drift", s.driftPage))
	// Deciding an approval is a web-session act (029 §7.3): the session's
	// group claims are at most as old as the login that stored them, a bearer
	// token's are as old as the token, and an open instance has no identity to
	// attribute a decision to. requireSession is applied here, at
	// registration, so no other route can reach the handler without it — and
	// there is deliberately no CLI verb and no /api/v1 route for it.
	r.web("POST /approvals/{id}/decide", s.navWrap("approval_decide", s.requireSession(s.decideApproval)))
	r.public("GET /assets/", s.assetHandler())
	// The blob asset route (spec 021 §4). Neither an API route nor a web
	// page: a browser <img> on a task page fetches it with a session cookie
	// and an agent fetches it with a bearer token, so it takes either — see
	// eitherGuard in authz.go and r.asset in router.go.
	r.asset("GET /blob/{hash}", s.serveBlob)
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
	s.hookMetrics = hookMetrics
	s.pollMetrics = reconcile.NewMetrics(reg)
	r.public("POST /hooks/github", hooks.NewGitHubHandler(s.st, s.cfg.GitHubWebhookSecret, s.log, onSkillPush, s.appAuth, hookMetrics))
	r.public("POST /hooks/flux", hooks.NewFluxHandler(s.st, s.cfg.FluxWebhookSecret, s.cfg.ClusterEnvMap, s.log, hookMetrics))
	r.public("POST /hooks/catalog", hooks.NewCatalogHandler(s.st, s.cfg.CatalogWebhookSecret, s.log, hookMetrics))

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
	r.api("GET /api/v1/tasks/{id}/cost", s.getTaskCost)
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
	r.api("POST /api/v1/tasks/{id}/instructions", s.enqueueInstruction)
	r.api("POST /api/v1/instructions/claim", s.claimInstructions)
	r.api("POST /api/v1/tasks/{id}/done", s.doneTask)
	r.api("POST /api/v1/tasks/{id}/abandon", s.abandonTask)
	r.api("POST /api/v1/tasks/{id}/reopen", s.reopenTask)
	r.api("GET /api/v1/tasks/{id}/timeline", s.taskTimeline)
	r.api("DELETE /api/v1/tasks/{id}", s.deleteTask)
	r.api("POST /api/v1/tasks/{id}/undelete", s.undeleteTask)
	r.api("GET /api/v1/tasks/{id}/blobs", s.listTaskBlobs)
	r.api("POST /api/v1/tasks/{id}/blobs", s.attachTaskBlob)
	r.api("DELETE /api/v1/tasks/{id}/blobs/{hash}", s.detachTaskBlob)

	r.api("POST /api/v1/docs", s.createDoc)
	r.api("GET /api/v1/docs", s.listDocs)
	r.api("GET /api/v1/docs/resolve", s.resolveDocRef)
	r.api("GET /api/v1/docs/{id}", s.getDoc)
	r.api("GET /api/v1/docs/{id}/versions", s.listDocVersions)
	r.api("GET /api/v1/docs/{id}/versions/{n}", s.getDocVersion)
	r.api("PUT /api/v1/docs/{id}/body", s.updateDocBody)
	r.api("PUT /api/v1/docs/{id}/edges", s.replaceDocEdges)
	r.api("POST /api/v1/docs/{id}/submit", s.submitDoc)
	r.api("POST /api/v1/docs/{id}/accept", s.acceptDoc)
	r.api("POST /api/v1/docs/{id}/revise", s.reviseDoc)
	r.api("POST /api/v1/docs/{id}/owner", s.transferDocOwner)
	r.api("POST /api/v1/docs/{id}/request-approval", s.requestDocApproval)
	r.api("PUT /api/v1/docs/{id}/revision", s.updateDocRevision)
	r.api("DELETE /api/v1/docs/{id}/revision", s.discardDocRevision)
	r.api("POST /api/v1/docs/{id}/revision/accept", s.acceptDocRevision)
	r.api("DELETE /api/v1/docs/{id}", s.deleteDoc)
	r.api("POST /api/v1/docs/{id}/undelete", s.undeleteDoc)

	// Requesting and listing only. Deciding is web-session-gated
	// (029 §7.3) and lives at POST /approvals/{id}/decide; see approvals.go.
	r.api("GET /api/v1/approvals", s.listApprovals)

	r.api("GET /api/v1/skills", s.listSkills)
	r.api("GET /api/v1/skills/{name}", s.getSkill)
	r.api("GET /api/v1/skills/{name}/archive/{hash}", s.skillArchive)
	r.api("POST /api/v1/skills/recommend", s.recommendSkills)
	r.api("POST /api/v1/skills/sync", s.syncSkills)

	r.api("POST /api/v1/runtime-events", s.createRuntimeEvent)

	r.api("POST /api/v1/blobs", s.uploadBlob)
	r.api("POST /api/v1/blobs/gc", s.blobGC)

	r.api("GET /api/v1/secrets/catalog", s.secretsCatalog)
	r.api("POST /api/v1/tasks/{id}/secrets-materialized", s.secretsMaterialized)
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
	r.api("GET /api/v1/projects/{id}/participants", s.listCrewMembers)
	r.api("POST /api/v1/projects/{id}/participants", s.addCrewMember)
	r.api("DELETE /api/v1/projects/{id}/participants/{actor}", s.removeCrewMember)
	r.api("PATCH /api/v1/projects/{id}", s.patchProject)
	r.api("POST /api/v1/projects/{id}/session-usage", s.reportProjectSessionUsage)
	r.api("POST /api/v1/projects/{id}/repos", s.addRepo)
	r.api("PATCH /api/v1/repos/{owner}/{name}", s.patchRepo)
	r.api("GET /api/v1/repos/doctor", s.reposDoctor)

	r.api("POST /api/v1/actors", s.createActor)
	r.api("POST /api/v1/tasks/{id}/tokens", s.mintTaskToken)
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
	r.api("GET /api/v1/graph/projection/failures", s.listProjectionFailures)
	r.api("POST /api/v1/event-subscribers/{name}/seek", s.seekEventSubscriber)

	r.api("GET /api/v1/whoami", s.whoami)
	r.api("POST /api/v1/reconcile", s.reconcile)

	// Spec 007's read surface (overview.go). The frontier and the critical
	// path answer from the backbone alone; drift and gaps need a graph and
	// 503 without one. The derive POST is admin-only — it replaces org-wide
	// named graphs.
	r.api("GET /api/v1/overview", s.getOverview)
	r.api("GET /api/v1/drift", s.getDrift)
	r.api("GET /api/v1/gaps", s.getGaps)
	r.api("GET /api/v1/frontier", s.getFrontier)
	r.api("GET /api/v1/critical-path", s.getCriticalPath)
	r.api("POST /api/v1/derive", s.postDerive)

	// The table describes exactly the routes above: an entry nothing
	// registered is dead policy that reads like a guard, so it fails the boot
	// rather than sitting in the file looking enforced.
	if err := r.checkComplete(); err != nil {
		return nil, err
	}
	// The nav zero-series are minted by the caller (NewServer), not here: they
	// are derived from what registration collected, so anything that runs
	// inside this function could be outrun by a route registered below it.
	return mux, nil
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

	// Normalised here as well as in serve.go, so an embedder that builds a
	// Config in Go — every test in this package included — cannot end up with
	// an unset or bogus environment. Empty becomes prod; anything but dev or
	// prod refuses the boot (039 §3).
	instanceEnv, err := ParseInstanceEnv(cfg.InstanceEnv)
	if err != nil {
		return nil, nil, err
	}
	s.cfg.InstanceEnv = instanceEnv

	if err := store.SetBranchTemplate(cfg.BranchTemplate); err != nil {
		return nil, nil, err
	}

	if cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" {
		if err := cfg.requireWebSecrets("OIDC"); err != nil {
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
		if err := cfg.requireWebSecrets("GitHub auth"); err != nil {
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

	// Spec 007's read surface, and the reader its server-side derivers need.
	// The service is always built — see the field comment — while the reader
	// exists only when the GitHub App does.
	s.overview = &overview.Service{Store: st, Graph: cfg.Graph}
	if appAuth != nil {
		s.repoReader = &storederive.GitHubReader{Auth: appAuth}
	}

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

	// Blob storage (spec 021). The feature is off unless an endpoint and a
	// bucket are both named; the injected store is the test path and wins, so
	// a test never has to stand up an S3 endpoint to exercise the handlers.
	// NewS3 then requires the credentials as well, so a half-configured
	// deployment refuses to boot instead of serving 502s.
	switch {
	case cfg.BlobStoreForTest != nil:
		s.blobs = cfg.BlobStoreForTest
	case cfg.BlobEndpoint != "" && cfg.BlobBucket != "":
		bs, err := blobstore.NewS3(blobstore.S3Config{
			Endpoint:  cfg.BlobEndpoint,
			Bucket:    cfg.BlobBucket,
			Region:    cfg.BlobRegion,
			AccessKey: cfg.BlobAccessKey,
			SecretKey: cfg.BlobSecretKey,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("blob store: %w", err)
		}
		s.blobs = bs
	}
	// Fail the boot, not the first upload, on an unwritable spool directory:
	// a read-only root filesystem with no volume mounted there 500s every
	// upload while the pod reports healthy. Gated on s.blobs rather than on
	// BlobEndpoint because the injected test store spools the same way.
	if s.blobs != nil {
		if err := checkSpoolWritable(cfg.BlobSpoolDir); err != nil {
			return nil, nil, err
		}
	}

	if cfg.BootstrapToken != "" {
		if err := st.BootstrapAdmin(context.Background(), cfg.BootstrapToken); err != nil {
			return nil, nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}

	s.initMetrics(reg)

	mux, err := s.registerRoutes(reg)
	if err != nil {
		return nil, nil, err
	}
	// After registration, never inside it: the set of nav destinations is only
	// complete once every route has been registered, so where a route sits
	// within registerRoutes cannot cost it its zero-series.
	s.initNavMetrics()

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

	// The doc-lifecycle subscriber (025 §15.4). Gated on the caller having
	// passed a background context rather than on s.bgCtx, which defaults to
	// context.Background(): the hundreds of tests that pass none stay
	// loop-free, while serve.go passes the process shutdown context, so
	// production wiring is free.
	if cfg.BackgroundCtx != nil {
		// context.Background(), not bgCtx: these two assertions are part of
		// starting up, so a shutdown racing the boot must fail the boot
		// rather than leave the loop running against a missing row.
		if err := st.EnsureEventSubscriber(context.Background(), docLifecycleSubscriber); err != nil {
			return nil, nil, fmt.Errorf("ensure %s subscriber: %w", docLifecycleSubscriber, err)
		}
		if err := st.EnsureServiceActor(context.Background(), watcherActorID, "doc-lifecycle watcher"); err != nil {
			return nil, nil, fmt.Errorf("ensure %s actor: %w", watcherActorID, err)
		}
		s.watcherMetrics = watcher.NewMetrics(reg)
		// First and only registration of the eventbus instruments: this is
		// the process's one subscriber loop. The horizon collector in
		// metrics.go registers a different family (worklode_event_log_horizon_id),
		// so the two do not collide.
		busMetrics := eventbus.NewMetrics(reg, st)
		go func() {
			err := eventbus.Run(cfg.BackgroundCtx, eventbus.Options{
				Store:   st,
				Name:    docLifecycleSubscriber,
				Handler: s.handleDocLifecycle,
				Poll:    cfg.EventPoll,
				Metrics: busMetrics,
				Log:     s.log,
			})
			// Run always ends with an error; context.Canceled is the ordinary
			// shutdown path and anything else means the subscriber stopped
			// consuming while the server kept serving — a silent stall unless
			// it is said out loud.
			if err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error("doc-lifecycle subscriber stopped", "err", err)
			}
		}()
	}

	return s.observe(mux), admin, nil
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
	writeJSON(w, http.StatusOK, model.HealthResponse{Status: "ok"})
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
// an http.Flusher no matter what it wraps — and observe wraps every single
// request in one. A handler that flushed would silently buffer instead,
// which for a server-sent-event stream means the client sees nothing until
// the connection closes.
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

// observe logs and counts one request. Logging and metrics were two
// middlewares, so every request — SSE streams included — was wrapped in a
// statusWriter and timed twice for what is one measurement.
func (s *server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		elapsed := time.Since(start)
		route := routeLabel(r)
		s.requests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
		s.durations.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		s.log.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", elapsed,
		)
	})
}

// auth wraps an /api/v1 handler with bearer-token authentication: it is the
// authentication half only, and puts the derived Subject — the one identity
// a handler reads, for the policy check and for attributing a write — into
// the request context. Authorization is requirePerm's job, which the router
// always composes inside this — see authz.go.
func (s *server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		actor, boundTask, err := s.st.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			s.mapStoreErr(w, err)
			return
		}
		sub := subjectFromActor(actor, authToken)
		sub.TaskID = boundTask
		next(w, withSubject(r, sub))
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("encode response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, model.ErrorResponse{Error: msg})
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
// ErrForbidden → 403, ErrBadTransition/ErrCycle/ErrInvalidInput → 422,
// ErrLeased/ErrBlocked/ErrRepoTaken/ErrEdgeExists/ErrDocExists/
// ErrRevisionExists → 409, ErrUnknownBlob → 422, anything else → 500 with a
// generic body (the detail is logged, not leaked).
func (s *server) mapStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	// A refusal about this row, not about the endpoint: the document accept
	// and transfer gates (025 §7) admit only the owner, whatever role the
	// caller holds. The message is the store's, because "someone else owns
	// it" is what the caller has to act on.
	case errors.Is(err, store.ErrForbidden):
		writeErr(w, http.StatusForbidden, err.Error())
	// A body citing a hash with no blob row: user error, not a server fault.
	// Matched on the sentinel and not on the constraint name, because the
	// other direction of that same FK -- a GC deleting a blob a task still
	// references -- is a server bug that has to stay a logged 500.
	case errors.Is(err, store.ErrBadTransition),
		errors.Is(err, store.ErrCycle),
		errors.Is(err, store.ErrUnknownBlob),
		errors.Is(err, store.ErrInvalidInput):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	// The name names several skills: the caller has to qualify it, and the
	// store's message lists the candidates they must choose between.
	case errors.Is(err, store.ErrAmbiguousSkill):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrLeased),
		errors.Is(err, store.ErrBlocked),
		errors.Is(err, store.ErrRepoTaken),
		errors.Is(err, store.ErrKeyTaken),
		errors.Is(err, store.ErrEdgeExists),
		errors.Is(err, store.ErrDocExists),
		errors.Is(err, store.ErrRevisionExists):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		s.log.Error("internal error", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

// recordEvent is the shared body of every non-document mutation: a random
// external id, v marshalled as the event payload, and apply inside
// RecordEvent so the write and its event commit together. It returns the
// error for the caller to map with mapStoreErr — a failed id or marshal is
// the same internal error it was when each handler spelled the three steps
// out itself. Document mutations use recordDocEvent, which additionally names
// the op and wraps the payload with the acting subject.
func (s *server) recordEvent(ctx context.Context, source, eventType string, v any,
	apply func(tx *sql.Tx, eventID int64) error) error {
	extID, err := randomExternalID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, _, err = s.st.RecordEvent(ctx, source, extID, eventType, payload, apply)
	return err
}

// recordTaskEvent is recordEvent for an event about one task: the payload is
// v's JSON object with "task" set to the task id, the key every task-scoped
// payload names its subject under (025 §15.2), so GET /api/v1/events
// attributes the event without a second read of state_log. v may be nil for
// an event whose payload is the attribution and nothing else.
//
// Events whose task id is minted inside the transaction (task.created,
// issue.promoted) cannot use this — nothing knows the id yet when the
// payload is marshalled. They call store.AttributeEventToTask from apply
// instead.
func (s *server) recordTaskEvent(ctx context.Context, source, eventType, taskID string, v any,
	apply func(tx *sql.Tx, eventID int64) error) error {
	fields := map[string]json.RawMessage{}
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &fields); err != nil {
			return fmt.Errorf("%s payload is not a JSON object: %w", eventType, err)
		}
	}
	id, err := json.Marshal(taskID)
	if err != nil {
		return err
	}
	fields["task"] = id
	return s.recordEvent(ctx, source, eventType, fields, apply)
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
