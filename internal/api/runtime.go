package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// validRuntimeEventKinds are the kinds the watcher may post. The flux kinds
// (flux_failure, flux_recovery) are produced by the /hooks/flux handler, not
// this endpoint.
var validRuntimeEventKinds = map[string]bool{
	"crashloop": true, "oom": true,
}

type runtimeEventRequest struct {
	Cluster    string `json:"cluster"`
	Kind       string `json:"kind"`
	Workload   string `json:"workload"`
	Image      string `json:"image"`
	Message    string `json:"message"`
	OccurredAt string `json:"occurred_at"`
	DedupeKey  string `json:"dedupe_key"`
}

// createRuntimeEvent handles POST /api/v1/runtime-events: one recorded event
// (source "watcher", external id = the caller's dedupe_key) whose apply
// inserts the runtime event row. The store resolves the artifact from the
// image name. A redelivered dedupe_key is a no-op answered with
// {"status": "duplicate"}.
func (s *server) createRuntimeEvent(w http.ResponseWriter, r *http.Request) {
	var req runtimeEventRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if !validRuntimeEventKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid kind: must be crashloop or oom")
		return
	}
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
		s.mapStoreErr(w, err)
		return
	}

	var id int64
	_, inserted, err := s.st.RecordEvent(r.Context(), "watcher", req.DedupeKey,
		"runtime."+req.Kind, payload,
		func(tx *sql.Tx, _ int64) error {
			var err error
			id, err = store.InsertRuntimeEvent(tx, store.RuntimeEvent{
				Cluster:    req.Cluster,
				Kind:       req.Kind,
				Workload:   req.Workload,
				Image:      req.Image,
				Message:    req.Message,
				OccurredAt: occurredAt,
			})
			return err
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if !inserted {
		writeJSON(w, http.StatusOK, map[string]any{"status": "duplicate"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "ok"})
}
