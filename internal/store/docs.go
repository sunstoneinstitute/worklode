package store

import (
	"context"
	"database/sql"
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
// decode.
var (
	validDocKinds    = map[string]bool{"spec": true, "adr": true, "plan": true}
	validDocStatuses = map[string]bool{"draft": true, "accepted": true, "superseded": true}
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
		return nil, fmt.Errorf("insert doc %s/%s: %w", in.Project, in.Slug, err)
	}

	if err := rebuildSections(tx, id, in.Kind, parsed.doc, 1); err != nil {
		return nil, err
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
	// what states them.
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, title = $3, issued = $4::date, updated_at = $5 WHERE id = $1`,
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
	if kind != "plan" {
		if err := lintAnchors(doc); err != nil {
			return parsedDoc{}, err
		}
	}
	return parsedDoc{doc: doc, issued: issued}, nil
}

// lintAnchors rejects the two anchor defects that make a section
// unaddressable: two headings claiming one anchor, and an anchor that
// disagrees with its heading number. secfmt.py writes the anchor as
// "sec-<number>", so the number is the anchor and a disagreement means one of
// them is a typo. Headings are named rather than line-numbered — the parser
// yields no line numbers, and the heading text locates the defect.
func lintAnchors(doc *designdoc.Document) error {
	seen := map[string]string{}
	for _, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		if prev, dup := seen[sec.Anchor]; dup {
			return fmt.Errorf("anchor #%s is claimed by both %q and %q: %w",
				sec.Anchor, prev, sec.Title, ErrInvalidInput)
		}
		seen[sec.Anchor] = sec.Title
		if sec.Number != "" && sec.Anchor != "sec-"+sec.Number {
			return fmt.Errorf("heading %q is numbered %s but anchored #%s: %w",
				sec.Title, sec.Number, sec.Anchor, ErrInvalidInput)
		}
	}
	return nil
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

// docEdgeRef is one frontmatter reference before resolution. Ref is verbatim,
// fragment included; FromAnchor is "" for a document-level edge.
type docEdgeRef struct {
	fromAnchor string
	typ        string
	ref        string
}

// rebuildEdges replaces a document's outbound edges from its frontmatter. It
// deletes and re-inserts, so doc_edges_unique is satisfied across calls; the
// dedupe in frontmatterEdges keeps one frontmatter from colliding with
// itself.
func rebuildEdges(tx *sql.Tx, docID int64, project string, fm *designdoc.Frontmatter) error {
	if _, err := tx.Exec(`DELETE FROM doc_edges WHERE from_doc = $1`, docID); err != nil {
		return fmt.Errorf("clear edges of doc %d: %w", docID, err)
	}
	for _, e := range frontmatterEdges(fm) {
		base, fragment := cutFragment(e.ref)
		toDoc, resolved, err := resolveDocRef(tx, project, base)
		if err != nil {
			return err
		}
		var toDocArg sql.NullInt64
		var toAnchor, toExternal sql.NullString
		if resolved {
			toDocArg = sql.NullInt64{Int64: toDoc, Valid: true}
			toAnchor = nullText(fragment)
		} else {
			// Unresolvable: the whole reference is kept verbatim, fragment
			// included, since nothing here can say what its anchor names.
			toExternal = sql.NullString{String: e.ref, Valid: true}
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_edges (from_doc, from_anchor, type, to_doc, to_anchor, to_external)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, nullText(e.fromAnchor), e.typ, toDocArg, toAnchor, toExternal,
		); err != nil {
			return fmt.Errorf("insert %s edge from doc %d to %q: %w", e.typ, docID, e.ref, err)
		}
	}
	return nil
}

// frontmatterEdges reads the acting-direction relations out of fm, deduped and
// in a deterministic order.
//
// The inverse spellings (isRequiredBy, amendedBy, isReplacedBy) are skipped:
// one row read backward is the inverse (025 §14), so writing them too would
// double every edge and let the two directions disagree.
func frontmatterEdges(fm *designdoc.Frontmatter) []docEdgeRef {
	if fm == nil {
		return nil
	}
	var out []docEdgeRef
	// covers reads the retired `implements` spelling too (026 §5.1); the
	// implements edge type stays reserved for components.
	for _, entry := range fm.CoverageEntries() {
		out = append(out, docEdgeRef{typ: "covers", ref: entry.Spec})
	}
	for _, ref := range fm.Requires {
		out = append(out, docEdgeRef{typ: "requires", ref: ref})
	}
	if fm.WasDerivedFrom != "" {
		out = append(out, docEdgeRef{typ: "wasDerivedFrom", ref: fm.WasDerivedFrom})
	}
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
				out = append(out, docEdgeRef{fromAnchor: anchor, typ: m.typ, ref: ref})
			}
		}
	}
	return dedupeEdgeRefs(out)
}

// dedupeEdgeRefs drops repeats of the same (from_anchor, type, ref), keeping
// first-seen order — a frontmatter that names one target twice must not
// collide with itself on doc_edges_unique.
func dedupeEdgeRefs(in []docEdgeRef) []docEdgeRef {
	seen := make(map[docEdgeRef]bool, len(in))
	out := in[:0]
	for _, e := range in {
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// docNumberPrefix reads a corpus filename's leading number, ignoring
// zero-padding ("004-execution-backbone" -> 4).
var docNumberPrefix = regexp.MustCompile(`^(\d+)(?:-|$)`)

// docShorthand is 025 §14.3's <KEY>-<TYPE>-<n> reference, e.g. "WL-SPEC-25".
var docShorthand = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR)-(\d+)$`)

// resolveDocRef finds the document in project that base names, base being a
// reference with any "#…" fragment already removed. Resolution is
// same-project only: a cross-corpus reference has no row here and belongs in
// to_external (025 §14.3).
//
// Three forms are tried, in order: the slug, 025 §14.3's <KEY>-<TYPE>-<n>
// shorthand against this project's key, and a corpus filename's leading
// number. The number form must match exactly one spec or ADR — a project can
// hold a spec 25 and an ADR 25, and a reference that cannot say which
// resolves to neither.
func resolveDocRef(tx *sql.Tx, project, base string) (int64, bool, error) {
	base = strings.TrimSuffix(path.Base(base), ".md")
	if base == "" || base == "." || base == "NO-SPEC" {
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

	if m := docNumberPrefix.FindStringSubmatch(base); m != nil {
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

// RecordDocEvent wraps RecordEvent for a document mutation, recording
// worklode_doc_operations_total{op,outcome}. op is one of
// create|update|accept|revise.
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

// nullText maps "" to NULL, for the document columns where absent and empty
// are the same thing.
func nullText(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
