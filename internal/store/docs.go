package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
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
	// Status is honoured only by the corpus importer; the API's create path
	// always leaves it empty, which means draft.
	Status string
}

// DocFilter narrows ListDocs. Zero-valued fields do not filter.
type DocFilter struct {
	Project string
	Kind    string
	Status  string
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
// depth gate runs here too, and the sections land published.
func CreateDoc(tx *sql.Tx, now time.Time, in DocInput, eventID int64) (*model.Doc, error) {
	if !validDocKinds[in.Kind] {
		return nil, fmt.Errorf("doc kind %q: %w", in.Kind, ErrInvalidInput)
	}
	if in.Slug == "" {
		return nil, fmt.Errorf("doc slug must not be empty: %w", ErrInvalidInput)
	}
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
	// Created accepted: run AcceptDoc's first-accept gate. There is no accepted
	// version to diff against, so only TooDeep can fire — Removed and
	// Renumbered come back empty by construction. Plans skip it: they carry no
	// sections and no anchors (025 §9).
	acceptedAtCreate := status == "accepted" && in.Kind != "plan"
	if acceptedAtCreate {
		diff := designdoc.CompareSections(&designdoc.Document{}, parsed.doc, designdoc.DepthLimit)
		if v := diff.Violations(); len(v) > 0 {
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
		                   issued, assignee, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8::date, $9, $10, $11, $11)
		 RETURNING id`,
		in.Project, in.Kind, number, in.Slug, title, in.Body, status,
		nullText(parsed.issued), nullText(assignee), nullText(in.CreatedBy), ts,
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
	if err := rebuildEdges(tx, id, in.Project, parsed.doc.Frontmatter); err != nil {
		return nil, err
	}
	if err := LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
		map[string]string{"field": "status", "new": status}); err != nil {
		return nil, err
	}

	return &model.Doc{
		ID: id, Project: in.Project, Kind: in.Kind, Number: in.Number,
		Slug: in.Slug, Title: title, Body: in.Body, Status: status, Version: 1,
		Issued: parsed.issued, Assignee: assignee, CreatedBy: in.CreatedBy,
		CreatedAt: ts, UpdatedAt: ts,
	}, nil
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
	ts := now.UTC().Truncate(time.Second)

	// The frontmatter is part of the body, so title and issued are rederived
	// from it here for the same reason CreateDoc derives them: the body is
	// what states them. issued only ever moves forward, though — a plan stays
	// mutable at accepted (025 §9), and a body edit that drops the key must
	// not erase the acceptance date, which is a lifecycle fact rather than a
	// property of the text.
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, title = $3, issued = coalesce($4::date, issued),
		                 updated_at = $5
		  WHERE id = $1`,
		id, body, title, nullText(parsed.issued), ts,
	); err != nil {
		return nil, fmt.Errorf("update doc %d body: %w", id, err)
	}
	if err := rebuildSections(tx, id, kind, parsed.doc, version); err != nil {
		return nil, err
	}
	if err := rebuildEdges(tx, id, project, parsed.doc.Frontmatter); err != nil {
		return nil, err
	}
	if err := LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
		map[string]string{"field": "body"}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// ReplaceDocEdges re-resolves a document's outbound edges from its stored
// body and appends a state_log row attributed to eventID. It is the corpus
// import's second pass: the frontmatter references that resolved to nothing
// when the document was created become real edges once the rest of the corpus
// is present.
//
// Nothing authored changes — not the body, not the sections, not the status,
// and the version does not move: the same source is being read again against a
// larger corpus. That is why, unlike UpdateDocBody, it works at any status
// including accepted and superseded; there is no published anchor to protect
// because no anchor is being restated.
//
// The clock is unused (nothing here is timestamped) and taken only so the
// signature matches the other document writers.
func ReplaceDocEdges(tx *sql.Tx, _ time.Time, id, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return err
	}
	if err := rebuildEdges(tx, id, d.project, parsed.doc.Frontmatter); err != nil {
		return err
	}
	return LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
		map[string]string{"field": "edges"})
}

// AcceptDoc is the manual commit of 025 §7: draft -> accepted, gated on the
// assignee. For a spec or ADR it freezes the document's published anchor set
// and flips the target of every document-level replaces edge to superseded,
// in the same transaction. For a plan it mints the plan's execution tasks
// instead (see acceptPlanDoc) — the second return is that minted set, in
// definition order, and nil for a spec or ADR.
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
	// Draft-only applies to both branches: a plan already accepted must never
	// mint a second time, which is what keeps doc.status = accepted ⟺ its
	// tasks exist true by construction (025 §9.2).
	if d.status != "draft" {
		return nil, nil, fmt.Errorf("doc %d is %s, not draft: %w", id, d.status, ErrInvalidInput)
	}

	if d.kind == "plan" {
		return acceptPlanDoc(tx, now, id, d, actorID, eventID)
	}

	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, nil, err
	}
	// No accepted version to diff against, so the comparison runs against an
	// empty document: Removed and Renumbered come back empty by construction
	// and TooDeep carries the one rule a first accept still enforces.
	diff := designdoc.CompareSections(&designdoc.Document{}, parsed.doc, designdoc.DepthLimit)
	if v := diff.Violations(); len(v) > 0 {
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
	if err := LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
		map[string]string{"field": "status", "old": d.status, "new": "accepted"}); err != nil {
		return nil, nil, err
	}
	doc, err := getDocTx(tx, id)
	return doc, nil, err
}

// acceptPlanDoc is AcceptDoc's plan branch (025 §9.2): parse the plan body's
// ## Tasks declarations, mint one draft task per definition with plan_doc set
// to id, wire each definition's blockedBy numbers as blocks edges between the
// minted tasks, then flip the document to accepted — all inside the caller's
// transaction, so accept and mint are one commit and a failed mint leaves the
// document draft.
//
// Plans carry no sections and no anchors (025 §9), so none of the spec/ADR
// branch's section or diff machinery runs here: there is nothing to publish
// and no CompareSections gate to evaluate. d.status is already known draft —
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

	// First pass mints every task and records its minted id by definition
	// number; second pass wires blockedBy once every number resolves, so a
	// forward reference (task 1 blockedBy task 2) needs no reordering.
	mintedID := make(map[int]string, len(defs))
	tasks := make([]model.Task, 0, len(defs))
	for _, def := range defs {
		task, err := CreateTask(tx, now, TaskInput{
			ProjectID: d.project,
			Title:     def.Title,
			Body:      def.Body,
			Priority:  def.Priority,
			Kind:      def.Kind,
			Skills:    def.Skills,
			CreatedBy: actorID,
			Draft:     true,
			PlanDoc:   id,
		}, eventID)
		if err != nil {
			return nil, nil, fmt.Errorf("mint task %d of plan %d: %w", def.Number, id, err)
		}
		mintedID[def.Number] = task.ID
		tasks = append(tasks, *task)
	}
	for _, def := range defs {
		for _, blocker := range def.BlockedBy {
			if err := AddEdge(tx, now, mintedID[blocker], mintedID[def.Number], "blocks", eventID); err != nil {
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
	if err := LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
		map[string]string{"field": "status", "old": d.status, "new": "accepted"}); err != nil {
		return nil, nil, err
	}
	doc, err := getDocTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	return doc, tasks, nil
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
	return LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
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
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("update revision of doc %d: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	return LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
		map[string]string{"field": "revision", "new": "updated"})
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
	if err := rebuildSections(tx, id, d.kind, candidate.doc, version); err != nil {
		return nil, err
	}
	if err := rebuildEdges(tx, id, d.project, candidate.doc.Frontmatter); err != nil {
		return nil, err
	}

	// The diff gate is what keeps a published anchor from being dropped; this
	// checks that it did. A failure here is a bug in the gate, not bad input,
	// so it carries no sentinel.
	after, err := priorSections(tx, id)
	if err != nil {
		return nil, err
	}
	for anchor, p := range prior {
		if _, still := after[anchor]; p.published && !still {
			return nil, fmt.Errorf(
				"internal: doc %d lost published anchor #%s in the section rebuild", id, anchor)
		}
	}

	// 025 §6 rule 5: last_revised_in moves on exactly the sections whose
	// content changed. Touching it elsewhere invalidates valid claims.
	for _, anchor := range diff.Changed {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET last_revised_in = $3 WHERE doc_id = $1 AND anchor = $2`,
			id, anchor, version,
		); err != nil {
			return nil, fmt.Errorf("stamp last_revised_in on #%s of doc %d: %w", anchor, id, err)
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
	if err := LogChange(tx, docEntityKind, strconv.FormatInt(id, 10), eventID,
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

// CheckDocAcceptable re-runs AcceptDoc's gates without accepting anything, and
// returns the same sentinels: ErrNotFound, ErrForbidden for an actor that is
// not the assignee, ErrInvalidInput for a document that is not draft.
//
// It exists for one caller. The typed accept emission (025 §15.3) derives its
// external id from the document's IRI and version, so a second accept of the
// same version conflicts at the log and eventbus.Emit skips apply — which
// means AcceptDoc's gates never run and the handler has no store answer to
// return. Without this the request would report success for an accept that
// did not happen, to an actor who may not even be the assignee. The gates live
// here, next to the ones AcceptDoc runs, so the two cannot drift.
func (s *Store) CheckDocAcceptable(ctx context.Context, id int64, actorID string) error {
	var status, assignee string
	var assigneeCol sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT status, assignee FROM docs WHERE id = $1`, id).Scan(&status, &assigneeCol)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load doc %d: %w", id, err)
	}
	assignee = assigneeCol.String
	// Assignee first, matching AcceptDoc: standing to touch the document does
	// not depend on its state, and checking state first would disclose it to
	// an actor who has none.
	if err := checkDocAssignee(id, assignee, actorID); err != nil {
		return err
	}
	if status != "draft" {
		return fmt.Errorf("doc %d is %s, not draft: %w", id, status, ErrInvalidInput)
	}
	return nil
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
func supersedeReplacedDocs(tx *sql.Tx, ts time.Time, docID, eventID int64) error {
	rows, err := tx.Query(
		`SELECT DISTINCT to_doc FROM doc_edges
		  WHERE from_doc = $1 AND type = 'replaces'
		    AND from_anchor IS NULL AND to_doc IS NOT NULL AND to_doc <> $1
		  ORDER BY to_doc`, docID)
	if err != nil {
		return fmt.Errorf("read replaces edges of doc %d: %w", docID, err)
	}
	defer rows.Close()
	var targets []int64
	for rows.Next() {
		var target int64
		if err := rows.Scan(&target); err != nil {
			return fmt.Errorf("scan replaces edge of doc %d: %w", docID, err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read replaces edges of doc %d: %w", docID, err)
	}

	for _, target := range targets {
		res, err := tx.Exec(
			`UPDATE docs SET status = 'superseded', updated_at = $2
			  WHERE id = $1 AND status = 'accepted'`, target, ts)
		if err != nil {
			return fmt.Errorf("supersede doc %d: %w", target, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("supersede doc %d: %w", target, err)
		}
		if n == 0 {
			continue
		}
		if err := LogChange(tx, docEntityKind, strconv.FormatInt(target, 10), eventID,
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
// preserving last_revised_in and published for every anchor that survives:
// those are accept-time facts about the section, not facts about the current
// text. A new anchor starts unpublished at the document's current version.
// Plans have no sections (025 §9), so nothing is written for one.
func rebuildSections(tx *sql.Tx, docID int64, kind string, doc *designdoc.Document, version int) error {
	if kind == "plan" {
		return nil
	}
	prior, err := priorSections(tx, docID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_sections WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("clear sections of doc %d: %w", docID, err)
	}
	position := 0
	for _, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		lastRevisedIn, published := version, false
		if p, ok := prior[sec.Anchor]; ok {
			lastRevisedIn, published = p.lastRevisedIn, p.published
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_sections (doc_id, anchor, number, heading, depth, position, last_revised_in, published)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			docID, sec.Anchor, nullText(sec.Number), sec.Title, sec.Level, position, lastRevisedIn, published,
		); err != nil {
			return fmt.Errorf("insert section #%s of doc %d: %w", sec.Anchor, docID, err)
		}
		position++
	}
	return nil
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
// fragment included; fromAnchor is "" for a document-level edge.
type docEdgeRef struct {
	fromAnchor string
	typ        string
	ref        string
}

// docEdgeRow is one edge after resolution — exactly the tuple
// doc_edges_unique keys, so equality here is the collision the index would
// report. toDoc is 0 and toExternal non-empty for an unresolved reference.
type docEdgeRow struct {
	fromAnchor string
	typ        string
	toDoc      int64
	toAnchor   string
	toExternal string
}

// rebuildEdges replaces a document's outbound edges from its frontmatter. It
// deletes and re-inserts, so doc_edges_unique is satisfied across calls.
//
// Within one frontmatter it dedupes on the *resolved* row rather than on the
// reference: two spellings of one target ("004-x.md" and
// "docs/specs/004-x.md", or a filename and its <KEY>-SPEC-<n> shorthand) are
// one edge, and inserting both would abort a legal document on a raw unique
// violation.
func rebuildEdges(tx *sql.Tx, docID int64, project string, fm *designdoc.Frontmatter) error {
	if _, err := tx.Exec(`DELETE FROM doc_edges WHERE from_doc = $1`, docID); err != nil {
		return fmt.Errorf("clear edges of doc %d: %w", docID, err)
	}
	seen := map[docEdgeRow]bool{}
	for _, e := range frontmatterEdges(fm) {
		base, fragment := cutFragment(e.ref)
		toDoc, resolved, err := resolveDocRef(tx, project, base)
		if err != nil {
			return err
		}
		if e.typ == "blocks" {
			if err := checkPlanOrdering(tx, docID, e.ref, toDoc, resolved); err != nil {
				return err
			}
		}
		row := docEdgeRow{fromAnchor: e.fromAnchor, typ: e.typ}
		if resolved {
			row.toDoc, row.toAnchor = toDoc, fragment
		} else {
			// Unresolvable: the whole reference is kept verbatim, fragment
			// included, since nothing here can say what its anchor names.
			row.toExternal = e.ref
		}
		if seen[row] {
			continue
		}
		seen[row] = true
		if _, err := tx.Exec(
			`INSERT INTO doc_edges (from_doc, from_anchor, type, to_doc, to_anchor, to_external)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, nullText(row.fromAnchor), row.typ, nullID(row.toDoc),
			nullText(row.toAnchor), nullText(row.toExternal),
		); err != nil {
			return fmt.Errorf("insert %s edge from doc %d to %q: %w", e.typ, docID, e.ref, err)
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
// the unminted-set arm blocks too. Cycles through two or more plans are
// reachable the same way and are not detected here (WL-144).
func checkPlanOrdering(tx *sql.Tx, docID int64, ref string, toDoc int64, resolved bool) error {
	if !resolved {
		return fmt.Errorf(
			"blocks edge from doc %d names %q, which no plan in this project resolves to (025 §5): %w",
			docID, ref, ErrInvalidInput)
	}
	if toDoc == docID {
		return fmt.Errorf(
			"blocks edge from doc %d names %q, itself: a plan cannot block itself (025 §5): %w",
			docID, ref, ErrInvalidInput)
	}
	for _, end := range []struct {
		id   int64
		side string
	}{{docID, "from"}, {toDoc, "to"}} {
		var kind string
		if err := tx.QueryRow(`SELECT kind FROM docs WHERE id = $1`, end.id).Scan(&kind); err != nil {
			return fmt.Errorf("read kind of doc %d: %w", end.id, err)
		}
		if kind != "plan" {
			return fmt.Errorf("blocks orders plan documents, but the %s end (doc %d) is a %s (025 §5): %w",
				end.side, end.id, kind, ErrInvalidInput)
		}
	}
	return nil
}

// frontmatterEdges reads the acting-direction relations out of fm, in a
// deterministic order. rebuildEdges dedupes what comes back, on the resolved
// row rather than on the reference text.
//
// The inverse spellings (isRequiredBy, blockedBy, amendedBy, isReplacedBy) are
// skipped: one row read backward is the inverse (025 §14), so writing them too
// would double every edge and let the two directions disagree. For plan
// ordering that means only the blocking plan can declare it — `blockedBy:`
// parses and writes nothing (WL-143).
//
// An empty reference is dropped rather than written as an empty to_external —
// a coverage entry qualified with a level but no `spec:`, say, names no
// target at all.
func frontmatterEdges(fm *designdoc.Frontmatter) []docEdgeRef {
	if fm == nil {
		return nil
	}
	var out []docEdgeRef
	add := func(anchor, typ, ref string) {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, docEdgeRef{fromAnchor: anchor, typ: typ, ref: ref})
		}
	}
	// covers reads the retired `implements` spelling too (026 §5.1); the
	// implements edge type stays reserved for components.
	for _, entry := range fm.CoverageEntries() {
		add("", "covers", entry.Spec)
	}
	for _, ref := range fm.Requires {
		add("", "requires", ref)
	}
	// blocks orders whole plan documents (025 §5, §9.3) — the ordering edge
	// that would otherwise need a container row to attach to. ns/ontology.ttl
	// still declares wl:blocks Task-to-Task; mirroring the document-level edge
	// there is WL-142.
	for _, ref := range fm.Blocks {
		add("", "blocks", ref)
	}
	add("", "wasDerivedFrom", fm.WasDerivedFrom)
	for _, m := range []struct {
		typ string
		src designdoc.AnchorMap
	}{{"amends", fm.Amends}, {"replaces", fm.Replaces}} {
		keys := make([]string, 0, len(m.src))
		for k := range m.src {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// "." is the document-level subject; anything else is one of this
			// document's own anchors, with or without its leading '#'.
			anchor := ""
			if k != "." {
				anchor = strings.TrimPrefix(k, "#")
			}
			for _, ref := range m.src[k] {
				add(anchor, m.typ, ref)
			}
		}
	}
	return out
}

// docBareNumber is a reference that is nothing but a corpus number, with or
// without zero-padding ("25", "025"). Deliberately anchored end-to-end: a
// number-*prefixed* reference is a filename, and "025-documents-2.md" that
// matched no slug means the document is not here — resolving it to spec 025
// on the shared prefix would write a wrong edge rather than a missing one.
var docBareNumber = regexp.MustCompile(`^(\d+)$`)

// docShorthand is 025 §14.3's <KEY>-<TYPE>-<n> reference, e.g. "WL-SPEC-25".
var docShorthand = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR)-(\d+)$`)

// resolveDocRef finds the document in project that base names, base being a
// reference with any "#…" fragment already removed. Resolution is
// same-project only: a cross-corpus reference has no row here and belongs in
// to_external (025 §14.3).
//
// Three forms are tried, in order: the slug, 025 §14.3's <KEY>-<TYPE>-<n>
// shorthand against this project's key, and a bare corpus number. The number
// form must match exactly one spec or ADR — a project can hold a spec 25 and
// an ADR 25, and a reference that cannot say which resolves to neither.
//
// 026 §4.3's NO-SPEC sentinel needs no case of its own: it matches none of the
// three forms, so it falls through to to_external, which is where a
// `covers: NO-SPEC` declaration belongs.
func resolveDocRef(tx *sql.Tx, project, base string) (int64, bool, error) {
	base = strings.TrimSuffix(path.Base(base), ".md")
	if base == "" || base == "." {
		return 0, false, nil
	}

	var id int64
	err := tx.QueryRow(
		`SELECT id FROM docs WHERE project_id = $1 AND slug = $2`, project, base).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("resolve doc ref %q by slug: %w", base, err)
	}

	if m := docShorthand.FindStringSubmatch(base); m != nil {
		n, convErr := strconv.Atoi(m[3])
		if convErr != nil {
			return 0, false, nil
		}
		err := tx.QueryRow(
			`SELECT d.id FROM docs d JOIN projects p ON p.id = d.project_id
			  WHERE d.project_id = $1 AND d.kind = $2 AND d.number = $3 AND p.key = $4`,
			project, strings.ToLower(m[2]), n, m[1]).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, false, fmt.Errorf("resolve doc ref %q by shorthand: %w", base, err)
		}
		return 0, false, nil
	}

	if m := docBareNumber.FindStringSubmatch(base); m != nil {
		n, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			return 0, false, nil
		}
		rows, err := tx.Query(
			`SELECT id FROM docs
			  WHERE project_id = $1 AND number = $2 AND kind IN ('spec','adr') LIMIT 2`, project, n)
		if err != nil {
			return 0, false, fmt.Errorf("resolve doc ref %q by number: %w", base, err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var candidate int64
			if err := rows.Scan(&candidate); err != nil {
				return 0, false, fmt.Errorf("resolve doc ref %q by number: %w", base, err)
			}
			ids = append(ids, candidate)
		}
		if err := rows.Err(); err != nil {
			return 0, false, fmt.Errorf("resolve doc ref %q by number: %w", base, err)
		}
		if len(ids) == 1 {
			return ids[0], true, nil
		}
	}
	return 0, false, nil
}

// cutFragment splits a trailing "#sec-…" fragment off a reference.
func cutFragment(ref string) (base, fragment string) {
	base, fragment, found := strings.Cut(ref, "#")
	if !found {
		return ref, ""
	}
	return base, fragment
}

// docColumns is the SELECT list scanDoc expects, in order.
const docColumns = `id, project_id, kind, number, slug, title, body, status, version, issued, assignee, created_by, created_at, updated_at`

func scanDoc(row rowScanner) (*model.Doc, error) {
	var d model.Doc
	var number sql.NullInt64
	var issued sql.NullTime
	var assignee, createdBy sql.NullString
	if err := row.Scan(&d.ID, &d.Project, &d.Kind, &number, &d.Slug, &d.Title, &d.Body,
		&d.Status, &d.Version, &issued, &assignee, &createdBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Number = int(number.Int64)
	if issued.Valid {
		d.Issued = issued.Time.Format(docDateLayout)
	}
	d.Assignee = assignee.String
	d.CreatedBy = createdBy.String
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
	where := "TRUE"
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
	defer rows.Close()

	var out []model.Doc
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, fmt.Errorf("scan doc: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list docs: %w", err)
	}
	return out, nil
}

// NeedsPlanning returns the accepted specs that have at least one section no
// accepted plan undertakes, each with the anchors that made it a gap (026
// §2.1). project narrows the answer; "" answers over every project.
//
// A section counts as planned when some accepted plan carries a `covers` edge
// naming both the spec and that exact anchor. Three consequences are
// deliberate:
//
//   - A whole-document edge (to_anchor IS NULL) discharges nothing. It cannot
//     say which present section it undertakes and would silently claim future
//     ones (026 §2.1), so it never appears in the planned set.
//   - `covers: NO-SPEC` resolves to no row and lands in to_external (026
//     §4.3), so it falls out of the join without a case of its own.
//   - Only accepted documents on both ends participate: a draft spec is not
//     yet owed planning, and a draft plan has not yet undertaken work.
//
// Coverage here is two-valued — an edge to the anchor means planned. 026 §2.1
// specifies a three-valued `coverage: full|partial|none` relation with
// `fullCoverageWith` closure checking, which doc_edges cannot express: it has
// no coverage-level column, so `rebuildEdges` writes the level away. Closing
// that gap needs a schema change and is tracked as WL-141.
func (s *Store) NeedsPlanning(ctx context.Context, project string) ([]model.Doc, []model.DocPlanningGap, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH planned AS (
		     SELECT DISTINCT e.to_doc AS doc_id, e.to_anchor AS anchor
		       FROM doc_edges e
		       JOIN docs p ON p.id = e.from_doc
		      WHERE e.type = 'covers'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		        AND p.kind = 'plan' AND p.status = 'accepted'
		 )
		 SELECT `+qualifiedDocColumns("d")+`, count(*)::int,
		        coalesce(json_agg(sec.anchor ORDER BY sec.position)
		                 FILTER (WHERE pl.anchor IS NULL), '[]')::text
		   FROM docs d
		   JOIN doc_sections sec ON sec.doc_id = d.id
		   LEFT JOIN planned pl ON pl.doc_id = sec.doc_id AND pl.anchor = sec.anchor
		  WHERE d.kind = 'spec' AND d.status = 'accepted'
		    AND ($1 = '' OR d.project_id = $1)
		  GROUP BY d.id
		 HAVING count(*) FILTER (WHERE pl.anchor IS NULL) > 0
		  ORDER BY d.project_id, d.number NULLS LAST, d.slug`, project)
	if err != nil {
		return nil, nil, fmt.Errorf("list specs needing planning: %w", err)
	}
	defer rows.Close()

	var docs []model.Doc
	var gaps []model.DocPlanningGap
	for rows.Next() {
		var gap model.DocPlanningGap
		var unplannedJSON string
		d, err := scanDoc(appendScan{rows, []any{&gap.Sections, &unplannedJSON}})
		if err != nil {
			return nil, nil, fmt.Errorf("scan spec needing planning: %w", err)
		}
		if err := json.Unmarshal([]byte(unplannedJSON), &gap.Unplanned); err != nil {
			return nil, nil, fmt.Errorf("decode unplanned anchors of doc %d: %w", d.ID, err)
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

// NeedsExecution returns the accepted plans whose task set holds at least one
// task that is not closed. project narrows the answer; "" answers over every
// project. "Closed" is taskClosed's notion, shared with the ready set and the
// blocks predicate, so the three cannot drift on what done means.
//
// This departs from 025 §18's "unminted or unfinished" deliberately, as the
// 2026-08-03 plan-acceptance plan records: through the accept path an
// accepted-but-unminted plan cannot exist, and the only accepted plans with no
// task set are the importer's *spent* plans, which must not be reported as
// pending work. The ordering need §18's "unminted" arm served is covered by
// the plan-to-plan blocks predicate (planBlockedCondition).
func (s *Store) NeedsExecution(ctx context.Context, project string) ([]model.Doc, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+qualifiedDocColumns("d")+`
		   FROM docs d
		  WHERE d.kind = 'plan' AND d.status = 'accepted'
		    AND ($1 = '' OR d.project_id = $1)
		    AND EXISTS (SELECT 1 FROM tasks t
		                 WHERE t.plan_doc = d.id AND NOT `+taskClosed("t")+`)
		  ORDER BY d.project_id, d.slug`, project)
	if err != nil {
		return nil, fmt.Errorf("list plans needing execution: %w", err)
	}
	defer rows.Close()

	var out []model.Doc
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plan needing execution: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plans needing execution: %w", err)
	}
	return out, nil
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

// qualifiedDocColumns renders docColumns with every name prefixed by alias,
// for queries that join docs against a table carrying a column of the same
// name (doc_sections.number).
func qualifiedDocColumns(alias string) string {
	cols := strings.Split(docColumns, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
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
	defer rows.Close()
	var out []model.DocSection
	for rows.Next() {
		var sec model.DocSection
		if err := rows.Scan(&sec.Anchor, &sec.Number, &sec.Heading, &sec.Depth,
			&sec.Position, &sec.LastRevisedIn, &sec.Published); err != nil {
			return nil, fmt.Errorf("scan section of doc %d: %w", docID, err)
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sections of doc %d: %w", docID, err)
	}
	return out, nil
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
}

// ListDocEdges returns a document's edges in both directions: out are the
// edges its own frontmatter declares, in are the edges other documents point
// at it with, each read backward — the type carries its inverse spelling and
// ToDoc names the other end, so a caller can link to it. For an inbound edge
// FromAnchor is the anchor in docID the edge lands on and ToAnchor the anchor
// it left from; an inbound edge never has ToExternal, since an unresolved
// reference names no row here.
//
// Both lists are fully ordered, so a caller may compare them as sequences.
func (s *Store) ListDocEdges(ctx context.Context, docID int64) (out, in []model.DocEdge, err error) {
	outRows, err := s.db.QueryContext(ctx,
		`SELECT type, coalesce(from_anchor,''), coalesce(to_doc,0),
		        coalesce(to_anchor,''), coalesce(to_external,'')
		   FROM doc_edges WHERE from_doc = $1
		  ORDER BY type, coalesce(from_anchor,''), coalesce(to_doc,0),
		           coalesce(to_anchor,''), coalesce(to_external,'')`, docID)
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
		`SELECT type, coalesce(to_anchor,''), from_doc, coalesce(from_anchor,''), ''
		   FROM doc_edges WHERE to_doc = $1
		  ORDER BY type, coalesce(to_anchor,''), from_doc, coalesce(from_anchor,'')`, docID)
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

// scanDocEdges drains a query selecting the five DocEdge columns in order.
func scanDocEdges(rows *sql.Rows) ([]model.DocEdge, error) {
	defer rows.Close()
	var out []model.DocEdge
	for rows.Next() {
		var e model.DocEdge
		if err := rows.Scan(&e.Type, &e.FromAnchor, &e.ToDoc, &e.ToAnchor, &e.ToExternal); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordDocEvent wraps RecordEvent for a document mutation, recording
// worklode_doc_operations_total{op,outcome}. op is one of
// create|update|accept|revise|edges.
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

// nullText maps "" to NULL, for the document columns where absent and empty
// are the same thing.
func nullText(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullID maps 0 to NULL, for the nullable doc_edges.to_doc reference.
func nullID(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id != 0}
}
