// docs.go serves spec 025's design documents — specs, ADRs and plans — over
// the JSON API. Every handler is the same shape as createTask: parse and
// validate, then wrap the store's writer in RecordDocEvent so the mutation,
// its state_log row and its event land in one transaction, then answer with
// the row the store read back.
//
// The lifecycle rules themselves live in internal/store/docs.go — the accept
// gate, the anchor diff, what a plan may and may not do. Nothing here
// re-decides them; the handlers' job is to name the caller, name the event,
// and turn a store sentinel into a status code (see mapStoreErr).
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// validDocKinds mirrors the docs.kind CHECK constraint (migration 0027) and
// the wl:Spec/wl:ADR/wl:Plan classes in ns/ontology.ttl. The store re-checks;
// this is here so a typo is a named 422 rather than a generic one.
var validDocKinds = map[string]bool{"spec": true, "adr": true, "plan": true}

// invalidDocKindMsg is what createDoc — today the only handler that gates on
// validDocKinds — answers with. It is a constant so a second write path names
// the kinds the same way this one does.
const invalidDocKindMsg = "invalid kind: must be spec, adr, or plan"

// docSource is the events.source every /api/v1 document mutation is recorded
// under. The CHECK on events.source admits no "doc" value and should not: the
// column says which surface a fact arrived through, and this one is the API
// client, exactly like a task created by the CLI.
const docSource = "cli"

// docID reads the {id} path value as a document id. A non-numeric id is
// answered 400 rather than 404: the path names no document that could ever
// have existed, and saying "not found" would read like one that was deleted.
func docID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "doc id must be a positive integer")
		return 0, false
	}
	return id, true
}

// createDoc handles POST /api/v1/docs.
func (s *server) createDoc(w http.ResponseWriter, r *http.Request) {
	var req model.CreateDocInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	req.Project = strings.TrimSpace(req.Project)
	req.Slug = strings.TrimSpace(req.Slug)
	req.Assignee = strings.TrimSpace(req.Assignee)
	if req.Status != "" {
		writeErr(w, http.StatusUnprocessableEntity,
			"status is import-only: a document is created as a draft and accepted "+
				"with POST /api/v1/docs/{id}/accept")
		return
	}
	if !validDocKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, invalidDocKindMsg)
		return
	}
	if req.Slug == "" {
		writeErr(w, http.StatusUnprocessableEntity, "slug is required")
		return
	}
	// Named 404 ahead of the transaction, as createTask does: CreateDoc's own
	// foreign key would otherwise surface as an anonymous failure.
	if _, err := s.st.GetProject(r.Context(), req.Project); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actor := actorFrom(r)
	// Same {doc, actor, request} shape recordDocEvent emits, built inline
	// because the id does not exist until CreateDoc runs: doc is 0 for a
	// create, and the created row is the event's own consequence.
	payload, err := json.Marshal(map[string]any{
		"doc":     0,
		"actor":   actor.ID,
		"request": req,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	now := s.st.Now()

	var created *model.Doc
	_, _, err = s.st.RecordDocEvent(r.Context(), "create", docSource, extID, "doc.created", payload,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.CreateDoc(tx, now, store.DocInput{
				Project:   req.Project,
				Kind:      req.Kind,
				Number:    req.Number,
				Slug:      req.Slug,
				Body:      req.Body,
				Assignee:  req.Assignee,
				CreatedBy: actor.ID,
			}, eventID)
			if err != nil {
				return err
			}
			created = d
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// listDocs handles GET /api/v1/docs?project=&kind=&status=.
func (s *server) listDocs(w http.ResponseWriter, r *http.Request) {
	docs, err := s.st.ListDocs(r.Context(), docFilterFrom(r))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.DocListResponse{Docs: withoutDocBodies(docs)})
}

// withoutDocBodies blanks the markdown source on a list projection. A corpus
// is tens of documents of tens of kilobytes each, and no list consumer reads
// the text — the one endpoint that serves a body is GET /api/v1/docs/{id},
// which serves one. Without this, the route most likely to be polled is also
// the largest response the server sends.
func withoutDocBodies(docs []model.Doc) []model.Doc {
	out := make([]model.Doc, len(docs))
	for i, d := range docs {
		d.Body = ""
		out[i] = d
	}
	return out
}

// docFilterFrom reads the three list selectors off the query string. An
// unknown value filters to nothing rather than erroring — the same way the
// task list treats a state nobody uses.
func docFilterFrom(r *http.Request) store.DocFilter {
	q := r.URL.Query()
	return store.DocFilter{
		Project: q.Get("project"),
		Kind:    q.Get("kind"),
		Status:  q.Get("status"),
	}
}

// getDoc handles GET /api/v1/docs/{id}: the document with the rows derived
// from its body — sections, edges both ways, and the open candidate revision
// if one exists.
func (s *server) getDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	detail, err := s.docDetail(r, id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// docDetail assembles GET /api/v1/docs/{id}'s projection, shared with the
// cockpit's document page so the two cannot drift.
func (s *server) docDetail(r *http.Request, id int64) (*model.DocDetail, error) {
	ctx := r.Context()
	d, err := s.st.GetDoc(ctx, id)
	if err != nil {
		return nil, err
	}
	sections, err := s.st.ListDocSections(ctx, id)
	if err != nil {
		return nil, err
	}
	out, in, err := s.st.ListDocEdges(ctx, id)
	if err != nil {
		return nil, err
	}
	detail := &model.DocDetail{Doc: *d, Sections: sections, Edges: out, EdgesIn: in}
	// No open revision is the ordinary case, not a failure: only an accepted
	// spec or ADR ever has one.
	rev, err := s.st.GetDocRevision(ctx, id)
	if err == nil {
		detail.Revision = rev
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return detail, nil
}

// updateDocBody handles PUT /api/v1/docs/{id}/body.
func (s *server) updateDocBody(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.UpdateDocBodyInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	now := s.st.Now()
	var updated *model.Doc
	err := s.recordDocEvent(w, r, "update", "doc.updated", id, req,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.UpdateDocBody(tx, now, id, req.Body, eventID)
			if err != nil {
				return err
			}
			updated = d
			return nil
		})
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// acceptDoc handles POST /api/v1/docs/{id}/accept: the manual commit of
// 025 §7, gated on the document's assignee. On a plan this also mints its
// execution tasks (025 §9.2); the response shape is unchanged for now
// (part 3 of the plan surfaces the minted set), but the mint count is
// recorded once the transaction commits.
func (s *server) acceptDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	actor := actorFrom(r)
	now := s.st.Now()
	var accepted *model.Doc
	var minted []model.Task
	err := s.recordDocEvent(w, r, "accept", "doc.accepted", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			d, tasks, err := store.AcceptDoc(tx, now, id, actor.ID, eventID)
			if err != nil {
				return err
			}
			accepted, minted = d, tasks
			return nil
		})
	if err != nil {
		return
	}
	s.st.RecordPlanTasksMinted(len(minted))
	writeJSON(w, http.StatusOK, accepted)
}

// reviseDoc handles POST /api/v1/docs/{id}/revise: opens the one candidate
// revision an accepted spec or ADR may carry (025 §7.2), and answers with it.
func (s *server) reviseDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	actor := actorFrom(r)
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "revise", "doc.revised", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.ReviseDoc(tx, now, id, actor.ID, eventID)
		})
	if err != nil {
		return
	}
	s.writeDocRevision(w, r, id)
}

// updateDocRevision handles PUT /api/v1/docs/{id}/revision: replaces the open
// candidate's body, which is parsed and linted here so a malformed candidate
// is refused at the edit rather than at the accept gate.
func (s *server) updateDocRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.UpdateDocBodyInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "update", "doc.revision_updated", id, req,
		func(tx *sql.Tx, eventID int64) error {
			return store.UpdateRevision(tx, now, id, req.Body, eventID)
		})
	if err != nil {
		return
	}
	s.writeDocRevision(w, r, id)
}

// acceptDocRevision handles POST /api/v1/docs/{id}/revision/accept: runs the
// 025 §6 anchor gate and, when clean, lands the candidate as the next version.
func (s *server) acceptDocRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	actor := actorFrom(r)
	now := s.st.Now()
	var landed *model.Doc
	err := s.recordDocEvent(w, r, "accept", "doc.revision_accepted", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.AcceptRevision(tx, now, id, actor.ID, eventID)
			if err != nil {
				return err
			}
			landed = d
			return nil
		})
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, landed)
}

// recordDocEvent is the shared body of every document mutation: a random
// external id, the request as the event payload (nil for the verbs that carry
// no body), and apply inside RecordDocEvent so the write, its state_log row
// and its event commit together. It writes the error response itself and
// returns the error, so a handler's failure path is one `if err != nil`.
func (s *server) recordDocEvent(
	w http.ResponseWriter, r *http.Request,
	op, eventType string, id int64, req any,
	apply func(tx *sql.Tx, eventID int64) error,
) error {
	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return err
	}
	// The payload records who asked for what against which document. The
	// actor matters most here: events carries no actor column, and acceptance
	// is an assignee-gated deliberate act (025 §7), so this is the only place
	// the log says who performed it. A wrapper rather than the request alone,
	// because the five bodyless verbs would otherwise record a bare null and
	// lose the subject. It is an event row and not an HTTP body, so no
	// internal/model declaration is owed (ADR 036 §3).
	payload, err := json.Marshal(map[string]any{
		"doc":     id,
		"actor":   actorFrom(r).ID,
		"request": req,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return err
	}
	if _, _, err := s.st.RecordDocEvent(r.Context(), op, docSource, extID, eventType, payload, apply); err != nil {
		s.mapStoreErr(w, err)
		return err
	}
	return nil
}

// writeDocRevision answers with a document's open candidate revision, read
// back after the transaction that opened or edited it.
func (s *server) writeDocRevision(w http.ResponseWriter, r *http.Request, id int64) {
	rev, err := s.st.GetDocRevision(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}
