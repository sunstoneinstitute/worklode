// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"context"
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

// reposDoctor handles GET /api/v1/repos/doctor[?repo=owner/name]: per-repo
// ingestion health. Admin-gated (permReconcile) — it reads across the whole
// org.
func (s *server) reposDoctor(w http.ResponseWriter, r *http.Request) {
	health, err := s.st.RepoIngestionHealth(r.Context(), r.URL.Query().Get("repo"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	senders, err := s.st.UnmappedSenders(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := model.ReposDoctorResponse{Repos: []model.RepoDoctor{}, UnmappedSenders: []model.UnmappedSender{}}
	for _, ri := range health {
		rj := model.RepoDoctor{
			Repo:            ri.Repo,
			Project:         ri.ProjectID,
			MappedAt:        ri.MappedAt,
			LastEventAt:     ri.LastEventAt,
			EventTypes:      ri.EventTypes,
			UnappliedEvents: ri.Unapplied,
			Stale:           ri.LastEventAt == nil || ri.LastEventAt.Before(ri.MappedAt),
		}
		if rj.EventTypes == nil {
			rj.EventTypes = []string{}
		}
		if s.appAuth != nil {
			// Confirmed by minting an installation token (the spec's check);
			// bounded per repo like addRepo's discovery.
			ctx, cancel := context.WithTimeout(r.Context(), discoveryTimeout)
			_, tokErr := s.appAuth.InstallationToken(ctx, ri.Repo)
			cancel()
			installed := tokErr == nil
			rj.AppInstalled = &installed
			if tokErr != nil {
				rj.AppError = tokErr.Error()
			}
		}
		resp.Repos = append(resp.Repos, rj)
	}
	for _, u := range senders {
		resp.UnmappedSenders = append(resp.UnmappedSenders,
			model.UnmappedSender{Repo: u.Repo, Events: u.Events, LastEventAt: u.LastEventAt})
	}
	writeJSON(w, http.StatusOK, resp)
}
