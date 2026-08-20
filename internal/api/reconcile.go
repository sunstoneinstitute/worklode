// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// whoami handles GET /api/v1/whoami: the calling actor's identity. Auth
// only, no admin gate — this is how the CLI (and lode doctor) asks whether a
// token is accepted and who it belongs to.
func (s *server) whoami(w http.ResponseWriter, r *http.Request) {
	sub := subjectFrom(r)
	writeJSON(w, http.StatusOK, model.WhoAmI{ID: sub.ActorID, Kind: sub.Kind, Admin: sub.HasRole(RoleAdmin)})
}
