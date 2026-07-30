package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

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

// --- projects ---------------------------------------------------------

// repoJSON is the wire form of a repo mapping: the repo and the terminal
// delivery state that counts as fully delivered for it.
type repoJSON struct {
	Repo      string `json:"repo"`
	DoneState string `json:"done_state"`
}

type projectJSON struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Key   string     `json:"key"`
	Repos []repoJSON `json:"repos"`
	Focus []string   `json:"focus"`
}

type createProjectRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// toProjectJSON builds the wire form of a project, normalizing nil repo and
// focus slices to empty arrays so they serialize as [] rather than null.
func toProjectJSON(p *store.Project, repos []store.RepoMapping) projectJSON {
	rs := make([]repoJSON, 0, len(repos))
	for _, m := range repos {
		rs = append(rs, repoJSON{Repo: m.Repo, DoneState: m.DoneState})
	}
	focus := p.Focus
	if focus == nil {
		focus = []string{}
	}
	return projectJSON{
		ID: p.ID, Name: p.Name, Key: p.Key, Repos: rs, Focus: focus,
	}
}

// createProject handles POST /api/v1/projects.
func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
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
	resp := struct {
		Projects []projectJSON `json:"projects"`
	}{Projects: make([]projectJSON, 0, len(ps))}
	for _, p := range ps {
		repos, err := s.st.ListRepos(r.Context(), p.ID)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Projects = append(resp.Projects, toProjectJSON(&p, repos))
	}
	writeJSON(w, http.StatusOK, resp)
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

type patchProjectRequest struct {
	Focus *[]string `json:"focus"`
}

// patchProject handles PATCH /api/v1/projects/{id}: currently only updates
// focus, the ordered list of concerns the project's ranking should
// prioritize (see store.SetProjectFocus). Admin-gated like the other project
// mutations, since focus affects claim-next ordering for everyone.
func (s *server) patchProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req patchProjectRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Focus == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no fields to update")
		return
	}
	if err := s.st.SetProjectFocus(r.Context(), id, *req.Focus); err != nil {
		s.mapStoreErr(w, err)
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
	writeJSON(w, http.StatusOK, toProjectJSON(p, repos))
}

type addRepoRequest struct {
	Repo string `json:"repo"`
	// DoneState is optional; empty leaves the mapping at the schema default.
	DoneState string `json:"done_state"`
}

// addRepo handles POST /api/v1/projects/{id}/repos.
func (s *server) addRepo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req addRepoRequest
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
	switch {
	case req.DoneState != "":
		if err := s.st.SetRepoDoneState(r.Context(), req.Repo, req.DoneState); err != nil {
			s.mapStoreErr(w, err)
			return
		}
		doneState = req.DoneState
	case s.appAuth != nil:
		doneState = s.discoverDoneState(r.Context(), req.Repo)
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"project_id": id, "repo": req.Repo, "done_state": doneState,
	})
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
	var req struct {
		DoneState string `json:"done_state"`
	}
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

type actorJSON struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

type createActorRequest struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

// createActor handles POST /api/v1/actors.
func (s *server) createActor(w http.ResponseWriter, r *http.Request) {
	var req createActorRequest
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
	writeJSON(w, http.StatusCreated, actorJSON{
		ID: req.ID, Kind: req.Kind, DisplayName: req.DisplayName, Admin: req.Admin,
	})
}

type createTokenRequest struct {
	Description string  `json:"description"`
	ExpiresAt   *string `json:"expires_at"`
}

// createToken handles POST /api/v1/actors/{id}/tokens. The plaintext token
// is returned exactly once; only its hash is stored (see Store.CreateToken).
func (s *server) createToken(w http.ResponseWriter, r *http.Request) {
	actorID := r.PathValue("id")
	var req createTokenRequest
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
	writeJSON(w, http.StatusCreated, map[string]string{"token": plaintext})
}

type revokeTokenRequest struct {
	Token string `json:"token"`
}

// revokeToken handles DELETE /api/v1/tokens: revoke by plaintext or hash.
func (s *server) revokeToken(w http.ResponseWriter, r *http.Request) {
	var req revokeTokenRequest
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

type issueJSON struct {
	Repo              string   `json:"repo"`
	Number            int64    `json:"number"`
	Title             string   `json:"title"`
	State             string   `json:"state"`
	TriageState       string   `json:"triage_state"`
	TaskID            string   `json:"task_id,omitempty"`
	AppliesToVersions []string `json:"applies_to_versions,omitempty"`
	URL               string   `json:"url"`
}

func toIssueJSON(is *store.Issue) issueJSON {
	out := issueJSON{
		Repo: is.Repo, Number: is.Number, Title: is.Title, State: is.State,
		TriageState: is.TriageState, AppliesToVersions: is.AppliesToVersions, URL: is.URL,
	}
	if is.TaskID != nil {
		out.TaskID = *is.TaskID
	}
	return out
}

// listInbox handles GET /api/v1/inbox?state=new.
func (s *server) listInbox(w http.ResponseWriter, r *http.Request) {
	issues, err := s.st.ListIssues(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := struct {
		Issues []issueJSON `json:"issues"`
	}{Issues: make([]issueJSON, 0, len(issues))}
	for i := range issues {
		resp.Issues = append(resp.Issues, toIssueJSON(&issues[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

type promoteRequest struct {
	Repo              string   `json:"repo"`
	Number            int64    `json:"number"`
	Title             string   `json:"title"`
	Body              string   `json:"body"`
	Priority          string   `json:"priority"`
	Kind              string   `json:"kind"`
	AppliesToVersions []string `json:"applies_to_versions"`
}

// promoteInbox handles POST /api/v1/inbox/promote: turn an inbox issue into a
// task. The repo contains a slash, so repo and number travel as body fields
// rather than path segments. When title is empty it defaults to the issue's
// own title, read inside the same transaction as the promotion.
func (s *server) promoteInbox(w http.ResponseWriter, r *http.Request) {
	var req promoteRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	if !validPriorities[req.Priority] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid priority: must be critical, high, medium, or low")
		return
	}
	if !validKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid kind: must be feature, bug, chore, or spec")
		return
	}
	project, err := s.st.ProjectForRepo(r.Context(), req.Repo)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actor := actorFrom(r)

	var created *store.Task
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "issue.promoted", payload,
		func(tx *sql.Tx, _ int64) error {
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
				CreatedBy: actor.ID,
			}, req.AppliesToVersions)
			if err != nil {
				return err
			}
			created = t
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTaskJSON(created))
}

type dismissRequest struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
}

// dismissInbox handles POST /api/v1/inbox/dismiss.
func (s *server) dismissInbox(w http.ResponseWriter, r *http.Request) {
	var req dismissRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "issue.dismissed", payload,
		func(tx *sql.Tx, _ int64) error {
			return store.DismissIssue(tx, req.Repo, req.Number)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- board ---------------------------------------------------------------

type holderJSON struct {
	ActorID   string    `json:"actor_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type boardTaskJSON struct {
	taskJSON
	Holder *holderJSON `json:"holder,omitempty"`
}

type boardProjectJSON struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	InProgress []boardTaskJSON `json:"in_progress"`
	InReview   []boardTaskJSON `json:"in_review"`
	Ready      []boardTaskJSON `json:"ready"`
	Blocked    []boardTaskJSON `json:"blocked"`
}

type runtimeEventJSON struct {
	ID         int64     `json:"id"`
	Cluster    string    `json:"cluster"`
	Kind       string    `json:"kind"`
	Workload   string    `json:"workload"`
	Image      string    `json:"image"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

func toRuntimeEventJSON(re *store.RuntimeEvent) runtimeEventJSON {
	return runtimeEventJSON{
		ID: re.ID, Cluster: re.Cluster, Kind: re.Kind, Workload: re.Workload,
		Image: re.Image, Message: re.Message, OccurredAt: re.OccurredAt,
	}
}

// boardResponse is the JSON shape of GET /api/v1/board, and the data the web
// board pages (GET / and GET /projects/{id}) render.
type boardResponse struct {
	Projects       []boardProjectJSON `json:"projects"`
	RecentFailures []runtimeEventJSON `json:"recent_failures"`
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

// assembleBoard builds the board for one project (projectFilter set) or
// every project (projectFilter ""): each project's tasks bucketed by state
// (in_progress with lease holder, in_review, ready, blocked) plus, when
// projectFilter is "", the last 10 runtime events store-wide.
//
// It is assembled here from existing store readers (ListTasks,
// BlockedTaskIDs, ActiveLease) rather than a dedicated Store.Board method —
// there is no additional query complexity to hide behind a store API. Shared
// by the JSON /api/v1/board handler and the GET / and GET /projects/{id} web
// pages, so the bucket logic lives in exactly one place.
//
// recent_failures is simplified from the full deployments->artifacts join
// described in the design to the last 10 runtime events store-wide, and is
// only included when no project filter is given (it is not project-scoped):
// RecentFailures is left nil (serializes as JSON null, decoded as a nil
// slice by callers) when a project filter narrows the response, and set to a
// non-nil (possibly empty) slice otherwise — that is how a CLI or other
// caller tells "board scoped to one project" from "board with no recent
// failures" apart.
func (s *server) assembleBoard(ctx context.Context, projectFilter string) (*boardResponse, error) {
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

	blocked, err := s.st.BlockedTaskIDs(ctx)
	if err != nil {
		return nil, err
	}

	resp := &boardResponse{Projects: make([]boardProjectJSON, 0, len(projects))}

	for _, p := range projects {
		tasks, err := s.st.ListTasks(ctx, store.TaskFilter{Project: p.ID})
		if err != nil {
			return nil, err
		}
		bp := boardProjectJSON{
			ID: p.ID, Name: p.Name,
			InProgress: []boardTaskJSON{}, InReview: []boardTaskJSON{},
			Ready: []boardTaskJSON{}, Blocked: []boardTaskJSON{},
		}
		for i := range tasks {
			t := &tasks[i]
			bt := boardTaskJSON{taskJSON: toTaskJSON(t)}
			switch {
			case t.State == "in_progress":
				// No active lease (e.g. it expired but the sweeper hasn't
				// moved the task back to ready yet) just means no holder;
				// any other error is a real failure.
				lease, err := s.st.ActiveLease(ctx, t.ID)
				if err == nil {
					bt.Holder = &holderJSON{ActorID: lease.ActorID, ExpiresAt: lease.ExpiresAt}
				} else if !errors.Is(err, store.ErrNotFound) {
					return nil, err
				}
				bp.InProgress = append(bp.InProgress, bt)
			case t.State == "in_review":
				bp.InReview = append(bp.InReview, bt)
			case t.State == "ready" && blocked[t.ID]:
				bp.Blocked = append(bp.Blocked, bt)
			case t.State == "ready":
				bp.Ready = append(bp.Ready, bt)
			}
		}
		resp.Projects = append(resp.Projects, bp)
	}

	if projectFilter == "" {
		events, err := s.st.ListRuntimeEvents(ctx, "", 10)
		if err != nil {
			return nil, err
		}
		resp.RecentFailures = make([]runtimeEventJSON, 0, len(events))
		for i := range events {
			resp.RecentFailures = append(resp.RecentFailures, toRuntimeEventJSON(&events[i]))
		}
	}

	return resp, nil
}
