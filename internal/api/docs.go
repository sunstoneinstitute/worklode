// docs.go serves WL-SPEC-77's design documents — specs, ADRs and plans — over
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
// accept emit WL-RULE-179's typed JSON-LD events (wl:DocumentSubmitted,
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
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// validDocKinds mirrors the docs.kind CHECK constraint (migration 0027) and
// the wl:Spec/wl:Plan classes in ns/ontology.ttl. The store re-checks;
// this is here so a typo is a named 422 rather than a generic one.
var validDocKinds = map[string]bool{"spec": true, "plan": true}

// validDocStatuses mirrors the docs.status CHECK constraint, derived from
// wlc:DesignDocStatus in ns/concept.ttl (WL-REQ-178). Only the corpus importer
// may state a status (see createDoc); the store re-checks.
var validDocStatuses = ns.Set(ns.DesignDocStatuses)

// invalidDocKindMsg is what createDoc — today the only handler that gates on
// validDocKinds — answers with. It is a constant so a second write path names
// the kinds the same way this one does.
const invalidDocKindMsg = "invalid kind: must be spec or plan"

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
	// The plan token budget (S6, S19): a plan body over the hard ceiling is
	// refused before the write; over the soft budget only warns.
	warnings, err := s.checkPlanBudget(r.Context(), req.Kind, req.Project, req.Body)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	actorID := actorIDFrom(r)
	now := s.st.Now()

	// The payload goes in with document id 0 — the row does not exist until
	// CreateDoc runs, and it is the event's own consequence — and the real id
	// is merged back inside the same transaction, exactly as a minted task id
	// is (WL-RULE-179, store.AttributeEventToTask). Without it the log's one
	// record of a document's creation names no document, and every reader of
	// it, the Progress stream included, has nothing to resolve
	// (WL-REQ-1339).
	var created *model.Doc
	err = s.recordDocEvent(w, r, "create", "doc.created", 0, req,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.CreateDoc(tx, now, store.DocInput{
				Project:   req.Project,
				Kind:      req.Kind,
				Number:    req.Number,
				Slug:      req.Slug,
				Body:      req.Body,
				Owner:     req.Owner,
				CreatedBy: actorID,
				// The authoring task (WL-REQ-177). Left empty by every caller
				// bound to no task, which is a document with no authoring
				// task — a normal state, not a refusal. See migration 0044.
				GeneratedByTask: req.GeneratedByTask,
				Status:          req.Status,
			}, eventID)
			if err != nil {
				return err
			}
			created = d
			return store.MergeEventPayload(tx, eventID, map[string]any{"doc": d.ID})
		})
	if err != nil {
		return
	}
	doc := s.withProjectKey(r.Context(), *created)
	doc.Warnings = warnings
	writeJSON(w, http.StatusCreated, doc)
}

// listDocs handles GET /api/v1/docs?project=&kind=&status=&owner=&deleted=
// plus the three derived selectors: ?needs_planning= and ?needs_execution=
// (WL-REQ-186), and ?bare_superseded= (WL-REQ-190, WL-REQ-167 rule 2). deleted=true
// switches the list from live documents to tombstoned ones (WL-REQ-117).
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

// withProjectKeys stamps each document's ProjectKey and, from it, Ref — the
// formatted id (model.Doc.FormatRef) a client cites. Every handler whose
// response a client renders as a document ref runs its docs through this.
func (s *server) withProjectKeys(ctx context.Context, docs []model.Doc) []model.Doc {
	keys := s.projectKeyByID(ctx)
	if len(keys) == 0 {
		return docs
	}
	for i := range docs {
		docs[i].ProjectKey = keys[docs[i].Project]
		docs[i].Ref = docs[i].FormatRef()
	}
	return docs
}

// withProjectKey is withProjectKeys for a single document.
func (s *server) withProjectKey(ctx context.Context, d model.Doc) model.Doc {
	return s.withProjectKeys(ctx, []model.Doc{d})[0]
}

func (s *server) listDocs(w http.ResponseWriter, r *http.Request) {
	var p model.DocListParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sel, err := docSelectorFrom(p)
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
	case sel.unresolved:
		docs, err := s.st.UnresolvedDocs(r.Context(), sel.filter.Project, sel.filter.Kind, sel.olderThanDays)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.DocListResponse{Docs: s.withProjectKeys(r.Context(), withoutDocBodies(docs))})
	case sel.bareSuperseded:
		rules, err := s.st.BareSupersededRules(r.Context(), sel.filter.Project, sel.filter.Kind)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.DocListResponse{Docs: []model.Doc{}, BareRules: rules})
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
// a reference names (WL-REQ-168). Every `lode doc <verb>` takes a ref, and
// resolving it here rather than by listing the corpus client-side keeps the
// ambiguity and tombstone-fallback rules beside the data — the same reason
// GET /api/v1/projects/resolve normalizes a remote URL server-side, and what
// lets the ref grammar grow without a client upgrade.
//
// Two tiers (WL-358). The store's id/exact-slug lookup runs first — it alone
// reaches tombstoned documents, which `lode doc undelete <slug>` needs. A
// miss then goes through the full WL-REQ-192 grammar `lode show` and the /docs/ref/
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
	var p model.DocResolveParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ref := strings.TrimSpace(p.Ref)
	if ref == "" {
		writeErr(w, http.StatusUnprocessableEntity, "ref is required")
		return
	}
	d, err := s.st.ResolveDocRef(r.Context(), ref)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if gd, gerr := s.resolveDocRefWeb(r.Context(), ref, ""); gerr == nil {
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

// lintDocs handles GET /api/v1/docs/lint?project=: the corpus-wide read-only
// report of dangling frontmatter references (WL-REQ-180) — store.LintDocs does
// the work; this just reads the query filter and shapes the response the
// same way every other doc list route does (?project= narrows, "" answers
// over every project).
func (s *server) lintDocs(w http.ResponseWriter, r *http.Request) {
	var p model.DocLintParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	findings, err := s.st.LintDocs(r.Context(), p.Project)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if findings == nil {
		findings = []model.DocLintFinding{}
	}
	writeJSON(w, http.StatusOK, findings)
}

// resolveExternalCovers handles POST /api/v1/docs/covers/resolve: re-resolve
// every plan's to_external covers entries to rules (WL-903), recorded as one
// corpus-wide event.
func (s *server) resolveExternalCovers(w http.ResponseWriter, r *http.Request) {
	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]any{"actor": actorIDFrom(r)})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	var out model.CoversResolveResponse
	if _, _, err := s.st.RecordDocEvent(r.Context(), "resolve", docSource, extID, "doc.covers_resolved", payload,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = store.ResolveExternalCovers(tx, eventID)
			return err
		}); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// listCorpusSections handles GET /api/v1/docs/sections?project=&number=: the
// cross-corpus section listing (WL-REQ-180). ?project= narrows to one project
// the way every other doc list route does; ?number= narrows to one section
// number or anchor across the corpus. Both empty answers over everything.
func (s *server) listCorpusSections(w http.ResponseWriter, r *http.Request) {
	var p model.DocSectionsParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.st.ListCorpusSections(r.Context(), p.Project, p.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	keys := s.projectKeyByID(r.Context())
	out := make([]model.DocSectionRow, len(rows))
	for i, row := range rows {
		row.Ref = model.Doc{
			ProjectKey: keys[row.Project], Kind: row.DocKind, Number: row.DocNumber,
		}.FormatRef()
		out[i] = row
	}
	writeJSON(w, http.StatusOK, out)
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
// directly; the JSON API goes through docSelectorFrom, which adds WL-REQ-186's
// derived selectors on top.
//
// status=all is not a status: it means "every status, terminal documents
// included", which is what an absent status already means here. It exists so
// `lode doc list --status all` and the cockpit's /docs?status=all can opt out
// of the terminal-status hiding their own default asks for (12 S5).
func docFilterFrom(r *http.Request) store.DocFilter {
	q := r.URL.Query()
	f := store.DocFilter{
		Project: q.Get("project"),
		Kind:    q.Get("kind"),
		Status:  q.Get("status"),
		Owner:   q.Get("owner"),
	}
	if f.Status == "all" {
		f.Status = ""
	}
	return f
}

// addDocNote handles POST /api/v1/docs/{id}/notes: one anchored, non-blocking
// note against a section the document has (WL-REQ-171). It gates on doc.write
// rather than doc.read because it writes a row, not because a note carries any
// authority over the document — it blocks nothing and settles nothing.
func (s *server) addDocNote(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.AddDocNoteInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	var note model.DocNote
	err := s.recordDocEvent(w, r, "note", "doc.note_added", id, req,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			note, err = store.AddDocNote(tx, now, id, req, actorID, eventID)
			return err
		})
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, note)
}

// listDocNotes handles GET /api/v1/docs/{id}/notes. The detail endpoint
// carries the same rows; this exists for a caller that wants them alone.
func (s *server) listDocNotes(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	notes, err := s.st.ListDocNotes(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

// docListSelector is GET /api/v1/docs' query string once validated: the four
// plain filters, plus at most one of the derived selectors — the three of
// WL-REQ-186 and WL-REQ-170's unresolved.
type docListSelector struct {
	filter         store.DocFilter
	needsPlanning  bool
	needsExecution bool
	bareSuperseded bool
	unresolved     bool
	// olderThanDays narrows unresolved to documents untouched for at least
	// that many days; 0 is every one of them. Meaningless without
	// unresolved, and refused there rather than silently ignored.
	olderThanDays int
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
// each imply a single kind while bare_superseded implies one of two (WL-SPEC-78
// §1.3, §1.6; WL-REQ-167 rule 2) — so a contradicting kind or status is an error
// rather than an empty result, which would read as "nothing to plan".
// Requesting more than one derived selector at once is an error for the same
// reason: needs_planning and needs_execution select disjoint kinds, and
// bare_superseded selects a disjoint status, so any conjunction is always
// empty.
//
// The CLI refuses the same combinations locally so the error needs no round
// trip; this is the authority, for the clients that are not the CLI.
//
// p is already decoded (readQuery, at the top of listDocs) rather than a
// *http.Request: the derived-selector logic below is what makes this its
// own function, not the query decoding, which model.DocListParams's tags
// now say once for the server, the CLI and the OpenAPI document alike.
func docSelectorFrom(p model.DocListParams) (docListSelector, error) {
	status := p.Status
	// status=all is not a status: it means "every status, terminal
	// documents included", which is what an absent status already means
	// here. It exists so `lode doc list --status all` can opt out of the
	// terminal-status hiding its own default asks for (12 S5).
	if status == "all" {
		status = ""
	}
	sel := docListSelector{
		filter: store.DocFilter{
			Project: p.Project, Kind: p.Kind, Status: status, Owner: p.Owner,
			Deleted: p.Deleted, HasNotes: p.HasNotes, HideTerminal: p.HideTerminal,
		},
		needsPlanning:  p.NeedsPlanning,
		needsExecution: p.NeedsExecution,
		bareSuperseded: p.BareSuperseded,
		unresolved:     p.Unresolved,
		olderThanDays:  p.OlderThanDays,
	}
	// A day count, not a duration: the CLI's "30d" is parsed there, so what
	// crosses the wire is already the number WL-REQ-170's clock counts in.
	// OlderThanDays is a plain int (not a pointer), so an explicit 0 reads
	// the same as absent; that only changes behavior for older_than_days=0
	// combined with unresolved=false, which no longer errors.
	if p.OlderThanDays != 0 {
		if p.OlderThanDays < 0 {
			return docListSelector{}, fmt.Errorf("older_than_days must be a non-negative integer, got %d", p.OlderThanDays)
		}
		if !sel.unresolved {
			return docListSelector{}, errors.New("older_than_days applies to unresolved=true only (WL-SPEC-77 §9)")
		}
	}

	derived := []docDerivedSelector{
		{sel.needsPlanning, "needs_planning", "accepted", "WL-SPEC-77 §13",
			func(k string) bool { return k == "spec" }, "spec"},
		{sel.needsExecution, "needs_execution", "accepted", "WL-SPEC-77 §13",
			func(k string) bool { return k == "plan" }, "plan"},
		{sel.bareSuperseded, "bare_superseded", "superseded", "WL-SPEC-77 §4",
			func(k string) bool { return k == "spec" }, "spec"},
		{sel.unresolved, "unresolved", "accepted", "WL-SPEC-77 §9",
			func(k string) bool { return k == "spec" || k == "plan" }, "spec or plan"},
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
				"needs_planning and needs_execution select disjoint kinds; pass one (WL-SPEC-77 §13)")
		}
		return docListSelector{}, fmt.Errorf(
			"%s are mutually exclusive selectors; pass one (WL-SPEC-77 §13)", strings.Join(on, " and "))
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
	notes, err := s.st.ListDocNotes(ctx, id)
	if err != nil {
		return nil, err
	}
	amendments, err := s.st.ListDocAmendments(ctx, id)
	if err != nil {
		return nil, err
	}
	detail := &model.DocDetail{
		Doc: s.withProjectKey(ctx, *d), Sections: sections, Edges: out, EdgesIn: in, Notes: notes,
		Amendments: amendments,
	}
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

// listDocReferrers handles GET /api/v1/docs/{id}/referrers?anchor=sec-N:
// WL-REQ-171's open work pointing at one section, which a fixer can read
// before editing accepted text. The anchor is required — a referrer is a
// section-level fact, and a document-wide answer would silently mix in
// sections nobody asked about.
func (s *server) listDocReferrers(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var p model.DocReferrersParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Anchor == "" {
		writeErr(w, http.StatusBadRequest, "anchor is required: a referrer names one section (WL-SPEC-77 §4)")
		return
	}
	refs, err := s.st.DocSectionReferrers(r.Context(), id, p.Anchor)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.DocReferrersResponse{Referrers: refs})
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
	// The plan token budget (S6, S19) needs the document's own kind and
	// project, which this write doesn't otherwise read.
	existing, err := s.st.GetDoc(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	warnings, err := s.checkPlanBudget(r.Context(), existing.Kind, existing.Project, req.Body)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	now := s.st.Now()
	var updated *model.Doc
	err = s.recordDocEvent(w, r, "update", "doc.updated", id, req,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.UpdateDocBody(tx, now, id, req.Body, req.IfVersion, eventID)
			if err != nil {
				return err
			}
			updated = d
			return nil
		})
	if err != nil {
		return
	}
	doc := s.withProjectKey(r.Context(), *updated)
	doc.Warnings = warnings
	writeJSON(w, http.StatusOK, doc)
}

// patchDoc handles POST /api/v1/docs/{id}/patch: WL-REQ-171's in-place
// amendment of an accepted spec or ADR, the one write that changes accepted
// text without a revision cycle. The §8.3 gates are the store's — the CLI
// re-checks none of them, so the rule that refuses an edit is stated in one
// place — and a refusal is a 422 carrying the rule's own message.
//
// The doc.patched event says what the amendment did, which the store only
// knows once it has done it: the payload is rewritten inside the same
// transaction. A refused patch rolls that transaction back and records no
// event at all; worklode_doc_operations_total{op="patch"} is where a refusal
// is counted.
func (s *server) patchDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.PatchDocInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	var doc *model.Doc
	var patch *model.DocPatchResult
	var stale int
	err := s.recordDocEvent(w, r, "patch", watcher.TypeDocPatched, id, req,
		func(tx *sql.Tx, eventID int64) error {
			d, p, err := store.PatchDoc(tx, now, store.DocPatchInput{
				ID: id, Body: req.Body, Substantive: req.Substantive, Note: req.Note,
				ActorID: actorID, TaskID: req.Task, SessionID: req.Session,
				IfVersion: req.IfVersion,
			}, eventID)
			if err != nil {
				return err
			}
			doc, patch = d, p
			// The plans this amendment left behind are marked in the same
			// transaction (WL-REQ-171): the amendment and the staleness it
			// causes commit together or not at all. The re-planning task is
			// minted by the doc-lifecycle subscriber off the doc.stale
			// events this records, not here.
			stale, err = store.MarkPlansStale(tx, now, p.UnexecutedCoveringPlans, "amended", d.Slug, p.ChangedAnchors, eventID)
			if err != nil {
				return err
			}
			payload, err := store.EventPayload(map[string]any{
				"doc": id, "actor": actorID, "request": req,
				"anchors": p.ChangedAnchors, "classification": p.Classification, "rule": p.RuleFired,
			})
			if err != nil {
				return err
			}
			return store.SetEventPayload(tx, eventID, payload)
		})
	if err != nil {
		return
	}
	s.st.RecordPlansStale(stale)
	writeJSON(w, http.StatusOK, model.DocPatchResponse{
		Doc: s.withProjectKey(r.Context(), *doc), Patch: *patch})
}

// replaceDocEdges handles PUT /api/v1/docs/{id}/edges: the importer's
// rewrite of a document's whole live edge set (ReplaceDocEdgesInput). The
// response is the same DocDetail GET serves, so the caller reads back the
// edge set it wrote.
func (s *server) replaceDocEdges(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.ReplaceDocEdgesInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "edges", "doc.edges_replaced", id, req,
		func(tx *sql.Tx, eventID int64) error {
			return store.ReplaceDocEdges(tx, now, id, req.Edges, eventID)
		})
	if err != nil {
		return
	}
	s.writeDocDetail(w, r, id)
}

// linkDocEdge handles POST /api/v1/docs/{id}/edges and unlinkDocEdge DELETE:
// add or remove one edge (WL-REQ-164). The store routes the write: a
// plan's next version, a draft's live set, or an accepted document's
// candidate revision.
func (s *server) linkDocEdge(w http.ResponseWriter, r *http.Request) {
	s.changeDocEdge(w, r, "doc.edge_linked", store.LinkDocEdge)
}

func (s *server) unlinkDocEdge(w http.ResponseWriter, r *http.Request) {
	s.changeDocEdge(w, r, "doc.edge_unlinked", store.UnlinkDocEdge)
}

func (s *server) changeDocEdge(w http.ResponseWriter, r *http.Request, eventType string,
	write func(*sql.Tx, time.Time, int64, model.DocEdgeInput, string, int64) error) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.DocEdgeInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID := actorIDFrom(r)
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "edges", eventType, id, req,
		func(tx *sql.Tx, eventID int64) error {
			return write(tx, now, id, req, actorID, eventID)
		})
	if err != nil {
		return
	}
	s.writeDocDetail(w, r, id)
}

// setDocColumns handles PATCH /api/v1/docs/{id}: sets the title and issued
// date a body no longer states (WL-REQ-168).
func (s *server) setDocColumns(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.DocColumnsInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	now := s.st.Now()
	err := s.recordDocEvent(w, r, "update", "doc.columns_set", id, req,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.SetDocColumns(tx, now, id, req, eventID)
			return err
		})
	if err != nil {
		return
	}
	s.writeDocDetail(w, r, id)
}

// writeDocDetail answers with GET /api/v1/docs/{id}'s projection, read after
// the transaction that wrote it.
func (s *server) writeDocDetail(w http.ResponseWriter, r *http.Request, id int64) {
	detail, err := s.docDetail(r, id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// emitDocAccepted is the shared body of the two routes that accept a
// document: POST /api/v1/docs/{id}/accept and the Progress page's Accept
// button (WL-REQ-1337). It emits WL-RULE-179's typed wl:DocumentAccepted
// and applies store.AcceptDoc inside it, so the owner gate, the state gate
// and plan minting stay the store's for both callers and the page cannot
// grant what `lode doc accept` would refuse.
//
// A false inserted means the (source, external_id) conflict fired: this
// document at this version was already accepted, so apply never ran and
// accepted is nil. What that means is the caller's contract to state.
func (s *server) emitDocAccepted(ctx context.Context, doc *model.Doc, actorID string) (
	accepted *model.Doc, minted []model.Task, inserted bool, err error) {

	now := s.st.Now()
	// From is the status the document is actually leaving, not a constant: a
	// plan re-accepted while accepted leaves "accepted" (WL-REQ-174), and an
	// event saying otherwise would put a transition that did not happen in the
	// append-only log.
	ev := eventbus.DocumentAccepted{
		Doc: store.DocIRI(*doc), Actor: actorID, At: now,
		Version: doc.Version, From: "wlc:" + doc.Status, To: "wlc:accepted",
	}
	_, inserted, err = eventbus.Emit(ctx, s.st, docSource, ev,
		func(tx *sql.Tx, eventID int64) error {
			d, tasks, err := store.AcceptDoc(tx, now, doc.ID, actorID, eventID)
			if err != nil {
				return err
			}
			accepted, minted = d, tasks
			return nil
		})
	// Counted before the caller's error branch, exactly as RecordDocEvent
	// counts it: a refused accept is an outcome of the accept op, not an
	// absence of one.
	s.st.RecordDocOp("accept", err)
	return accepted, minted, inserted, err
}

// acceptDoc handles POST /api/v1/docs/{id}/accept: the manual commit of
// WL-REQ-170, gated on the document's owner. On a plan this also mints its
// execution tasks (WL-REQ-174) in the same transaction; the response carries
// the doc and, for a plan, the minted set (model.AcceptDocResponse) — empty
// and omitted for a spec or ADR, so their response stays byte-identical.
//
// The event is WL-RULE-179's typed wl:DocumentAccepted, whose external id is
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
	accepted, minted, inserted, err := s.emitDocAccepted(r.Context(), doc, actorID)
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
			// nothing left to do (WL-REQ-174). Answering with the document and
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
// Submission is an event, not a status (WL-RULE-179) — no document column moves
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
// revision an accepted spec or ADR may carry (WL-REQ-170), and answers with it.
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
// to another actor (WL-REQ-170). The current owner or an admin may transfer;
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

// withdrawDoc handles POST /api/v1/docs/{id}/withdraw: WL-REQ-170's close verb.
// An accepted or stale document that will not be executed and that nothing
// replaces leaves the corpus here, with the justification on the event.
//
// The store owns which statuses may be withdrawn, so a draft or an
// already-superseded document arrives back as its 422 rather than being
// re-checked here.
func (s *server) withdrawDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.WithdrawDocInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	now := s.st.Now()
	var doc *model.Doc
	err := s.recordDocEvent(w, r, "withdraw", "doc.withdrawn", id, req,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.WithdrawDoc(tx, now, id, eventID)
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
	// The candidate is the subject here, not the document, so the document's
	// version is not what a compare-and-swap would be about. Refused rather
	// than ignored: a caller that asked for the check must not be told the
	// write was guarded when it was not.
	if req.IfVersion != 0 {
		writeErr(w, http.StatusUnprocessableEntity,
			"if_version names a document version, and a revision edit writes the open candidate: drop it")
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
// open candidate without landing it (WL-REQ-170's close-without-merging), which
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
// WL-REQ-167 anchor gate and, when clean, lands the candidate as the next version.
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
	// is an owner-gated deliberate act (WL-REQ-170), so this is the only place
	// the log says who performed it. A wrapper rather than the request alone,
	// because the five bodyless verbs would otherwise record a bare null and
	// lose the subject. It is an event row and not an HTTP body, so no
	// internal/model declaration is owed (WL-RULE-1349).
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

// docRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var docRouteDocs = map[string]routeDoc{
	"POST /api/v1/docs": {
		summary:   "Create a design document",
		request:   model.CreateDocInput{},
		responses: map[int]any{http.StatusCreated: model.Doc{}},
	},
	"GET /api/v1/docs": {
		summary:   "List design documents",
		responses: map[int]any{http.StatusOK: model.DocListResponse{}},
		params:    model.DocListParams{},
	},
	"GET /api/v1/docs/resolve": {
		summary:   "Resolve a document reference to its document",
		responses: map[int]any{http.StatusOK: model.Doc{}},
		params:    model.DocResolveParams{},
	},
	"GET /api/v1/docs/lint": {
		summary:   "List dangling frontmatter references across the corpus",
		responses: map[int]any{http.StatusOK: []model.DocLintFinding{}},
		params:    model.DocLintParams{},
	},
	"POST /api/v1/docs/covers/resolve": {
		summary:   "Re-resolve every plan's unresolved covers references",
		responses: map[int]any{http.StatusOK: model.CoversResolveResponse{}},
	},
	"GET /api/v1/docs/sections": {
		summary:   "List sections across the document corpus",
		responses: map[int]any{http.StatusOK: []model.DocSectionRow{}},
		params:    model.DocSectionsParams{},
	},
	"GET /api/v1/docs/{id}": {
		summary:   "Get a document with its sections, edges and notes",
		responses: map[int]any{http.StatusOK: model.DocDetail{}},
	},
	"GET /api/v1/docs/{id}/versions": {
		summary:   "List a document's versions",
		responses: map[int]any{http.StatusOK: []model.DocVersionSummary{}},
	},
	"GET /api/v1/docs/{id}/referrers": {
		summary:   "List open work pointing at a document section",
		responses: map[int]any{http.StatusOK: model.DocReferrersResponse{}},
		params:    model.DocReferrersParams{},
	},
	"GET /api/v1/docs/{id}/versions/{n}": {
		summary:   "Get one version of a document",
		responses: map[int]any{http.StatusOK: model.DocVersion{}},
	},
	"PUT /api/v1/docs/{id}/body": {
		summary:   "Replace a document's body",
		request:   model.UpdateDocBodyInput{},
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
	"POST /api/v1/docs/{id}/patch": {
		summary:   "Amend an accepted document in place",
		request:   model.PatchDocInput{},
		responses: map[int]any{http.StatusOK: model.DocPatchResponse{}},
	},
	"PUT /api/v1/docs/{id}/edges": {
		summary:   "Replace a document's whole edge set",
		request:   model.ReplaceDocEdgesInput{},
		responses: map[int]any{http.StatusOK: model.DocDetail{}},
	},
	"POST /api/v1/docs/{id}/edges": {
		summary:   "Add one edge to a document",
		request:   model.DocEdgeInput{},
		responses: map[int]any{http.StatusOK: model.DocDetail{}},
	},
	"DELETE /api/v1/docs/{id}/edges": {
		summary:   "Remove one edge from a document",
		request:   model.DocEdgeInput{},
		responses: map[int]any{http.StatusOK: model.DocDetail{}},
	},
	"PATCH /api/v1/docs/{id}": {
		summary:   "Set a document's title or issued date",
		request:   model.DocColumnsInput{},
		responses: map[int]any{http.StatusOK: model.DocDetail{}},
	},
	"POST /api/v1/docs/{id}/submit": {
		summary:   "Submit a document for review",
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
	"POST /api/v1/docs/{id}/accept": {
		summary:   "Accept a document, minting a plan's tasks",
		responses: map[int]any{http.StatusOK: model.AcceptDocResponse{}},
	},
	"POST /api/v1/docs/{id}/revise": {
		summary:   "Open a candidate revision of an accepted document",
		responses: map[int]any{http.StatusOK: model.DocRevision{}},
	},
	"POST /api/v1/docs/{id}/withdraw": {
		summary:   "Withdraw a document from the corpus",
		request:   model.WithdrawDocInput{},
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
	"POST /api/v1/docs/{id}/owner": {
		summary:   "Transfer a document to another owner",
		request:   model.TransferDocOwnerInput{},
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
	"POST /api/v1/docs/{id}/notes": {
		summary:   "Add an anchored note to a document",
		request:   model.AddDocNoteInput{},
		responses: map[int]any{http.StatusOK: model.DocNote{}},
	},
	"GET /api/v1/docs/{id}/notes": {
		summary:   "List a document's anchored notes",
		responses: map[int]any{http.StatusOK: []model.DocNote{}},
	},
	"PUT /api/v1/docs/{id}/revision": {
		summary:   "Replace the open candidate revision's body",
		request:   model.UpdateDocBodyInput{},
		responses: map[int]any{http.StatusOK: model.DocRevision{}},
	},
	"DELETE /api/v1/docs/{id}/revision": {
		summary:   "Discard the open candidate revision",
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
	"POST /api/v1/docs/{id}/revision/accept": {
		summary:   "Land the candidate revision as the next version",
		responses: map[int]any{http.StatusOK: model.Doc{}},
	},
}
