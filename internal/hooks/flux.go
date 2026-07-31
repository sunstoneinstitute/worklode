package hooks

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxFluxBody caps webhook request bodies at 5 MiB, the same limit as the
// GitHub handler; Flux notification-controller events are small JSON
// documents well under this.
const maxFluxBody = 5 << 20

// fluxDefaultEnvironment is the environment a deployment is filed under when
// its cluster can't be resolved to a configured environment. See
// resolveEnvironment.
const fluxDefaultEnvironment = "dev"

// fluxTargetKind is the deployments.target_kind value used for both
// Kustomization and HelmRelease events. v1 collapses HelmRelease into
// "flux_kustomization" because the deployments table's target_kind CHECK
// constraint only allows 'flux_kustomization' | 'pypi' | 'manual' — the
// involvedObject.kind distinction is preserved in the event type
// ("flux.HelmRelease.*" vs "flux.Kustomization.*"), not in target_kind.
const fluxTargetKind = "flux_kustomization"

type fluxHandler struct {
	st         *store.Store
	secret     string
	clusterEnv map[string]string
	log        *slog.Logger
	metrics    *Metrics
}

// NewFluxHandler returns the POST /hooks/flux handler. It verifies the
// X-Signature HMAC (Flux notification-controller's "generic-hmac" Provider:
// "sha256=<hex>" over the raw body) against secret, records each delivery
// exactly once, and applies the per-event effects. Flux sends no delivery
// id, so the idempotency key is the SHA-256 of the request body. An empty
// secret makes the handler refuse all requests with 503 — a misconfigured
// server must not accept unauthenticated webhooks.
func NewFluxHandler(st *store.Store, secret string, clusterEnv map[string]string, log *slog.Logger, m *Metrics) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return &fluxHandler{st: st, secret: secret, clusterEnv: clusterEnv, log: log, metrics: m}
}

// fluxEvent is the part of a Flux notification-controller Event payload the
// handler needs.
type fluxEvent struct {
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"involvedObject"`
	Severity  string            `json:"severity"`
	Timestamp string            `json:"timestamp"`
	Message   string            `json:"message"`
	Reason    string            `json:"reason"`
	Metadata  map[string]string `json:"metadata"`
}

func (h *fluxHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	result := "error"
	defer func() { h.metrics.event("flux", "flux", result) }()

	if h.secret == "" {
		writeErr(w, http.StatusServiceUnavailable, "flux webhook secret not configured")
		return
	}

	// The signature covers the exact request bytes: read the raw body first
	// (capped at maxFluxBody), verify, and only then parse.
	r.Body = http.MaxBytesReader(w, r.Body, maxFluxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "read body")
		return
	}
	if !validSignature(h.secret, body, r.Header.Get("X-Signature")) {
		result = "rejected"
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var ev fluxEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	// Flux sends no per-delivery id: the idempotency key is the SHA-256 of
	// the exact bytes received, so a redelivered event (identical body)
	// dedupes and any change to the body counts as a new delivery.
	sum := sha256.Sum256(body)
	externalID := hex.EncodeToString(sum[:])

	kind := ev.InvolvedObject.Kind
	typ := fmt.Sprintf("flux.%s.%s", kind, ev.Reason)

	var apply func(tx *sql.Tx, eventID int64) error
	ignored := kind != "Kustomization" && kind != "HelmRelease"
	if ignored {
		typ += ".ignored"
	} else {
		apply = func(tx *sql.Tx, eventID int64) error {
			return h.apply(tx, eventID, ev)
		}
	}

	_, inserted, err := h.st.RecordEvent(r.Context(), "flux", externalID, typ, body, apply)
	if err != nil {
		h.log.Error("flux webhook: apply", "kind", kind, "reason", ev.Reason, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	switch {
	case !inserted:
		result = "ok"
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
	case ignored:
		result = "ignored"
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	default:
		result = "ok"
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// resolveEnvironment maps a Flux event's cluster to a deployment
// environment. cluster is metadata["cluster"] from the event, or "" if the
// event carries none. Resolution order:
//  1. cluster is set and present in clusterEnv: use that mapping.
//  2. cluster is empty and clusterEnv has exactly one entry: use that
//     entry's environment (a single-cluster deployment has nothing else to
//     disambiguate on).
//  3. anything else (cluster set but unmapped, or cluster empty with zero
//     or several configured clusters): fluxDefaultEnvironment ("dev").
func resolveEnvironment(cluster string, clusterEnv map[string]string) string {
	if cluster != "" {
		if env, ok := clusterEnv[cluster]; ok {
			return env
		}
		return fluxDefaultEnvironment
	}
	if len(clusterEnv) == 1 {
		for _, env := range clusterEnv {
			return env
		}
	}
	return fluxDefaultEnvironment
}

// revisionSHA extracts the commit SHA from a Flux metadata.revision value.
// Recognized formats: "main@sha1:<sha>", "sha1:<sha>", and a bare "<sha>".
// An empty revision returns "".
func revisionSHA(revision string) string {
	if revision == "" {
		return ""
	}
	if i := strings.LastIndex(revision, "sha1:"); i >= 0 {
		return revision[i+len("sha1:"):]
	}
	if i := strings.LastIndex(revision, "@"); i >= 0 {
		return revision[i+1:]
	}
	return revision
}

// confirmFluxDelivery advances the Flux side of the deploy frontier for
// environment when the reconciled revision maps to a repo we track, then
// resolves the tasks the new frontier covers.
func (h *fluxHandler) confirmFluxDelivery(tx *sql.Tx, now time.Time, environment, revision string, eventID int64) error {
	// clusterEnv is unvalidated operator config (LODE_CLUSTER_ENV_MAP), so
	// environment can be anything; env_deploys only accepts dev|prod and a
	// CHECK violation would abort the whole delivery.
	if environment != "dev" && environment != "prod" {
		return nil
	}
	sha := revisionSHA(revision)
	if sha == "" {
		return nil
	}
	repo, mainID, err := store.MainIDForSHAAnyRepo(tx, sha)
	if err != nil {
		return err
	}
	if mainID == nil {
		// A revision we cannot correlate must not latch flux_seen: that would
		// gate the repo/env permanently on a signal we can never confirm.
		return nil
	}
	latched, err := store.BumpEnvDeployFlux(tx, now, repo, environment, *mainID)
	if err != nil {
		return err
	}
	if latched {
		// Permanent switch to dual-signal gating; if the correlation is wrong
		// (shared history between tracked repos) this line is the only trace.
		h.log.Info("flux delivery gating latched", "repo", repo, "environment", environment, "revision", revision)
	}
	return resolveFrontier(tx, now, repo, environment, eventID)
}

// apply resolves the target deployment and records its effect: a status
// update, and for failures/recoveries a runtime event. It must run inside
// the same transaction as the event insert (via RecordEvent) so a delivery
// is all-or-nothing.
func (h *fluxHandler) apply(tx *sql.Tx, eventID int64, ev fluxEvent) error {
	cluster := ev.Metadata["cluster"]
	environment := resolveEnvironment(cluster, h.clusterEnv)
	targetName := ev.InvolvedObject.Namespace + "/" + ev.InvolvedObject.Name

	var artifactID *int64
	if sha := revisionSHA(ev.Metadata["revision"]); sha != "" {
		id, err := store.ArtifactIDBySourceSHA(tx, sha)
		if err != nil {
			return err
		}
		artifactID = id
	}

	now := h.st.Now()
	occurredAt := now
	if ev.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, ev.Timestamp); err == nil {
			occurredAt = t
		}
	}

	failed := ev.Severity == "error" || ev.Reason == "ReconciliationFailed" || ev.Reason == "HealthCheckFailed"
	switch {
	case failed:
		if err := store.UpsertDeployment(tx, now, store.Deployment{
			ArtifactID:  artifactID,
			Environment: environment,
			TargetKind:  fluxTargetKind,
			TargetName:  targetName,
			Status:      "failed",
		}); err != nil {
			return err
		}
		_, err := store.InsertRuntimeEvent(tx, store.RuntimeEvent{
			Cluster:    cluster,
			Kind:       "flux_failure",
			Workload:   targetName,
			ArtifactID: artifactID,
			Message:    ev.Message,
			OccurredAt: occurredAt,
		})
		return err

	case ev.Reason == "ReconciliationSucceeded":
		// Read the prior status before upserting: a success arriving after
		// a failed status is a recovery, worth its own runtime event.
		priorStatus, err := store.DeploymentStatus(tx, environment, fluxTargetKind, targetName)
		if err != nil {
			return err
		}
		if err := store.UpsertDeployment(tx, now, store.Deployment{
			ArtifactID:  artifactID,
			Environment: environment,
			TargetKind:  fluxTargetKind,
			TargetName:  targetName,
			Status:      "deployed",
		}); err != nil {
			return err
		}
		if err := h.confirmFluxDelivery(tx, now, environment, ev.Metadata["revision"], eventID); err != nil {
			return err
		}
		if priorStatus != "failed" {
			return nil
		}
		_, err = store.InsertRuntimeEvent(tx, store.RuntimeEvent{
			Cluster:    cluster,
			Kind:       "flux_recovery",
			Workload:   targetName,
			ArtifactID: artifactID,
			Message:    ev.Message,
			OccurredAt: occurredAt,
		})
		return err

	default:
		// Other info-severity reasons (Progressing, garbage collection,
		// ...): reconciliation is underway but not yet resolved either way.
		return store.UpsertDeployment(tx, now, store.Deployment{
			ArtifactID:  artifactID,
			Environment: environment,
			TargetKind:  fluxTargetKind,
			TargetName:  targetName,
			Status:      "reconciling",
		})
	}
}
