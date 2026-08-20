// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
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

// parseSince resolves a --since value against now: an RFC 3339 timestamp is
// taken as-is; a Go duration ("720h") means now minus that duration.
func parseSince(s string, now time.Time) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("since %q is neither RFC 3339 nor a Go duration", s)
	}
	u := now.Add(-d).UTC()
	return &u, nil
}

// reconcile handles POST /api/v1/reconcile: engine 1 (replay stored events)
// then engine 2 (poll GitHub — a later plan in this series; skipped until
// the App is configured AND that plan lands). Synchronous by design: a
// scoped run is fast and the unscoped run is the scheduled case where
// waiting is acceptable (spec 013 §API).
func (s *server) reconcile(w http.ResponseWriter, r *http.Request) {
	var req model.ReconcileInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Repo != "" && req.Task != "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo and task are mutually exclusive")
		return
	}
	since, err := parseSince(req.Since, s.st.Now())
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	runID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := model.ReconcileResponse{RunID: runID, DryRun: req.DryRun}

	// Engine 1. --task cannot bound replay (an ignored event's task binding
	// is unknown before its apply runs), so a task-scoped run goes straight
	// to polling.
	if req.Task == "" {
		// resolveBranch is nil unless a GitHub App is configured — same
		// nil-guard the webhook handler uses (internal/hooks/github.go's
		// NewGitHubHandler), so a replayed release.published without an App
		// falls back to applyRelease's existing default instead of panicking
		// on a nil s.appAuth.
		var resolveBranch func(ctx context.Context, repo, branch string) (string, error)
		if s.appAuth != nil {
			resolveBranch = s.appAuth.BranchSHA
		}
		replay, err := hooks.Replay(r.Context(), s.st, hooks.ReplayOptions{
			Repo: req.Repo, Since: since, DryRun: req.DryRun,
			ResolveBranch: resolveBranch, Metrics: s.hookMetrics,
		})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Replay = replay
	}

	// Engine 2 lands in a later plan in this series (not this one), which
	// replaces this line with the poll call.
	resp.PollSkipped = "github app auth not configured"

	writeJSON(w, http.StatusOK, resp)
}
