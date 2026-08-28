package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// docEntityKind is the state_log entity_kind every document mutation is
// recorded under; the entity id is the doc id in decimal. Documents ride the
// same event-logged transaction machinery as tasks (025 §5), so a mutation
// with no state_log row would be the one entity the timeline cannot render.
const docEntityKind = "doc"

// docDateLayout is the lexical form of docs.issued on the wire.
const docDateLayout = "2006-01-02"

// validDocKinds and validDocStatuses mirror the docs CHECK constraints, so a
// bad input is ErrInvalidInput rather than a Postgres error the caller has to
// decode. The status set is generated from wlc:DesignDocStatus (025 §17); the
// kinds mirror the wl:Spec/wl:ADR/wl:Plan classes, which are not a SKOS scheme.
var (
	validDocKinds    = map[string]bool{"spec": true, "adr": true, "plan": true}
	validDocStatuses = ns.Set(ns.DesignDocStatuses)
)

// DocInput carries the fields for creating a document. Number 0 means
// auto-assign the next free one for (project, kind); an explicit value stays
// legal but is the rare override (029 §4). Owner defaults to CreatedBy —
// the accept gate is owner-only, so a document with none could never be
// accepted.
type DocInput struct {
	Project   string
	Kind      string // spec | adr | plan
	Number    int
	Slug      string
	Body      string
	Owner     string
	CreatedBy string
	// GeneratedByTask is the task that authored the document (025 §12). Empty
	// for every caller bound to no task — a cockpit author, an agent outside a
	// worktree, `lode doc import` — which is a normal state, not a refusal.
	GeneratedByTask string
	// Status is honoured only by the corpus importer; the API's create path
	// always leaves it empty, which means draft.
	Status string
}

// DocFilter narrows ListDocs. Zero-valued fields do not filter.
type DocFilter struct {
	Project string
	Kind    string
	Status  string
	// Deleted switches the list from live documents to tombstoned ones
	// (044 §5). See TaskFilter.Deleted for why it is a switch.
	Deleted bool
}

// logDocChange records one document mutation in the state log. Nine write
// paths in this file need it; the entity kind and the id formatting are
// stated here rather than re-typed at each of them.
func logDocChange(tx *sql.Tx, docID, eventID int64, change map[string]string) error {
	return LogChange(tx, docEntityKind, strconv.FormatInt(docID, 10), eventID, change)
}

// snapshotDocVersion archives a document's current row into doc_versions
// (025 §4.5), before the caller's own UPDATE docs SET ... overwrites it in
// the same transaction. Called only from the two sites that bump
// docs.version — UpdateDocBody's plan branch and AcceptRevision — so a draft
// spec/ADR body edit, which never bumps version, never snapshots.
func snapshotDocVersion(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(
		// ON CONFLICT DO NOTHING is unreachable with both current callers:
		// docs.version only increases and each snapshots the pre-bump value.
		// Kept as belt-and-braces against a version somehow going backwards.
		`INSERT INTO doc_versions (doc_id, version, body, title, issued, created_at)
		 SELECT id, version, body, title, issued, updated_at FROM docs WHERE id = $1
		 ON CONFLICT (doc_id, version) DO NOTHING`, docID,
	); err != nil {
		return fmt.Errorf("snapshot doc %d before version bump: %w", docID, err)
	}
	return nil
}

// CreateDoc inserts a document, its section rows and its frontmatter-derived
// edges inside the given transaction, and appends a state_log row attributed
// to eventID. Call it from a RecordDocEvent apply callback with the store's
// clock as now.
//
// Title comes from the body's H1, falling back to the slug; issued comes from
// the frontmatter. A parse failure, a malformed issued date, or (on a spec or
// ADR) an anchor defect is ErrInvalidInput.
//
// Status is the corpus importer's affordance. Creating a spec or ADR straight
// at accepted must therefore establish what AcceptDoc would have: the 025 §6.1
// depth gate runs here too, the sections land published, and the supersession
// cascade fires on every document-level `replaces` edge that resolves.
func CreateDoc(tx *sql.Tx, now time.Time, in DocInput, eventID int64) (*model.Doc, error) {
	if !validDocKinds[in.Kind] {
		return nil, fmt.Errorf("doc kind %q: %w", in.Kind, ErrInvalidInput)
	}
	if in.Slug == "" {
		return nil, fmt.Errorf("doc slug must not be empty: %w", ErrInvalidInput)
	}
	if in.Number < 0 {
		return nil, fmt.Errorf("a %s needs a non-negative corpus number, got %d: %w", in.Kind, in.Number, ErrInvalidInput)
	}
	// Every kind draws its number from its project's (project_id, kind) row in
	// project_entity_seq — the same counter deliverables and, as of 029 §4's
	// plan-numbers cutover, plans already use. An explicit in.Number
	// stays legal — the rare case of reserving one, or a corpus import
	// preserving the number already in a spec/ADR's filename — checked for
	// collision by docs_project_kind_number below; the upsert then bumps the
	// counter past it so a later auto-assign never retraces it.
	docSeqKind := strings.ToUpper(in.Kind)
	docNumber := int64(in.Number)
	if docNumber == 0 {
		// The upsert both creates the counter row on a project's first
		// document of this kind (next = 2, ordinal 1) and advances it
		// afterwards, holding the row lock for the rest of the transaction
		// so two concurrent creates cannot draw the same number.
		if err := tx.QueryRow(
			`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, $2, 2)
			 ON CONFLICT (project_id, kind) DO UPDATE SET next = project_entity_seq.next + 1
			 RETURNING next - 1`,
			in.Project, docSeqKind,
		).Scan(&docNumber); err != nil {
			return nil, fmt.Errorf("allocate %s number: %w", in.Kind, err)
		}
	} else if _, err := tx.Exec(
		`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, $2, $3 + 1)
		 ON CONFLICT (project_id, kind) DO UPDATE
		   SET next = GREATEST(project_entity_seq.next, $3 + 1)`,
		in.Project, docSeqKind, docNumber,
	); err != nil {
		return nil, fmt.Errorf("reserve %s number %d: %w", in.Kind, docNumber, err)
	}
	status := in.Status
	if status == "" {
		status = "draft"
	}
	if !validDocStatuses[status] {
		return nil, fmt.Errorf("doc status %q: %w", status, ErrInvalidInput)
	}

	parsed, err := parseDocBody(in.Kind, in.Body)
	if err != nil {
		return nil, err
	}
	// Created accepted: run AcceptDoc's first-accept gate. Plans skip it: they
	// carry no sections and no anchors (025 §9).
	acceptedAtCreate := status == "accepted" && in.Kind != "plan"
	if acceptedAtCreate {
		if v := designdoc.DepthViolations(parsed.doc, designdoc.DepthLimit); len(v) > 0 {
			return nil, fmt.Errorf("doc %s/%s cannot be created accepted: %s: %w",
				in.Project, in.Slug, strings.Join(v, "; "), ErrInvalidInput)
		}
	}

	title, ok := designdoc.Title(parsed.doc)
	if !ok {
		title = in.Slug
	}
	owner := in.Owner
	if owner == "" {
		owner = in.CreatedBy
	}
	ts := now.UTC().Truncate(time.Second)

	number := sql.NullInt64{Int64: docNumber, Valid: true}
	var id int64
	err = tx.QueryRow(
		`INSERT INTO docs (project_id, kind, number, slug, title, body, status, version,
		                   issued, owner, created_by, generated_by_task, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8::date, $9, $10, $11, $12, $12)
		 RETURNING id`,
		in.Project, in.Kind, number, in.Slug, title, in.Body, status,
		nullText(parsed.issued), nullText(owner), nullText(in.CreatedBy),
		nullText(in.GeneratedByTask), ts,
	).Scan(&id)
	if err != nil {
		// The two unique indexes are the identity rules of 025 §5, so a
		// caller sees one sentinel rather than having to decode pgconn.
		if isUniqueViolationOn(err, "docs_project_slug") {
			return nil, fmt.Errorf("project %s already has a doc slugged %s: %w",
				in.Project, in.Slug, ErrDocExists)
		}
		if isUniqueViolationOn(err, "docs_project_kind_number") {
			return nil, fmt.Errorf("project %s already has %s %d: %w",
				in.Project, in.Kind, docNumber, ErrDocExists)
		}
		// A worktree can outlive the task it was named for, so an authoring
		// task the backbone has never heard of is a caller mistake worth
		// naming rather than an anonymous constraint failure. ErrInvalidInput
		// and not ErrNotFound: the request names a document to create and a
		// bad field on it, and mapStoreErr flattens ErrNotFound to a bare
		// "not found" that would read as if the document were missing.
		if pgViolation(err, "23503", "docs_generated_by_task_fkey") {
			return nil, fmt.Errorf("generated_by_task names no task %q: %w",
				in.GeneratedByTask, ErrInvalidInput)
		}
		return nil, fmt.Errorf("insert doc %s/%s: %w", in.Project, in.Slug, err)
	}

	if err := rebuildSections(tx, id, in.Kind, parsed.doc, 1); err != nil {
		return nil, err
	}
	if acceptedAtCreate {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET published = true WHERE doc_id = $1`, id); err != nil {
			return nil, fmt.Errorf("publish sections of doc %d: %w", id, err)
		}
	}
	if err := rebuildEdges(tx, now, id, in.Kind, in.Project, parsed.doc.Frontmatter); err != nil {
		return nil, err
	}
	// The third thing AcceptDoc does, after the depth gate and the publish flag:
	// what this document replaces stops being current the moment it lands
	// accepted. Only targets already in the corpus and already accepted move —
	// supersedeReplacedDocs' own guard — so an import that writes the replacing
	// document first is caught later instead, by repointExternalEdges (WL-133).
	if acceptedAtCreate {
		if err := supersedeReplacedDocs(tx, ts, id, eventID); err != nil {
			return nil, err
		}
	}
	// A reference resolves once, at write time, so references already stored
	// unresolved because this document did not exist yet are re-pointed now —
	// that is what makes corpus import order-independent (WL-130).
	if err := repointExternalEdges(tx, in.Project, ts, id, eventID); err != nil {
		return nil, err
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{"field": "status", "new": status}); err != nil {
		return nil, err
	}

	// Read the row back rather than restating the input: repointExternalEdges
	// may have superseded this very document on the way in — an already-imported
	// accepted document that replaces it, whose cascade could not reach it until
	// now — and a caller told "accepted" by a create that landed superseded is
	// the same order-dependence this whole path exists to remove.
	return getDocTx(tx, id)
}

// UpdateDocBody replaces a document's body in place, rebuilding its sections
// and edges from the new source, and appends a state_log row attributed to
// eventID. It returns the updated row so the caller need not re-read it.
//
// An accepted spec or ADR is ErrInvalidInput: those are revised, never edited
// in place, so their published anchors pass the 025 §6 diff gate. Plans stay
// freely mutable at any status (025 §9).
func UpdateDocBody(tx *sql.Tx, now time.Time, id int64, body string, eventID int64) (*model.Doc, error) {
	var kind, status, project string
	var version int
	err := tx.QueryRow(
		`SELECT kind, status, project_id, version FROM docs WHERE id = $1 FOR UPDATE`, id,
	).Scan(&kind, &status, &project, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load doc %d: %w", id, err)
	}
	if kind != "plan" && status != "draft" {
		return nil, fmt.Errorf("doc %d is %s: revise it instead of editing the body (025 §7): %w",
			id, status, ErrInvalidInput)
	}

	parsed, err := parseDocBody(kind, body)
	if err != nil {
		return nil, err
	}
	title, ok := designdoc.Title(parsed.doc)
	if !ok {
		if err := tx.QueryRow(`SELECT slug FROM docs WHERE id = $1`, id).Scan(&title); err != nil {
			return nil, fmt.Errorf("load doc %d slug: %w", id, err)
		}
	}
	if kind == "plan" {
		if err := checkPlanTasksMinted(tx, id, parsed.doc); err != nil {
			return nil, err
		}
		// Snapshot the version this edit is about to overwrite (025 §4.5):
		// docs still holds its pre-update body, title and issued here, before
		// the UPDATE below runs.
		if err := snapshotDocVersion(tx, id); err != nil {
			return nil, err
		}
		// A plan is edited in place rather than revised (025 §9), so its body
		// edit is what a spec's accepted revision is: the next version of the
		// document. Nothing else moves the number for a plan, and re-accepting
		// one needs it to — the acceptance event's external id is derived from
		// the document's IRI and version (§15.3), so a re-accept at an
		// unchanged version collapses at the log, which is exactly the no-op
		// an unedited plan should be.
		version++
	}
	// A plan's edit bumps version above, so second precision is enough to
	// order its history; a draft spec/ADR is edited in place with no version
	// bump (025 §7), so updated_at is its only externally visible signal that
	// a second edit happened, and needs full precision to carry it — two
	// edits inside the same wall-clock second must not collapse into one
	// (WL-285).
	ts := now.UTC()
	if kind == "plan" {
		ts = ts.Truncate(time.Second)
	}

	// The frontmatter is part of the body, so title and issued are rederived
	// from it here for the same reason CreateDoc derives them: the body is
	// what states them. issued only ever moves forward, though — a plan stays
	// mutable at accepted (025 §9), and a body edit that drops the key must
	// not erase the acceptance date, which is a lifecycle fact rather than a
	// property of the text.
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, title = $3, issued = coalesce($4::date, issued),
		                 version = $5, updated_at = $6
		  WHERE id = $1`,
		id, body, title, nullText(parsed.issued), version, ts,
	); err != nil {
		return nil, fmt.Errorf("update doc %d body: %w", id, err)
	}
	if err := rebuildSections(tx, id, kind, parsed.doc, version); err != nil {
		return nil, err
	}
	if err := rebuildEdges(tx, now, id, kind, project, parsed.doc.Frontmatter); err != nil {
		return nil, err
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{"field": "body"}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// AcceptDoc is the manual commit of 025 §7: draft -> accepted, gated on the
// owner. For a spec or ADR it freezes the document's published anchor set
// and flips the target of every document-level replaces edge to superseded,
// in the same transaction. For a plan it mints the plan's execution tasks
// instead (see acceptPlanDoc) — the second return is that minted set, in
// definition order, and nil for a spec or ADR.
//
// A plan is also accepted from accepted: re-acceptance is how a declaration
// added to an accepted plan reaches the task set (§9.2), and it mints only
// what has no row yet.
//
// The depth limit is evaluated at publication (025 §6 rule 6), so a first
// accept still rejects an anchored heading below designdoc.DepthLimit even
// though rules 1-3 exempt drafts.
func AcceptDoc(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*model.Doc, []model.Task, error) {
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, nil, err
	}
	// Owner first, matching AcceptRevision: standing to touch the document
	// does not depend on its state, and checking state first would disclose it
	// to an actor who has none.
	if err := checkDocOwner(id, d.owner, actorID); err != nil {
		return nil, nil, err
	}
	if d.kind == "plan" {
		// A plan is accepted from draft and re-accepted while accepted
		// (025 §9.2): it stays freely mutable, and re-acceptance is how a
		// declaration added after the first accept reaches the task set.
		// Superseded is still refused — there is nothing left to execute.
		if d.status != "draft" && d.status != "accepted" {
			return nil, nil, fmt.Errorf(
				"doc %d is %s: a plan is accepted from draft or re-accepted while accepted (025 §9.2): %w",
				id, d.status, ErrInvalidInput)
		}
		return acceptPlanDoc(tx, now, id, d, actorID, eventID)
	}
	// Draft-only for a spec or ADR: an accepted one is revised (§7.2), never
	// re-accepted.
	if d.status != "draft" {
		return nil, nil, fmt.Errorf("doc %d is %s, not draft: %w", id, d.status, ErrInvalidInput)
	}

	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, nil, err
	}
	// The depth gate is the one rule a first accept still enforces: rules 1-3
	// need an accepted version to diff against, and there is none.
	if v := designdoc.DepthViolations(parsed.doc, designdoc.DepthLimit); len(v) > 0 {
		return nil, nil, fmt.Errorf("doc %d cannot be accepted: %s: %w", id, strings.Join(v, "; "), ErrInvalidInput)
	}

	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(
		`UPDATE docs SET status = 'accepted', updated_at = $2 WHERE id = $1`, id, ts); err != nil {
		return nil, nil, fmt.Errorf("accept doc %d: %w", id, err)
	}
	if _, err := tx.Exec(
		`UPDATE doc_sections SET published = true WHERE doc_id = $1`, id); err != nil {
		return nil, nil, fmt.Errorf("publish sections of doc %d: %w", id, err)
	}
	if err := supersedeReplacedDocs(tx, ts, id, eventID); err != nil {
		return nil, nil, err
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{"field": "status", "old": d.status, "new": "accepted"}); err != nil {
		return nil, nil, err
	}
	doc, err := getDocTx(tx, id)
	return doc, nil, err
}

// lockedDoc is the row the lifecycle writers read and lock before deciding.
type lockedDoc struct {
	kind    string
	status  string
	project string
	slug    string
	body    string
	owner   string
	version int
}

// lockDoc reads a document FOR UPDATE, so two accepts of one document
// serialise rather than racing.
func lockDoc(tx *sql.Tx, id int64) (lockedDoc, error) {
	var d lockedDoc
	var owner sql.NullString
	err := tx.QueryRow(
		`SELECT kind, status, project_id, slug, body, owner, version
		   FROM docs WHERE id = $1 FOR UPDATE`, id,
	).Scan(&d.kind, &d.status, &d.project, &d.slug, &d.body, &owner, &d.version)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedDoc{}, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return lockedDoc{}, fmt.Errorf("load doc %d: %w", id, err)
	}
	d.owner = owner.String
	return d, nil
}

// checkDocOwner enforces 025 §7's accept gate: acceptance is the owner's
// deliberate act. A document with no owner can be accepted by nobody, which
// is why CreateDoc defaults it to the creator.
func checkDocOwner(id int64, owner, actorID string) error {
	if owner == "" {
		return fmt.Errorf("doc %d has no owner to accept it: %w", id, ErrForbidden)
	}
	if owner != actorID {
		return fmt.Errorf("doc %d is owned by %s, not %s: %w", id, owner, actorID, ErrForbidden)
	}
	return nil
}

// CheckDocAcceptable re-runs AcceptDoc's gates without accepting anything, and
// returns the same sentinels: ErrNotFound, ErrForbidden for an actor that is
// not the owner, ErrInvalidInput for a document that is not draft.
//
// The first return says the accept has already happened and mints nothing
// more: an accepted plan re-accepted at a version it was accepted at. That is
// a legal no-op rather than a refusal (025 §9.2), and the caller answers with
// the document unchanged.
//
// It exists for one caller. The typed accept emission (025 §15.3) derives its
// external id from the document's IRI and version, so a second accept of the
// same version conflicts at the log and eventbus.Emit skips apply — which
// means AcceptDoc's gates never run and the handler has no store answer to
// return. Without this the request would report success for an accept that
// did not happen, to an actor who may not even be the owner. The gates live
// here, next to the ones AcceptDoc runs, so the two cannot drift.
func (s *Store) CheckDocAcceptable(ctx context.Context, id int64, actorID string) (settled bool, err error) {
	var kind, status, owner string
	var ownerCol sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT kind, status, owner FROM docs WHERE id = $1`, id).Scan(&kind, &status, &ownerCol)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return false, fmt.Errorf("load doc %d: %w", id, err)
	}
	owner = ownerCol.String
	// Owner first, matching AcceptDoc: standing to touch the document does
	// not depend on its state, and checking state first would disclose it to
	// an actor who has none.
	if err := checkDocOwner(id, owner, actorID); err != nil {
		return false, err
	}
	if kind == "plan" && status == "accepted" {
		return true, nil
	}
	if status != "draft" {
		return false, fmt.Errorf("doc %d is %s, not draft: %w", id, status, ErrInvalidInput)
	}
	return false, nil
}

// checkDocOwnerOrAdmin is checkDocOwner plus the admin bypass 025 §7.3 gives
// ownership transfer: the current owner may always transfer, and so may an
// actor whose actors.admin column is set, whether or not they own the
// document. No caller plumbs an isAdmin flag into internal/store today, so
// this reads the column itself, in the same transaction as the row lock, next
// to the gate it extends.
func checkDocOwnerOrAdmin(tx *sql.Tx, id int64, owner, actorID string) error {
	if owner == actorID {
		return nil
	}
	var admin bool
	err := tx.QueryRow(`SELECT admin FROM actors WHERE id = $1`, actorID).Scan(&admin)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load actor %s: %w", actorID, err)
	}
	if !admin {
		return fmt.Errorf("doc %d is owned by %s, not %s: %w", id, owner, actorID, ErrForbidden)
	}
	return nil
}

// TransferDocOwner reassigns a document's owner (025 §7.3), so a document
// whose owner has left the org is not stuck forever unacceptable. The current
// owner or an admin may transfer; anyone else is ErrForbidden. Transferring
// to the actor that already owns it is a legal no-op that still answers
// success — Task 5's bulk transfer is a client-side loop over many
// documents, and re-running it has to be safe.
func TransferDocOwner(tx *sql.Tx, now time.Time, id int64, newOwner, actorID string, eventID int64) (*model.Doc, error) {
	if newOwner == "" {
		return nil, fmt.Errorf("owner must not be empty: %w", ErrInvalidInput)
	}
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, err
	}
	if err := checkDocOwnerOrAdmin(tx, id, d.owner, actorID); err != nil {
		return nil, err
	}
	if newOwner == d.owner {
		return getDocTx(tx, id)
	}
	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(
		`UPDATE docs SET owner = $2, updated_at = $3 WHERE id = $1`, id, newOwner, ts,
	); err != nil {
		// docs_assignee_fkey: ALTER TABLE ... RENAME COLUMN (0058_doc_owner)
		// renamed the column, not the constraint Postgres auto-named for it.
		if pgViolation(err, "23503", "docs_assignee_fkey") {
			return nil, fmt.Errorf("owner names no actor %q: %w", newOwner, ErrInvalidInput)
		}
		return nil, fmt.Errorf("transfer doc %d owner: %w", id, err)
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{"field": "owner", "old": d.owner, "new": newOwner}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// supersedeReplacedDocs flips every accepted document a document-level
// replaces edge names to superseded, in the accepting transaction.
// Section-scoped replaces edges flip nothing — section-level supersession
// stays derived (025 §3.3) — and an edge resolving to to_external names no
// row here.
//
// Only an accepted target moves: 025 §7's ladder is draft -> accepted ->
// superseded, and a draft pushed straight to superseded would be unreachable
// by every verb here — editable by none, acceptable by none, revisable by
// none. A draft target is therefore left alone, and logs nothing.
//
// Nor does a tombstoned target move. It is found for the caller, not named by
// them, and that is the case 044 §4 says a tombstone stops: flipping it to
// superseded would mutate and log against a row nothing can see.
func supersedeReplacedDocs(tx *sql.Tx, ts time.Time, docID, eventID int64) error {
	rows, err := tx.Query(
		`SELECT DISTINCT e.to_doc FROM doc_edges e
		   JOIN docs t ON t.id = e.to_doc AND t.deleted_at IS NULL
		  WHERE e.from_doc = $1 AND e.type = 'replaces'
		    AND e.from_anchor IS NULL AND e.to_doc IS NOT NULL AND e.to_doc <> $1
		  ORDER BY e.to_doc`, docID)
	if err != nil {
		return fmt.Errorf("read replaces edges of doc %d: %w", docID, err)
	}
	targets, err := scanColumn[int64](rows, fmt.Sprintf("read replaces edges of doc %d", docID))
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		return nil
	}

	// One UPDATE over the whole target set; RETURNING names exactly the rows
	// that moved, which is what the per-target RowsAffected check used to
	// establish. Sorted back into id order so the state_log reads the same as
	// when this walked the (already ordered) targets one at a time.
	rows, err = tx.Query(
		`UPDATE docs SET status = 'superseded', updated_at = $2
		  WHERE id = ANY($1::bigint[]) AND status = 'accepted'
		 RETURNING id`, targets, ts)
	if err != nil {
		return fmt.Errorf("supersede docs replaced by %d: %w", docID, err)
	}
	moved, err := scanColumn[int64](rows, fmt.Sprintf("supersede docs replaced by %d", docID))
	if err != nil {
		return err
	}
	slices.Sort(moved)

	for _, target := range moved {
		if err := logDocChange(tx, target, eventID,
			map[string]string{
				"field":       "status",
				"new":         "superseded",
				"replaced_by": strconv.FormatInt(docID, 10),
			}); err != nil {
			return err
		}
	}
	return nil
}

// parsedDoc is a body parsed and validated once, for the fields both write
// paths derive from it.
type parsedDoc struct {
	doc    *designdoc.Document
	issued string
}

// parseDocBody parses body and rejects what the schema cannot express: an
// unparseable document, an issued date that is not YYYY-MM-DD, and (on a spec
// or ADR) an anchor defect.
func parseDocBody(kind, body string) (parsedDoc, error) {
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return parsedDoc{}, fmt.Errorf("parse document body: %w: %w", err, ErrInvalidInput)
	}
	var issued string
	if doc.Frontmatter != nil {
		issued = strings.TrimSpace(doc.Frontmatter.Issued)
	}
	if issued != "" {
		if _, err := time.Parse(docDateLayout, issued); err != nil {
			return parsedDoc{}, fmt.Errorf("frontmatter issued %q is not YYYY-MM-DD: %w", issued, ErrInvalidInput)
		}
	}
	// Plans carry no anchors (025 §9), so the lint applies to specs and ADRs
	// only. designdoc.LintAnchors is the one implementation; `lode doc
	// anchors` reports its findings as a pre-accept lint, and here any finding
	// refuses the write.
	if kind != "plan" {
		if v := designdoc.LintAnchors(doc); len(v) > 0 {
			return parsedDoc{}, fmt.Errorf("%s: %w", strings.Join(v, "; "), ErrInvalidInput)
		}
	}
	return parsedDoc{doc: doc, issued: issued}, nil
}

// priorSection is the accept-time state rebuildSections carries forward.
type priorSection struct {
	lastRevisedIn int
	published     bool
}

// rebuildSections replaces a document's section rows from its parsed source,
// reading the prior state itself. Callers that already hold that map — the
// accept path reads it to gate the revision — call rebuildSectionsFrom instead
// and skip the reread.
func rebuildSections(tx *sql.Tx, docID int64, kind string, doc *designdoc.Document, version int) error {
	if kind == "plan" {
		return nil
	}
	prior, err := priorSections(tx, docID)
	if err != nil {
		return err
	}
	_, err = rebuildSectionsFrom(tx, docID, kind, doc, version, prior)
	return err
}

// rebuildSectionsFrom replaces a document's section rows from its parsed
// source, preserving last_revised_in and published for every anchor in prior
// that survives: those are accept-time facts about the section, not facts
// about the current text. A new anchor starts unpublished at the document's
// current version. Plans have no sections (025 §9), so nothing is written for
// one. The returned map is the state the rebuilt rows carry, so a caller
// needing to compare before against after does not have to read them back.
func rebuildSectionsFrom(tx *sql.Tx, docID int64, kind string, doc *designdoc.Document, version int,
	prior map[string]priorSection) (map[string]priorSection, error) {

	after := map[string]priorSection{}
	if kind == "plan" {
		return after, nil
	}
	if _, err := tx.Exec(`DELETE FROM doc_sections WHERE doc_id = $1`, docID); err != nil {
		return nil, fmt.Errorf("clear sections of doc %d: %w", docID, err)
	}

	// One INSERT over parallel arrays rather than one per section: a 60-section
	// spec is a round trip per heading otherwise, and every accept pays it.
	var (
		anchors   []string
		numbers   []*string
		headings  []string
		depths    []int32
		positions []int32
		revisions []int32
		published []bool
	)
	for _, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		p := priorSection{lastRevisedIn: version}
		if q, ok := prior[sec.Anchor]; ok {
			p = q
		}
		anchors = append(anchors, sec.Anchor)
		numbers = append(numbers, nullTextPtr(sec.Number))
		headings = append(headings, sec.Title)
		depths = append(depths, int32(sec.Level))
		positions = append(positions, int32(len(positions)))
		revisions = append(revisions, int32(p.lastRevisedIn))
		published = append(published, p.published)
		after[sec.Anchor] = p
	}
	if len(anchors) == 0 {
		return after, nil
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_sections (doc_id, anchor, number, heading, depth, position, last_revised_in, published)
		 SELECT $1::bigint, s.anchor, s.number, s.heading, s.depth, s.position,
		        s.last_revised_in, s.published
		   FROM unnest($2::text[], $3::text[], $4::text[], $5::int[], $6::int[], $7::int[], $8::boolean[])
		        AS s(anchor, number, heading, depth, position, last_revised_in, published)`,
		docID, anchors, numbers, headings, depths, positions, revisions, published,
	); err != nil {
		return nil, fmt.Errorf("insert sections of doc %d: %w", docID, err)
	}
	return after, nil
}

// priorSections reads the accept-time state of a document's current sections,
// keyed by anchor.
func priorSections(tx *sql.Tx, docID int64) (map[string]priorSection, error) {
	rows, err := tx.Query(
		`SELECT anchor, last_revised_in, published FROM doc_sections WHERE doc_id = $1`, docID)
	if err != nil {
		return nil, fmt.Errorf("read sections of doc %d: %w", docID, err)
	}
	defer rows.Close()
	out := map[string]priorSection{}
	for rows.Next() {
		var anchor string
		var p priorSection
		if err := rows.Scan(&anchor, &p.lastRevisedIn, &p.published); err != nil {
			return nil, fmt.Errorf("scan section of doc %d: %w", docID, err)
		}
		out[anchor] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sections of doc %d: %w", docID, err)
	}
	return out, nil
}

// docColumns is the SELECT list scanDoc expects, in order. The three
// tombstone columns (migration 0034) are last so positional scans elsewhere
// are unaffected by their addition; they are all-null or all-set together.
const docColumns = `id, project_id, kind, number, slug, title, body, status, version, issued, owner, created_by, generated_by_task, created_at, updated_at, deleted_at, deleted_by, delete_justification`

// docColumnsD is docColumns under the `d` alias, for the queries that join
// docs against a table carrying a column of the same name (doc_sections.number).
var docColumnsD = qualifyColumns(docColumns, "d")

func scanDoc(row rowScanner) (*model.Doc, error) {
	var d model.Doc
	var number sql.NullInt64
	var issued sql.NullTime
	var owner, createdBy, generatedByTask sql.NullString
	var deletedAt sql.NullTime
	var deletedBy, justification sql.NullString
	if err := row.Scan(&d.ID, &d.Project, &d.Kind, &number, &d.Slug, &d.Title, &d.Body,
		&d.Status, &d.Version, &issued, &owner, &createdBy, &generatedByTask,
		&d.CreatedAt, &d.UpdatedAt,
		&deletedAt, &deletedBy, &justification); err != nil {
		return nil, err
	}
	d.Tombstone = tombstoneFrom(deletedAt, deletedBy, justification)
	d.Number = int(number.Int64)
	if issued.Valid {
		d.Issued = issued.Time.Format(docDateLayout)
	}
	d.Owner = owner.String
	d.CreatedBy = createdBy.String
	d.GeneratedByTask = generatedByTask.String
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	return &d, nil
}

// getDocTx reads one document inside an open transaction, so a writer can
// return the row it just wrote without a second round trip.
func getDocTx(tx *sql.Tx, id int64) (*model.Doc, error) {
	d, err := scanDoc(tx.QueryRow(`SELECT `+docColumns+` FROM docs WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get doc %d: %w", id, err)
	}
	return d, nil
}

// GetDoc looks up one document by id. Returns ErrNotFound if it does not
// exist.
func (s *Store) GetDoc(ctx context.Context, id int64) (*model.Doc, error) {
	d, err := scanDoc(s.db.QueryRowContext(ctx, `SELECT `+docColumns+` FROM docs WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get doc %d: %w", id, err)
	}
	return d, nil
}

// ListDocs returns the matching documents in corpus order: kind, then number
// (plans, which have none, last within their kind), then slug.
func (s *Store) ListDocs(ctx context.Context, f DocFilter) ([]model.Doc, error) {
	// 044 §4: a tombstoned document is out of every list by default.
	where := "deleted_at IS NULL"
	if f.Deleted {
		where = "deleted_at IS NOT NULL"
	}
	var args []any
	for _, c := range []struct {
		column string
		value  string
	}{{"project_id", f.Project}, {"kind", f.Kind}, {"status", f.Status}} {
		if c.value == "" {
			continue
		}
		args = append(args, c.value)
		where += fmt.Sprintf(" AND %s = $%d", c.column, len(args))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+docColumns+` FROM docs WHERE `+where+
			` ORDER BY project_id, kind, number NULLS LAST, slug`, args...)
	if err != nil {
		return nil, fmt.Errorf("list docs: %w", err)
	}
	return collectRows(rows, "list docs", byValue(scanDoc))
}

// scanDocVersionSummary scans one row of ListDocVersions' union: version,
// title, issued, created_at, in that order.
func scanDocVersionSummary(row rowScanner) (model.DocVersionSummary, error) {
	var v model.DocVersionSummary
	var issued sql.NullTime
	if err := row.Scan(&v.Version, &v.Title, &issued, &v.CreatedAt); err != nil {
		return model.DocVersionSummary{}, err
	}
	if issued.Valid {
		v.Issued = issued.Time.Format(docDateLayout)
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return v, nil
}

// ListDocVersions returns every version of a document, newest first: its
// current row (docs) and every version it has superseded (doc_versions),
// (025 §4.5).
func (s *Store) ListDocVersions(ctx context.Context, id int64) (out []model.DocVersionSummary, err error) {
	defer func() { s.metrics.docOp("list-versions", err) }()

	rows, err := s.db.QueryContext(ctx,
		`SELECT version, title, issued, updated_at FROM docs WHERE id = $1
		 UNION ALL
		 SELECT version, title, issued, created_at FROM doc_versions WHERE doc_id = $1
		 ORDER BY version DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("list versions of doc %d: %w", id, err)
	}
	out, err = collectRows(rows, fmt.Sprintf("list versions of doc %d", id), scanDocVersionSummary)
	return out, err
}

// GetDocVersion returns one version of a document, current or superseded
// (025 §4.5): the live docs row when version equals its current version,
// otherwise the matching doc_versions row. ErrNotFound if neither exists.
func (s *Store) GetDocVersion(ctx context.Context, id int64, version int) (out model.DocVersion, err error) {
	defer func() { s.metrics.docOp("get-version", err) }()

	var issued sql.NullTime
	err = s.db.QueryRowContext(ctx,
		`SELECT version, title, body, issued, updated_at FROM docs
		  WHERE id = $1 AND version = $2`, id, version,
	).Scan(&out.Version, &out.Title, &out.Body, &issued, &out.CreatedAt)
	if err == nil {
		out.Doc = id
		if issued.Valid {
			out.Issued = issued.Time.Format(docDateLayout)
		}
		out.CreatedAt = out.CreatedAt.UTC()
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.DocVersion{}, fmt.Errorf("get version %d of doc %d: %w", version, id, err)
	}

	out = model.DocVersion{}
	err = s.db.QueryRowContext(ctx,
		`SELECT title, body, issued, created_at FROM doc_versions
		  WHERE doc_id = $1 AND version = $2`, id, version,
	).Scan(&out.Title, &out.Body, &issued, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DocVersion{}, fmt.Errorf("doc %d has no version %d: %w", id, version, ErrNotFound)
	}
	if err != nil {
		return model.DocVersion{}, fmt.Errorf("get version %d of doc %d: %w", version, id, err)
	}
	out.Doc = id
	out.Version = version
	if issued.Valid {
		out.Issued = issued.Time.Format(docDateLayout)
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

// BareSupersededSections returns the superseded specs and ADRs that have at
// least one section nothing explains — 025 §6 rule 2's "bare superseded
// section", read as a derived query rather than an accept-time gate (per the
// decision recorded at 025 §3.3: section-level supersession stays derived).
// project and kind both narrow the answer; "" in either does not filter.
//
// A section counts as explained by a `replaces` edge that names it, at either
// granularity doc_edges can express:
//
//   - a section-scoped edge (to_doc = this document, to_anchor = the section)
//     explains exactly that section;
//   - a document-scoped edge (to_doc = this document, to_anchor IS NULL)
//     explains every section of the document — the successor supersedes it
//     wholesale, so no per-section listing is owed.
//
// The successor's own status is deliberately not required: the edge itself is
// the explanation 025 §3.3 asks for, and demanding an accepted successor would
// report an explained section as bare. Its *liveness* is required, though — a
// tombstoned successor explains nothing, the same way it is not itself
// reported here. A `to_external` edge names no local row and so explains
// nothing, whatever it once pointed at. Plans carry no
// sections (025 §9), so the JOIN against doc_sections excludes them
// structurally, independent of the kind predicate below.
//
// Not answered here: rule 2's other branch, a `dct:description` saying why a
// section went away. `0027_docs` has no free-text column on doc_sections or
// doc_edges to read for that, on either the section or its explaining edge, so
// it stays unmechanised — it lands with section-level supersession in the
// graph (025 §3.3), tracked by WL-150. This mirrors NeedsPlanning's
// WL-141 gap: a rule 025 states that today's schema cannot fully answer.
func (s *Store) BareSupersededSections(ctx context.Context, project, kind string) (
	[]model.Doc, []model.DocSupersessionGap, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH replaced_section AS (
		     SELECT DISTINCT e.to_doc AS doc_id, e.to_anchor AS anchor
		       FROM doc_edges e
		       JOIN docs s ON s.id = e.from_doc AND s.deleted_at IS NULL
		      WHERE e.type = 'replaces'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		 ), replaced_doc AS (
		     SELECT DISTINCT e.to_doc AS doc_id
		       FROM doc_edges e
		       JOIN docs s ON s.id = e.from_doc AND s.deleted_at IS NULL
		      WHERE e.type = 'replaces'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NULL
		 )
		 SELECT `+docColumnsD+`, count(*)::int,
		        coalesce(json_agg(sec.anchor ORDER BY sec.position)
		                 FILTER (WHERE rs.anchor IS NULL), '[]')::text
		   FROM docs d
		   JOIN doc_sections sec ON sec.doc_id = d.id
		   LEFT JOIN replaced_section rs ON rs.doc_id = sec.doc_id AND rs.anchor = sec.anchor
		  WHERE d.status = 'superseded'
		    AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		    AND ($2 = '' OR d.kind = $2)
		    AND NOT EXISTS (SELECT 1 FROM replaced_doc rd WHERE rd.doc_id = d.id)
		  GROUP BY d.id
		 HAVING count(*) FILTER (WHERE rs.anchor IS NULL) > 0
		  ORDER BY d.project_id, d.number NULLS LAST, d.slug`, project, kind)
	if err != nil {
		return nil, nil, fmt.Errorf("list bare superseded sections: %w", err)
	}
	defer rows.Close()

	var docs []model.Doc
	var gaps []model.DocSupersessionGap
	for rows.Next() {
		var gap model.DocSupersessionGap
		var unexplainedJSON string
		d, err := scanDoc(appendScan{rows, []any{&gap.Sections, &unexplainedJSON}})
		if err != nil {
			return nil, nil, fmt.Errorf("scan bare superseded doc: %w", err)
		}
		if err := json.Unmarshal([]byte(unexplainedJSON), &gap.Unexplained); err != nil {
			return nil, nil, fmt.Errorf("decode unexplained anchors of doc %d: %w", d.ID, err)
		}
		gap.Doc = d.ID
		docs = append(docs, *d)
		gaps = append(gaps, gap)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list bare superseded sections: %w", err)
	}
	return docs, gaps, nil
}

// appendScan lets scanDoc read a row that carries extra trailing columns: it
// forwards the document's own destinations and appends the caller's. Without
// it every query that joins something onto docs would need its own copy of
// the fourteen-column scan.
type appendScan struct {
	rowScanner
	extra []any
}

func (a appendScan) Scan(dest ...any) error {
	return a.rowScanner.Scan(append(dest, a.extra...)...)
}

// ListDocSections returns a document's sections in document order. A plan
// carries none (025 §9), which is an empty result rather than an error.
func (s *Store) ListDocSections(ctx context.Context, docID int64) ([]model.DocSection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT anchor, coalesce(number,''), heading, depth, position, last_revised_in, published
		   FROM doc_sections WHERE doc_id = $1 ORDER BY position`, docID)
	if err != nil {
		return nil, fmt.Errorf("list sections of doc %d: %w", docID, err)
	}
	return collectRows(rows, fmt.Sprintf("list sections of doc %d", docID), func(r rowScanner) (model.DocSection, error) {
		var sec model.DocSection
		if err := r.Scan(&sec.Anchor, &sec.Number, &sec.Heading, &sec.Depth,
			&sec.Position, &sec.LastRevisedIn, &sec.Published); err != nil {
			return model.DocSection{}, err
		}
		return sec, nil
	})
}

// RecordDocEvent wraps RecordEvent for a document mutation, recording
// worklode_doc_operations_total{op,outcome}. op is one of
// create|update|accept|revise|discard|edges|transfer.
//
// The write functions themselves take a *sql.Tx rather than owning one, so a
// single transaction can host a document mutation and its consequences —
// plan acceptance mints tasks in the same commit (025 §9.2). This wrapper is
// where the metric lives because it is the one place every such mutation
// passes through.
func (s *Store) RecordDocEvent(
	ctx context.Context,
	op, source, externalID, typ string,
	payload []byte,
	apply func(tx *sql.Tx, eventID int64) error,
) (id int64, inserted bool, err error) {
	id, inserted, err = s.RecordEvent(ctx, source, externalID, typ, payload, apply)
	s.metrics.docOp(op, err)
	return id, inserted, err
}

// RecordDocOp records one document mutation's outcome
// (worklode_doc_operations_total) for a caller that records its event
// through eventbus.Emit rather than RecordDocEvent — the typed emission
// path of 025 §15.3, which cannot go through the wrapper because the
// payload needs the event id before the insert. Nil-safe.
func (s *Store) RecordDocOp(op string, err error) { s.metrics.docOp(op, err) }

// DocIRI is a document's canonical subject IRI (spec 025 §4.1's
// wlid:doc/spec-worklode-025 form): wlid:doc/<kind>-<project>-<number>
// zero-padded to three digits for the numbered kinds, and
// wlid:doc/plan-<project>-<slug> for plans, which carry no number
// (025 §14.3). Project-qualified because the identity rules of §5 are
// per project: two projects may each hold a spec 25.
func DocIRI(d model.Doc) string {
	if d.Kind == "plan" {
		return "wlid:doc/plan-" + d.Project + "-" + d.Slug
	}
	return fmt.Sprintf("wlid:doc/%s-%s-%03d", d.Kind, d.Project, d.Number)
}

// DocBySubjectIRI resolves an event's wl:subject back to its row.
//
// Reconstructs the IRI in SQL and compares, rather than parsing iri in Go:
// both a project id and a plan slug may contain hyphens, so
// "wlid:doc/<kind>-<project>-<tail>" is not unambiguously splittable back
// into its parts.
func (s *Store) DocBySubjectIRI(ctx context.Context, iri string) (*model.Doc, error) {
	d, err := scanDoc(s.db.QueryRowContext(ctx,
		`SELECT `+docColumns+` FROM docs
		  WHERE 'wlid:doc/' || kind || '-' || project_id || '-' ||
		        CASE WHEN kind = 'plan' THEN slug ELSE lpad(number::text, 3, '0') END = $1`,
		iri))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc with subject %q: %w", iri, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve doc subject %q: %w", iri, err)
	}
	return d, nil
}
