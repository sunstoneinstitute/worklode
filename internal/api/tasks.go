package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

var validPriorities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true,
}

var validKinds = map[string]bool{
	"feature": true, "bug": true, "chore": true, "spec": true,
}

var validEdgeTypes = map[string]bool{
	"blocks": true, "child_of": true,
}

// taskJSON is the wire form of a task.
type taskJSON struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Priority  string    `json:"priority"`
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toTaskJSON(t *store.Task) taskJSON {
	return taskJSON{
		ID:        t.ID,
		Project:   t.ProjectID,
		Title:     t.Title,
		Body:      t.Body,
		Priority:  t.Priority,
		Kind:      t.Kind,
		State:     t.State,
		CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}

type createTaskRequest struct {
	Project  string `json:"project"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Priority string `json:"priority"`
	Kind     string `json:"kind"`
	Draft    bool   `json:"draft"`
}

// createTask handles POST /api/v1/tasks.
func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "title is required")
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
	if _, err := s.st.GetProject(r.Context(), req.Project); err != nil {
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
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.created", payload,
		func(tx *sql.Tx, eventID int64) error {
			t, err := store.CreateTask(tx, s.st.Now(), store.TaskInput{
				ProjectID: req.Project,
				Title:     req.Title,
				Body:      req.Body,
				Priority:  req.Priority,
				Kind:      req.Kind,
				CreatedBy: actor.ID,
				Draft:     req.Draft,
			})
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

type edgeOut struct {
	To   string `json:"to"`
	Type string `json:"type"`
}

type edgeIn struct {
	From string `json:"from"`
	Type string `json:"type"`
}

type taskDetailJSON struct {
	taskJSON
	Blocked bool `json:"blocked"`
	Edges   struct {
		Out []edgeOut `json:"out"`
		In  []edgeIn  `json:"in"`
	} `json:"edges"`
	Lease *leaseJSON `json:"lease,omitempty"`
}

// getTask handles GET /api/v1/tasks/{id}. The response includes "lease" when
// the task has an active lease, so a CLI `show` can display the holder
// without a second request.
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

	resp := taskDetailJSON{taskJSON: toTaskJSON(t), Blocked: blocked[id]}
	resp.Edges.Out = make([]edgeOut, 0, len(out))
	for _, e := range out {
		resp.Edges.Out = append(resp.Edges.Out, edgeOut{To: e.ToTask, Type: e.Type})
	}
	resp.Edges.In = make([]edgeIn, 0, len(in))
	for _, e := range in {
		resp.Edges.In = append(resp.Edges.In, edgeIn{From: e.FromTask, Type: e.Type})
	}
	if lease, err := s.st.ActiveLease(r.Context(), id); err == nil {
		l := toLeaseJSON(lease)
		resp.Lease = &l
	} else if !errors.Is(err, store.ErrNotFound) {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// listTasks handles GET /api/v1/tasks?project=&state=&priority=.
// state is repeatable and/or comma-separated.
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
	tasks, err := s.st.ListTasks(r.Context(), store.TaskFilter{
		Project:  q.Get("project"),
		States:   states,
		Priority: q.Get("priority"),
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := struct {
		Tasks []taskJSON `json:"tasks"`
	}{Tasks: make([]taskJSON, 0, len(tasks))}
	for i := range tasks {
		resp.Tasks = append(resp.Tasks, toTaskJSON(&tasks[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

type patchTaskRequest struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Priority *string `json:"priority"`
	State    *string `json:"state"`
}

// patchStateFrom maps the states PATCH may move a task into to the required
// current state. Only lease-free transitions are allowed here: "ready"
// publishes a draft, "in_progress" reworks a task whose review requested
// changes. Every other transition has a dedicated endpoint (claim, release,
// done, abandon, reopen) that also manages the task's lease.
var patchStateFrom = map[string]string{
	"ready":       "draft",
	"in_progress": "in_review",
}

// patchTask handles PATCH /api/v1/tasks/{id}: updates only the sent fields.
// An optional "state" field requests a state transition in the same event;
// see patchStateFrom for the two transitions PATCH may perform.
func (s *server) patchTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req patchTaskRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Title == nil && req.Body == nil && req.Priority == nil && req.State == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no fields to update")
		return
	}
	if req.Priority != nil && !validPriorities[*req.Priority] {
		writeErr(w, http.StatusUnprocessableEntity, "invalid priority: must be critical, high, medium, or low")
		return
	}
	var stateFrom string
	if req.State != nil {
		var ok bool
		stateFrom, ok = patchStateFrom[*req.State]
		if !ok {
			writeErr(w, http.StatusUnprocessableEntity,
				`state must be "ready" (from draft) or "in_progress" (from in_review); use the claim, release, done, or abandon endpoints for other transitions`)
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
			if err := store.UpdateTaskFields(tx, s.st.Now(), id, req.Title, req.Body, req.Priority, nil, nil); err != nil {
				return err
			}
			for field, val := range map[string]*string{
				"title": req.Title, "body": req.Body, "priority": req.Priority,
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
	writeJSON(w, http.StatusOK, toTaskJSON(t))
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
		writeErr(w, http.StatusUnprocessableEntity, "invalid edge type: must be blocks or child_of")
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
