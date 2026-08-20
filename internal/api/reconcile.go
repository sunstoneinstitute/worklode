// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"net/http"
)

// whoamiJSON is the wire form of GET /api/v1/whoami.
type whoamiJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}

// whoami handles GET /api/v1/whoami: the calling actor's identity. Auth
// only, no admin gate — this is how the CLI (and lode doctor) asks whether a
// token is accepted and who it belongs to.
func (s *server) whoami(w http.ResponseWriter, r *http.Request) {
	sub := subjectFrom(r)
	writeJSON(w, http.StatusOK, whoamiJSON{ID: sub.ActorID, Kind: sub.Kind, Admin: sub.HasRole(RoleAdmin)})
}
