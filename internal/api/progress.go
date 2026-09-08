// progress.go serves the project Progress page (WL-SPEC-66 §2): one bulk
// read of the project's corpus and task set (store.ProjectProgress), derived
// into model.ProjectProgress by internal/progress, rendered by ui.Progress.
// Nothing is stored and nothing is cached — every state on the page is
// recomputed per request, so it can never disagree with the run board or
// `lode doc todo` (§1).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/progress"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// progressPage handles GET /projects/{id}/progress, runBoardPage-shaped: the
// project header first (so an unknown project 404s like every other project
// route), then the one progress read.
//
// A project with no spec has no Progress page at all (§2, amending 056 §2):
// the sidebar shows no entry and the route answers 404. The specs in the
// reader's own input decide that, so the page and the sidebar cannot disagree
// about whether the project has any.
func (s *server) progressPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	in, err := s.st.ProjectProgress(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	if len(in.Specs) == 0 {
		webErr(w, http.StatusNotFound, "not found")
		return
	}

	p := progress.Derive(in)
	s.renderWeb(w, r, http.StatusOK, "progress page", ui.Progress(ui.ProgressView{
		Page:          ui.PageProps{Title: "worklode: " + project.Name + ": Progress"},
		CanonicalURL:  "/projects/" + project.ID + "/progress",
		Project:       project,
		Viewer:        actorIDFrom(r),
		P:             p,
		Legend:        ui.ProgressLegend(p),
		ReviewEnabled: s.hasReviewSurface(),
	}))
}

// hasSpecs is the sidebar's Progress-entry bit. It is read here rather than
// carried on model.CockpitProject because the JSON cockpit's shape is
// contracted by spec 032 and this is a page affordance, not a change to that
// contract — the same reason projectPage reads its agent sessions and rally
// off the store. A failed read logs and hides the entry: the page it leads to
// would fail the same way.
func (s *server) hasSpecs(ctx context.Context, projectID string) bool {
	has, err := s.st.ProjectHasSpecs(ctx, projectID)
	if err != nil {
		s.log.Error("check project specs", "err", err, "project", projectID)
		return false
	}
	return has
}

// getProjectProgress handles GET /api/v1/projects/{id}/progress: the same
// derived model.ProjectProgress the page renders, as JSON, so an agent reads
// what a person sees (066 §7). The project is read first so an unknown id
// 404s rather than answering with an empty corpus. A project with no spec is
// not a 404 here — the page hides itself because there is nothing to draw,
// but "this project has no specs" is a real answer to an API question.
func (s *server) getProjectProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(ctx, projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	in, err := s.st.ProjectProgress(ctx, projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, progress.Derive(in))
}

// --- writes (WL-SPEC-66 §3) -------------------------------------------------

// progressAccept handles POST /projects/{id}/progress/accept (§3.2). It
// performs exactly what POST /api/v1/docs/{id}/accept performs — the same
// typed event, the same store.AcceptDoc — so the owner gate (025 §7) and, for
// a plan, minting its tasks in the same transaction (025 §9.2) are the
// store's. This page can never accept what `lode doc accept` would refuse.
//
// Two refusals are the route's own. A document of another project is a 404:
// the route is project-scoped, whatever the caller may do with that document
// elsewhere. A document that is not draft is a 409, because draft is the
// state the button is offered on (§3.2) and a plan is otherwise re-acceptable
// while accepted, which would turn a stale page's second click into a silent
// no-op reported as success. Neither discloses anything the viewer did not
// just read off the page.
func (s *server) progressAccept(w http.ResponseWriter, r *http.Request) {
	var body model.ProgressAcceptInput
	sub, project, ok := s.beginJSONPost(w, r, "accept", &body)
	if !ok {
		return
	}
	ctx := r.Context()

	doc, err := s.st.GetDoc(ctx, body.Doc)
	if err != nil {
		s.observeProgressWrite("accept", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	if doc.Project != project.ID {
		s.observeProgressWrite("accept", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if doc.Status != "draft" {
		s.observeProgressWrite("accept", "conflict")
		writeErr(w, http.StatusConflict, fmt.Sprintf("doc %d is %s, not draft", doc.ID, doc.Status))
		return
	}

	accepted, minted, inserted, err := s.emitDocAccepted(ctx, doc, sub.ActorID)
	switch {
	// The backbone refused the state, not the actor: the depth gate, a plan
	// that will not parse, or a document that left draft between the check
	// above and the row lock. §4.2 rule 4 makes that a 409 here rather than
	// mapStoreErr's 422, which on this route means a malformed body.
	case errors.Is(err, store.ErrInvalidInput):
		s.observeProgressWrite("accept", "conflict")
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		s.observeProgressWrite("accept", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
	case !inserted:
		// Someone else accepted this document at this version while the
		// request was in flight, so nothing was applied here. Reporting it as
		// a success would credit this actor with an accept the owner gate
		// never admitted (§4.2 rule 7).
		s.observeProgressWrite("accept", "conflict")
		writeErr(w, http.StatusConflict, fmt.Sprintf("doc %d was already accepted", doc.ID))
	default:
		s.st.RecordPlanTasksMinted(len(minted))
		s.observeProgressWrite("accept", "ok")
		writeJSON(w, http.StatusOK, model.ProgressAcceptResponse{
			Doc: accepted.ID, Status: accepted.Status, Minted: len(minted),
		})
	}
}

// progressWriteOutcome bounds a store error to one of the write metric's
// outcomes: "refused" is the act declined — an unknown document, an actor
// who is not the owner — and "error" is a fault. Every write on this page
// classifies its store errors through this one function.
func progressWriteOutcome(err error) string {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		return "refused"
	}
	return "error"
}

// progressPlan handles POST /projects/{id}/progress/plan (§3.4): mint the
// planning task for a spec, the same task 025 §15.4 mints when the spec is
// accepted. Title and guard come from internal/watcher, so a task minted
// here and one minted on acceptance are the same thing.
//
// The guard runs before anything else: an open design task about the spec is
// returned as the answer, with existing true, so a second click on a stale
// page mints nothing (§4.2 rule 7). Only then does the route check what the
// button is offered on — an unplanned section — and refuse a spec that has
// none with a 409.
func (s *server) progressPlan(w http.ResponseWriter, r *http.Request) {
	var body model.ProgressPlanInput
	sub, project, ok := s.beginJSONPost(w, r, "plan", &body)
	if !ok {
		return
	}
	ctx := r.Context()

	doc, err := s.st.GetDoc(ctx, body.Doc)
	if err != nil {
		s.observeProgressWrite("plan", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	// A document of another project, or one that is not a spec, is not found
	// on this route: the planning task is about a spec of this project and
	// nothing else.
	if doc.Project != project.ID || doc.Kind != "spec" {
		s.observeProgressWrite("plan", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	open, err := s.st.OpenTaskForDoc(ctx, doc.ID, "design")
	if err != nil {
		s.observeProgressWrite("plan", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	if open != "" {
		s.observeProgressWrite("plan", "ok")
		writeJSON(w, http.StatusOK, model.ProgressPlanResponse{Task: open, Existing: true})
		return
	}

	unplanned, err := s.specHasUnplannedSection(ctx, project.ID, doc.ID)
	if err != nil {
		s.observeProgressWrite("plan", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	if !unplanned {
		s.observeProgressWrite("plan", "conflict")
		writeErr(w, http.StatusConflict, fmt.Sprintf("doc %d has no unplanned section", doc.ID))
		return
	}

	var minted string
	err = s.recordEvent(ctx, "web", "task.created",
		map[string]any{"doc": store.DocIRI(*doc), "kind": "design", "route": "progress/plan"},
		func(tx *sql.Tx, eventID int64) error {
			id, err := s.mintPlanningTask(tx, doc, sub.ActorID, eventID)
			if err != nil {
				return err
			}
			minted = id
			// The id is allocated inside this transaction, after the payload
			// was marshalled, so the event names its task from here (025 §15.2).
			return store.AttributeEventToTask(tx, eventID, id)
		})
	if err != nil {
		s.observeProgressWrite("plan", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	s.observeProgressWrite("plan", "ok")
	writeJSON(w, http.StatusOK, model.ProgressPlanResponse{Task: minted})
}

// progressSpec is one spec's row as the page derives it, or nil when the
// project's progress model holds no such spec. Every write that has to know
// what the page showed reads it through here rather than a query of its own,
// so a route can never refuse an act the page offered, or offer one the route
// refuses.
func (s *server) progressSpec(ctx context.Context, projectID string, doc int64) (*model.ProgressSpec, error) {
	in, err := s.st.ProjectProgress(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, g := range progress.Derive(in).Groups {
		for i, spec := range g.Specs {
			if spec.Doc == doc {
				return &g.Specs[i], nil
			}
		}
	}
	return nil, nil
}

// specHasUnplannedSection reports whether the spec still has a section no
// plan covers (§1.2) — §3.4's condition for offering the Plan button.
func (s *server) specHasUnplannedSection(ctx context.Context, projectID string, doc int64) (bool, error) {
	spec, err := s.progressSpec(ctx, projectID, doc)
	if err != nil || spec == nil {
		return false, err
	}
	for _, sec := range spec.Sections {
		if sec.State == "unplanned" {
			return true, nil
		}
	}
	return false, nil
}

// mintPlanningTask creates 025 §15.4's planning task about doc inside tx —
// §3.4's act. Title and body come from internal/watcher, so a task minted
// here, one minted by the Plan button, and one minted when the spec was
// accepted are all the same thing. The caller guards with OpenTaskForDoc
// first; this mints unconditionally.
func (s *server) mintPlanningTask(tx *sql.Tx, doc *model.Doc, actorID string, eventID int64) (string, error) {
	iri := store.DocIRI(*doc)
	t, err := store.CreateTask(tx, s.st.Now(), store.TaskInput{
		ProjectID: doc.Project,
		Title:     watcher.PlanningTitle(doc.Title),
		Body:      watcher.PlanningBody(iri, doc.Version, eventID),
		Kind:      "design",
		Priority:  "medium",
		AboutDoc:  doc.ID,
		CreatedBy: actorID,
	}, eventID)
	if err != nil {
		return "", err
	}
	return t.ID, nil
}

// --- the rally act (WL-SPEC-66 §3.5) -----------------------------------------

// progressRallyAdd handles POST /projects/{id}/progress/rally/add: put the
// work that drives one spec to completion into the project's draft rally,
// minting what does not exist yet. Membership is progress.RallyMembers, a
// pure rule over the same derivation the page rendered, so the button and the
// route cannot disagree about what "the rest of this spec" means.
//
// Everything the act does commits together under one rally.assembled event:
// the draft rally is created if the project has none, the planning task and
// the accept prompts are minted, and every member is added. Adding is
// set-like in the store, so a second add of the same spec reuses what the
// first minted and answers 200 with added 0 (§4.2 rule 7).
func (s *server) progressRallyAdd(w http.ResponseWriter, r *http.Request) {
	var body model.ProgressRallyAddInput
	sub, project, ok := s.beginJSONPost(w, r, "rally/add", &body)
	if !ok {
		return
	}
	ctx := r.Context()

	doc, err := s.st.GetDoc(ctx, body.Doc)
	if err != nil {
		s.observeProgressWrite("rally/add", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	// A rally is assembled from a spec of this project. Anything else is not
	// found here, whatever it is elsewhere.
	if doc.Project != project.ID || doc.Kind != "spec" {
		s.observeProgressWrite("rally/add", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	spec, err := s.progressSpec(ctx, project.ID, doc.ID)
	if err != nil {
		s.observeProgressWrite("rally/add", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	if spec == nil {
		s.observeProgressWrite("rally/add", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	// The one fact RallyMembers needs and the derivation does not carry: the
	// open prompt to accept each draft plan, which is a member in its own
	// right once it exists (§3.5 case 3).
	facts := map[string]progress.PlanFacts{}
	for _, p := range spec.Plans {
		if p.State != "draft" {
			continue
		}
		open, err := s.st.OpenTaskForDoc(ctx, p.Doc, "decision")
		if err != nil {
			s.observeProgressWrite("rally/add", progressWriteOutcome(err))
			s.mapStoreErr(w, err)
			return
		}
		facts[p.Ref] = progress.PlanFacts{DecisionTask: open}
	}
	members := progress.RallyMembers(*spec, facts)

	var rallyID string
	var added int
	err = s.recordEvent(ctx, "web", "rally.assembled",
		map[string]any{"doc": store.DocIRI(*doc), "route": "progress/rally/add"},
		func(tx *sql.Tx, eventID int64) error {
			now := s.st.Now()
			rally, err := store.EnsureDraftRally(tx, now, project.ID, sub.ActorID, eventID)
			if err != nil {
				return err
			}
			rallyID = rally.ID
			ids := slices.Clone(members.Execute)
			if members.NeedsPlanning {
				id, err := s.mintPlanningTask(tx, doc, sub.ActorID, eventID)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			for _, planDoc := range members.NeedsAccept {
				t, err := store.MintAcceptDecision(tx, now, planDoc, sub.ActorID, eventID)
				if err != nil {
					return err
				}
				ids = append(ids, t.ID)
			}
			added, err = store.AddRallyMembers(tx, now, rally.ID, ids, eventID)
			if err != nil {
				return err
			}
			// The rally's id is only known inside this transaction when the
			// add created it, so the event names its task from here.
			return store.AttributeEventToTask(tx, eventID, rally.ID)
		})
	if err != nil {
		s.observeProgressWrite("rally/add", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}

	band, err := s.st.DraftRallyBand(ctx, project.ID)
	if err != nil || band == nil {
		s.observeProgressWrite("rally/add", "error")
		s.mapStoreErr(w, err)
		return
	}
	s.observeProgressWrite("rally/add", "ok")
	writeJSON(w, http.StatusOK, model.ProgressRallyAddResponse{
		Rally: rallyID, Added: added, Members: band.Members, Specs: band.Specs,
	})
}

// progressRallyConfirm handles POST /projects/{id}/progress/rally/confirm:
// publish the draft rally, which is what 005 §2a calls activation. The
// backbone allows one active rally per project and migration 0069's partial
// unique index is what enforces it, so a project that already has one is
// refused there rather than by a check here — and the refusal names the rally
// holding the slot, which is the sentence the footer shows (§3.5).
func (s *server) progressRallyConfirm(w http.ResponseWriter, r *http.Request) {
	s.progressRallyMove(w, r, "rally/confirm", "ready")
}

// progressRallyDiscard handles POST /projects/{id}/progress/rally/discard:
// abandon the draft rally, which drops it from every reader and takes its
// edges out of the footer's count.
func (s *server) progressRallyDiscard(w http.ResponseWriter, r *http.Request) {
	s.progressRallyMove(w, r, "rally/discard", "abandoned")
}

// progressRallyMove is the body Confirm and Discard share: both move the one
// draft rally out of draft, and both are a 409 when the project has none —
// the footer is not drawn without one, so a request arriving here came from a
// stale page.
func (s *server) progressRallyMove(w http.ResponseWriter, r *http.Request, route, to string) {
	_, project, ok := s.beginJSONPost(w, r, route, nil)
	if !ok {
		return
	}
	ctx := r.Context()

	rally, err := s.st.DraftRally(ctx, project.ID)
	if errors.Is(err, store.ErrNotFound) {
		s.observeProgressWrite(route, "conflict")
		writeErr(w, http.StatusConflict, "this project has no draft rally")
		return
	}
	if err != nil {
		s.observeProgressWrite(route, progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}

	err = s.recordTaskEvent(ctx, "web", "task.transition", rally.ID,
		map[string]string{"from": "draft", "to": to, "route": "progress/" + route},
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, s.st.Now(), rally.ID, "draft", to, eventID)
		})
	switch {
	case err == nil:
		s.observeProgressWrite(route, "ok")
		writeJSON(w, http.StatusOK, model.ProgressRallyResponse{Rally: rally.ID, State: to})
	// The project already has an active rally (the unique index refused the
	// publish), or the draft moved between the read above and the write.
	// Both are the page being stale, which is a 409, not a fault.
	case errors.Is(err, store.ErrInvalidInput), errors.Is(err, store.ErrBadTransition):
		s.observeProgressWrite(route, "conflict")
		s.writeRallyConflict(ctx, w, project.ID, err)
	default:
		s.observeProgressWrite(route, progressWriteOutcome(err))
		s.mapStoreErr(w, err)
	}
}

// writeRallyConflict writes the 409 a refused Confirm gets. When the project
// has an active rally it is named, in the sentence §3.5 puts in the footer,
// so the page can say which rally holds the slot without a second read; with
// no active rally the store's own reason stands.
func (s *server) writeRallyConflict(ctx context.Context, w http.ResponseWriter, projectID string, cause error) {
	active, err := s.st.ActiveRally(ctx, projectID)
	if err != nil {
		writeErr(w, http.StatusConflict, cause.Error())
		return
	}
	writeJSON(w, http.StatusConflict, model.ProgressRallyConflict{
		Error:  active.ID + " is already active; finish or abandon it first",
		Active: active.ID,
	})
}

// --- the live stream (WL-SPEC-66 §5.1) --------------------------------------

// progressEvents handles GET /projects/{id}/progress/events: the project's
// slice of the event log, followed live, so the Progress page redraws on
// what changed rather than polling the whole derivation. Same poll loop as
// streamEvents (cursor, heartbeats, Last-Event-ID, flush, context
// cancellation) — see that handler's comment for why it is a poller and not
// a listener. Where this route differs: every polled event is resolved
// through progress.Resolve and looked up in one batched store.ProgressRefs
// call, and only the refs scoped to this route's project become frames — a
// project with nothing this page shows still advances its cursor and sends
// nothing.
//
// permWebRead, not permEventStream: unlike the admin-only log follow, this
// route is open to every cockpit reader, which §4.5 allows because a frame
// carries nothing about a task, plan or spec that reader could not already
// read off the page itself.
func (s *server) progressEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.st.GetProject(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	q := r.URL.Query()
	cursor := int64(-1) // -1: no cursor given, start at the head
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid after: must be a non-negative integer event id")
			return
		}
		cursor = n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid Last-Event-ID: must be a non-negative integer event id")
			return
		}
		cursor = n
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		for _, k := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering"} {
			h.Del(k)
		}
		s.log.Error("progress event stream: response writer cannot flush", "err", err)
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	s.observeProgressStreamOpen()
	defer s.observeProgressStreamClose()

	ctx, endStream := context.WithCancel(ctx)
	defer endStream()
	defer context.AfterFunc(s.bgCtx, endStream)()

	if cursor < 0 {
		var err error
		if cursor, err = s.streamHead(ctx, ""); err != nil {
			s.streamEnd(ctx, "find progress stream head", err)
			return
		}
	}

	ticker := time.NewTicker(streamPoll())
	defer ticker.Stop()
	lastWrite := time.Now()

	for {
		events, err := s.st.ListEvents(ctx, store.EventFilter{After: cursor, Limit: streamPageSize})
		if err != nil {
			s.streamEnd(ctx, "list progress events", err)
			return
		}
		var frames []progressFrame
		if len(events) > 0 {
			frames, err = s.progressFrames(ctx, project.ID, events)
			if err != nil {
				s.streamEnd(ctx, "resolve progress refs", err)
				return
			}
		}
		switch {
		case len(frames) > 0:
			for _, f := range frames {
				if err := writeProgressFrame(w, f); err != nil {
					if errors.Is(err, errEncodeEvent) {
						s.log.Error("progress event stream: encoding a frame failed", "event", f.id, "err", err)
					}
					return
				}
			}
			if err := rc.Flush(); err != nil {
				return
			}
			s.observeProgressStreamFrames(len(frames))
			lastWrite = time.Now()
		case time.Since(lastWrite) >= streamHeartbeat():
			// An SSE comment, same as streamEvents': ignored by every client,
			// but it keeps proxies and idle timeouts from dropping a stream
			// that is live but has nothing to say.
			if _, err := w.Write([]byte(":\n\n")); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
			lastWrite = time.Now()
		}
		for _, e := range events {
			cursor = e.ID
		}

		if len(events) == streamPageSize {
			continue // behind: keep draining without waiting a tick
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// progressFrame pairs one derived model.ProgressEventFrame with the id of
// the backbone event that produced it — the id: line, and what an encoding
// failure is attributed to.
type progressFrame struct {
	id    int64
	frame model.ProgressEventFrame
}

// progressOrigin is the one fact about an event that progress.Resolve throws
// away and a frame still needs: which event, and when, produced the touch a
// ProgressRef came back for.
type progressOrigin struct {
	eventID int64
	typ     string
	at      time.Time
}

// progressFrames resolves one poll's events into the frames this project's
// stream sends. Every event is reduced to a touch (progress.Resolve), every
// touch's task or document id is batched into one store.ProgressRefs call —
// never one call per event — and a ref outside projectID is dropped (§5.1).
//
// A touch with a kind but no id — a deploy event, whose Touch cannot carry
// the whole set of tasks it transitioned — has nothing to look up and
// contributes no frame; the same is true of an event whose family this page
// does not model at all (progress.Resolve's zero Touch).
func (s *server) progressFrames(ctx context.Context, projectID string, events []store.Event) ([]progressFrame, error) {
	var taskIDs []string
	var docIDs []int64
	taskOrigin := map[string]progressOrigin{}
	docOrigin := map[int64]progressOrigin{}
	for _, e := range events {
		t := progress.Resolve(e.Type, e.Payload)
		o := progressOrigin{eventID: e.ID, typ: e.Type, at: e.ReceivedAt}
		switch {
		case t.Task != "":
			if _, seen := taskOrigin[t.Task]; !seen {
				taskIDs = append(taskIDs, t.Task)
			}
			taskOrigin[t.Task] = o
		case t.Doc != 0:
			if _, seen := docOrigin[t.Doc]; !seen {
				docIDs = append(docIDs, t.Doc)
			}
			docOrigin[t.Doc] = o
		}
	}
	if len(taskIDs) == 0 && len(docIDs) == 0 {
		return nil, nil
	}

	refs, err := s.st.ProgressRefs(ctx, taskIDs, docIDs)
	if err != nil {
		return nil, err
	}

	var out []progressFrame
	for _, ref := range refs {
		if ref.Project != projectID {
			continue
		}
		// The id ProgressRefs was asked about is a task id for a task ref;
		// for a doc ref it is the plan's id, or — a bare spec, which
		// resolves to itself — the sole entry of Specs.
		var o progressOrigin
		switch {
		case ref.Task != "":
			o = taskOrigin[ref.Task]
		case ref.Plan != 0:
			o = docOrigin[ref.Plan]
		case len(ref.Specs) == 1:
			o = docOrigin[ref.Specs[0]]
		}
		out = append(out, progressFrame{
			id: o.eventID,
			frame: model.ProgressEventFrame{
				Event: o.typ, Task: ref.Task, State: ref.State,
				Plan: ref.Plan, Specs: ref.Specs, Rally: ref.Rally, At: o.at,
			},
		})
	}
	return out, nil
}

// writeProgressFrame writes one SSE message for a resolved touch, mirroring
// writeEventFrame. event: is always the constant "progress" here — unlike
// the raw log follow, nothing about this frame's shape is attacker-supplied,
// so there is no line-break to strip.
func writeProgressFrame(w io.Writer, f progressFrame) error {
	data, err := json.Marshal(f.frame)
	if err != nil {
		return fmt.Errorf("%w %d: %w", errEncodeEvent, f.id, err)
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: progress\ndata: %s\n\n", f.id, data)
	return err
}
