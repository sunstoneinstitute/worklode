package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// secretsCatalog handles GET /api/v1/secrets/catalog. Authenticated only —
// the name → op:// map must not leak vault/item structure (spec 017), which
// is why the route table guards it with permSecretRead rather than reusing
// task.read. The file is re-read per request so a Secret update propagates
// without a restart; it is small and requests are rare (one per claim).
// The catalog Secret is projected per environment and mounted optional, so an
// absent file means "no catalog here", not a server fault: same 404 as an
// unconfigured path.
func (s *server) secretsCatalog(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SecretsCatalogPath == "" {
		writeErr(w, http.StatusNotFound, "secrets catalog not configured")
		return
	}
	data, err := os.ReadFile(s.cfg.SecretsCatalogPath)
	if errors.Is(err, fs.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "secrets catalog not configured")
		return
	}
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
	dir := filepath.Dir(s.cfg.SecretsCatalogPath)
	out := model.SecretCatalogResponse{Secrets: make([]model.SecretCatalogEntry, 0, len(cat.Entries))}
	for _, e := range cat.Entries {
		wire := model.SecretCatalogEntry{
			Name: e.Name, Ref: e.Ref, Env: e.Env,
			Description: e.Description, Baseline: e.Baseline,
		}
		if e.Templated() {
			text, err := readTemplate(dir, e)
			if err != nil {
				// The catalog is admin-controlled: a broken entry fails
				// loudly at the source rather than degrading per claim
				// (spec 042 §5). The fix is a deployment-repo PR.
				s.log.Error("secrets catalog template", "entry", e.Name, "err", err)
				writeErr(w, http.StatusInternalServerError, "internal error")
				return
			}
			wire.Template = text
			wire.Creds = make(map[string]string, len(e.Creds))
			for _, c := range e.Creds {
				wire.Creds[c.Placeholder] = c.Ref
			}
		}
		out.Secrets = append(out.Secrets, wire)
	}
	writeJSON(w, http.StatusOK, out)
}

// readTemplate loads and validates a templated entry's template. The template
// key names a sibling key of the projected catalog Secret, which surfaces as a
// file in the same mount directory — never a path, so a separator or ".." in
// it is a rejection rather than a traversal.
func readTemplate(dir string, e secrets.Entry) (string, error) {
	if strings.ContainsAny(e.Template, `/\`) || e.Template == ".." || e.Template == "." {
		return "", fmt.Errorf("template %q must name a sibling catalog key, not a path", e.Template)
	}
	data, err := os.ReadFile(filepath.Join(dir, e.Template))
	if err != nil {
		return "", err
	}
	text := string(data)
	if err := secrets.ValidateTemplate(e, text); err != nil {
		return "", err
	}
	return text, nil
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
	var req model.SecretsMaterializedInput
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
	actorID := actorIDFrom(r)
	err := s.recordEvent(r.Context(), "cli", "secrets_materialized",
		map[string]any{"task": id, "actor": actorID, "names": req.Names},
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
