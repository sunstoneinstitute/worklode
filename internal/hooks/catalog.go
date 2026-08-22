package hooks

// Data-catalog ingest — spec 029 §3.1, §3.2. A deliverable declares how it is
// verified ("by address"); the catalog reports facts about that address, and
// worklode files each fact as evidence against every open entity that
// declared it. Routing is a declaration lookup, not a static map: unlike the
// GitHub hook's repo → project mapping, the artifact address itself is the
// key.
//
// The contract below is PROVISIONAL: no data-platform emitter exists yet, so
// it deliberately mirrors the Flux generic-hmac shape and will be settled
// against the first real emitter.
//
//	POST /hooks/catalog
//
//	Auth:  X-Signature: sha256=<hex> — HMAC-SHA256 over the exact request
//	       bytes, key = LODE_CATALOG_WEBHOOK_SECRET. Identical scheme to
//	       /hooks/flux. An unset secret answers 503: a misconfigured server
//	       must not accept unauthenticated webhooks.
//	Idem:  X-Catalog-Delivery: <opaque id>, when the emitter has one. Absent,
//	       the idempotency key is the SHA-256 of the body, as Flux does it.
//	Body (application/json):
//	  {
//	    "event":       "dataset.published",    // optional emitter event name
//	    "artifact":    "bigquery://sunstone-prod/cow/casualties",  // REQUIRED
//	    "state":       "published",            // REQUIRED, see the set below
//	    "catalog":     "prod",                 // optional catalog instance
//	    "version":     "2026-08-19T09:12:00Z", // optional emitter snapshot id
//	    "url":         "https://catalog.../datasets/cow.casualties", // optional
//	    "occurred_at": "2026-08-19T09:12:03Z", // optional RFC3339, default now
//	    "detail":      { }                     // optional free-form, jsonb
//	  }
//	  state is one of: published | updated | deprecated | removed | failed.
//	Ack:   200 {"status":"ok"|"duplicate"|"unrouted"}
//
// artifact is compared after trimming surrounding whitespace and nothing
// else — no scheme or case normalisation, because dataset identifiers are
// case-sensitive in the catalogs we care about.
//
// A delivery no declaration matches still lands in events, with no evidence
// rows and an "unrouted" ack, the way the GitHub hook records an unmapped
// repo's delivery. Its applied_at stays NULL, which is what puts it in
// reconcile engine 1's candidate set (store.UnappliedEvents): once the
// declaration exists, the next replay files the stored payload as evidence
// and marks the delivery applied. A routed delivery is marked applied at
// record time, so only genuinely unfiled deliveries stay candidates (WL-256).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxCatalogBody caps webhook request bodies at 5 MiB, the same limit as the
// GitHub and Flux handlers.
const maxCatalogBody = 5 << 20

// catalogStates is the reportable set, mirroring the artifact_evidence state
// CHECK. Anything else is a 400: an unbounded state would reach the database
// as a constraint violation and the metric label as a cardinality leak.
var catalogStates = []string{"published", "updated", "deprecated", "removed", "failed"}

type catalogHandler struct {
	ap      *catalogApplier
	secret  string
	log     *slog.Logger
	metrics *Metrics
}

// NewCatalogHandler returns the POST /hooks/catalog handler. See the contract
// at the top of this file.
func NewCatalogHandler(st *store.Store, secret string, log *slog.Logger, m *Metrics) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return &catalogHandler{ap: &catalogApplier{st: st, log: log}, secret: secret, log: log, metrics: m}
}

// catalogEvent is the payload a catalog emitter posts. Catalog is parsed but
// not projected onto an evidence column: which instance reported is a
// property of the delivery, and the stored event payload keeps it.
type catalogEvent struct {
	Event      string          `json:"event"`
	Artifact   string          `json:"artifact"`
	State      string          `json:"state"`
	Catalog    string          `json:"catalog"`
	Version    string          `json:"version"`
	URL        string          `json:"url"`
	OccurredAt string          `json:"occurred_at"`
	Detail     json.RawMessage `json:"detail"`
}

func (h *catalogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Every exit records exactly one delivery; result stays "error" unless a
	// branch below sets it, so new early returns default to error, not
	// silence. The event label is the validated state — bounded by
	// catalogStates — or "invalid" for a payload that never got that far.
	result, eventLabel := "error", "invalid"
	defer func() { h.metrics.event("catalog", eventLabel, result) }()

	if h.secret == "" {
		writeErr(w, http.StatusServiceUnavailable, "catalog webhook secret not configured")
		return
	}

	// The signature covers the exact request bytes: read the raw body first
	// (capped at maxCatalogBody), verify, and only then parse.
	body, ok := readSignedBody(w, r, maxCatalogBody)
	if !ok {
		return
	}
	if !validSignature(h.secret, body, r.Header.Get("X-Signature")) {
		result = "rejected"
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var ev catalogEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	ev.Artifact = strings.TrimSpace(ev.Artifact)
	if ev.Artifact == "" {
		writeErr(w, http.StatusBadRequest, "artifact is required")
		return
	}
	if !slices.Contains(catalogStates, ev.State) {
		writeErr(w, http.StatusBadRequest, "state must be one of "+strings.Join(catalogStates, ", "))
		return
	}
	eventLabel = ev.State

	// An emitter with a delivery id dedupes on it; without one the key is the
	// SHA-256 of the exact bytes, so a redelivered event dedupes and any
	// change to the body counts as a new delivery.
	externalID := strings.TrimSpace(r.Header.Get("X-Catalog-Delivery"))
	if externalID == "" {
		sum := sha256.Sum256(body)
		externalID = hex.EncodeToString(sum[:])
	}
	typ := "catalog." + ev.Event
	if ev.Event == "" {
		typ = "catalog." + ev.State
	}

	var applied catalogResult
	_, inserted, err := h.ap.st.RecordEvent(r.Context(), "catalog", externalID, typ, body,
		func(tx *sql.Tx, eventID int64) error {
			return h.ap.applyThenMark(tx, eventID, ev, &applied)
		})
	if err != nil {
		h.log.Error("catalog webhook: apply", "artifact", ev.Artifact, "state", ev.State, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Counted after the transaction committed, so a rolled-back delivery
	// leaves no evidence and no count of it.
	h.metrics.catalogEvidenceFiled(applied)

	// A redelivery acks "duplicate" whatever it would otherwise have been.
	status := "ok"
	result = "ok"
	switch {
	case !inserted:
		status = "duplicate"
	case !applied.Routed():
		result, status = "unrouted", "unrouted"
	}
	writeJSON(w, http.StatusOK, model.WebhookAck{Status: status})
}

// catalogApplier files a reported catalog fact against the entities that
// declared the artifact, independent of how the delivery arrived — the
// catalog counterpart of applier (apply.go). The webhook handler owns one;
// Replay builds one for stored deliveries.
type catalogApplier struct {
	st  *store.Store
	log *slog.Logger
}

// catalogResult is what one catalog apply did. Targets is every open
// declaration of the artifact; Written is the subset this event filed new
// evidence for (a redelivery of an already-filed event writes none).
type catalogResult struct {
	State   string
	Targets []store.DeclaredEntity
	Written []store.DeclaredEntity
}

// Routed reports whether any open declaration claimed the artifact. Routing
// is the completion test, not the write: a delivery whose evidence rows all
// already exist is filed, while an unrouted one has an apply still to run and
// stays a replay candidate.
func (r catalogResult) Routed() bool { return len(r.Targets) > 0 }

// applyThenMark runs the apply and, when it routed, sets applied_at in the
// same transaction, so a filed delivery leaves the replay candidate set. An
// unrouted delivery stays in it: the declaration it needs may arrive later.
// out receives the result whether or not it routed.
func (a *catalogApplier) applyThenMark(tx *sql.Tx, eventID int64, ev catalogEvent, out *catalogResult) error {
	res, err := a.apply(tx, eventID, ev)
	if err != nil {
		return err
	}
	*out = res
	if !res.Routed() {
		return nil
	}
	return store.MarkEventApplied(tx, eventID, a.st.Now())
}

// applyStored routes a *stored* catalog delivery the way applier.applyForType
// routes a stored GitHub one: it parses the recorded payload back into the
// event the live handler validated and returns the same apply the webhook
// path runs. A payload that does not parse, or that predates a validation
// rule, is an error the replayer reports and skips rather than a candidate it
// retries forever.
func (a *catalogApplier) applyStored(payload []byte, out *catalogResult) (func(tx *sql.Tx, eventID int64) error, error) {
	var ev catalogEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("parse catalog payload: %w", err)
	}
	ev.Artifact = strings.TrimSpace(ev.Artifact)
	if ev.Artifact == "" {
		return nil, errors.New("catalog payload has no artifact")
	}
	if !slices.Contains(catalogStates, ev.State) {
		return nil, fmt.Errorf("catalog payload has unknown state %q", ev.State)
	}
	return func(tx *sql.Tx, eventID int64) error {
		return a.applyThenMark(tx, eventID, ev, out)
	}, nil
}

// apply files the reported fact against every open entity that declared the
// artifact, reporting both the entities it matched and the ones it wrote. It
// must run inside the same transaction as the event insert (via RecordEvent)
// so a delivery is all-or-nothing.
//
// Provenance is always "observed": the catalog is an emitter, not a person
// (029 §3.2). An entity that already has evidence from this event is skipped
// by the insert's conflict clause, so a replay writes nothing twice.
func (a *catalogApplier) apply(tx *sql.Tx, eventID int64, ev catalogEvent) (catalogResult, error) {
	res := catalogResult{State: ev.State}
	targets, err := store.OpenDeclarationsForArtifact(tx, ev.Artifact)
	if err != nil {
		return catalogResult{}, err
	}
	res.Targets = targets

	// An unparseable timestamp falls back to the store clock, but it is worth
	// a warning: occurred_at is what the deliverable projection orders on, so
	// a substituted clock can let a stale report win over a newer one.
	occurredAt := a.st.Now()
	if ev.OccurredAt != "" {
		t, parseErr := time.Parse(time.RFC3339, ev.OccurredAt)
		if parseErr != nil {
			a.log.Warn("catalog webhook: unparseable occurred_at, using the store clock",
				"artifact", ev.Artifact, "occurred_at", ev.OccurredAt, "err", parseErr)
		} else {
			occurredAt = t
		}
	}

	for _, target := range targets {
		inserted, err := store.InsertArtifactEvidence(tx, eventID, model.ArtifactEvidence{
			EntityKind: target.Kind,
			EntityID:   target.ID,
			Artifact:   ev.Artifact,
			Source:     "catalog",
			State:      ev.State,
			Provenance: "observed",
			Version:    ev.Version,
			URL:        ev.URL,
			Detail:     ev.Detail,
			OccurredAt: occurredAt,
		})
		if err != nil {
			return catalogResult{}, err
		}
		if inserted {
			res.Written = append(res.Written, target)
		}
	}
	return res, nil
}
