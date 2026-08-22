package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// validActorKinds is the actors.kind CHECK constraint, mirrored in Go so
// callers get a clean 422 instead of a raw constraint violation.
var validActorKinds = map[string]bool{
	"human": true, "agent": true, "service": true,
}

// projectKeyRe mirrors the projects_key_format CHECK constraint, so callers
// get a clean 422 instead of a raw constraint violation.
var projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// reservedProjectKeys are the <TYPE> tokens of the <PROJECTKEY>-<TYPE>-<n>
// document shorthand (025 §14.3). The same CHECK constraint rejects them; this
// mirrors it for a clean 422.
var reservedProjectKeys = map[string]bool{"SPEC": true, "ADR": true}

// --- projects ---------------------------------------------------------

// toProjectJSON builds the wire form of a project, normalizing nil repo and
// focus slices to empty arrays so they serialize as [] rather than null.
func toProjectJSON(p *store.Project, repos []model.RepoMapping) model.Project {
	rs := make([]model.RepoMapping, 0, len(repos))
	rs = append(rs, repos...)
	focus := p.Focus
	if focus == nil {
		focus = []string{}
	}
	return model.Project{
		ID: p.ID, Name: p.Name, Key: p.Key, Repos: rs, Focus: focus,
	}
}

// createProject handles POST /api/v1/projects.
func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var req model.CreateProjectInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "id is required")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if !projectKeyRe.MatchString(req.Key) {
		writeErr(w, http.StatusUnprocessableEntity,
			"key must be an uppercase code matching ^[A-Z][A-Z0-9]{1,9}$")
		return
	}
	if reservedProjectKeys[req.Key] {
		writeErr(w, http.StatusUnprocessableEntity,
			"key SPEC and ADR are reserved by the document shorthand (025 §14.3)")
		return
	}
	if err := s.st.CreateProject(r.Context(), req.ID, req.Name, req.Key); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProjectJSON(
		&store.Project{ID: req.ID, Name: req.Name, Key: req.Key}, nil))
}

// listProjects handles GET /api/v1/projects: every project with its mapped repos.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.st.ListProjects(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	// One query for every project's repos: ListRepos per row would be an N+1
	// on a request that already listed the projects.
	reposByProject, err := s.st.ListReposForProjects(r.Context(), ids)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := model.ProjectListResponse{Projects: make([]model.Project, 0, len(ps))}
	for _, p := range ps {
		resp.Projects = append(resp.Projects, toProjectJSON(&p, reposByProject[p.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// toTokenCountsJSON builds the wire form of a token breakdown, shared by the
// daily rows and the window totals.
func toTokenCountsJSON(t store.TokenCounts) model.TokenCounts {
	return model.TokenCounts{
		InputTokens:        t.Input,
		CacheWrite5mTokens: t.CacheWrite5m,
		CacheWrite1hTokens: t.CacheWrite1h,
		CacheReadTokens:    t.CacheRead,
		OutputTokens:       t.Output,
	}
}

// toCostReportJSON builds the wire form of a cost window, normalizing nil
// slices to empty arrays so days and totals never serialize as null. Shared
// by a project's cost and a task's.
func toCostReportJSON(pc *store.CostReport) model.CostReport {
	out := model.CostReport{
		Days:   make([]model.CostDay, 0, len(pc.Days)),
		Totals: make([]model.CostTotals, 0, len(pc.Totals)),
	}
	for _, d := range pc.Days {
		out.Days = append(out.Days, model.CostDay{
			Day:            d.Day.Format(time.DateOnly),
			Currency:       d.Currency,
			TokenCounts:    toTokenCountsJSON(d.Tokens),
			CostAmount:     d.Cost,
			UnpricedTokens: d.UnpricedTokens,
		})
	}
	for _, t := range pc.Totals {
		out.Totals = append(out.Totals, model.CostTotals{
			Currency:       t.Currency,
			TokenCounts:    toTokenCountsJSON(t.Tokens),
			CostAmount:     t.Cost,
			UnpricedTokens: t.UnpricedTokens,
		})
	}
	return out
}

// dayParam reads an optional YYYY-MM-DD query parameter. An absent one yields
// the zero time, which ProjectCost reads as unbounded on that side.
func dayParam(r *http.Request, name string) (time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return time.Time{}, nil
	}
	day, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s %q: want YYYY-MM-DD", name, v)
	}
	return day, nil
}

// getProject handles GET /api/v1/projects/{id}: one project with its repos
// and its accounted cost. Read-only, so unlike the project mutations it is
// not admin-gated. Optional from and to (YYYY-MM-DD) bound the cost window,
// inclusive on both ends; either may be omitted for unbounded.
func (s *server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	from, err := dayParam(r, "from")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := dayParam(r, "to")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	p, err := s.st.GetProject(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	cost, err := s.st.ProjectCost(r.Context(), id, from, to)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ProjectDetail{
		Project: toProjectJSON(p, repos),
		Cost:    toCostReportJSON(cost),
	})
}

// resolveProjectByRemote handles GET /api/v1/projects/resolve?remote=<url>:
// the repo → project mapping the CLI needs to scope commands to the repo it
// is run from. The URL is normalized here rather than in the CLI so a
// normalization fix ships without a client upgrade.
func (s *server) resolveProjectByRemote(w http.ResponseWriter, r *http.Request) {
	repo, err := repourl.Normalize(r.URL.Query().Get("remote"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	p, err := s.st.ProjectForRepo(r.Context(), repo)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(r.Context(), p.ID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(p, repos))
}

// derefString returns *p, or "" when p is nil — for optional string body
// fields that default to empty when the caller omits them.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// patchProject handles PATCH /api/v1/projects/{id}: updates any subset of the
// ranking focus (store.SetProjectFocus), the curated pinned-focus card
// (store.PinProjectFocus), and the curated next-decision card
// (store.SetProjectNextDecision) in one call. Admin-gated like the other
// project mutations, since focus affects claim-next ordering for everyone.
func (s *server) patchProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.PatchProjectInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	// FocusNote and DecisionTitle are the triggers for their cards; the guard
	// counts them (and focus) so a body with no trigger field is a clean 422.
	if req.Focus == nil && req.FocusNote == nil && req.DecisionTitle == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no fields to update")
		return
	}
	if req.Focus != nil {
		if err := s.st.SetProjectFocus(r.Context(), id, *req.Focus); err != nil {
			s.mapStoreErr(w, err)
			return
		}
	}
	if req.FocusNote != nil {
		if err := s.st.PinProjectFocus(r.Context(), id, *req.FocusNote,
			derefString(req.FocusPinnedBy), s.st.Now()); err != nil {
			s.mapStoreErr(w, err)
			return
		}
	}
	if req.DecisionTitle != nil {
		if err := s.st.SetProjectNextDecision(r.Context(), id, *req.DecisionTitle,
			derefString(req.DecisionAccountable), derefString(req.DecisionReadiness)); err != nil {
			s.mapStoreErr(w, err)
			return
		}
	}

	p, err := s.st.GetProject(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(p, repos))
}

// addRepo handles POST /api/v1/projects/{id}/repos.
func (s *server) addRepo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.AddRepoInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	// Validated before the insert so a bad done_state does not leave the repo
	// mapped with the wrong terminal state.
	if req.DoneState != "" && !store.ValidDoneState(req.DoneState) {
		writeErr(w, http.StatusUnprocessableEntity, doneStateErrMsg)
		return
	}
	if _, err := s.st.GetProject(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if err := s.st.AddRepo(r.Context(), id, req.Repo); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	doneState := store.DefaultDoneState
	var warnings []string
	switch {
	case req.DoneState != "":
		if err := s.st.SetRepoDoneState(r.Context(), req.Repo, req.DoneState); err != nil {
			s.mapStoreErr(w, err)
			return
		}
		doneState = req.DoneState
		warnings = s.subscriptionWarnings(r.Context())
	case s.appAuth != nil:
		// Done-state discovery and the subscription check are independent
		// GitHub round trips, each bounded by discoveryTimeout; run them
		// concurrently so a slow GitHub costs one timeout, not two.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			doneState = s.discoverDoneState(r.Context(), req.Repo)
		}()
		go func() {
			defer wg.Done()
			warnings = s.subscriptionWarnings(r.Context())
		}()
		wg.Wait()
	}
	writeJSON(w, http.StatusCreated, model.AddRepoResult{
		ProjectID: id, Repo: req.Repo, DoneState: doneState, Warnings: warnings,
	})
}

// subscriptionWarnings names the events the webhook handler routes that this
// installation is not subscribed to. Like done-state discovery it never gates
// the mapping: the repo is already mapped, and a GitHub failure must not fail
// the request — so any error yields no warnings.
func (s *server) subscriptionWarnings(ctx context.Context) []string {
	if s.appAuth == nil {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	subscribed, err := s.appAuth.SubscribedEvents(sctx)
	if err != nil {
		s.log.Warn("check app event subscriptions", "err", err)
		return nil
	}
	have := make(map[string]bool, len(subscribed))
	for _, e := range subscribed {
		have[e] = true
	}
	var missing []string
	for _, e := range hooks.HandledEvents() {
		if !have[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"github app is not subscribed to: " + strings.Join(missing, ", ") +
		" — those webhooks will never arrive"}
}

// discoveryTimeout bounds the GitHub round trips addRepo makes; the mapping is
// already committed, so a slow GitHub must not hold the response.
const discoveryTimeout = 5 * time.Second

// discoverDoneState seeds a freshly mapped repo's terminal delivery state from
// its GitHub environments and releases, and returns what is now stored.
// Discovery never gates the mapping (delivery-lifecycle design spec): any
// failure is logged and leaves the repo at the schema default.
func (s *server) discoverDoneState(ctx context.Context, repo string) string {
	dctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	state, err := s.appAuth.DiscoverDoneState(dctx, repo)
	if err != nil {
		s.log.Warn("discover repo done_state", "repo", repo, "err", err)
		return store.DefaultDoneState
	}
	if err := s.st.SetRepoDoneState(ctx, repo, state); err != nil {
		s.log.Warn("store discovered done_state", "repo", repo, "state", state, "err", err)
		return store.DefaultDoneState
	}
	return state
}

// doneStateErrMsg is the 422 message for an unusable done_state value.
const doneStateErrMsg = "invalid done_state: must be merged, deployed_prod, or released"

// patchRepo handles PATCH /api/v1/repos/{owner}/{name}: currently only
// done_state is settable.
func (s *server) patchRepo(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("owner") + "/" + r.PathValue("name")
	var req model.SetRepoDoneStateInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.DoneState == "" {
		writeErr(w, http.StatusUnprocessableEntity, "done_state is required")
		return
	}
	if err := s.st.SetRepoDoneState(r.Context(), repo, req.DoneState); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- actors and tokens --------------------------------------------------

// createActor handles POST /api/v1/actors.
func (s *server) createActor(w http.ResponseWriter, r *http.Request) {
	var req model.CreateActorInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "id is required")
		return
	}
	if !validActorKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid kind: must be human, agent, or service")
		return
	}
	if err := s.st.CreateActor(r.Context(), req.ID, req.Kind, req.DisplayName, req.Admin); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, model.Actor{
		ID: req.ID, Kind: req.Kind, DisplayName: req.DisplayName, Admin: req.Admin,
	})
}

// createToken handles POST /api/v1/actors/{id}/tokens. The plaintext token
// is returned exactly once; only its hash is stored (see Store.CreateToken).
func (s *server) createToken(w http.ResponseWriter, r *http.Request) {
	actorID := r.PathValue("id")
	var req model.CreateTokenInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if _, err := s.st.GetActor(r.Context(), actorID); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "invalid expires_at: must be RFC3339")
			return
		}
		expiresAt = &t
	}

	plaintext, err := s.st.CreateToken(r.Context(), actorID, req.Description, expiresAt)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, model.TokenResponse{Token: plaintext})
}

// revokeToken handles DELETE /api/v1/tokens: revoke by plaintext or hash.
func (s *server) revokeToken(w http.ResponseWriter, r *http.Request) {
	var req model.RevokeTokenInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "token is required")
		return
	}
	if err := s.st.RevokeToken(r.Context(), req.Token); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- inbox ---------------------------------------------------------------

// listInbox handles GET /api/v1/inbox?state=new&project=worklode.
func (s *server) listInbox(w http.ResponseWriter, r *http.Request) {
	issues, err := s.st.ListIssues(r.Context(),
		r.URL.Query().Get("state"), r.URL.Query().Get("project"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := model.IssueListResponse{Issues: issues}
	if resp.Issues == nil {
		resp.Issues = []model.Issue{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// promoteInbox handles POST /api/v1/inbox/promote: turn an inbox issue into a
// task. The repo contains a slash, so repo and number travel as body fields
// rather than path segments. When title is empty it defaults to the issue's
// own title, read inside the same transaction as the promotion.
func (s *server) promoteInbox(w http.ResponseWriter, r *http.Request) {
	var req model.PromoteInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	if !validPriorities[req.Priority] {
		writeErr(w, http.StatusUnprocessableEntity, invalidPriorityMsg)
		return
	}
	req.Kind = s.normalizeTaskKind(req.Kind, "promote")
	if !validKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, invalidKindMsg)
		return
	}
	req.Parent = strings.TrimSpace(req.Parent)
	if req.Parent != "" {
		// Named 404 ahead of the transaction: AddEdge's own lookup stays the
		// authority for the rest of the spec-004 invariants, but its
		// ErrNotFound would otherwise be reported anonymously.
		if _, err := s.st.GetTask(r.Context(), req.Parent); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "parent not found: "+req.Parent)
			return
		}
	}
	project, err := s.st.ProjectForRepo(r.Context(), req.Repo)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	actorID := actorIDFrom(r)

	// Mirror remote images here rather than at import (spec 021 §12): a
	// promote is where an issue-derived body first becomes a task body, and
	// `lode inbox import` carries no body at all — neither githubauth.Issue
	// nor model.Issue has one, so there would be nothing there to rewrite.
	// req.Body is mutated before recordEvent serialises req, so the event
	// payload is the body that was stored, not the off-site URLs it was
	// derived from; a payload still naming the remote URLs would make the
	// provenance record disagree with tasks.body, and any replay of it would
	// undo the mirroring. Deliberately outside recordEvent: this fetches over
	// the network, and a slow origin must never hold a database lock.
	req.Body = s.mirrorRemoteImages(r.Context(), req.Repo, req.Body)

	var created *model.Task
	err = s.recordEvent(r.Context(), "cli", "issue.promoted", req,
		func(tx *sql.Tx, eventID int64) error {
			title := req.Title
			if strings.TrimSpace(title) == "" {
				t, err := store.IssueTitle(tx, req.Repo, req.Number)
				if err != nil {
					return err
				}
				title = t
			}
			t, err := store.PromoteIssue(tx, s.st.Now(), req.Repo, req.Number, store.TaskInput{
				ProjectID: project.ID,
				Title:     title,
				Body:      req.Body,
				Priority:  req.Priority,
				Kind:      req.Kind,
				CreatedBy: actorID,
				Draft:     req.Draft,
			}, req.AppliesToVersions, eventID)
			if err != nil {
				return err
			}
			created = t
			// The promoted task's id is minted here, after the payload was
			// marshalled, so the event names it from inside the same
			// transaction (025 §15.2).
			if err := store.AttributeEventToTask(tx, eventID, t.ID); err != nil {
				return err
			}
			if req.Parent != "" {
				// Same transaction as the promotion: there is no window
				// where the child exists unparented.
				if err := store.AddEdge(tx, s.st.Now(), t.ID, req.Parent, "child_of", eventID); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// dismissInbox handles POST /api/v1/inbox/dismiss.
func (s *server) dismissInbox(w http.ResponseWriter, r *http.Request) {
	var req model.DismissInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}

	err := s.recordEvent(r.Context(), "cli", "issue.dismissed", req,
		func(tx *sql.Tx, _ int64) error {
			return store.DismissIssue(tx, req.Repo, req.Number)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// linkInbox handles POST /api/v1/inbox/link: mark an inbox issue as covered
// by a task that already exists, instead of creating a new one.
func (s *server) linkInbox(w http.ResponseWriter, r *http.Request) {
	var req model.LinkInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	if strings.TrimSpace(req.TaskID) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "task_id is required")
		return
	}
	// Named 404 ahead of the transaction: store.LinkIssue's own check stays
	// the authority, but its ErrNotFound would otherwise be reported
	// anonymously.
	if _, err := s.st.GetTask(r.Context(), req.TaskID); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "task not found: "+req.TaskID)
		return
	}

	err := s.recordTaskEvent(r.Context(), "cli", "issue.linked", req.TaskID, req,
		func(tx *sql.Tx, _ int64) error {
			return store.LinkIssue(tx, req.Repo, req.Number, req.TaskID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- board ---------------------------------------------------------------

func toRuntimeEventJSON(re *store.RuntimeEvent) model.RuntimeEvent {
	return model.RuntimeEvent{
		ID: re.ID, Cluster: re.Cluster, Kind: re.Kind, Workload: re.Workload,
		Image: re.Image, Message: re.Message, OccurredAt: re.OccurredAt,
	}
}

// board handles GET /api/v1/board?project=: a read-only summary of each
// project's tasks bucketed by state, for the CLI's `lode board` command.
func (s *server) board(w http.ResponseWriter, r *http.Request) {
	resp, err := s.assembleBoard(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// workBuckets is the per-project bucketing of work facts shared by the
// board (assembleBoard) and Home's card counts. Blocked means a ready task
// with at least one open blocker or blocking plan (ProjectWorkFact.Blocked);
// in_progress and in_review tasks bucket by state; done tasks bucket
// nowhere. Order within each bucket matches the input order. The
// /projects/{id} cockpit (cockpit.go) computes the same ready&&Blocked()
// rule separately, for its own response shape.
type workBuckets struct {
	InProgress, InReview, Ready, Blocked []store.ProjectWorkFact
}

// bucketWorkFacts buckets one project's facts the way assembleBoard and
// Home's card counts both need, so "blocked" is computed in exactly one
// place for the web surface.
func bucketWorkFacts(facts []store.ProjectWorkFact) workBuckets {
	var b workBuckets
	for _, f := range facts {
		switch {
		case f.Task.State == "in_progress":
			b.InProgress = append(b.InProgress, f)
		case f.Task.State == "in_review":
			b.InReview = append(b.InReview, f)
		case f.Task.State == "ready" && f.Blocked():
			b.Blocked = append(b.Blocked, f)
		case f.Task.State == "ready":
			b.Ready = append(b.Ready, f)
		}
	}
	return b
}

// lastActivity returns the newest Task.UpdatedAt across facts (all states,
// done included), or the zero time for an empty slice.
func lastActivity(facts []store.ProjectWorkFact) time.Time {
	var newest time.Time
	for _, f := range facts {
		if f.Task.UpdatedAt.After(newest) {
			newest = f.Task.UpdatedAt
		}
	}
	return newest
}

// assembleBoard builds the board for one project (projectFilter set) or
// every project (projectFilter ""): each project's tasks bucketed by state
// (in_progress with lease holder, in_review, ready, blocked) plus, when
// projectFilter is "", the last 10 runtime events store-wide.
//
// It is assembled here from ListProjectWorkFacts, the store's shared,
// UI-neutral bulk reader (parent/lease/blocker facts in one read), rather
// than a dedicated Store.Board method — there is no additional query
// complexity to hide behind a store API. Shared by the JSON /api/v1/board
// handler and the GET /work web page; Home's card counts share only the
// bucketing (bucketWorkFacts), not this assembly.
//
// recent_failures is simplified from the full deployments->artifacts join
// described in the design to the last 10 runtime events store-wide, and is
// only included when no project filter is given (it is not project-scoped):
// RecentFailures is left nil (serializes as JSON null, decoded as a nil
// slice by callers) when a project filter narrows the response, and set to a
// non-nil (possibly empty) slice otherwise — that is how a CLI or other
// caller tells "board scoped to one project" from "board with no recent
// failures" apart.
func (s *server) assembleBoard(ctx context.Context, projectFilter string) (*model.BoardResponse, error) {
	var projects []store.Project
	if projectFilter != "" {
		p, err := s.st.GetProject(ctx, projectFilter)
		if err != nil {
			return nil, err
		}
		projects = []store.Project{*p}
	} else {
		ps, err := s.st.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		projects = ps
	}

	facts, err := s.st.ListProjectWorkFacts(ctx, projectFilter)
	if err != nil {
		return nil, err
	}
	// Facts come back in one global (priority, id) order; grouping by
	// project here preserves that order within each group, since it is the
	// same order a per-project query would have produced.
	byProject := make(map[string][]store.ProjectWorkFact, len(projects))
	for _, f := range facts {
		byProject[f.Task.Project] = append(byProject[f.Task.Project], f)
	}

	resp := &model.BoardResponse{Projects: make([]model.BoardProject, 0, len(projects))}

	for _, p := range projects {
		bp := model.BoardProject{
			ID: p.ID, Name: p.Name,
			InProgress: []model.BoardTask{}, InReview: []model.BoardTask{},
			Ready: []model.BoardTask{}, Blocked: []model.BoardTask{},
		}
		buckets := bucketWorkFacts(byProject[p.ID])
		toBoardTasks := func(fs []store.ProjectWorkFact, holders bool) []model.BoardTask {
			out := make([]model.BoardTask, 0, len(fs))
			for _, f := range fs {
				bt := model.BoardTask{Task: f.Task}
				if f.Parent != nil {
					bt.Parent = f.Parent.ID
				}
				if holders && f.Lease != nil {
					bt.Holder = &model.Holder{ActorID: f.Lease.ActorID, ExpiresAt: f.Lease.ExpiresAt}
				}
				out = append(out, bt)
			}
			return out
		}
		bp.InProgress = toBoardTasks(buckets.InProgress, true)
		bp.InReview = toBoardTasks(buckets.InReview, false)
		bp.Ready = toBoardTasks(buckets.Ready, false)
		bp.Blocked = toBoardTasks(buckets.Blocked, false)
		resp.Projects = append(resp.Projects, bp)
	}

	if projectFilter == "" {
		events, err := s.st.ListRuntimeEvents(ctx, "", 10)
		if err != nil {
			return nil, err
		}
		resp.RecentFailures = make([]model.RuntimeEvent, 0, len(events))
		for i := range events {
			resp.RecentFailures = append(resp.RecentFailures, toRuntimeEventJSON(&events[i]))
		}
	}

	return resp, nil
}
