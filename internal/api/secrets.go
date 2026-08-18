package api

import (
	"net/http"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
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
