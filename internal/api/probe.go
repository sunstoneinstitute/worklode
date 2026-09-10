package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// probeTargets handles GET /api/v1/probe-targets: every artifact address a
// still-open entity declared (029 §3.2), for the prober to poll.
func (s *server) probeTargets(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.st.ProbeTargets(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ProbeTargetsResponse{Artifacts: artifacts})
}

// createArtifactReport handles POST /api/v1/artifact-reports: one recorded
// event (source "prober", external id = the caller's dedupe_key) whose apply
// files the reported state as evidence against every open entity that
// declared the artifact address (029 §3.2). A report for an address nobody
// declares still lands in events with no evidence rows and an "unrouted"
// ack — the same shape the signed catalog/ci/pipeline ingests use — and
// stays a replay candidate since RecordEvent never marks it applied.
func (s *server) createArtifactReport(w http.ResponseWriter, r *http.Request) {
	var req model.ArtifactReportInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}

	// The label defaults to "invalid" and only becomes the reported state once
	// it is confirmed to be one of the bounded five — an unvalidated req.State
	// would otherwise punch an unbounded cardinality hole in the metric.
	state, result := "invalid", "invalid"
	defer func() { s.observeProbeReport(state, result) }()

	if !slices.Contains(hooks.CatalogStates, req.State) {
		writeErr(w, http.StatusUnprocessableEntity, "state must be one of "+strings.Join(hooks.CatalogStates, ", "))
		return
	}
	state = req.State
	if strings.TrimSpace(req.DedupeKey) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "dedupe_key is required")
		return
	}
	occurredAt := s.st.Now()
	if req.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "invalid occurred_at: must be RFC3339")
			return
		}
		occurredAt = t
	}

	payload, err := json.Marshal(req)
	if err != nil {
		result = "error"
		s.mapStoreErr(w, err)
		return
	}

	var targets []store.DeclaredEntity
	_, inserted, err := s.st.RecordEvent(r.Context(), "prober", req.DedupeKey,
		"prober."+req.State, payload,
		func(tx *sql.Tx, eventID int64) error {
			targets, err = store.OpenDeclarationsForArtifact(tx, "address", req.Artifact)
			if err != nil {
				return err
			}
			for _, target := range targets {
				if _, err := store.InsertArtifactEvidence(tx, eventID, model.ArtifactEvidence{
					EntityKind: target.Kind,
					EntityID:   target.ID,
					Artifact:   req.Artifact,
					Source:     "prober",
					State:      req.State,
					Provenance: "observed",
					Version:    req.Version,
					URL:        req.URL,
					OccurredAt: occurredAt,
				}); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		result = "error"
		s.mapStoreErr(w, err)
		return
	}

	status := "ok"
	result = "ok"
	switch {
	case !inserted:
		status = "duplicate"
		result = "duplicate"
	case len(targets) == 0:
		status = "unrouted"
		result = "unrouted"
	}
	writeJSON(w, http.StatusOK, model.WebhookAck{Status: status})
}
