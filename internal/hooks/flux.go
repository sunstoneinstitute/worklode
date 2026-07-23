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
}

// NewFluxHandler returns the POST /hooks/flux handler. It verifies the
// X-Signature HMAC (Flux notification-controller's "generic-hmac" Provider:
// "sha256=<hex>" over the raw body) against secret, records each delivery
// exactly once, and applies the per-event effects. Flux sends no delivery
// id, so the idempotency key is the SHA-256 of the request body. An empty
// secret makes the handler refuse all requests with 503 — a misconfigured
// server must not accept unauthenticated webhooks.
func NewFluxHandler(st *store.Store, secret string, clusterEnv map[string]string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return &fluxHandler{st: st, secret: secret, clusterEnv: clusterEnv, log: log}
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
		apply = func(tx *sql.Tx, _ int64) error {
			return h.apply(tx, ev)
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
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
	case ignored:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	default:
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

// apply resolves the target deployment and records its effect: a status
// update, and for failures/recoveries a runtime event. It must run inside
// the same transaction as the event insert (via RecordEvent) so a delivery
// is all-or-nothing.
func (h *fluxHandler) apply(tx *sql.Tx, ev fluxEvent) error {
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
