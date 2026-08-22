// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/reconcile"
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
		resp.Repos = append(resp.Repos, rj)
	}
	s.checkAppInstalls(r.Context(), resp.Repos)
	for _, u := range senders {
		resp.UnmappedSenders = append(resp.UnmappedSenders,
			model.UnmappedSender{Repo: u.Repo, Events: u.Events, LastEventAt: u.LastEventAt})
	}
	writeJSON(w, http.StatusOK, resp)
}

// appCheckConcurrency caps the GitHub round trips reposDoctor has in flight
// at once, so a large org's doctor run is neither serialized nor a burst of
// hundreds of requests at GitHub's rate limiter.
const appCheckConcurrency = 8

// appCheckBudget bounds the whole App-install phase, not each call: with only
// a per-call timeout, repo count still multiplies the worst case (a hundred
// repos × discoveryTimeout held the handler open for minutes). It sits well
// inside the CLI's 30s request timeout, so an unreachable GitHub costs the
// operator a partial report rather than a client-side failure. A var only so
// the timeout test can shrink it instead of waiting it out.
var appCheckBudget = 15 * time.Second

// checkAppInstalls fills in each repo's AppInstalled/AppError from GitHub.
//
// One GET /repos/{repo}/installation per repo: that endpoint alone answers
// the only question the report asks, so the token mint the check used to do
// on top of it was a second round trip — and a credential nothing read.
//
// AppInstalled stays nil for a repo whose check could not run (budget spent,
// GitHub 5xx, transport failure); false is reserved for GitHub actually
// saying the App is not installed. "We could not tell" must not read as "not
// installed" in an operator's diagnosis.
func (s *server) checkAppInstalls(ctx context.Context, repos []model.RepoDoctor) {
	if s.appAuth == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, appCheckBudget)
	defer cancel()

	// No error path: every outcome is recorded on its own repo, so a failing
	// check never cancels its siblings or fails the report.
	var g errgroup.Group
	g.SetLimit(appCheckConcurrency)
	for i := range repos {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, discoveryTimeout)
			defer ccancel()
			_, err := s.appAuth.InstallationID(cctx, repos[i].Repo)
			switch {
			case err == nil:
				installed := true
				repos[i].AppInstalled = &installed
			case errors.Is(err, githubauth.ErrAppNotInstalled):
				installed := false
				repos[i].AppInstalled = &installed
				repos[i].AppError = err.Error()
			default:
				repos[i].AppError = err.Error()
			}
			return nil
		})
	}
	_ = g.Wait()

	// One line for the whole phase, not one per repo: with GitHub down, a
	// per-repo warning is a hundred lines saying the same thing. The report
	// already tells the operator which repos went unchecked; the log is what
	// tells the server's own watcher that the check degraded at all.
	unchecked := 0
	for i := range repos {
		if repos[i].AppInstalled == nil {
			unchecked++
		}
	}
	if unchecked > 0 {
		s.log.Warn("repos doctor could not check github app installation",
			"unchecked", unchecked, "repos", len(repos))
	}
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
// then engine 2 (poll GitHub — skipped when no App is configured).
// Synchronous by design: a scoped run is fast and the unscoped run is the
// scheduled case where waiting is acceptable (spec 013 §API).
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
			Log: s.log, ResolveBranch: resolveBranch, Metrics: s.hookMetrics,
		})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Replay = replay
	}

	// Engine 2. Polling asks GitHub directly, so it needs the App; without
	// it engine 1 still ran, and the response says which config gap kept
	// polling from happening rather than leaving poll silently null.
	if s.appAuth == nil {
		resp.PollSkipped = "github app auth not configured (LODE_GITHUB_APP_ID / LODE_GITHUB_APP_PRIVATE_KEY)"
	} else {
		poll, err := reconcile.Poll(r.Context(), s.st, s.appAuth, reconcile.Options{
			Repo: req.Repo, Task: req.Task, Since: since, DryRun: req.DryRun, RunID: runID,
			Log: s.log, Metrics: s.pollMetrics,
		})
		if err != nil {
			// Not a 500: engine 1 has already run, and outside a dry run it
			// has already written and marked events applied. Discarding the
			// response would leave the operator unable to tell what was
			// repaired or whether re-running is safe — so the poll failure
			// gets its own section, the way per-repo failures already get
			// PollResult.Errors.
			s.log.Error("reconcile poll failed", "run_id", runID, "repo", req.Repo, "task", req.Task, "err", err)
			resp.PollError = err.Error()
		} else {
			resp.Poll = poll
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
