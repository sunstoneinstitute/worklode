package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// DocInput carries the fields for creating a document. Number is 0 for plans,
// which carry none (025 §14.3). Assignee defaults to CreatedBy — the accept
// gate is assignee-only, so a document with none could never be accepted.
type DocInput struct {
	Project   string
	Kind      string // spec | adr | plan
	Number    int
	Slug      string
	Body      string
	Assignee  string
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
	// docs_number_matches_kind enforces both halves of 025 §14.3 in the schema;
	// these two cases are kept so a caller gets ErrInvalidInput naming the field
	// rather than a CHECK violation.
	switch {
	case in.Kind == "plan" && in.Number != 0:
		return nil, fmt.Errorf("a plan carries no number (025 §14.3), got %d: %w", in.Number, ErrInvalidInput)
	case in.Kind != "plan" && in.Number <= 0:
		return nil, fmt.Errorf("a %s needs a corpus number, got %d: %w", in.Kind, in.Number, ErrInvalidInput)
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
	assignee := in.Assignee
	if assignee == "" {
		assignee = in.CreatedBy
	}
	ts := now.UTC().Truncate(time.Second)

	var number sql.NullInt64
	if in.Kind != "plan" {
		number = sql.NullInt64{Int64: int64(in.Number), Valid: true}
	}
	var id int64
	err = tx.QueryRow(
		`INSERT INTO docs (project_id, kind, number, slug, title, body, status, version,
		                   issued, assignee, created_by, generated_by_task, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8::date, $9, $10, $11, $12, $12)
		 RETURNING id`,
		in.Project, in.Kind, number, in.Slug, title, in.Body, status,
		nullText(parsed.issued), nullText(assignee), nullText(in.CreatedBy),
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
				in.Project, in.Kind, in.Number, ErrDocExists)
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
		// A plan is edited in place rather than revised (025 §9), so its body
		// edit is what a spec's accepted revision is: the next version of the
		// document. Nothing else moves the number for a plan, and re-accepting
		// one needs it to — the acceptance event's external id is derived from
		// the document's IRI and version (§15.3), so a re-accept at an
		// unchanged version collapses at the log, which is exactly the no-op
		// an unedited plan should be.
		version++
	}
	ts := now.UTC().Truncate(time.Second)

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

// ReplaceDocEdges re-resolves a document's outbound edges from its stored
// body and appends a state_log row attributed to eventID: the frontmatter
// references that resolved to nothing when the document was created become
// real edges once the rest of the corpus is present.
//
// CreateDoc now re-points existing unresolved references as their targets
// arrive (repointExternalEdges), so corpus import no longer depends on this
// pass; it is a repair path for edges that went stale some other way.
//
// Nothing authored changes — not the body, not the sections, not the status,
// and the version does not move: the same source is being read again against a
// larger corpus. That is why, unlike UpdateDocBody, it works at any status
// including accepted and superseded; there is no published anchor to protect
// because no anchor is being restated.
//
// The clock stamps only an artifact declaration the re-read frontmatter
// carries (rebuildEdges), plus the supersession cascade below when it fires.
//
// A repaired document-level `replaces` edge can newly resolve here exactly as
// it can in repointExternalEdges (WL-133): rebuildEdges re-reads the same
// frontmatter against a corpus that may now hold the target. The same two
// guards apply — supersedeReplacedFrom's (a plan replacer cascades nothing,
// a draft replacer's own accept will run the cascade) and
// supersedeReplacedDocs' own (only an accepted target moves).
func ReplaceDocEdges(tx *sql.Tx, now time.Time, id, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return err
	}
	if err := rebuildEdges(tx, now, id, d.kind, d.project, parsed.doc.Frontmatter); err != nil {
		return err
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{"field": "edges"}); err != nil {
		return err
	}
	if d.kind != "plan" && d.status != "draft" {
		ts := now.UTC().Truncate(time.Second)
		if err := supersedeReplacedDocs(tx, ts, id, eventID); err != nil {
			return err
		}
	}
	return nil
}

// AcceptDoc is the manual commit of 025 §7: draft -> accepted, gated on the
// assignee. For a spec or ADR it freezes the document's published anchor set
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
	// Assignee first, matching AcceptRevision: standing to touch the document
	// does not depend on its state, and checking state first would disclose it
	// to an actor who has none.
	if err := checkDocAssignee(id, d.assignee, actorID); err != nil {
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

// acceptPlanDoc is AcceptDoc's plan branch (025 §9.2): parse the plan body's
// ## Tasks declarations, mint one draft task per declaration that has no row
// yet with plan_doc set to id, wire the newly minted tasks' blockedBy numbers
// as blocks edges, then flip the document to accepted — all inside the
// caller's transaction, so accept and mint are one commit and a failed mint
// leaves the document as it was.
//
// Re-accepting an accepted plan runs the same code: a declaration whose title
// already names a row is left alone — no re-mint, no field overwrite, no
// state change — so a re-accept of an unedited plan mints nothing and is a
// safe no-op. The match is on plan_task_key, the declaration title recorded at
// mint, which is why a title edit inside the plan reads as withdrawing one
// declaration and adding another: a minted task is execution fact and outlives
// its declaration, so nothing here deletes a task whose declaration is gone.
//
// Plans carry no sections and no anchors (025 §9), so none of the spec/ADR
// branch's section or diff machinery runs here: there is nothing to publish
// and no depth gate to evaluate. d.status is already known draft or accepted —
// AcceptDoc checks it before branching.
func acceptPlanDoc(tx *sql.Tx, now time.Time, id int64, d lockedDoc, actorID string, eventID int64) (*model.Doc, []model.Task, error) {
	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, nil, err
	}
	defs, err := designdoc.PlanTasks(parsed.doc)
	if err != nil {
		return nil, nil, fmt.Errorf("doc %d cannot be accepted: %w: %w", id, err, ErrInvalidInput)
	}
	minted, err := plantaskRows(tx, id)
	if err != nil {
		return nil, nil, err
	}

	// First pass resolves every definition number to a task id — minting the
	// ones that have none — and the second wires blockedBy once every number
	// resolves, so a forward reference (task 1 blockedBy task 2) needs no
	// reordering.
	taskID := make(map[int]string, len(defs))
	fresh := make(map[int]bool, len(defs))
	tasks := make([]model.Task, 0, len(defs))
	for _, def := range defs {
		if existing, ok := minted[def.Title]; ok {
			taskID[def.Number] = existing
			continue
		}
		task, err := CreateTask(tx, now, TaskInput{
			ProjectID:   d.project,
			Title:       def.Title,
			Body:        def.Body,
			Priority:    def.Priority,
			Kind:        def.Kind,
			Skills:      def.Skills,
			CreatedBy:   actorID,
			Draft:       true,
			PlanDoc:     id,
			PlanTaskKey: def.Title,
		}, eventID)
		if err != nil {
			return nil, nil, fmt.Errorf("mint task %d of plan %d: %w", def.Number, id, err)
		}
		taskID[def.Number] = task.ID
		fresh[def.Number] = true
		tasks = append(tasks, *task)
	}
	// Only a newly minted task gets its declared blockers wired. An edge into
	// a task that already exists would change how that task ranks and when it
	// is claimable — a change to an existing row, which re-acceptance does not
	// make.
	for _, def := range defs {
		if !fresh[def.Number] {
			continue
		}
		for _, blocker := range def.BlockedBy {
			if err := AddEdge(tx, now, taskID[blocker], taskID[def.Number], "blocks", eventID); err != nil {
				return nil, nil, fmt.Errorf(
					"wire blocks edge task %d -> %d of plan %d: %w", blocker, def.Number, id, err)
			}
		}
	}

	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(
		`UPDATE docs SET status = 'accepted', updated_at = $2 WHERE id = $1`, id, ts); err != nil {
		return nil, nil, fmt.Errorf("accept doc %d: %w", id, err)
	}
	// A first accept logs the status move; a re-accept logs what it minted,
	// because the status did not move and an "accepted -> accepted" line would
	// say nothing about what changed.
	change := map[string]string{"field": "status", "old": d.status, "new": "accepted"}
	if d.status == "accepted" {
		change = map[string]string{"field": "plan_tasks", "new": strconv.Itoa(len(tasks))}
	}
	if err := logDocChange(tx, id, eventID, change); err != nil {
		return nil, nil, err
	}
	doc, err := getDocTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	return doc, tasks, nil
}

// plantaskRows reads a plan's minted task set as declaration title -> task id
// (025 §9.2): what acceptPlanDoc matches declarations against, and what
// checkPlanTasksMinted uses to decide whether a body edit has a task set to
// stay consistent with.
//
// Soft-deleted tasks are included deliberately. A deleted task is withdrawn
// work, not absent work; skipping it here would have the next re-accept mint
// its declaration again and undo the withdrawal, and the partial unique index
// on (plan_doc, plan_task_key) would refuse the insert anyway.
func plantaskRows(tx *sql.Tx, docID int64) (map[string]string, error) {
	rows, err := tx.Query(
		`SELECT plan_task_key, id FROM tasks WHERE plan_doc = $1`, docID)
	if err != nil {
		return nil, fmt.Errorf("read minted tasks of plan %d: %w", docID, err)
	}
	defer rows.Close()
	minted := map[string]string{}
	for rows.Next() {
		var key, taskID string
		if err := rows.Scan(&key, &taskID); err != nil {
			return nil, fmt.Errorf("read minted tasks of plan %d: %w", docID, err)
		}
		minted[key] = taskID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read minted tasks of plan %d: %w", docID, err)
	}
	return minted, nil
}

// checkPlanTasksMinted refuses a plan body edit that would leave a plan whose
// tasks are already minted without the valid ## Tasks section a re-accept has
// to read (025 §9.2). Without it an accepted plan's declarations could be
// rewritten into something unparseable, and the drift between the document and
// its task set would surface only at the next accept — or never.
//
// It binds only once something has been minted. A draft plan is written a
// paragraph at a time and its ## Tasks section is legitimately incomplete
// until the accept gate reads it, and an accepted plan that minted nothing is
// §9.2's historical import, which never had a task set to stay consistent
// with.
//
// What it does not refuse is a declaration that disappeared or was retitled.
// §9.2 is explicit that a minted task outlives its declaration — withdrawing
// work is a task transition, not a document edit — so an edit that drops one
// leaves the row alone, and one that retitles it declares a task the next
// re-accept mints. Only the ambiguity a re-accept cannot resolve is an error,
// and designdoc.PlanTasks names it.
func checkPlanTasksMinted(tx *sql.Tx, id int64, doc *designdoc.Document) error {
	minted, err := plantaskRows(tx, id)
	if err != nil {
		return err
	}
	if len(minted) == 0 {
		return nil
	}
	if _, err := designdoc.PlanTasks(doc); err != nil {
		return fmt.Errorf(
			"doc %d has %d minted task(s), so its \"## Tasks\" section must stay readable: %w: %w",
			id, len(minted), err, ErrInvalidInput)
	}
	return nil
}

// ReviseDoc opens a candidate revision against an accepted spec or ADR: a copy
// of the current body to edit while the accepted version stays authoritative
// (025 §7.2). One candidate at a time.
//
// Plans are edited in place with UpdateDocBody (025 §9) and drafts are edited
// in place because there is nothing to revise against; both are
// ErrInvalidInput.
func ReviseDoc(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	if d.kind == "plan" {
		return fmt.Errorf("doc %d is a plan: plans are edited in place (025 §9): %w", id, ErrInvalidInput)
	}
	if d.status != "accepted" {
		return fmt.Errorf("doc %d is %s: only an accepted document is revised (025 §7.2): %w",
			id, d.status, ErrInvalidInput)
	}

	if _, err := tx.Exec(
		`INSERT INTO doc_revisions (doc_id, body, created_by, created_at) VALUES ($1, $2, $3, $4)`,
		id, d.body, nullText(actorID), now.UTC().Truncate(time.Second),
	); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("doc %d already has an open revision: %w", id, ErrRevisionExists)
		}
		return fmt.Errorf("open revision of doc %d: %w", id, err)
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "revision", "new": "open"})
}

// UpdateRevision replaces the body of a document's open candidate revision.
// The body is parsed and linted here so a malformed candidate is refused at
// the edit rather than at the accept gate. ErrNotFound if no revision is open.
//
// The document's status is rechecked, not only its revision: a document
// superseded since the revision opened has nothing left to land, and saying so
// at the edit beats a confusing refusal at the accept gate.
func UpdateRevision(tx *sql.Tx, now time.Time, id int64, body string, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	if d.status != "accepted" {
		return fmt.Errorf("doc %d is %s: only an accepted document has a revision to edit: %w",
			id, d.status, ErrInvalidInput)
	}
	if _, err := parseDocBody(d.kind, body); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE doc_revisions SET body = $2 WHERE doc_id = $1`, id, body)
	if err != nil {
		return fmt.Errorf("update revision of doc %d: %w", id, err)
	}
	if err := requireOneAffected(res, fmt.Sprintf("update revision of doc %d", id),
		fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)); err != nil {
		return err
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "revision", "new": "updated"})
}

// DiscardRevision withdraws a document's open candidate revision without
// landing it — the close-without-merging half of the pull request 025 §7.2
// says a revision structurally is. Deleting the row frees the
// one-candidate-per-document slot, so the next ReviseDoc succeeds immediately
// instead of hitting ErrRevisionExists. ErrNotFound if no revision is open.
//
// Gated on the document's assignee or the revision's created_by: anyone with
// doc.write may propose a revision, and either its author or the document's
// assignee may withdraw it. That pairing is what keeps ReviseDoc open — an
// unwanted candidate can always be cleared by someone.
//
// Unlike AcceptRevision this checks no status: a candidate left behind on a
// document that has since been superseded is exactly the litter discard
// exists to remove.
//
// Nothing is stamped, so now goes unused; the signature matches the other
// document writers, as ReplaceDocEdges' does.
func DiscardRevision(tx *sql.Tx, _ time.Time, id int64, actorID string, eventID int64) (*model.Doc, error) {
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, err
	}
	// The revision is read before the gate because the gate depends on its
	// created_by. Nothing is disclosed by that ordering: whether a candidate
	// is open is already on the detail endpoint for any doc.read holder.
	var createdBy sql.NullString
	var body string
	err = tx.QueryRow(
		`SELECT created_by, body FROM doc_revisions WHERE doc_id = $1 FOR UPDATE`, id).
		Scan(&createdBy, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load revision of doc %d: %w", id, err)
	}
	if err := checkRevisionDiscarder(id, d.assignee, createdBy.String, actorID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`DELETE FROM doc_revisions WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("discard revision of doc %d: %w", id, err)
	}
	// The one state_log row in this file that carries a body, because this is
	// the one verb after which the text is nowhere else: doc_revisions has no
	// history and the delete is hard, the docs row never held a candidate, and
	// the request that asked for the discard names no body to record. An
	// accepted body stays on the document; an edited one is in the update's
	// own event payload.
	if err := logDocChange(tx, id, eventID, map[string]string{
		"field": "revision", "new": "discarded", "discarded_body": body,
	}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// AcceptRevision lands a document's open candidate revision: it runs the
// 025 §6 constraint check against the accepted version and, when clean, swaps
// the body, bumps the version, rebuilds sections and edges, stamps
// last_revised_in on exactly the changed anchors, publishes every anchor the
// new version carries, applies any new document-level replaces edges, and
// consumes the candidate — one transaction, assignee-gated like AcceptDoc.
//
// The append-only rule protects the anchors the accepted version *published*
// (025 §7.2), so a never-published row that disappears is legal; renumbering
// and excess depth are violations regardless.
func AcceptRevision(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*model.Doc, error) {
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, err
	}
	if err := checkDocAssignee(id, d.assignee, actorID); err != nil {
		return nil, err
	}
	if d.kind == "plan" {
		return nil, fmt.Errorf("doc %d is a plan: plans are edited in place (025 §9): %w", id, ErrInvalidInput)
	}
	if d.status != "accepted" {
		return nil, fmt.Errorf("doc %d is %s: only an accepted document has a revision to land: %w",
			id, d.status, ErrInvalidInput)
	}

	var candidateBody string
	err = tx.QueryRow(
		`SELECT body FROM doc_revisions WHERE doc_id = $1 FOR UPDATE`, id).Scan(&candidateBody)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load revision of doc %d: %w", id, err)
	}

	accepted, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, fmt.Errorf("parse the accepted body of doc %d: %w", id, err)
	}
	candidate, err := parseDocBody(d.kind, candidateBody)
	if err != nil {
		return nil, err
	}

	prior, err := priorSections(tx, id)
	if err != nil {
		return nil, err
	}
	diff := designdoc.CompareSections(accepted.doc, candidate.doc, designdoc.DepthLimit)
	// Removed is filtered down to the published anchors before Violations
	// renders it, so the text stays in one place and an unpublished removal
	// raises nothing.
	removed := diff.Removed[:0:0]
	for _, anchor := range diff.Removed {
		if prior[anchor].published {
			removed = append(removed, anchor)
		}
	}
	diff.Removed = removed
	if v := diff.Violations(); len(v) > 0 {
		return nil, fmt.Errorf("revision of doc %d cannot be accepted: %s: %w",
			id, strings.Join(v, "; "), ErrInvalidInput)
	}

	version := d.version + 1
	title, ok := designdoc.Title(candidate.doc)
	if !ok {
		title = d.slug
	}
	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, title = $3, issued = coalesce($4::date, issued),
		                 version = $5, updated_at = $6
		  WHERE id = $1`,
		id, candidateBody, title, nullText(candidate.issued), version, ts,
	); err != nil {
		return nil, fmt.Errorf("land revision of doc %d: %w", id, err)
	}
	after, err := rebuildSectionsFrom(tx, id, d.kind, candidate.doc, version, prior)
	if err != nil {
		return nil, err
	}
	if err := rebuildEdges(tx, now, id, d.kind, d.project, candidate.doc.Frontmatter); err != nil {
		return nil, err
	}

	// The diff gate is what keeps a published anchor from being dropped; this
	// checks that it did. A failure here is a bug in the gate, not bad input,
	// so it carries no sentinel.
	for anchor, p := range prior {
		if _, still := after[anchor]; p.published && !still {
			return nil, fmt.Errorf(
				"internal: doc %d lost published anchor #%s in the section rebuild", id, anchor)
		}
	}

	// 025 §6 rule 5: last_revised_in moves on exactly the sections whose
	// content changed. Touching it elsewhere invalidates valid claims.
	if len(diff.Changed) > 0 {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET last_revised_in = $3
			  WHERE doc_id = $1 AND anchor = ANY($2::text[])`,
			id, diff.Changed, version,
		); err != nil {
			return nil, fmt.Errorf("stamp last_revised_in on doc %d: %w", id, err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE doc_sections SET published = true WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("publish sections of doc %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM doc_revisions WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("consume revision of doc %d: %w", id, err)
	}
	if err := supersedeReplacedDocs(tx, ts, id, eventID); err != nil {
		return nil, err
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{
			"field": "version",
			"old":   strconv.Itoa(d.version),
			"new":   strconv.Itoa(version),
		}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// GetDocRevision returns a document's open candidate revision, or ErrNotFound
// when none is open.
func (s *Store) GetDocRevision(ctx context.Context, id int64) (*model.DocRevision, error) {
	var r model.DocRevision
	var createdBy sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT doc_id, body, created_by, created_at FROM doc_revisions WHERE doc_id = $1`, id,
	).Scan(&r.Doc, &r.Body, &createdBy, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get revision of doc %d: %w", id, err)
	}
	r.CreatedBy = createdBy.String
	r.CreatedAt = r.CreatedAt.UTC()
	return &r, nil
}

// lockedDoc is the row the lifecycle writers read and lock before deciding.
type lockedDoc struct {
	kind     string
	status   string
	project  string
	slug     string
	body     string
	assignee string
	version  int
}

// lockDoc reads a document FOR UPDATE, so two accepts of one document
// serialise rather than racing.
func lockDoc(tx *sql.Tx, id int64) (lockedDoc, error) {
	var d lockedDoc
	var assignee sql.NullString
	err := tx.QueryRow(
		`SELECT kind, status, project_id, slug, body, assignee, version
		   FROM docs WHERE id = $1 FOR UPDATE`, id,
	).Scan(&d.kind, &d.status, &d.project, &d.slug, &d.body, &assignee, &d.version)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedDoc{}, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return lockedDoc{}, fmt.Errorf("load doc %d: %w", id, err)
	}
	d.assignee = assignee.String
	return d, nil
}

// checkDocAssignee enforces 025 §7's accept gate: acceptance is the assignee's
// deliberate act. A document with no assignee can be accepted by nobody, which
// is why CreateDoc defaults it to the creator.
func checkDocAssignee(id int64, assignee, actorID string) error {
	if assignee == "" {
		return fmt.Errorf("doc %d has no assignee to accept it: %w", id, ErrForbidden)
	}
	if assignee != actorID {
		return fmt.Errorf("doc %d is assigned to %s, not %s: %w", id, assignee, actorID, ErrForbidden)
	}
	return nil
}

// checkRevisionDiscarder gates withdrawing an open candidate on the document's
// assignee or the revision's author. Wider than checkDocAssignee on purpose:
// accepting is the maintainer's act, but closing a proposal without merging it
// is also the proposer's, which is what lets ReviseDoc stay open to any
// doc.write holder (025 §7.2's pull-request analogy).
//
// An empty actorID matches nobody, including a revision or document whose own
// column is empty.
func checkRevisionDiscarder(id int64, assignee, createdBy, actorID string) error {
	if actorID != "" && (actorID == assignee || actorID == createdBy) {
		return nil
	}
	return fmt.Errorf(
		"revision of doc %d was opened by %q and the doc is assigned to %q: %q may discard neither: %w",
		id, createdBy, assignee, actorID, ErrForbidden)
}

// CheckDocAcceptable re-runs AcceptDoc's gates without accepting anything, and
// returns the same sentinels: ErrNotFound, ErrForbidden for an actor that is
// not the assignee, ErrInvalidInput for a document that is not draft.
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
// did not happen, to an actor who may not even be the assignee. The gates live
// here, next to the ones AcceptDoc runs, so the two cannot drift.
func (s *Store) CheckDocAcceptable(ctx context.Context, id int64, actorID string) (settled bool, err error) {
	var kind, status, assignee string
	var assigneeCol sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT kind, status, assignee FROM docs WHERE id = $1`, id).Scan(&kind, &status, &assigneeCol)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return false, fmt.Errorf("load doc %d: %w", id, err)
	}
	assignee = assigneeCol.String
	// Assignee first, matching AcceptDoc: standing to touch the document does
	// not depend on its state, and checking state first would disclose it to
	// an actor who has none.
	if err := checkDocAssignee(id, assignee, actorID); err != nil {
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

// docEdgeRef is one frontmatter reference before resolution. ref is verbatim,
// fragment included; fromAnchor is "" for a document-level edge. coverage and
// completedWith carry a covers entry's authored level and, for a partial
// entry, its fullCoverageWith closure (026 §2.1, §5); owner carries a defers
// entry's named owner, verbatim, the same way (026 §5.3). Every other
// relation leaves all three zero.
//
// inverse marks the one spelling that writes a row with its ends the other way
// round: `blockedBy` is `blocks` authored from the blocked plan (025 §5). typ
// is already the stored type by then, so everything downstream sees a `blocks`
// edge and only the row's two ends differ.
type docEdgeRef struct {
	fromAnchor    string
	typ           string
	ref           string
	coverage      string
	completedWith []string
	owner         string
	inverse       bool
}

// docEdgeRow is one edge after resolution — exactly the tuple
// doc_edges_unique keys, so equality here is the collision the index would
// report. toDoc is 0 and toExternal non-empty for an unresolved reference.
// fromDoc is the writing document for every relation but an inverse-authored
// `blocks`, which is why it is a field rather than assumed.
// The coverage level is not part of this tuple — doc_edges_unique does not
// cover it — so rebuildEdges tracks it alongside the row in its dedupe map.
type docEdgeRow struct {
	fromDoc    int64
	fromAnchor string
	typ        string
	toDoc      int64
	toAnchor   string
	toExternal string
}

// closureRef is one resolved fullCoverageWith target: a doc id when it
// resolved, or the verbatim reference in toExternal when it did not (026
// §2.1 — unresolvable closes nothing, same as an unresolved doc_edges
// target). resolved distinguishes toDoc's zero value from "this is doc 0".
type closureRef struct {
	resolved   bool
	toDoc      int64
	toExternal string
}

// docEdgeSeen is what rebuildEdges' dedupe map remembers about a resolved
// row already seen in this frontmatter: its level, and — for a partial
// entry — its resolved fullCoverageWith closure, so a second occurrence of
// the same section can be checked for agreement on both, not just the level.
type docEdgeSeen struct {
	level   string
	closure []closureRef
}

// resolveClosure resolves a partial covers entry's fullCoverageWith list
// against project, skipping blank entries and preserving authored order. It
// doubles as the comparable value rebuildEdges uses to detect two entries
// for the same section proposing different closures.
func resolveClosure(tx *sql.Tx, project string, refs []string) ([]closureRef, error) {
	var out []closureRef
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		cwBase, _ := designdoc.SplitFragment(ref) // plans take no anchors
		cwDoc, cwResolved, err := resolveDocRef(tx, project, cwBase)
		if err != nil {
			return nil, err
		}
		if cwResolved {
			out = append(out, closureRef{resolved: true, toDoc: cwDoc})
		} else {
			// Unresolvable: kept verbatim, same as doc_edges' to_external — and
			// being unresolvable, it closes nothing (026 §2.1).
			out = append(out, closureRef{toExternal: ref})
		}
	}
	return out, nil
}

// closureEqual reports whether two resolved fullCoverageWith closures name
// the same set of targets, order irrelevant.
func closureEqual(a, b []closureRef) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := slices.Clone(a), slices.Clone(b)
	less := func(x, y closureRef) int {
		if x.resolved != y.resolved {
			if x.resolved {
				return -1
			}
			return 1
		}
		if x.toDoc != y.toDoc {
			return cmp.Compare(x.toDoc, y.toDoc)
		}
		return strings.Compare(x.toExternal, y.toExternal)
	}
	slices.SortFunc(sa, less)
	slices.SortFunc(sb, less)
	return slices.Equal(sa, sb)
}

// rebuildEdges replaces the edges a document's frontmatter declares. It
// deletes and re-inserts, so doc_edges_unique is satisfied across calls;
// doc_coverage_completed_with cascades off doc_edges, so clearing the parent
// clears it too.
//
// "Declares" rather than "outbound" because of `blockedBy:`, the one spelling
// whose row runs the other way: it stores the `blocks` row the *other* plan
// would have written (025 §5), so the row's from end is that plan while
// declared_by stays this document. Everything below therefore scopes by
// declared_by, and the two coincide for every other relation.
//
// Within one frontmatter it dedupes on the *resolved* row rather than on the
// reference: two spellings of one target ("004-x.md" and
// "docs/specs/004-x.md", or a filename and its <KEY>-SPEC-<n> shorthand) are
// one edge, and inserting both would abort a legal document on a raw unique
// violation. The dedupe map carries the coverage level and, for a partial
// entry, its resolved fullCoverageWith closure alongside the row: a repeated
// resolved target at the *same* level with the *same* closure is still one
// edge, but the same section covered twice with a different level or a
// different closure is a contradiction the frontmatter cannot mean (026
// §2.1), so that is ErrInvalidInput rather than a raw unique-index violation.
//
// A covers edge's level is normalised here — an empty entry means full — and
// validated: anything other than full/partial/none is ErrInvalidInput. The
// empty case is reached only from the object form with `coverage:` absent;
// the bare-string form decodes straight to "full"
// (designdoc.Coverage.UnmarshalYAML) and never passes through here empty. An
// object entry omitting the required key (026 §5.1) is a defect
// scripts/secmeta.py reports — this fallback just keeps it reading as full
// rather than inventing a fourth state. A partial edge's fullCoverageWith
// closure is resolved the same way doc_edges resolves its own targets and
// stored in doc_coverage_completed_with, in authored order.
//
// A defers edge (026 §5.3) is checked, not merely written: the from end must
// be a plan, the `spec` reference must carry a `#sec-N` fragment (unlike
// covers, which tolerates a whole-document claim — a whole-document deferral
// would silently defer sections not yet written), the owner must be named,
// must carry no fragment (an owner is a document, 026 §5.3 — secmeta.py
// refuses the same), and must not resolve to the deferring plan itself. The
// owner is
// then resolved exactly as a fullCoverageWith target and stored as the
// edge's sole doc_coverage_completed_with row, at position 0. coverage stays
// NULL for a defers edge — a deferral is not a level. The same entry
// authored twice is one edge, same as covers; the same section deferred to
// two different owners is the contradiction covers refuses for two
// disagreeing levels, refused here as ErrInvalidInput too.
func rebuildEdges(tx *sql.Tx, now time.Time, docID int64, kind, project string, fm *designdoc.Frontmatter) error {
	// declared_by, not from_doc: a `blockedBy:` row's from end is the *other*
	// plan (025 §5), and this document is still the one answerable for it. The
	// two coincide for every other relation, so this only widens what a
	// rewrite clears to exactly what the frontmatter put there — and, just as
	// importantly, leaves the rows the other plan declared alone.
	if _, err := tx.Exec(`DELETE FROM doc_edges WHERE declared_by = $1`, docID); err != nil {
		return fmt.Errorf("clear edges of doc %d: %w", docID, err)
	}
	// The artifact key is not an edge — it declares the catalog address(es)
	// this document is verified by (029 §3.1), which is what routes a
	// /hooks/catalog delivery to it (WL-255). Declarations are additive and
	// idempotent: removing the key from a later body does not undeclare, the
	// same as every other declaration surface.
	if fm != nil {
		for _, a := range fm.Artifact {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			if utf8.RuneCountInString(a) > maxArtifactURI {
				return fmt.Errorf("doc %d artifact %q is too long: %w", docID, a[:40]+"…", ErrInvalidInput)
			}
			if err := DeclareArtifact(tx, now, "doc", strconv.FormatInt(docID, 10), a); err != nil {
				return err
			}
		}
	}
	seen := map[docEdgeRow]docEdgeSeen{}
	for _, e := range frontmatterEdges(fm) {
		base, fragment := designdoc.SplitFragment(e.ref)
		toDoc, resolved, err := resolveDocRef(tx, project, base)
		if err != nil {
			return err
		}
		if e.typ == "blocks" {
			if err := checkPlanOrdering(tx, docID, kind, e.ref, toDoc, resolved, e.inverse); err != nil {
				return err
			}
		}
		if e.typ == "defers" {
			if kind != "plan" {
				return fmt.Errorf("doc %d defers %q, but defers is plan-only and doc %d is a %s (026 §5.3): %w",
					docID, e.ref, docID, kind, ErrInvalidInput)
			}
			if fragment == "" {
				return fmt.Errorf(
					"doc %d defers %q with no #sec-N fragment: defers is section-scoped, unlike covers (026 §5.3): %w",
					docID, e.ref, ErrInvalidInput)
			}
			if strings.TrimSpace(e.owner) == "" {
				return fmt.Errorf("doc %d defers %q with no owner: a deferral names its owner (026 §5.3): %w",
					docID, e.ref, ErrInvalidInput)
			}
			if _, ownerFragment := designdoc.SplitFragment(e.owner); ownerFragment != "" {
				return fmt.Errorf(
					"doc %d defers %q to %q: the owner is a document, no fragment (026 §5.3): %w",
					docID, e.ref, e.owner, ErrInvalidInput)
			}
		}

		level := ""
		if e.typ == "covers" {
			level = strings.TrimSpace(e.coverage)
			if level == "" {
				level = "full"
			}
			if level != "full" && level != "partial" && level != "none" {
				return fmt.Errorf("doc %d covers %q with unknown coverage level %q (026 §5.1): %w",
					docID, e.ref, level, ErrInvalidInput)
			}
		}
		var closure []closureRef
		if level == "partial" {
			closure, err = resolveClosure(tx, project, e.completedWith)
			if err != nil {
				return err
			}
		}
		if e.typ == "defers" {
			closure, err = resolveClosure(tx, project, []string{e.owner})
			if err != nil {
				return err
			}
			if len(closure) == 1 && closure[0].resolved && closure[0].toDoc == docID {
				return fmt.Errorf(
					"doc %d defers %q to itself: a plan cannot defer a section to itself (026 §5.3): %w",
					docID, e.ref, ErrInvalidInput)
			}
		}

		row := docEdgeRow{fromDoc: docID, fromAnchor: e.fromAnchor, typ: e.typ}
		if resolved {
			row.toDoc, row.toAnchor = toDoc, fragment
		} else {
			// Unresolvable: the whole reference is kept verbatim, fragment
			// included, since nothing here can say what its anchor names.
			row.toExternal = e.ref
		}
		if e.inverse {
			// `blockedBy: [Q]` is the row Q→P, the one `blocks: [P]` on Q
			// would have written. checkPlanOrdering has already refused an
			// unresolved or non-plan end, so toDoc is a plan and there is no
			// to_external case to swap. Anchors stay empty: a blocks edge is
			// document-level, and the CHECK says so.
			row.fromDoc, row.toDoc = row.toDoc, docID
		}
		if prior, ok := seen[row]; ok {
			if prior.level != level {
				return fmt.Errorf("doc %d %s %q twice, as %s and %s (026 §5.1): %w",
					docID, e.typ, e.ref, prior.level, level, ErrInvalidInput)
			}
			if level == "partial" && !closureEqual(prior.closure, closure) {
				return fmt.Errorf("doc %d %s %q twice, both %s but with different fullCoverageWith closures (026 §5.1): %w",
					docID, e.typ, e.ref, level, ErrInvalidInput)
			}
			if e.typ == "defers" && !closureEqual(prior.closure, closure) {
				return fmt.Errorf("doc %d defers %q twice, deferred to two different owners (026 §5.3): %w",
					docID, e.ref, ErrInvalidInput)
			}
			continue
		}
		seen[row] = docEdgeSeen{level: level, closure: closure}

		var coverageCol sql.NullString
		if e.typ == "covers" {
			coverageCol = sql.NullString{String: level, Valid: true}
		}
		// ON CONFLICT is reachable for one case only: both plans spelling the
		// same ordering, one with `blocks:` and one with `blockedBy:`. That is
		// the same fact twice, not a contradiction, so it stays one row and the
		// writer takes it over rather than the write failing on the unique
		// index. Every other relation's from end is docID, and the DELETE above
		// cleared those, so no cross-document collision exists to swallow.
		var edgeID int64
		if err := tx.QueryRow(
			`INSERT INTO doc_edges
			   (from_doc, from_anchor, type, to_doc, to_anchor, to_external, coverage, declared_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (from_doc, coalesce(from_anchor,''), type,
			              coalesce(to_doc, 0), coalesce(to_anchor,''), coalesce(to_external,''))
			 DO UPDATE SET declared_by = EXCLUDED.declared_by
			 RETURNING id`,
			row.fromDoc, nullText(row.fromAnchor), row.typ, nullID(row.toDoc),
			nullText(row.toAnchor), nullText(row.toExternal), coverageCol, docID,
		).Scan(&edgeID); err != nil {
			return fmt.Errorf("insert %s edge from doc %d to %q: %w", e.typ, row.fromDoc, e.ref, err)
		}

		if level != "partial" && e.typ != "defers" {
			continue
		}
		// resolveClosure already dropped blank entries, so pos here is a
		// contiguous 0-based rank — unlike ranging over the raw completedWith
		// list, which would reopen the gap resolveClosure closed. A defers
		// edge's closure is always the single resolved owner (026 §5.3), so
		// this loop writes exactly one doc_coverage_completed_with row for it.
		for pos, c := range closure {
			var toDocCol sql.NullInt64
			var toExternalCol sql.NullString
			if c.resolved {
				toDocCol = nullID(c.toDoc)
			} else {
				toExternalCol = nullText(c.toExternal)
			}
			if _, err := tx.Exec(
				`INSERT INTO doc_coverage_completed_with (edge_id, position, to_doc, to_external)
				 VALUES ($1, $2, $3, $4)`,
				edgeID, pos, toDocCol, toExternalCol,
			); err != nil {
				return fmt.Errorf("insert fullCoverageWith[%d] of doc %d covers %q: %w",
					pos, docID, e.ref, err)
			}
		}
	}
	return nil
}

// repointExternalEdges re-points the project's already-stored unresolved
// references that name newDocID, in both doc_edges and the
// doc_coverage_completed_with closure. rebuildEdges resolves a reference once,
// at write time, so without this a document written before its target existed
// would keep a dangling to_external forever and corpus import would be
// order-dependent (WL-130). Both passes are project-scoped, which is exactly
// resolveDocRef's resolution scope.
//
// Only references resolving to newDocID move: one resolving to some other
// document was already re-pointed when that document was created. Tombstoned
// referring documents are skipped: the sweep finds them for the caller rather
// than being named by them, and marking one `touched` would log a change
// against a row nothing can see (044 §4).
//
// Collapsing two spellings of one target onto one row can collide with
// doc_edges_unique, so a candidate whose re-pointed tuple another row already
// holds is deleted instead of updated (doc_coverage_completed_with cascades
// with it). Where the surviving row and the deleted one disagree on coverage
// level or closure, the lower-id row wins — which rebuildEdges would instead
// have refused as a contradiction (026 §5.1). That disagreement is deliberately
// not ErrInvalidInput here: it lives in *another* document's frontmatter, and
// failing this document's creation for it would wedge an import on an unrelated
// defect.
//
// A re-pointed document-level `replaces` edge also carries a side effect: the
// supersession cascade its replacing document could not run, because at accept
// (or accepted-at-create) time the target was not in the corpus yet. It runs
// here instead, from the replacing end, once the edge resolves — see
// supersedeReplacedFrom.
//
// The re-point is attributed to the creating document's event and logged as an
// edges change on each referring document whose rows moved.
func repointExternalEdges(tx *sql.Tx, project string, ts time.Time, newDocID, eventID int64) error {
	// Distinct referring documents whose rows changed, logged once each below.
	touched := map[int64]bool{}
	// Referring documents whose re-pointed row was a document-level `replaces`
	// edge, so the cascade is re-run from each of them below.
	replacers := map[int64]bool{}
	type externalEdge struct {
		id         int64
		fromDoc    int64
		fromAnchor string
		typ        string
		ref        string
	}
	// collectRows closes the cursor before any of the writes below run: the
	// same *sql.Tx cannot interleave a write with an open Rows.
	rows, err := tx.Query(
		`SELECT e.id, e.from_doc, coalesce(e.from_anchor,''), e.type, e.to_external
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc
		  WHERE d.project_id = $1 AND d.deleted_at IS NULL AND e.to_external IS NOT NULL
		  ORDER BY e.id`, project)
	if err != nil {
		return fmt.Errorf("read unresolved edges of project %s: %w", project, err)
	}
	candidates, err := collectRows(rows, "read unresolved edges of project "+project,
		func(r rowScanner) (externalEdge, error) {
			var c externalEdge
			err := r.Scan(&c.id, &c.fromDoc, &c.fromAnchor, &c.typ, &c.ref)
			return c, err
		})
	if err != nil {
		return err
	}

	for _, c := range candidates {
		base, fragment := designdoc.SplitFragment(c.ref)
		toDoc, resolved, err := resolveDocRef(tx, project, base)
		if err != nil {
			return err
		}
		if !resolved || toDoc != newDocID {
			continue
		}
		// Recorded before the duplicate branch: both branches leave a resolved
		// document-level `replaces` row from c.fromDoc to newDocID standing, so
		// both owe the cascade.
		if c.typ == "replaces" && c.fromAnchor == "" {
			replacers[c.fromDoc] = true
		}
		// The pre-check reads live state and candidates run in id order, so two
		// spellings of one target in one document collapse: the first
		// re-points, the second finds it and deletes itself.
		var dup int
		err = tx.QueryRow(
			`SELECT 1 FROM doc_edges
			  WHERE from_doc = $1 AND coalesce(from_anchor,'') = $2 AND type = $3
			    AND to_doc = $4 AND coalesce(to_anchor,'') = $5 AND to_external IS NULL
			    AND id <> $6`,
			c.fromDoc, c.fromAnchor, c.typ, newDocID, fragment, c.id).Scan(&dup)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check duplicate of edge %d of doc %d: %w", c.id, c.fromDoc, err)
		}
		if err == nil {
			if _, err := tx.Exec(`DELETE FROM doc_edges WHERE id = $1`, c.id); err != nil {
				return fmt.Errorf("drop duplicate edge %d of doc %d: %w", c.id, c.fromDoc, err)
			}
			touched[c.fromDoc] = true
			continue
		}
		if _, err := tx.Exec(
			`UPDATE doc_edges SET to_doc = $1, to_anchor = $2, to_external = NULL WHERE id = $3`,
			newDocID, nullText(fragment), c.id,
		); err != nil {
			return fmt.Errorf("re-point edge %d of doc %d to doc %d: %w", c.id, c.fromDoc, newDocID, err)
		}
		touched[c.fromDoc] = true
	}

	// Second pass, after the first: rows hanging off edges the first pass
	// deleted are already gone. An unresolvable closure entry closes nothing
	// (026 §2.1), so a dangling one silently changes coverage-completeness
	// answers. The primary key (edge_id, position) does not move, so there is
	// no collision case here.
	type closureRow struct {
		edgeID   int64
		fromDoc  int64
		position int
		ref      string
	}
	closureRows, err := tx.Query(
		`SELECT cw.edge_id, e.from_doc, cw.position, cw.to_external
		   FROM doc_coverage_completed_with cw
		   JOIN doc_edges e ON e.id = cw.edge_id
		   JOIN docs d ON d.id = e.from_doc
		  WHERE d.project_id = $1 AND d.deleted_at IS NULL AND cw.to_external IS NOT NULL
		  ORDER BY cw.edge_id, cw.position`, project)
	if err != nil {
		return fmt.Errorf("read unresolved closure entries of project %s: %w", project, err)
	}
	closures, err := collectRows(closureRows, "read unresolved closure entries of project "+project,
		func(r rowScanner) (closureRow, error) {
			var row closureRow
			err := r.Scan(&row.edgeID, &row.fromDoc, &row.position, &row.ref)
			return row, err
		})
	if err != nil {
		return err
	}

	for _, r := range closures {
		cwBase, _ := designdoc.SplitFragment(r.ref) // plans take no anchors
		toDoc, resolved, err := resolveDocRef(tx, project, cwBase)
		if err != nil {
			return err
		}
		if !resolved || toDoc != newDocID {
			continue
		}
		if _, err := tx.Exec(
			`UPDATE doc_coverage_completed_with SET to_doc = $1, to_external = NULL
			  WHERE edge_id = $2 AND position = $3`,
			newDocID, r.edgeID, r.position,
		); err != nil {
			return fmt.Errorf("re-point fullCoverageWith[%d] of edge %d to doc %d: %w",
				r.position, r.edgeID, newDocID, err)
		}
		touched[r.fromDoc] = true
	}

	// One row per referring document, in id order so the log is deterministic.
	// The new document is skipped: CreateDoc logs its own status change.
	for _, id := range slices.Sorted(maps.Keys(touched)) {
		if id == newDocID {
			continue
		}
		if err := logDocChange(tx, id, eventID,
			map[string]string{"field": "edges"}); err != nil {
			return err
		}
	}
	// After the edge changes are logged: the supersession is their consequence,
	// and reads that way in the state log.
	return supersedeReplacedFrom(tx, ts, replacers, eventID)
}

// supersedeReplacedFrom re-runs the supersession cascade from each document in
// replacers, for the documents whose `replaces` edge only just resolved.
//
// The cascade normally fires once, when the replacing document is accepted
// (AcceptDoc, AcceptRevision) or created accepted (CreateDoc). A corpus import
// that writes the replacing document before its target defeats that: at accept
// time the edge was still to_external and named no row, so nothing moved.
// repointExternalEdges is where that edge finally resolves, so it is also where
// the missed cascade belongs (WL-133).
//
// Two guards decide whether a replacer's cascade runs at all. A draft replacer
// has superseded nothing yet — its own accept will run the cascade, now that
// the edge resolves. A plan never cascades, matching acceptPlanDoc, which does
// not run one either. Whether a *target* moves stays supersedeReplacedDocs'
// own judgement: only an accepted one does, so a draft target is still left to
// climb 025 §7's ladder rather than being pushed past accepted.
func supersedeReplacedFrom(tx *sql.Tx, ts time.Time, replacers map[int64]bool, eventID int64) error {
	for _, from := range slices.Sorted(maps.Keys(replacers)) {
		var kind, status string
		err := tx.QueryRow(`SELECT kind, status FROM docs WHERE id = $1`, from).Scan(&kind, &status)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read status of replacing doc %d: %w", from, err)
		}
		if kind == "plan" || status == "draft" {
			continue
		}
		if err := supersedeReplacedDocs(tx, ts, from, eventID); err != nil {
			return err
		}
	}
	return nil
}

// checkPlanOrdering enforces that a blocks edge runs between two *distinct*
// plan documents (025 §5): it orders plan against plan, and
// planBlockedCondition reads it as the blocking plan's whole task set. An
// unresolved reference is refused too — nothing here can say it names a plan,
// and a to_external ordering edge would gate nothing while looking like it
// did.
//
// A plan naming its own slug resolves to itself (resolveDocRef matches within
// the project), which would wedge that plan's task set forever: with
// from_doc = to_doc its own open tasks block themselves, and while it is draft
// the unminted-set arm blocks too. A cycle through two or more plans wedges
// them the same way — each plan's tasks are held by the next plan's open set,
// so no set can ever close — and plans stay mutable at any status, so it is
// only the write closing the cycle that can catch it. Both are refused here,
// the way AddEdge refuses a child_of cycle between tasks (WL-144).
//
// docKind is the *declaring* document's own kind, which every caller already
// holds (from lockDoc or from the create input) — re-reading it here would
// cost a query per blocks edge in the frontmatter. inverse says which end of
// the row the declaring document is: false for `blocks:`, where it is the from
// end, true for `blockedBy:`, where it is the to end (025 §5). Every check
// below is about the row, so it reads the ends rather than the author — which
// is why the two spellings cannot disagree about what is legal.
func checkPlanOrdering(tx *sql.Tx, docID int64, docKind, ref string, otherDoc int64, resolved, inverse bool) error {
	if !resolved {
		return fmt.Errorf(
			"blocks edge from doc %d names %q, which no plan in this project resolves to (025 §5): %w",
			docID, ref, ErrInvalidInput)
	}
	if otherDoc == docID {
		return fmt.Errorf(
			"blocks edge from doc %d names %q, itself: a plan cannot block itself (025 §5): %w",
			docID, ref, ErrInvalidInput)
	}
	var otherKind string
	if err := tx.QueryRow(`SELECT kind FROM docs WHERE id = $1`, otherDoc).Scan(&otherKind); err != nil {
		return fmt.Errorf("read kind of doc %d: %w", otherDoc, err)
	}
	fromDoc, fromKind, toDoc, toKind := docID, docKind, otherDoc, otherKind
	if inverse {
		fromDoc, fromKind, toDoc, toKind = otherDoc, otherKind, docID, docKind
	}
	// The from end first, matching the order the two-query loop reported in.
	if fromKind != "plan" {
		return fmt.Errorf("blocks orders plan documents, but the from end (doc %d) is a %s (025 §5): %w",
			fromDoc, fromKind, ErrInvalidInput)
	}
	if toKind != "plan" {
		return fmt.Errorf("blocks orders plan documents, but the to end (doc %d) is a %s (025 §5): %w",
			toDoc, toKind, ErrInvalidInput)
	}
	back, err := blocksPath(tx, toDoc, fromDoc)
	if err != nil {
		return err
	}
	if back != nil {
		chain, err := blocksChainText(tx, append([]int64{fromDoc}, back...))
		if err != nil {
			return err
		}
		return fmt.Errorf("blocks edge from doc %d names %q, closing the cycle %s (025 §5): %w",
			docID, ref, chain, ErrInvalidInput)
	}
	return nil
}

// blocksPath returns the documents on a path from start to target over stored
// `blocks` edges — start first, target last — or nil when target is
// unreachable. checkPlanOrdering walks it from the proposed edge's *to* end
// back towards its *from* end: a path that arrives means the proposed edge
// closes a cycle.
//
// The walk reads what is stored, and rebuildEdges clears the writing
// document's own edges before re-inserting them one at a time, so a rewrite
// never trips over the row it is about to replace. Only resolved edges
// (to_doc) are walked — an unresolved reference names no document, and
// checkPlanOrdering refuses one anyway. Breadth-first over a visited set, so
// the chain reported is a shortest one and the walk terminates even on a graph
// that is already cyclic. A start == target self-edge is not reported here;
// checkPlanOrdering refuses that case before it gets this far.
func blocksPath(tx *sql.Tx, start, target int64) ([]int64, error) {
	prev := map[int64]int64{}
	visited := map[int64]bool{start: true}
	frontier := []int64{start}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]

		rows, err := tx.Query(
			`SELECT to_doc FROM doc_edges
			  WHERE from_doc = $1 AND type = 'blocks' AND to_doc IS NOT NULL`, cur)
		if err != nil {
			return nil, fmt.Errorf("walk blocks edges of doc %d: %w", cur, err)
		}
		var next []int64
		for rows.Next() {
			var to int64
			if err := rows.Scan(&to); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan blocks edge of doc %d: %w", cur, err)
			}
			next = append(next, to)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("walk blocks edges of doc %d: %w", cur, err)
		}
		rows.Close()

		for _, to := range next {
			if visited[to] {
				continue
			}
			prev[to] = cur
			if to == target {
				path := []int64{to}
				for at := to; at != start; {
					at = prev[at]
					path = append([]int64{at}, path...)
				}
				return path, nil
			}
			visited[to] = true
			frontier = append(frontier, to)
		}
	}
	return nil, nil
}

// blocksChainText renders a chain of document ids as "a blocks b blocks a", by
// slug, so a refused write names the cycle rather than just reporting one. A
// document whose slug cannot be read falls back to its id — the caller is
// already on an error path and a lookup failure must not mask what refused the
// write.
func blocksChainText(tx *sql.Tx, chain []int64) (string, error) {
	parts := make([]string, 0, len(chain))
	for _, id := range chain {
		var slug string
		if err := tx.QueryRow(`SELECT slug FROM docs WHERE id = $1`, id).Scan(&slug); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("read slug of doc %d: %w", id, err)
			}
			slug = fmt.Sprintf("doc %d", id)
		}
		parts = append(parts, slug)
	}
	return strings.Join(parts, " blocks "), nil
}

// frontmatterEdges reads the recorded relations out of fm — the walk is
// designdoc.Frontmatter.Refs, the rel set designdoc.StoredRels — in the
// deterministic order that walk fixes. rebuildEdges dedupes what comes back,
// on the resolved row rather than on the reference text.
//
// The inverse spellings (isRequiredBy, amendedBy, isReplacedBy) are what
// ActingRels leaves out: one row read backward is the inverse (025 §14), so
// writing them too would double every edge and let the two directions
// disagree.
//
// `blockedBy` is the exception, and not a second edge: it writes the same
// single `blocks` row with its two ends swapped, so `blockedBy: [plan-2]` on
// plan-3 stores exactly what `blocks: [plan-3]` on plan-2 would have (025 §5,
// WL-143). Only the row's ends move — one direction is still all that is
// stored — and the spelling exists because a numbered plan series is authored
// forward: part 3 knows it follows part 2, while part 2 may be accepted and
// spent by then. That is why it is translated to typ "blocks" here rather than
// carried as a type of its own.
//
// covers reads the retired `implements` spelling too (026 §5.1). Each entry's
// level and, for a partial entry, its fullCoverageWith closure ride along with
// the ref; rebuildEdges normalises and validates the level and resolves the
// closure. fullCoverageWith beside full or none is invalid (026 §5.1) and
// contributes nothing to any outcome, so it is dropped here rather than carried
// to a level that cannot use it.
//
// defers carries its named owner the same way a partial covers entry carries
// its fullCoverageWith closure: the ref is the deferred section and the owner
// rides beside it as docEdgeRef.owner rather than a separate walk.
// rebuildEdges resolves the owner exactly as it resolves a fullCoverageWith
// target and stores it in doc_coverage_completed_with at position 0 (026
// §5.3) — the same completion side-table a partial entry uses, because a
// deferral is that same assertion read at level zero: full coverage of this
// section arrives with the named owner.
//
// The implements edge *type* is a different subject: a component's evidence
// about its own code (026 §6.2), declared in `.worklode/implements.yaml`. That
// is 025 §11 machinery, and it is not built — so no writer emits the type here
// or anywhere else. The doc_edges CHECK admitting a value is not the same as
// something producing it; TestDocEdgeTypesWithoutWriter pins that gap so it is
// not re-diagnosed as a defect (WL-132).
//
// blocks orders whole plan documents (025 §5, §9.3) — the ordering edge that
// would otherwise need a container row to attach to. ns/ontology.ttl still
// declares wl:blocks Task-to-Task; mirroring the document-level edge there is
// WL-142.
func frontmatterEdges(fm *designdoc.Frontmatter) []docEdgeRef {
	var out []docEdgeRef
	for _, r := range fm.RefsFor(designdoc.StoredRels...) {
		e := docEdgeRef{fromAnchor: r.SrcAnchor, typ: r.Rel, ref: r.Ref}
		if r.Rel == "blockedBy" {
			e.typ, e.inverse = "blocks", true
		}
		if r.Coverage != nil {
			e.coverage = strings.TrimSpace(r.Coverage.Coverage)
			if e.coverage == "partial" {
				e.completedWith = r.Coverage.FullCoverageWith
			}
		}
		if r.Deferral != nil {
			e.owner = r.Deferral.To
		}
		out = append(out, e)
	}
	return out
}

// resolveDocRef finds the document that base names, base being a reference
// with any "#…" fragment already removed.
//
// Three forms are tried, in order: the slug, 025 §14.3's <KEY>-<TYPE>-<n>
// shorthand, and a bare corpus number. The number form must match exactly one
// spec or ADR — a project can hold a spec 25 and an ADR 25, and a reference
// that cannot say which resolves to neither.
//
// Distance decides the scope, as 025 §14.3 does: the slug and bare-number
// forms are same-project only, because a filename or a corpus number means
// nothing outside the corpus that mints it, so a cross-corpus reference in
// either form belongs in to_external. The shorthand is the one form that
// crosses, which is what it exists for — it carries the project key, and
// projects_key_format makes projects.key unique and excludes SPEC/ADR, so the
// key alone identifies the corpus and the middle token can never be one.
//
// 026 §4.3's NO-SPEC sentinel needs no case of its own: it matches none of the
// three forms, so it falls through to to_external, which is where a
// `covers: NO-SPEC` declaration belongs.
//
// A tombstone releases its slug and corpus number (migration 0034), so a live
// and a deleted document may share either. Every arm therefore prefers the live
// row and only falls back to a tombstoned one when no live row matches: a
// tombstone must not shadow the document that replaced it, and — in the number
// arm — must not count as the rival that makes a live corpus number ambiguous.
// The fallback is what keeps a reference to a deleted document resolvable at
// all, which 044 §4 needs for `lode show`.
func resolveDocRef(tx *sql.Tx, project, base string) (int64, bool, error) {
	base = strings.TrimSuffix(path.Base(base), ".md")
	if base == "" || base == "." {
		return 0, false, nil
	}

	// (deleted_at IS NULL) DESC puts the live row first; false sorts before
	// true under DESC, so a tombstone is only reached when there is none.
	var id int64
	err := tx.QueryRow(
		`SELECT id FROM docs WHERE project_id = $1 AND slug = $2
		  ORDER BY (deleted_at IS NULL) DESC, id LIMIT 1`, project, base).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("resolve doc ref %q by slug: %w", base, err)
	}

	if sh, ok := designdoc.ParseShorthand(base); ok {
		err := tx.QueryRow(
			`SELECT d.id FROM docs d JOIN projects p ON p.id = d.project_id
			  WHERE p.key = $1 AND d.kind = $2 AND d.number = $3
			  ORDER BY (d.deleted_at IS NULL) DESC, d.id LIMIT 1`,
			sh.Key, sh.Kind(), sh.Number).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, false, fmt.Errorf("resolve doc ref %q by shorthand: %w", base, err)
		}
		return 0, false, nil
	}

	// Bare numbers only — a number-*prefixed* reference is a filename, and
	// "025-documents-2.md" that matched no slug means the document is not
	// here; resolving it to spec 025 on the shared prefix would write a wrong
	// edge rather than a missing one.
	if nf, ok := designdoc.ParseNumberForm(base); ok && nf.Rest == "" {
		// Live rows first, tombstones only if there are none. Each pass is
		// LIMIT 2, so ambiguity is decided within one liveness class: two live
		// rows are ambiguous, and two tombstones are ambiguous only when no
		// live row answered.
		for _, liveness := range []string{"deleted_at IS NULL", "deleted_at IS NOT NULL"} {
			ids, err := docsByNumber(tx, project, nf.Number, liveness)
			if err != nil {
				return 0, false, fmt.Errorf("resolve doc ref %q by number: %w", base, err)
			}
			if len(ids) == 1 {
				return ids[0], true, nil
			}
			if len(ids) > 1 {
				return 0, false, nil
			}
		}
	}
	return 0, false, nil
}

// docsByNumber returns up to two spec/ADR ids in project with the given corpus
// number, restricted to one liveness class. Two is all resolveDocRef needs: it
// resolves exactly one match and calls anything more ambiguous.
func docsByNumber(tx *sql.Tx, project string, number int, liveness string) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT id FROM docs
		  WHERE project_id = $1 AND number = $2 AND kind IN ('spec','adr')
		    AND `+liveness+`
		  ORDER BY id LIMIT 2`, project, number)
	if err != nil {
		return nil, err
	}
	return scanColumn[int64](rows, "docs by number")
}

// docColumns is the SELECT list scanDoc expects, in order. The three
// tombstone columns (migration 0034) are last so positional scans elsewhere
// are unaffected by their addition; they are all-null or all-set together.
const docColumns = `id, project_id, kind, number, slug, title, body, status, version, issued, assignee, created_by, generated_by_task, created_at, updated_at, deleted_at, deleted_by, delete_justification`

// docColumnsD is docColumns under the `d` alias, for the queries that join
// docs against a table carrying a column of the same name (doc_sections.number).
var docColumnsD = qualifyColumns(docColumns, "d")

func scanDoc(row rowScanner) (*model.Doc, error) {
	var d model.Doc
	var number sql.NullInt64
	var issued sql.NullTime
	var assignee, createdBy, generatedByTask sql.NullString
	var deletedAt sql.NullTime
	var deletedBy, justification sql.NullString
	if err := row.Scan(&d.ID, &d.Project, &d.Kind, &number, &d.Slug, &d.Title, &d.Body,
		&d.Status, &d.Version, &issued, &assignee, &createdBy, &generatedByTask,
		&d.CreatedAt, &d.UpdatedAt,
		&deletedAt, &deletedBy, &justification); err != nil {
		return nil, err
	}
	d.Tombstone = tombstoneFrom(deletedAt, deletedBy, justification)
	d.Number = int(number.Int64)
	if issued.Valid {
		d.Issued = issued.Time.Format(docDateLayout)
	}
	d.Assignee = assignee.String
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

// ResolveDocRef resolves a document reference to its row (025 §14.3): a
// positive integer is the id itself, anything else is matched against slugs,
// exact match only — corpus-number and SPEC/ADR shorthand resolution stay
// unbuilt. The rule lives here, beside the data, so resolving a ref costs one
// indexed lookup instead of a listing of the whole corpus, and so every
// client answers a given ref the same way.
//
// Slugs are unique per project, not globally, so a slug naming documents in
// two projects is ErrInvalidInput rather than an arbitrary pick; the caller
// disambiguates with a numeric id. A slug matching no live document falls
// back to the tombstoned ones — 044 §4 keeps a deleted row addressable, and
// `lode doc undelete <slug>` has no other way to name it. Live documents win
// outright, since the fallback applies only when no live document matched, so
// a tombstone never shadows a live document.
func (s *Store) ResolveDocRef(ctx context.Context, ref string) (*model.Doc, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		return s.GetDoc(ctx, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+docColumns+` FROM docs WHERE slug = $1 ORDER BY project_id, id`, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve doc %q: %w", ref, err)
	}
	matches, err := collectRows(rows, "resolve doc", byValue(scanDoc))
	if err != nil {
		return nil, err
	}
	var live []model.Doc
	for _, d := range matches {
		if d.Tombstone == nil {
			live = append(live, d)
		}
	}
	if len(live) > 0 {
		matches = live
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		return nil, fmt.Errorf("no document with id or slug %q: %w", ref, ErrNotFound)
	default:
		return nil, fmt.Errorf("slug %q matches %d documents; pass a numeric id to disambiguate: %w",
			ref, len(matches), ErrInvalidInput)
	}
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

// NeedsPlanning returns the accepted specs that have at least one section no
// accepted or superseded plan discharges, each with the anchors that made it
// a gap and why (026 §2.1). project narrows the answer; "" answers over every
// project.
//
// The discharging set is not `accepted` alone: 026 §2.1's "A superseded plan
// discharges what it covered" reads the status as "not draft", since a
// superseded plan is one that was accepted and then carried out (025 §9's "a
// plan is spent once executed") — reading the set as `accepted` alone would
// report a shipped third of the corpus as unplanned work. A section is
// discharged when some such plan's `covers` edge claims it `full`, or claims
// it `partial` with a `fullCoverageWith` set that closes: every named plan is
// itself accepted or superseded and itself contributes `full` or `partial` to
// that same section. `fullCoverageWith` is checked, never taken on trust — an
// empty list, an unresolved reference, a draft target, a `none` target, or a
// target that does not itself cover the section all leave it open.
//
// An undischarged section is classified by the strongest reading that holds,
// in order: "partial" when some accepted-or-superseded plan claims it
// `partial` (whether or not that claim closed); "deferred" when none claims
// `partial` but some such plan hands it off to a named owner with `defers`
// (026 §5.3) — the report names the owner, recovered from the same
// doc_coverage_completed_with row a partial entry's fullCoverageWith uses,
// because a deferral is that same assertion read at level zero; "bound-only"
// when every accepted-or-superseded plan naming it claims `none`; "unplanned"
// when no such plan names it, deferral included, at all. A deferral is
// delivered by any covering plan discharging the section under the rules
// above, so it is checked against the same `cov`/`closed` machinery as
// covers, not against who was named.
//
// Four further consequences are deliberate:
//
//   - A whole-document edge (to_anchor IS NULL) discharges nothing. It cannot
//     say which present section it undertakes and would silently claim future
//     ones (026 §2.1), so it never appears in the discharged set.
//   - `covers: NO-SPEC` resolves to no row and lands in to_external (026
//     §4.3), so it falls out of the join without a case of its own.
//   - Only an accepted spec and an accepted-or-superseded plan participate: a
//     draft spec is not yet owed planning, and a draft plan has not yet
//     undertaken work — its `defers` entries do not classify a section either.
//     A tombstoned document participates on neither end (044 §4) — it is
//     neither owed planning nor able to discharge or defer a section.
//   - A deferral's owner is reported however it resolved at write time: a
//     slug when the reference named a live document, the reference text
//     verbatim (`w.to_external`) when it did not — the same fallback
//     fullCoverageWith uses.
//
// A plan naming itself in its own `fullCoverageWith` closes its own section.
// §2.1's closure test is only that each named plan is accepted or superseded
// and contributes `full` or `partial` — it says nothing about the naming plan
// — so this is not a bug; narrowing it to siblings would be a spec change
// (tracked in docs/follow-ups.md).
func (s *Store) NeedsPlanning(ctx context.Context, project string) ([]model.Doc, []model.DocPlanningGap, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH cov AS (
		     SELECT e.id, e.from_doc AS plan_id, e.to_doc AS doc_id,
		            e.to_anchor AS anchor, e.coverage
		       FROM doc_edges e
		       JOIN docs p ON p.id = e.from_doc
		      WHERE e.type = 'covers'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		        AND p.kind = 'plan' AND p.status IN ('accepted','superseded')
		        AND p.deleted_at IS NULL
		 ),
		 def_raw AS (
		     SELECT e.to_doc AS doc_id, e.to_anchor AS anchor,
		            coalesce(owner_doc.slug, w.to_external) AS owner
		       FROM doc_edges e
		       JOIN docs p ON p.id = e.from_doc
		       JOIN doc_coverage_completed_with w ON w.edge_id = e.id
		       LEFT JOIN docs owner_doc ON owner_doc.id = w.to_doc
		      WHERE e.type = 'defers'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		        AND p.kind = 'plan' AND p.status IN ('accepted','superseded')
		        AND p.deleted_at IS NULL
		 ),
		 def AS (
		     SELECT doc_id, anchor,
		            -- Comma without a space: the CLI joins anchors with spaces,
		            -- so a spaced separator would split one gap across tokens.
		            string_agg(DISTINCT owner, ',' ORDER BY owner) AS owner
		       FROM def_raw
		      GROUP BY doc_id, anchor
		 ),
		 closed AS (
		     SELECT c.id
		       FROM cov c
		      WHERE c.coverage = 'partial'
		        AND EXISTS (SELECT 1 FROM doc_coverage_completed_with w
		                     WHERE w.edge_id = c.id)
		        AND NOT EXISTS (
		              SELECT 1 FROM doc_coverage_completed_with w
		               WHERE w.edge_id = c.id
		                 AND NOT EXISTS (
		                       SELECT 1 FROM cov o
		                        WHERE o.plan_id = w.to_doc
		                          AND o.doc_id = c.doc_id AND o.anchor = c.anchor
		                          AND o.coverage IN ('full','partial')))
		 ),
		 resolved AS (
		     SELECT c.doc_id, c.anchor,
		            bool_or(c.coverage = 'full' OR cl.id IS NOT NULL) AS discharged,
		            bool_or(c.coverage = 'partial')                   AS any_partial
		       FROM cov c
		       LEFT JOIN closed cl ON cl.id = c.id
		      GROUP BY c.doc_id, c.anchor
		 )
		 SELECT `+docColumnsD+`, count(*)::int,
		        coalesce(json_agg(json_strip_nulls(json_build_object(
		                     'anchor', sec.anchor,
		                     'coverage', CASE WHEN coalesce(r.any_partial, false) THEN 'partial'
		                                      WHEN def.doc_id IS NOT NULL         THEN 'deferred'
		                                      WHEN r.doc_id IS NOT NULL           THEN 'bound-only'
		                                      ELSE 'unplanned' END,
		                     'owner', CASE WHEN NOT coalesce(r.any_partial, false)
		                                        AND def.doc_id IS NOT NULL
		                                   THEN def.owner END))
		                 ORDER BY sec.position)
		                 FILTER (WHERE r.discharged IS NOT TRUE), '[]')::text
		   FROM docs d
		   JOIN doc_sections sec ON sec.doc_id = d.id
		   LEFT JOIN resolved r ON r.doc_id = sec.doc_id AND r.anchor = sec.anchor
		   LEFT JOIN def ON def.doc_id = sec.doc_id AND def.anchor = sec.anchor
		  WHERE d.kind = 'spec' AND d.status = 'accepted'
		    AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		  GROUP BY d.id
		 HAVING count(*) FILTER (WHERE r.discharged IS NOT TRUE) > 0
		  ORDER BY d.project_id, d.number NULLS LAST, d.slug`, project)
	if err != nil {
		return nil, nil, fmt.Errorf("list specs needing planning: %w", err)
	}
	defer rows.Close()

	var docs []model.Doc
	var gaps []model.DocPlanningGap
	for rows.Next() {
		var gap model.DocPlanningGap
		var gapsJSON string
		d, err := scanDoc(appendScan{rows, []any{&gap.Sections, &gapsJSON}})
		if err != nil {
			return nil, nil, fmt.Errorf("scan spec needing planning: %w", err)
		}
		if err := json.Unmarshal([]byte(gapsJSON), &gap.Gaps); err != nil {
			return nil, nil, fmt.Errorf("decode planning gaps of doc %d: %w", d.ID, err)
		}
		gap.Doc = d.ID
		docs = append(docs, *d)
		gaps = append(gaps, gap)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list specs needing planning: %w", err)
	}
	return docs, gaps, nil
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

// NeedsExecution returns the accepted plans whose task set holds at least one
// live task that is not closed. project narrows the answer; "" answers over
// every project. "Closed" is taskClosed's notion, shared with the ready set and
// the blocks predicate, so the three cannot drift on what done means; a
// tombstoned task is out on top of that (044 §4), matching planUnfinished.
//
// This departs from 025 §18's "unminted or unfinished" deliberately, as the
// 2026-08-03 plan-acceptance plan records: the accepted plans with no task set
// at all are the importer's *spent* plans, which must not be reported as
// pending work. The ordering need §18's "unminted" arm served is covered by
// the plan-to-plan blocks predicate (planBlockedCondition).
//
// A declaration added to an accepted plan and not yet re-accepted (025 §9.2)
// is invisible here, because whether one exists is a fact about the body and
// not about any row. Re-accepting the plan is what makes it visible; nothing
// SQL can see says it is owed.
func (s *Store) NeedsExecution(ctx context.Context, project string) ([]model.Doc, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+docColumnsD+`
		   FROM docs d
		  WHERE d.kind = 'plan' AND d.status = 'accepted'
		    AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		    AND EXISTS (SELECT 1 FROM tasks t
		                 WHERE t.plan_doc = d.id AND t.deleted_at IS NULL
		                   AND NOT `+taskClosed("t")+`)
		  ORDER BY d.project_id, d.slug`, project)
	if err != nil {
		return nil, fmt.Errorf("list plans needing execution: %w", err)
	}
	return collectRows(rows, "list plans needing execution", byValue(scanDoc))
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

// docEdgeInverse names the reading of each edge type from the far end (025
// §14): one row carries both directions, so an inbound edge is the stored row
// relabelled rather than a second row that could disagree. Every type in the
// doc_edges CHECK has an entry; ListDocEdges refuses a type that does not,
// because emitting the forward name would state the relation backwards.
var docEdgeInverse = map[string]string{
	"covers":         "isCoveredBy",
	"implements":     "isImplementedBy",
	"amends":         "amendedBy",
	"replaces":       "isReplacedBy",
	"requires":       "isRequiredBy",
	"wasDerivedFrom": "hadDerivation",
	"blocks":         "blockedBy",
	"defers":         "isDeferredBy",
}

// ListDocEdges returns a document's edges in both directions: out are the
// edges leaving it, in are the edges other documents point at it with, each
// read backward — the type carries its inverse spelling and ToDoc names the
// other end, so a caller can link to it. For an inbound edge FromAnchor is the
// anchor in docID the edge lands on and ToAnchor the anchor it left from; an
// inbound edge never has ToExternal, since an unresolved reference names no
// row here.
//
// Both lists are edges of this document, not necessarily declarations by it:
// a plan's `blockedBy` writes the row from the *other* plan (025 §5), so it
// shows up outbound there and inbound here. Direction is the relation, never
// authorship.
//
// Each resolved far end is named as well as identified: one join carries the
// other document's slug, kind and number back with its id, so a caller can
// render "spec 25" instead of "document 42" without a query per edge. An
// unresolved outbound edge (to_external) joins to nothing and leaves them
// empty.
//
// Inbound edges from a tombstoned document are not listed: hiding a document
// hides the edges leaving it. Outbound edges are unfiltered — they are this
// document's own view, and a deleted target is still resolvable by id.
//
// Both lists are fully ordered, so a caller may compare them as sequences.
func (s *Store) ListDocEdges(ctx context.Context, docID int64) (out, in []model.DocEdge, err error) {
	outRows, err := s.db.QueryContext(ctx,
		`SELECT e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc,0),
		        coalesce(e.to_anchor,''), coalesce(e.to_external,''),
		        coalesce(d.slug,''), coalesce(d.kind,''), coalesce(d.number,0)
		   FROM doc_edges e LEFT JOIN docs d ON d.id = e.to_doc
		  WHERE e.from_doc = $1
		  ORDER BY e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc,0),
		           coalesce(e.to_anchor,''), coalesce(e.to_external,'')`, docID)
	if err != nil {
		return nil, nil, fmt.Errorf("list edges out of doc %d: %w", docID, err)
	}
	out, err = scanDocEdges(outRows)
	if err != nil {
		return nil, nil, fmt.Errorf("list edges out of doc %d: %w", docID, err)
	}

	// from_doc and to_anchor swap into the reader's frame: the row is read
	// from docID's end, so what the writer called its target anchor is the
	// anchor here, and its source anchor is the far one.
	inRows, err := s.db.QueryContext(ctx,
		`SELECT e.type, coalesce(e.to_anchor,''), e.from_doc, coalesce(e.from_anchor,''), '',
		        d.slug, d.kind, coalesce(d.number,0)
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc
		  WHERE e.to_doc = $1 AND d.deleted_at IS NULL
		  ORDER BY e.type, coalesce(e.to_anchor,''), e.from_doc, coalesce(e.from_anchor,'')`, docID)
	if err != nil {
		return nil, nil, fmt.Errorf("list edges into doc %d: %w", docID, err)
	}
	in, err = scanDocEdges(inRows)
	if err != nil {
		return nil, nil, fmt.Errorf("list edges into doc %d: %w", docID, err)
	}
	for i := range in {
		inverse, ok := docEdgeInverse[in[i].Type]
		if !ok {
			return nil, nil, fmt.Errorf(
				"internal: doc edge type %q has no declared inverse (store.docEdgeInverse)", in[i].Type)
		}
		in[i].Type = inverse
	}
	return out, in, nil
}

// scanDocEdges drains a query selecting the DocEdge columns in order: the
// five stored ones, then the joined far end's slug, kind and number.
func scanDocEdges(rows *sql.Rows) ([]model.DocEdge, error) {
	defer rows.Close()
	var out []model.DocEdge
	for rows.Next() {
		var e model.DocEdge
		if err := rows.Scan(&e.Type, &e.FromAnchor, &e.ToDoc, &e.ToAnchor, &e.ToExternal,
			&e.ToSlug, &e.ToKind, &e.ToNumber); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordDocEvent wraps RecordEvent for a document mutation, recording
// worklode_doc_operations_total{op,outcome}. op is one of
// create|update|accept|revise|discard|edges.
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

// RecordPlanTasksMinted records n tasks minted by one plan accept
// (worklode_doc_plan_tasks_minted_total). AcceptDoc is a package-level
// function with no *Store to record through, so the caller — the API's
// acceptDoc handler — calls this once the accepting transaction has
// committed, with the length of AcceptDoc's minted-task return. Nil-safe:
// a store opened without WithMetrics records nothing.
func (s *Store) RecordPlanTasksMinted(n int) {
	s.metrics.planTasksMinted(n)
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
