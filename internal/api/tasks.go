package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

var validPriorities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true,
}

// validKinds mirrors the tasks.kind CHECK constraint (migration 0017) and
// wlc:TaskKind in ns/concept.ttl; all three carry the same six kinds.
var validKinds = map[string]bool{
	"feature": true, "bug": true, "chore": true, "spec": true,
	"review": true, "spike": true,
}

// invalidKindMsg is shared by every handler that gates on validKinds, so the
// message cannot drift from the map when a kind is added.
const invalidKindMsg = "invalid kind: must be feature, bug, chore, spec, review, or spike"

var validEdgeTypes = map[string]bool{
	"blocks": true, "child_of": true, "follow_up_to": true,
}

type createTaskRequest struct {
	Project    string   `json:"project"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Priority   string   `json:"priority"`
	Kind       string   `json:"kind"`
	Concern    string   `json:"concern"`
	Draft      bool     `json:"draft"`
	Parent     string   `json:"parent"`
	FollowUpTo string   `json:"follow_up_to"`
	Skills     []string `json:"skills"`
}

// createTask handles POST /api/v1/tasks.
func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	req.Parent = strings.TrimSpace(req.Parent)
	req.FollowUpTo = strings.TrimSpace(req.FollowUpTo)
	if strings.TrimSpace(req.Title) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "title is required")
		return
	}
	if !validPriorities[req.Priority] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid priority: must be critical, high, medium, or low")
		return
	}
	if !validKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, invalidKindMsg)
		return
	}
	if req.Concern != "" && !store.ValidConcern(req.Concern) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid concern: must be completeness, performance, usability, or security")
		return
	}
	if _, err := s.st.GetProject(r.Context(), req.Project); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if req.Parent != "" {
		// Named 404 here, ahead of the transaction: AddEdge's own lookup
		// inside RecordEvent stays the authority for everything else (same
		// project, no cycle, one parent per task, depth cap), but its
		// ErrNotFound would otherwise collide with GetProject's and be
		// reported as an anonymous 404.
		if _, err := s.st.GetTask(r.Context(), req.Parent); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "parent not found: "+req.Parent)
			return
		}
	}
	if req.FollowUpTo != "" {
		// Named 404 for the same reason as Parent's: AddEdge's ErrNotFound
		// would otherwise be reported as an anonymous 404 indistinguishable
		// from the project lookup's.
		if _, err := s.st.GetTask(r.Context(), req.FollowUpTo); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "follow_up_to not found: "+req.FollowUpTo)
			return
		}
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
	now := s.st.Now()

	var created *model.Task
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.created", payload,
		func(tx *sql.Tx, eventID int64) error {
			t, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: req.Project,
				Title:     req.Title,
				Body:      req.Body,
				Priority:  req.Priority,
				Kind:      req.Kind,
				Concern:   req.Concern,
				CreatedBy: actor.ID,
				Draft:     req.Draft,
				Skills:    req.Skills,
			})
			if err != nil {
				return err
			}
			created = t
			if req.Parent != "" {
				// Same transaction as the insert: there is no window where
				// the child exists unparented.
				if err := store.AddEdge(tx, now, t.ID, req.Parent, "child_of"); err != nil {
					return err
				}
			}
			if req.FollowUpTo != "" {
				if err := store.AddEdge(tx, now, t.ID, req.FollowUpTo, "follow_up_to"); err != nil {
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

// getTask handles GET /api/v1/tasks/{id}. The response includes "lease" when
// the task has an active lease, so a CLI `show` can display the holder
// without a second request.
// edgesToJSON converts a task's outgoing and incoming store edges to their
// wire types, shared by getTask and listTasks' detail expansion so the two
// projections cannot drift. Always returns non-nil slices, even for nil
// input, so "edges" serializes as [] rather than null for an edgeless task.
func edgesToJSON(out, in []store.Edge) ([]model.TaskEdgeOut, []model.TaskEdgeIn) {
	outJSON := make([]model.TaskEdgeOut, 0, len(out))
	for _, e := range out {
		outJSON = append(outJSON, model.TaskEdgeOut{To: e.ToTask, Type: e.Type})
	}
	inJSON := make([]model.TaskEdgeIn, 0, len(in))
	for _, e := range in {
		inJSON = append(inJSON, model.TaskEdgeIn{From: e.FromTask, Type: e.Type})
	}
	return outJSON, inJSON
}

func (s *server) getTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	blocked, err := s.st.BlockedTaskIDs(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out, in, err := s.st.ListEdges(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := model.TaskDetail{Task: *t, Blocked: blocked[id]}
	resp.Edges.Out, resp.Edges.In = edgesToJSON(out, in)
	if lease, err := s.st.ActiveLease(r.Context(), id); err == nil {
		l := toLeaseJSON(lease)
		resp.Lease = &l
	} else if !errors.Is(err, store.ErrNotFound) {
		s.mapStoreErr(w, err)
		return
	}

	parent, err := s.st.ParentOf(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	progress, err := s.st.ChildProgress(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp.Hierarchy.Progress = model.TaskProgress{Closed: progress.Closed, Total: progress.Total}
	if parent != nil {
		resp.Hierarchy.Parent = &model.TaskParent{ID: parent.ID, Title: parent.Title, State: parent.State}
	}
	writeJSON(w, http.StatusOK, resp)
}

// listTasks handles
// GET /api/v1/tasks?project=&state=&priority=&kind=&parent=&assignee=&has_children=&repo=&updated_since=&detail=.
// state is repeatable and/or comma-separated; has_children=true narrows to
// containers; updated_since is an RFC3339 instant that narrows to the tasks
// touched at or after it (the incremental fetch a polling mirror makes);
// detail=true adds "blocked" and "edges" to each row (see model.TaskListDetail)
// at the cost of two extra bulk queries.
func (s *server) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var states []string
	for _, v := range q["state"] {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				states = append(states, p)
			}
		}
	}
	// The repo filter takes any remote URL form as well as owner/name, and is
	// normalized here rather than in the client for resolveProjectByRemote's
	// reason: a normalization fix must ship without a client upgrade.
	var repo string
	if raw := q.Get("repo"); raw != "" {
		var err error
		if repo, err = repourl.Normalize(raw); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	// A watermark that is not a timestamp is refused rather than dropped: an
	// ignored one looks like a working incremental sync while returning
	// everything, which the client would then take as "all of this changed".
	var updatedSince time.Time
	if raw := q.Get("updated_since"); raw != "" {
		var err error
		if updatedSince, err = time.Parse(time.RFC3339, raw); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "updated_since must be an RFC3339 timestamp, e.g. 2026-08-18T09:30:00Z")
			return
		}
	}
	tasks, err := s.st.ListTasks(r.Context(), store.TaskFilter{
		Project:  q.Get("project"),
		States:   states,
		Priority: q.Get("priority"),
		Kind:     q.Get("kind"),
		Parent:   q.Get("parent"),
		Assignee: q.Get("assignee"),
		Repo:     repo,

		HasChildren:  q.Get("has_children") == "true",
		UpdatedSince: updatedSince,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if q.Get("detail") != "true" {
		resp := model.TaskListResponse{Tasks: make([]model.Task, 0, len(tasks))}
		resp.Tasks = append(resp.Tasks, tasks...)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	s.observeListExpansion("tasks", "detail")

	ids := make([]string, 0, len(tasks))
	for i := range tasks {
		ids = append(ids, tasks[i].ID)
	}
	blocked, err := s.st.BlockedTaskIDs(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	edges, err := s.st.ListEdgesForTasks(r.Context(), ids)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := model.TaskListDetailResponse{Tasks: make([]model.TaskListDetail, 0, len(tasks))}
	for i := range tasks {
		row := model.TaskListDetail{Task: tasks[i], Blocked: blocked[tasks[i].ID]}
		te := edges[tasks[i].ID]
		row.Edges.Out, row.Edges.In = edgesToJSON(te.Out, te.In)
		resp.Tasks = append(resp.Tasks, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

type patchTaskRequest struct {
	Title              *string `json:"title"`
	Body               *string `json:"body"`
	Priority           *string `json:"priority"`
	Concern            *string `json:"concern"`
	NeedsDecomposition *bool   `json:"needs_decomposition"`
	State              *string `json:"state"`
}

// patchStateFrom maps the states PATCH may move a task into to the required
// current state. Only lease-free transitions are allowed here: "ready"
// publishes a draft, "in_progress" reworks a task whose review requested
// changes, "in_review" is the human submit-for-review manual route (a task
// with no PR, moved by assign/start rather than claim). The GitHub PR webhook
// (internal/hooks/github.go) is the automatic route to in_review for
// PR-backed tasks. Every other transition has a dedicated endpoint (claim,
// release, done, abandon, reopen) that also manages the task's lease.
var patchStateFrom = map[string]string{
	"ready":       "draft",
	"in_progress": "in_review",
	"in_review":   "in_progress",
}

// patchTask handles PATCH /api/v1/tasks/{id}: updates only the sent fields.
// An optional "state" field requests a state transition in the same event;
// see patchStateFrom for the three transitions PATCH may perform.
func (s *server) patchTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req patchTaskRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Title == nil && req.Body == nil && req.Priority == nil && req.Concern == nil &&
		req.NeedsDecomposition == nil && req.State == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no fields to update")
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "title must not be blank")
		return
	}
	if req.Priority != nil && !validPriorities[*req.Priority] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid priority: must be critical, high, medium, or low")
		return
	}
	if req.Concern != nil && *req.Concern != "" && *req.Concern != "none" && !store.ValidConcern(*req.Concern) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid concern: must be completeness, performance, usability, or security")
		return
	}
	var stateFrom string
	if req.State != nil {
		var ok bool
		stateFrom, ok = patchStateFrom[*req.State]
		if !ok {
			writeErr(w, http.StatusUnprocessableEntity,
				`state must be "ready" (from draft), "in_progress" (from in_review), or "in_review" (from in_progress); use the claim, release, done, or abandon endpoints for other transitions`)
			return
		}
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

	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.updated", payload,
		func(tx *sql.Tx, eventID int64) error {
			if err := store.UpdateTaskFields(tx, s.st.Now(), id, req.Title, req.Body, req.Priority, req.Concern, req.NeedsDecomposition); err != nil {
				return err
			}
			for field, val := range map[string]*string{
				"title": req.Title, "body": req.Body, "priority": req.Priority, "concern": req.Concern,
			} {
				if val == nil {
					continue
				}
				if err := store.LogChange(tx, "task", id, eventID,
					map[string]string{"field": field, "new": *val}); err != nil {
					return err
				}
			}
			if req.State != nil {
				// Transition reads the current state inside the tx and fails
				// with ErrBadTransition (422) unless it equals stateFrom. It
				// also writes the state_log row.
				if err := store.Transition(tx, s.st.Now(), id, stateFrom, *req.State, eventID); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type edgeRequest struct {
	To   *string `json:"to"`
	From *string `json:"from"`
	Type string  `json:"type"`
}

// resolveEdge validates an edge request against the {id} path task and
// returns the (from, to) endpoints. A written response means failure.
func resolveEdge(w http.ResponseWriter, id string, req edgeRequest) (from, to string, ok bool) {
	if (req.To == nil) == (req.From == nil) {
		writeErr(w, http.StatusUnprocessableEntity, "exactly one of to/from must be set")
		return "", "", false
	}
	if !validEdgeTypes[req.Type] {
		writeErr(w, http.StatusUnprocessableEntity,
			"invalid edge type: must be blocks, child_of, or follow_up_to")
		return "", "", false
	}
	if req.To != nil {
		from, to = id, *req.To
	} else {
		from, to = *req.From, id
	}
	if from == to {
		writeErr(w, http.StatusUnprocessableEntity, "self-edge not allowed")
		return "", "", false
	}
	return from, to, true
}

// addEdge handles POST /api/v1/tasks/{id}/edges.
func (s *server) addEdge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req edgeRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	from, to, ok := resolveEdge(w, id, req)
	if !ok {
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]string{"from": from, "to": to, "type": req.Type})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.edge_added", payload,
		func(tx *sql.Tx, _ int64) error {
			return store.AddEdge(tx, s.st.Now(), from, to, req.Type)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"from": from, "to": to, "type": req.Type})
}

// removeEdge handles DELETE /api/v1/tasks/{id}/edges.
func (s *server) removeEdge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req edgeRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	from, to, ok := resolveEdge(w, id, req)
	if !ok {
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]string{"from": from, "to": to, "type": req.Type})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.edge_removed", payload,
		func(tx *sql.Tx, _ int64) error {
			return store.RemoveEdge(tx, from, to, req.Type)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setSkillsRequest struct {
	Skills []string `json:"skills"`
}

// setTaskSkills handles PUT /api/v1/tasks/{id}/skills: replaces the task's
// pinned skill names, always surfaced in a recommendation regardless of
// embedding similarity.
func (s *server) setTaskSkills(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req setSkillsRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
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
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.skills_set", payload,
		func(tx *sql.Tx, _ int64) error {
			return store.SetTaskSkills(tx, s.st.Now(), id, req.Skills)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	// Read back the stored (cleaned: trimmed, deduped) list rather than
	// echoing the raw request, so the response never lies about what was
	// actually persisted.
	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": t.Skills})
}
