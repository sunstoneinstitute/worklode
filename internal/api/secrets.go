package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// catalogEntryJSON is the wire form of one catalog entry.
type catalogEntryJSON struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Description string `json:"description"`
	Baseline    bool   `json:"baseline"`
}

// secretsCatalog handles GET /api/v1/secrets/catalog. Authenticated only —
// the name → op:// map must not leak vault/item structure (spec 017), which
// is why the route table guards it with permSecretRead rather than reusing
// task.read. The file is re-read per request so a ConfigMap update propagates
// without a restart; it is small and requests are rare (one per claim).
func (s *server) secretsCatalog(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SecretsCatalogPath == "" {
		writeErr(w, http.StatusNotFound, "secrets catalog not configured")
		return
	}
	data, err := os.ReadFile(s.cfg.SecretsCatalogPath)
	if err != nil {
		s.log.Error("read secrets catalog", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	cat, err := secrets.ParseCatalog(data)
	if err != nil {
		s.log.Error("parse secrets catalog", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := struct {
		Secrets []catalogEntryJSON `json:"secrets"`
	}{Secrets: make([]catalogEntryJSON, 0, len(cat.Entries))}
	for _, e := range cat.Entries {
		out.Secrets = append(out.Secrets, catalogEntryJSON{
			Name: e.Name, Ref: e.Ref, Description: e.Description, Baseline: e.Baseline,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// secretsMaterialized handles POST /api/v1/tasks/{id}/secrets-materialized:
// the claim-ceremony hook reporting which secret names it put in the local
// keystore. The strict name grammar is the redaction guarantee — an op://
// ref or a raw value cannot pass validation, so neither can ever enter the
// event log.
//
// routeGuards guards it with permTaskClaim, not permSecretRead: this is a
// step of the claim ceremony performed by the actor taking the lease, so it
// belongs with claim/renew/release/start/stop rather than with reading the
// catalog. permSecretRead is a read of org vault topology; nothing about
// being allowed to see the catalog should imply being allowed to write to a
// task's audit trail.
func (s *server) secretsMaterialized(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Names []string `json:"names"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if len(req.Names) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "names is required")
		return
	}
	if !validSecretNames(req.Names) {
		writeErr(w, http.StatusUnprocessableEntity, invalidSecretNameMsg)
		return
	}
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actor := actorFrom(r)
	var actorID string
	if actor != nil {
		actorID = actor.ID
	}
	payload, err := json.Marshal(map[string]any{
		"task": id, "actor": actorID, "names": req.Names,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "secrets_materialized", payload,
		func(tx *sql.Tx, eventID int64) error {
			return store.LogChange(tx, "task", id, eventID,
				map[string]any{"field": "secrets_materialized", "names": req.Names})
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
