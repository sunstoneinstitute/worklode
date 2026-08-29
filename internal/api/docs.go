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
//
// Two verbs are the exception to the RecordDocEvent shape above: submit and
// accept emit 025 §15.3's typed JSON-LD events (wl:DocumentSubmitted,
// wl:DocumentAccepted) through eventbus.Emit, because those are the two the
// doc-lifecycle subscriber consumes (§15.4). Every other verb still writes a
// dotted doc.* event; retyping the rest is a separate decision, not made here.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// validDocKinds mirrors the docs.kind CHECK constraint (migration 0027) and
// the wl:Spec/wl:ADR/wl:Plan classes in ns/ontology.ttl. The store re-checks;
// this is here so a typo is a named 422 rather than a generic one.
var validDocKinds = map[string]bool{"spec": true, "adr": true, "plan": true}

// validDocStatuses mirrors the docs.status CHECK constraint, derived from
// wlc:DesignDocStatus in ns/concept.ttl (025 §17). Only the corpus importer
// may state a status (see createDoc); the store re-checks.
var validDocStatuses = ns.Set(ns.DesignDocStatuses)

// invalidDocKindMsg is what createDoc — today the only handler that gates on
// validDocKinds — answers with. It is a constant so a second write path names
// the kinds the same way this one does.
const invalidDocKindMsg = "invalid kind: must be spec, adr, or plan"

// invalidDocStatusMsg names the statuses a corpus import may assert.
var invalidDocStatusMsg = "invalid status: must be " + ns.OrList(ns.DesignDocStatuses)

// importOnlyStatusMsg is the refusal every caller without doc.import gets for
// a non-empty status. The field is declared on the wire so the refusal can
// name it rather than silently dropping it.
const importOnlyStatusMsg = "status is import-only: a document is created as a draft and accepted " +
	"with POST /api/v1/docs/{id}/accept"

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
	req.Owner = strings.TrimSpace(req.Owner)
	req.Status = strings.TrimSpace(req.Status)
	req.GeneratedByTask = strings.TrimSpace(req.GeneratedByTask)
	// A stated status bypasses the accept gate, so it needs the importer's
	// authority. Without it the field stays refused exactly as before.
	if req.Status != "" {
		if d := Decide(Request{Subject: subjectFrom(r), Permission: permDocImport}); !d.Allowed {
			writeErr(w, http.StatusUnprocessableEntity, importOnlyStatusMsg)
			return
		}
		if !validDocStatuses[req.Status] {
			writeErr(w, http.StatusUnprocessableEntity, invalidDocStatusMsg)
			return
		}
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

	actorID := actorIDFrom(r)
	now := s.st.Now()

	// Document id 0 in the payload: the row does not exist until CreateDoc
	// runs, and it is the event's own consequence.
	var created *model.Doc
	err := s.recordDocEvent(w, r, "create", "doc.created", 0, req,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.CreateDoc(tx, now, store.DocInput{
				Project:   req.Project,
				Kind:      req.Kind,
				Number:    req.Number,
				Slug:      req.Slug,
				Body:      req.Body,
				Owner:     req.Owner,
				CreatedBy: actorID,
				// The authoring task (025 §12). Left empty by every caller
				// bound to no task, which is a document with no authoring
				// task — a normal state, not a refusal. See migration 0044.
				GeneratedByTask: req.GeneratedByTask,
				Status:          req.Status,
			}, eventID)
			if err != nil {
				return err
			}
			created = d
			return nil
		})
	if err != nil {
		return
	}
	writeJSON(w, http.StatusCreated, s.withProjectKey(r.Context(), *created))
}

// listDocs handles GET /api/v1/docs?project=&kind=&status=&owner=&deleted=
// plus the three derived selectors: ?needs_planning= and ?needs_execution=
// (026 §2.1), and ?bare_superseded= (026 §2.4, 025 §6 rule 2). deleted=true
// switches the list from live documents to tombstoned ones (044 §5).
// projectKeyByID reads the project id -> key map that a document's formatted
// id needs (model.Doc.ProjectKey): the shorthand is built from the key, and a
// document carries only its project id.
//
// Read per request, like the cockpit's sibling projectKeys and for the same
// reason: it is one indexed SELECT over a table with a row per project, and
// reading it live is what makes a new project's documents render their ref on
// the next call instead of after a restart.
//
// A failed read degrades to the empty map rather than failing the request.
// DocRef falls back to the unqualified "SPEC-29", so the caller loses the
// corpus qualifier and nothing else.
func (s *server) projectKeyByID(ctx context.Context) map[string]string {
	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		s.log.Warn("rendering documents without a project key: projects unreadable", "err", err)
		return nil
	}
	keys := make(map[string]string, len(projects))
	for _, p := range projects {
		keys[p.ID] = p.Key
	}
	return keys
}

// withProjectKeys stamps each document's ProjectKey, the half of its formatted
// id that lives on the project rather than the document. Every handler whose
// response a client renders as a document ref runs its docs through this.
func (s *server) withProjectKeys(ctx context.Context, docs []model.Doc) []model.Doc {
	keys := s.projectKeyByID(ctx)
	if len(keys) == 0 {
		return docs
	}
	for i := range docs {
		docs[i].ProjectKey = keys[docs[i].Project]
	}
	return docs
}

// withProjectKey is withProjectKeys for a single document.
func (s *server) withProjectKey(ctx context.Context, d model.Doc) model.Doc {
	return s.withProjectKeys(ctx, []model.Doc{d})[0]
}

func (s *server) listDocs(w http.ResponseWriter, r *http.Request) {
	sel, err := docSelectorFrom(r)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	switch {
	case sel.needsPlanning:
		docs, gaps, err := s.st.NeedsPlanning(r.Context(), sel.filter.Project)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.DocListResponse{
			Docs: s.withProjectKeys(r.Context(), withoutDocBodies(docs)), PlanningGaps: gaps,
		})
	case sel.needsExecution:
		docs, err := s.st.NeedsExecution(r.Context(), sel.filter.Project)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.DocListResponse{Docs: s.withProjectKeys(r.Context(), withoutDocBodies(docs))})
	case sel.bareSuperseded:
		docs, gaps, err := s.st.BareSupersededSections(r.Context(), sel.filter.Project, sel.filter.Kind)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.DocListResponse{
			Docs: s.withProjectKeys(r.Context(), withoutDocBodies(docs)), SupersessionGaps: gaps,
		})
	default:
		docs, err := s.st.ListDocs(r.Context(), sel.filter)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.DocListResponse{Docs: s.withProjectKeys(r.Context(), withoutDocBodies(docs))})
	}
}

// resolveDocRef handles GET /api/v1/docs/resolve?ref=<ref>: the one document
// a reference names (025 §14.3). Every `lode doc <verb>` takes a ref, and
// resolving it here rather than by listing the corpus client-side keeps the
// ambiguity and tombstone-fallback rules beside the data — the same reason
// GET /api/v1/projects/resolve normalizes a remote URL server-side, and what
// lets the ref grammar grow without a client upgrade.
//
// Two tiers (WL-358). The store's id/exact-slug lookup runs first — it alone
// reaches tombstoned documents, which `lode doc undelete <slug>` needs. A
// miss then goes through the full 026 §3 grammar `lode show` and the /docs/ref/
// redirect already resolve (designdoc.ResolveRef via resolveDocRefWeb), so
// the <KEY>-<TYPE>-<n> shorthand, a corpus path, and the number forms name a
// document on every doc surface, not just some.
//
// The body is blanked as it is on a list: the caller wants an id, and follows
// with GET /api/v1/docs/{id} when it wants the text.
//
// No dedicated metric. Every outcome this route derives is already its own
// status code on http_requests_total's {route, code}: 200 resolved, 404 no
// such document, 422 an ambiguous ref.
func (s *server) resolveDocRef(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		writeErr(w, http.StatusUnprocessableEntity, "ref is required")
		return
	}
	d, err := s.st.ResolveDocRef(r.Context(), ref)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if gd, gerr := s.resolveDocRefWeb(r.Context(), ref); gerr == nil {
				gd.Body = ""
				writeJSON(w, http.StatusOK, gd)
				return
			} else if amb := (*designdoc.AmbiguousRefError)(nil); errors.As(gerr, &amb) {
				writeErr(w, http.StatusUnprocessableEntity, gerr.Error())
				return
			}
			// Any other grammar miss keeps the store's own not-found below.
		}
		s.mapStoreErr(w, err)
		return
	}
	d.Body = ""
	writeJSON(w, http.StatusOK, d)
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

// docFilterFrom reads the four plain list filters off the query string. An
// unknown value filters to nothing rather than erroring — the same way the
// task list treats a state nobody uses. The cockpit's /docs page calls it
// directly; the JSON API goes through docSelectorFrom, which adds 026 §2's
// derived selectors on top.
func docFilterFrom(r *http.Request) store.DocFilter {
	q := r.URL.Query()
	return store.DocFilter{
		Project: q.Get("project"),
		Kind:    q.Get("kind"),
		Status:  q.Get("status"),
		Owner:   q.Get("owner"),
	}
}

// docListSelector is GET /api/v1/docs' query string once validated: the four
// plain filters, plus at most one of the three derived selectors of 026 §2.
type docListSelector struct {
	filter         store.DocFilter
	needsPlanning  bool
	needsExecution bool
	bareSuperseded bool
}

// docDerivedSelector names one derived selector's implied status and
// acceptable kinds, so docSelectorFrom can check all three the same way
// instead of repeating the kind/status logic per selector. kindOK reports
// whether a restated --kind is compatible with the selector rather than
// contradicting it; kindWant names the acceptable kind(s) for the message.
type docDerivedSelector struct {
	on            bool
	name          string
	impliedStatus string
	cite          string
	kindOK        func(kind string) bool
	kindWant      string
}

// docSelectorFrom reads the list selectors off the query string.
//
// The four plain filters take any value — an unknown one filters to nothing,
// the same way the task list treats a state nobody uses. The three derived
// selectors do not: each implies a status, and needs_planning/needs_execution
// each imply a single kind while bare_superseded implies one of two (026
// §2.1, §2.4; 025 §6 rule 2) — so a contradicting kind or status is an error
// rather than an empty result, which would read as "nothing to plan".
// Requesting more than one derived selector at once is an error for the same
// reason: needs_planning and needs_execution select disjoint kinds, and
// bare_superseded selects a disjoint status, so any conjunction is always
// empty.
//
// The CLI refuses the same combinations locally so the error needs no round
// trip; this is the authority, for the clients that are not the CLI.
func docSelectorFrom(r *http.Request) (docListSelector, error) {
	q := r.URL.Query()
	sel := docListSelector{filter: docFilterFrom(r)}
	var err error
	if sel.needsPlanning, err = queryBool(q, "needs_planning"); err != nil {
		return docListSelector{}, err
	}
	if sel.needsExecution, err = queryBool(q, "needs_execution"); err != nil {
		return docListSelector{}, err
	}
	if sel.bareSuperseded, err = queryBool(q, "bare_superseded"); err != nil {
		return docListSelector{}, err
	}
	// A switch, not an addition (044 §5): deleted=true lists the tombstoned
	// documents instead of the live ones. Read here rather than in
	// docFilterFrom, which the cockpit's read-only /docs page also calls and
	// which has no tombstone surface.
	if sel.filter.Deleted, err = queryBool(q, "deleted"); err != nil {
		return docListSelector{}, err
	}

	derived := []docDerivedSelector{
		{sel.needsPlanning, "needs_planning", "accepted", "026 §2.1",
			func(k string) bool { return k == "spec" }, "spec"},
		{sel.needsExecution, "needs_execution", "accepted", "026 §2.1",
			func(k string) bool { return k == "plan" }, "plan"},
		{sel.bareSuperseded, "bare_superseded", "superseded", "025 §6",
			func(k string) bool { return k == "spec" || k == "adr" }, "spec or adr"},
	}
	var on []string
	for _, c := range derived {
		if c.on {
			on = append(on, c.name)
		}
	}
	if len(on) > 1 {
		if len(on) == 2 && on[0] == "needs_planning" && on[1] == "needs_execution" {
			return docListSelector{}, errors.New(
				"needs_planning and needs_execution select disjoint kinds; pass one (026 §2.1)")
		}
		return docListSelector{}, fmt.Errorf(
			"%s are mutually exclusive selectors; pass one (025 §6, 026 §2)", strings.Join(on, " and "))
	}
	for _, c := range derived {
		if !c.on {
			continue
		}
		if sel.filter.Kind != "" && !c.kindOK(sel.filter.Kind) {
			return docListSelector{}, fmt.Errorf(
				"%s implies kind=%s; drop kind or pass %s (%s)", c.name, c.kindWant, c.kindWant, c.cite)
		}
		if sel.filter.Status != "" && sel.filter.Status != c.impliedStatus {
			return docListSelector{}, fmt.Errorf(
				"%s implies status=%s; drop status or pass %s (%s)", c.name, c.impliedStatus, c.impliedStatus, c.cite)
		}
	}
	return sel, nil
}

// queryBool reads a boolean query parameter. Absent is false; present with an
// empty value ("?needs_planning") is true; anything ParseBool refuses is named
// rather than silently read as off.
func queryBool(q url.Values, name string) (bool, error) {
	if !q.Has(name) {
		return false, nil
	}
	raw := q.Get(name)
	if raw == "" {
		return true, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q", name, raw)
	}
	return v, nil
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
	detail := &model.DocDetail{Doc: s.withProjectKey(ctx, *d), Sections: sections, Edges: out, EdgesIn: in}
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

// listDocVersions handles GET /api/v1/docs/{id}/versions.
func (s *server) listDocVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	versions, err := s.st.ListDocVersions(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	// ListDocVersions never errors for an unknown doc: its query is a UNION
	// of the live docs row and its archived versions, so a document that
	// exists always has at least one row (its current version). An empty
	// result is the same "no such doc" getDoc answers with a 404 for.
	if len(versions) == 0 {
		s.mapStoreErr(w, fmt.Errorf("doc %d: %w", id, store.ErrNotFound))
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// getDocVersion handles GET /api/v1/docs/{id}/versions/{n}.
func (s *server) getDocVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || version <= 0 || version > math.MaxInt32 {
		writeErr(w, http.StatusBadRequest, "version must be a positive integer")
		return
	}
	v, err := s.st.GetDocVersion(r.Context(), id, version)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
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
	writeJSON(w, http.StatusOK, s.withProjectKey(r.Context(), *updated))
}

// replaceDocEdges handles PUT /api/v1/docs/{id}/edges. It re-resolves the
// document's frontmatter references against the documents that exist now,
// turning to_external placeholders into real to_doc edges. CreateDoc re-points
// references as their targets arrive, so this is a repair path — an import that
// died between passes, say — not the mechanism import depends on.
//
// It carries no request body: the document's own stored body is the source,
// and nothing else about the document changes. The response is the same
// DocDetail GET serves, so the caller reads back the edge set it asked for.
func (s *server) replaceDocEdges(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "edges", "doc.edges_rebuilt", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.ReplaceDocEdges(tx, now, id, eventID)
		})
	if err != nil {
		return
	}
	detail, err := s.docDetail(r, id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// acceptDoc handles POST /api/v1/docs/{id}/accept: the manual commit of
// 025 §7, gated on the document's owner. On a plan this also mints its
// execution tasks (025 §9.2) in the same transaction; the response carries
// the doc and, for a plan, the minted set (model.AcceptDocResponse) — empty
// and omitted for a spec or ADR, so their response stays byte-identical.
//
// The event is 025 §15.3's typed wl:DocumentAccepted, whose external id is
// derived from the document's IRI and version, so a retried request records
// one event rather than two.
func (s *server) acceptDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	// Read the document before the transaction because that external id needs
	// its IRI and version before the insert. The pre-read decides nothing:
	// store.AcceptDoc re-locks the row FOR UPDATE and re-checks the owner
	// and the draft-only rule inside the transaction, so a document that
	// changed in between is still refused there, not here.
	doc, err := s.st.GetDoc(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	var accepted *model.Doc
	var minted []model.Task
	// From is the status the document is actually leaving, not a constant: a
	// plan re-accepted while accepted leaves "accepted" (025 §9.2), and an
	// event saying otherwise would put a transition that did not happen in the
	// append-only log.
	ev := eventbus.DocumentAccepted{
		Doc: store.DocIRI(*doc), Actor: actorID, At: now,
		Version: doc.Version, From: "wlc:" + doc.Status, To: "wlc:accepted",
	}
	_, inserted, err := eventbus.Emit(r.Context(), s.st, docSource, ev,
		func(tx *sql.Tx, eventID int64) error {
			d, tasks, err := store.AcceptDoc(tx, now, id, actorID, eventID)
			if err != nil {
				return err
			}
			accepted, minted = d, tasks
			return nil
		})
	// Counted before the error branch, exactly as RecordDocEvent counts it:
	// a refused accept is an outcome of the accept op, not an absence of one.
	s.st.RecordDocOp("accept", err)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if !inserted {
		// The (source, external_id) conflict means this document at this
		// version was already accepted, so Emit skipped apply and AcceptDoc's
		// gates never ran — accepted is nil. Answering 200 here would report
		// an accept that did not happen, to an actor the owner gate might
		// not even admit, so the refusal AcceptDoc would have raised is raised
		// here instead. Typing the event must not quietly change what the
		// endpoint answers.
		settled, err := s.st.CheckDocAcceptable(r.Context(), id, actorID)
		if settled {
			// An accepted plan re-accepted at a version already accepted:
			// every declaration in this body has a row, so the accept has
			// nothing left to do (025 §9.2). Answering with the document and
			// an empty minted set says exactly that. A plan edited since
			// carries a new version, so this is not the path it takes. The op
			// is already counted as a success above; counting it again here
			// would make one request two.
			writeJSON(w, http.StatusOK, model.AcceptDocResponse{Doc: s.withProjectKey(r.Context(), *doc)})
			return
		}
		if err == nil {
			// Unreachable by construction: a failed accept rolls its event
			// back with it, so an event at this version implies the document
			// left draft. Named rather than ignored — silently returning the
			// pre-read row would hide a broken invariant behind a 200.
			err = fmt.Errorf("internal: doc %d accepted at version %d has no event effect but is still draft",
				id, doc.Version)
		}
		s.st.RecordDocOp("accept", err)
		s.mapStoreErr(w, err)
		return
	}
	s.st.RecordPlanTasksMinted(len(minted))
	writeJSON(w, http.StatusOK, model.AcceptDocResponse{Doc: s.withProjectKey(r.Context(), *accepted), Tasks: minted})
}

// submitDoc handles POST /api/v1/docs/{id}/submit: the document enters review.
// Submission is an event, not a status (025 §15.4) — no document column moves
// — so this emits wl:DocumentSubmitted with no apply at all and answers with
// the document unchanged.
//
// A second submit of the same version is a 200 that inserts nothing: the
// deterministic external id collapses it at the log, before any guard could
// run. What the submission means is the doc-lifecycle watcher's to decide.
func (s *server) submitDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	d, err := s.st.GetDoc(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	ev := eventbus.DocumentSubmitted{
		Doc: store.DocIRI(*d), Actor: actorIDFrom(r), At: s.st.Now(), Version: d.Version,
	}
	_, _, err = eventbus.Emit(r.Context(), s.st, docSource, ev, nil)
	s.st.RecordDocOp("submit", err)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withProjectKey(r.Context(), *d))
}

// reviseDoc handles POST /api/v1/docs/{id}/revise: opens the one candidate
// revision an accepted spec or ADR may carry (025 §7.2), and answers with it.
func (s *server) reviseDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "revise", "doc.revised", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.ReviseDoc(tx, now, id, actorID, eventID)
		})
	if err != nil {
		return
	}
	s.writeDocRevision(w, r, id)
}

// transferDocOwner handles POST /api/v1/docs/{id}/owner: hands the document
// to another actor (025 §7.3). The current owner or an admin may transfer;
// transferring to the actor that already owns it is a no-op that still
// answers 200, since Task 5's bulk form is a client-side loop over many
// documents and relies on re-running being safe.
func (s *server) transferDocOwner(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.TransferDocOwnerInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	var doc *model.Doc
	err := s.recordDocEvent(w, r, "transfer", "doc.owner_changed", id, req,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.TransferDocOwner(tx, now, id, req.Owner, actorID, eventID)
			if err != nil {
				return err
			}
			doc = d
			return nil
		})
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.withProjectKey(r.Context(), *doc))
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

// discardDocRevision handles DELETE /api/v1/docs/{id}/revision: withdraws the
// open candidate without landing it (025 §7.2's close-without-merging), which
// frees the document's one candidate slot. Either the owner or the
// revision's author may; anyone else gets 403.
//
// It answers with the document, which the discard leaves untouched — read
// inside the discarding transaction like acceptDocRevision's, and the reason
// no discard response type is owed to internal/model.
func (s *server) discardDocRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	var doc *model.Doc
	err := s.recordDocEvent(w, r, "discard", "doc.revision_discarded", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.DiscardRevision(tx, now, id, actorID, eventID)
			if err != nil {
				return err
			}
			doc = d
			return nil
		})
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.withProjectKey(r.Context(), *doc))
}

// acceptDocRevision handles POST /api/v1/docs/{id}/revision/accept: runs the
// 025 §6 anchor gate and, when clean, lands the candidate as the next version.
func (s *server) acceptDocRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	var landed *model.Doc
	err := s.recordDocEvent(w, r, "accept", "doc.revision_accepted", id, nil,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.AcceptRevision(tx, now, id, actorID, eventID)
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
	// is an owner-gated deliberate act (025 §7), so this is the only place
	// the log says who performed it. A wrapper rather than the request alone,
	// because the five bodyless verbs would otherwise record a bare null and
	// lose the subject. It is an event row and not an HTTP body, so no
	// internal/model declaration is owed (ADR 036 §3).
	payload, err := json.Marshal(map[string]any{
		"doc":     id,
		"actor":   actorIDFrom(r),
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
