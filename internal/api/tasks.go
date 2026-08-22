package api

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

var validPriorities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true,
}

// invalidPriorityMsg and invalidConcernMsg are shared by every handler that
// gates on validPriorities / store.ValidConcern, for invalidKindMsg's reason:
// the message cannot drift from the set it describes.
const (
	invalidPriorityMsg = "invalid priority: must be critical, high, medium, or low"
	invalidConcernMsg  = "invalid concern: must be completeness, performance, usability, or security"
)

// validSecretNames rejects the request early with a clean message; the store
// re-checks (defense in depth for non-HTTP callers).
func validSecretNames(names []string) bool {
	for _, n := range names {
		if !secrets.ValidName(n) {
			return false
		}
	}
	return true
}

// invalidSecretNameMsg is shared by every handler that gates on
// validSecretNames, so the message cannot drift from the grammar. It names
// both halves of the contract: a caller told only the pattern after
// submitting PATH would read the rejection as a bug (ADR 047 §4).
const invalidSecretNameMsg = "invalid secret name: must match ^[A-Z][A-Z0-9_]*$ " +
	"and must not be loader-sensitive (LD_*, DYLD_*, PATH, IFS, ENV, BASH_ENV, PYTHONPATH, ...)"

// validKinds mirrors the tasks.kind CHECK constraint (migration 0025) and
// wlc:TaskKind in ns/concept.ttl. The list is generated from the Turtle by
// scripts/nsgen.py, so adding a kind is one commit over ns/concept.ttl, the
// regenerated internal/ns/gen.go, and the migration (025 §17).
var validKinds = ns.Set(ns.TaskKinds)

// invalidKindMsg is shared by every handler that gates on validKinds, so the
// message cannot drift from the set when a kind is added.
var invalidKindMsg = "invalid kind: must be " + ns.OrList(ns.TaskKinds)

var validEdgeTypes = map[string]bool{
	"blocks": true, "child_of": true, "follow_up_to": true,
}

// createTask handles POST /api/v1/tasks.
func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	var req model.CreateTaskInput
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
		writeErr(w, http.StatusUnprocessableEntity, invalidPriorityMsg)
		return
	}
	req.Kind = s.normalizeTaskKind(req.Kind, "create")
	if !validKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, invalidKindMsg)
		return
	}
	if req.Concern != "" && !store.ValidConcern(req.Concern) {
		writeErr(w, http.StatusUnprocessableEntity, invalidConcernMsg)
		return
	}
	if !validSecretNames(req.Secrets) {
		writeErr(w, http.StatusUnprocessableEntity, invalidSecretNameMsg)
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

	actorID := actorIDFrom(r)
	now := s.st.Now()

	var created *model.Task
	err := s.recordEvent(r.Context(), "cli", "task.created", req,
		func(tx *sql.Tx, eventID int64) error {
			t, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: req.Project,
				Title:     req.Title,
				Body:      req.Body,
				Priority:  req.Priority,
				Kind:      req.Kind,
				Concern:   req.Concern,
				CreatedBy: actorID,
				Draft:     req.Draft,
				Skills:    req.Skills,
				Secrets:   req.Secrets,
			}, eventID)
			if err != nil {
				return err
			}
			created = t
			if req.Parent != "" {
				// Same transaction as the insert: there is no window where
				// the child exists unparented.
				if err := store.AddEdge(tx, now, t.ID, req.Parent, "child_of", eventID); err != nil {
					return err
				}
			}
			if req.FollowUpTo != "" {
				if err := store.AddEdge(tx, now, t.ID, req.FollowUpTo, "follow_up_to", eventID); err != nil {
					return err
				}
			}
			if err := store.ReconcileEmbedded(tx, now, t.ID,
				blobref.Extract(req.Body), actorID); err != nil {
				return err
			}
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

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

	resp := model.TaskDetail{Task: *t, Blocked: blocked[id]}
	resp.Edges.Out, resp.Edges.In = edgesToJSON(out, in)
	if lease, err := s.st.ActiveLease(r.Context(), id); err == nil {
		l := toLeaseJSON(lease)
		resp.Lease = &l
		sessions, err := s.st.AgentSessionsForLease(r.Context(), lease.ID)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.AgentSessions = sessions
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
	resp.Hierarchy.Progress = progress
	if parent != nil {
		resp.Hierarchy.Parent = &model.TaskParent{ID: parent.ID, Title: parent.Title, State: parent.State}
	}

	blobs, err := s.st.ListTaskBlobs(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp.Blobs = make([]model.TaskBlob, 0, len(blobs))
	for _, b := range blobs {
		b.URL = "/blob/" + b.Hash
		resp.Blobs = append(resp.Blobs, b)
	}

	writeJSON(w, http.StatusOK, resp)
}

// getTaskCost handles
// GET /api/v1/tasks/{id}/cost?from=&to=&children=: a task's accounted usage
// and cost (spec 025 §15.6, AC31). children=true widens the scope to the
// task's child_of descendants, so a container task's own report is not
// always zero. No dedicated metric: this is an ordinary read with no derived
// outcome, so the generic http_requests_total / http_request_duration_seconds
// middleware (022 §0) is sufficient and 022 §8's add-a-metric rule is
// deliberately not triggered.
func (s *server) getTaskCost(w http.ResponseWriter, r *http.Request) {
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
	var children bool
	if raw := r.URL.Query().Get("children"); raw != "" {
		children, err = strconv.ParseBool(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid children: want true or false")
			return
		}
	}

	tc, err := s.st.TaskCost(r.Context(), id, children, from, to)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.TaskCost{
		Task:             id,
		IncludesChildren: children,
		Sessions:         tc.Sessions,
		Cost:             toCostReportJSON(&tc.CostReport),
	})
}

// listTasks handles
// GET /api/v1/tasks?project=&state=&priority=&kind=&parent=&assignee=&has_children=&repo=&updated_since=&plan_doc=&about_doc=&deleted=&detail=&tree=&root=.
// state is repeatable and/or comma-separated; has_children=true narrows to
// containers; updated_since is an RFC3339 instant that narrows to the tasks
// touched at or after it (the incremental fetch a polling mirror makes);
// plan_doc narrows to the tasks minted from that plan document — the query
// that is the plan's task set (025 §9.2, §1); about_doc narrows to the tasks
// that reference that document (025 §15.4); deleted=true switches the list
// from live tasks to tombstoned ones (044 §5); detail=true adds "blocked" and
// "edges" to each row (see model.TaskListDetail) at the cost of two extra
// bulk queries; tree=true answers with the hierarchy instead of a flat list
// (see listTaskTree), and root names the single container it covers.
func (s *server) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// Normalised so `lode task list --kind spec` keeps returning the rows
	// migration 0025 rewrote to design, rather than an empty set.
	kind := s.normalizeTaskKind(q.Get("kind"), "list")
	var states []string
	for _, v := range q["state"] {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				states = append(states, p)
			}
		}
	}
	// tree=true answers with the hierarchy instead of a flat list, and is
	// checked before the remaining filters because it reads only project and
	// state (plus root); see listTaskTree.
	tree, err := queryBool(q, "tree")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if tree {
		s.listTaskTree(w, r, q, states)
		return
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
	// A plan_doc that does not parse is refused rather than ignored, the same
	// stance updated_since takes: silently dropping it would read as "no
	// tasks minted" instead of "the query was malformed".
	var planDoc int64
	if raw := q.Get("plan_doc"); raw != "" {
		var err error
		if planDoc, err = strconv.ParseInt(raw, 10, 64); err != nil || planDoc <= 0 {
			writeErr(w, http.StatusBadRequest, "plan_doc must be a positive integer")
			return
		}
	}
	// Same stance as plan_doc: a non-numeric about_doc is refused rather than
	// silently ignored.
	var aboutDoc int64
	if raw := q.Get("about_doc"); raw != "" {
		var err error
		if aboutDoc, err = strconv.ParseInt(raw, 10, 64); err != nil || aboutDoc <= 0 {
			writeErr(w, http.StatusBadRequest, "about_doc must be a positive integer")
			return
		}
	}
	// A switch, not an addition (044 §5): ?deleted=true lists the tombstoned
	// rows instead of the live ones, because a list mixing the two invites
	// acting on a row that is not there. A non-boolean value is named rather
	// than read as off, the same stance queryBool takes everywhere else.
	deleted, err := queryBool(q, "deleted")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tasks, err := s.st.ListTasks(r.Context(), store.TaskFilter{
		Project:  q.Get("project"),
		States:   states,
		Priority: q.Get("priority"),
		Kind:     kind,
		Parent:   q.Get("parent"),
		Assignee: q.Get("assignee"),
		Repo:     repo,

		HasChildren:  q.Get("has_children") == "true",
		UpdatedSince: updatedSince,
		PlanDoc:      planDoc,
		AboutDoc:     aboutDoc,
		Deleted:      deleted,
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

// listTaskTree answers GET /api/v1/tasks?tree=true&project=&state=&root=
// with model.TaskTreeResponse: every container in scope, its derived
// progress, and its direct children, in one response. Without it a client
// rendering the hierarchy fetches the containers and then one child list per
// container — an N+1 against this endpoint (WL-169).
//
// project and state narrow which containers appear, exactly as they narrow
// the flat list; children come back whatever their state, so the progress
// counts and the listed children describe the same set. root reports that one
// task and its children instead, whatever its own parentage. The other list
// filters do not apply and are ignored.
func (s *server) listTaskTree(w http.ResponseWriter, r *http.Request, q url.Values, states []string) {
	s.observeListExpansion("tasks", "tree")
	nodes, err := s.st.TaskTree(r.Context(), store.TaskTreeFilter{
		Project: q.Get("project"),
		States:  states,
		Root:    q.Get("root"),
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := model.TaskTreeResponse{Nodes: make([]model.TaskTreeNode, 0, len(nodes))}
	for _, n := range nodes {
		// [] rather than null for a container whose children are all
		// tombstoned, so a client can range over the field unconditionally.
		if n.Children == nil {
			n.Children = []model.Task{}
		}
		resp.Nodes = append(resp.Nodes, n)
	}
	writeJSON(w, http.StatusOK, resp)
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
	var req model.EditTaskInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Title == nil && req.Body == nil && req.Priority == nil && req.Concern == nil &&
		req.NeedsDecomposition == nil && req.State == nil && req.Secrets == nil &&
		req.Artifacts == nil && req.Kind == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no fields to update")
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "title must not be blank")
		return
	}
	if req.Kind != nil {
		normalized := s.normalizeTaskKind(*req.Kind, "edit")
		if !validKinds[normalized] {
			writeErr(w, http.StatusUnprocessableEntity, invalidKindMsg)
			return
		}
		req.Kind = &normalized
	}
	if req.Priority != nil && !validPriorities[*req.Priority] {
		writeErr(w, http.StatusUnprocessableEntity, invalidPriorityMsg)
		return
	}
	if req.Concern != nil && *req.Concern != "" && *req.Concern != "none" && !store.ValidConcern(*req.Concern) {
		writeErr(w, http.StatusUnprocessableEntity, invalidConcernMsg)
		return
	}
	if req.Secrets != nil && !validSecretNames(*req.Secrets) {
		writeErr(w, http.StatusUnprocessableEntity, invalidSecretNameMsg)
		return
	}
	if req.Artifacts != nil {
		if msg := validateArtifacts(*req.Artifacts); msg != "" {
			writeErr(w, http.StatusUnprocessableEntity, msg)
			return
		}
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

	err := s.recordEvent(r.Context(), "cli", "task.updated", req,
		func(tx *sql.Tx, eventID int64) error {
			if err := store.UpdateTaskFields(tx, s.st.Now(), id, req.Title, req.Body, req.Priority, req.Concern, req.Secrets, req.NeedsDecomposition, req.Kind); err != nil {
				return err
			}
			for field, val := range map[string]*string{
				"title": req.Title, "body": req.Body, "priority": req.Priority, "concern": req.Concern,
				"kind": req.Kind,
			} {
				if val == nil {
					continue
				}
				if err := store.LogChange(tx, "task", id, eventID,
					map[string]string{"field": field, "new": *val}); err != nil {
					return err
				}
			}
			if req.Secrets != nil {
				if err := store.LogChange(tx, "task", id, eventID,
					map[string]any{"field": "secrets", "new": *req.Secrets}); err != nil {
					return err
				}
			}
			if req.Artifacts != nil {
				for _, a := range *req.Artifacts {
					if err := store.DeclareArtifact(tx, s.st.Now(), "task", id, strings.TrimSpace(a)); err != nil {
						return err
					}
				}
				if err := store.LogChange(tx, "task", id, eventID,
					map[string]any{"field": "artifacts", "new": *req.Artifacts}); err != nil {
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
			// Reconcile against the body as stored, not as patched: a PATCH
			// that leaves the body alone must still reconcile against the
			// body that is actually there.
			body, err := store.TaskBody(tx, id)
			if err != nil {
				return err
			}
			if err := store.ReconcileEmbedded(tx, s.st.Now(), id,
				blobref.Extract(body), actorIDFrom(r)); err != nil {
				return err
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

// resolveEdge validates an edge request against the {id} path task and
// returns the (from, to) endpoints. A written response means failure.
func resolveEdge(w http.ResponseWriter, id string, req model.EdgeInput) (from, to string, ok bool) {
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
	var req model.EdgeInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	from, to, ok := resolveEdge(w, id, req)
	if !ok {
		return
	}

	err := s.recordEvent(r.Context(), "cli", "task.edge_added",
		map[string]string{"from": from, "to": to, "type": req.Type},
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, s.st.Now(), from, to, req.Type, eventID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, model.Edge{From: from, To: to, Type: req.Type})
}

// removeEdge handles DELETE /api/v1/tasks/{id}/edges.
func (s *server) removeEdge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.EdgeInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	from, to, ok := resolveEdge(w, id, req)
	if !ok {
		return
	}

	err := s.recordEvent(r.Context(), "cli", "task.edge_removed",
		map[string]string{"from": from, "to": to, "type": req.Type},
		func(tx *sql.Tx, eventID int64) error {
			return store.RemoveEdge(tx, from, to, req.Type, eventID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setTaskSkills handles PUT /api/v1/tasks/{id}/skills: replaces the task's
// pinned skill names, always surfaced in a recommendation regardless of
// embedding similarity.
func (s *server) setTaskSkills(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.SetSkillsInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}

	err := s.recordEvent(r.Context(), "cli", "task.skills_set", req,
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
	writeJSON(w, http.StatusOK, model.TaskSkills{Skills: t.Skills})
}
