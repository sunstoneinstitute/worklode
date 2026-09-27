package store

import (
	"context"
	"database/sql"
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

// ReplaceDocEdges rewrites document id's whole live edge set from edges,
// through the same resolution and guards a header's edges went through, and
// appends a state_log row attributed to eventID. It is the importer's write
// (PUT /api/v1/docs/{id}/edges): it works at any status and moves no version,
// since it restates a corpus rather than editing one document.
func ReplaceDocEdges(tx *sql.Tx, _ time.Time, id int64, edges []model.DocEdgeInput, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	refs := make([]docEdgeRef, 0, len(edges))
	for _, in := range edges {
		e, err := edgeRefFromInput(in)
		if err != nil {
			return err
		}
		refs = append(refs, e)
	}
	if err := replaceEdgeRefs(tx, id, d.kind, d.project, refs, false); err != nil {
		return err
	}
	return logDocChange(tx, id, eventID, map[string]string{"field": "edges"})
}

// LinkDocEdge adds one edge to document docID (WL-SPEC-77 §3, §8). Where it
// lands depends on the document: a plan moves to its next version first
// (bumpDocVersion) and writes doc_edges; a draft spec or ADR writes doc_edges
// in place; an accepted one writes its candidate revision's edge set, opening
// a candidate authored by actor when none is open. A superseded, withdrawn or
// spent document is ErrInvalidInput, and an edge it already holds is
// ErrEdgeExists.
func LinkDocEdge(tx *sql.Tx, now time.Time, docID int64, in model.DocEdgeInput, actor string, eventID int64) error {
	return changeDocEdge(tx, now, docID, in, actor, eventID, true)
}

// UnlinkDocEdge removes one edge from document docID, routed exactly as
// LinkDocEdge routes. An edge the target set does not hold is ErrNotFound.
func UnlinkDocEdge(tx *sql.Tx, now time.Time, docID int64, in model.DocEdgeInput, actor string, eventID int64) error {
	return changeDocEdge(tx, now, docID, in, actor, eventID, false)
}

func changeDocEdge(tx *sql.Tx, now time.Time, docID int64, in model.DocEdgeInput, actor string, eventID int64, link bool) error {
	e, err := edgeRefFromInput(in)
	if err != nil {
		return err
	}
	d, err := lockDoc(tx, docID)
	if err != nil {
		return err
	}
	candidate := false
	switch {
	case d.status == "superseded" || d.status == "withdrawn" || d.status == "spent":
		return fmt.Errorf("doc %d is %s: its edges no longer change: %w", docID, d.status, ErrInvalidInput)
	case d.kind == "plan":
		if _, err := bumpDocVersion(tx, docID); err != nil {
			return err
		}
	case d.status == "accepted":
		candidate = true
		var open bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM doc_revisions WHERE doc_id = $1)`, docID).Scan(&open); err != nil {
			return fmt.Errorf("read revision of doc %d: %w", docID, err)
		}
		if !open {
			if err := openRevision(tx, now, docID, d.body, actor); err != nil {
				return err
			}
		}
	}
	if link {
		err = insertEdgeRefs(tx, docID, d.kind, d.project, []docEdgeRef{e}, candidate)
		if isUniqueViolationOn(err, "doc_edges_unique") || isUniqueViolationOn(err, "doc_revision_edges_unique") {
			return fmt.Errorf("doc %d already %s %q: %w", docID, e.typ, e.ref, ErrEdgeExists)
		}
	} else {
		err = deleteEdgeRef(tx, docID, d.kind, d.project, e, candidate)
	}
	if err != nil {
		return err
	}
	return logDocChange(tx, docID, eventID, map[string]string{"field": "edges"})
}

// edgeRefFromInput checks one caller-named edge and turns it into the
// docEdgeRef a header entry would have produced. The type must be a declared
// doc_edges type with a writer; an inverse spelling is refused naming the
// type to declare instead (WL-SPEC-77 §8.1).
func edgeRefFromInput(in model.DocEdgeInput) (docEdgeRef, error) {
	typ := strings.TrimSpace(in.Type)
	if acting, ok := designdoc.InverseOf[typ]; ok {
		return docEdgeRef{}, fmt.Errorf(
			"edge type %s is an inverse and is not stored: declare it on the other document as %s (WL-SPEC-77 §8.1): %w",
			typ, acting, ErrInvalidInput)
	}
	if !slices.Contains(ns.DeclaredEdges("doc_edges"), typ) {
		return docEdgeRef{}, fmt.Errorf("edge type %q is not a document edge type; use %s (WL-SPEC-77 §8.1): %w",
			typ, ns.OrList(designdoc.StoredRels), ErrInvalidInput)
	}
	if !slices.Contains(designdoc.StoredRels, typ) {
		return docEdgeRef{}, fmt.Errorf("edge type %s has no writer (026 §6.2); use %s: %w",
			typ, ns.OrList(designdoc.StoredRels), ErrInvalidInput)
	}
	e := docEdgeRef{
		fromAnchor: strings.TrimSpace(in.FromAnchor),
		typ:        typ,
		ref:        strings.TrimSpace(in.To),
		owner:      strings.TrimSpace(in.Owner),
	}
	if e.ref == "" {
		return docEdgeRef{}, fmt.Errorf("a %s edge names its target: %w", typ, ErrInvalidInput)
	}
	if typ != "defers" && e.owner != "" {
		return docEdgeRef{}, fmt.Errorf("owner belongs to a defers edge, not %s (026 §5.3): %w", typ, ErrInvalidInput)
	}
	return e, nil
}

// deleteEdgeRef removes the rows e resolves to from docID's live or candidate
// edge set. ErrNotFound when none of them is there.
func deleteEdgeRef(tx *sql.Tx, docID int64, kind, project string, e docEdgeRef, candidate bool) error {
	resolvedCovers, err := resolveCoversRefs(tx, kind, project, []docEdgeRef{e})
	if err != nil {
		return err
	}
	rows, _, err := resolveEdgeRef(tx, docID, kind, project, e, resolvedCovers)
	if err != nil {
		return err
	}
	table, from := "doc_edges", "from_doc"
	if candidate {
		table, from = "doc_revision_edges", "doc_id"
	}
	var n int64
	for _, row := range rows {
		res, err := tx.Exec(
			`DELETE FROM `+table+` WHERE `+from+` = $1 AND coalesce(from_anchor,'') = $2 AND type = $3
			    AND coalesce(to_doc,0) = $4 AND coalesce(to_rule,0) = $5
			    AND coalesce(to_anchor,'') = $6 AND coalesce(to_external,'') = $7`,
			docID, row.fromAnchor, row.typ, row.toDoc, row.toRule, row.toAnchor, row.toExternal)
		if err != nil {
			return fmt.Errorf("unlink %s edge of doc %d to %q: %w", e.typ, docID, e.ref, err)
		}
		k, err := res.RowsAffected()
		if err != nil {
			return err
		}
		n += k
	}
	if n == 0 {
		return fmt.Errorf("doc %d has no %s edge to %q: %w", docID, e.typ, e.ref, ErrNotFound)
	}
	return nil
}

// docEdgeRef is one frontmatter reference before resolution. ref is verbatim,
// fragment included; fromAnchor is "" for a document-level edge. owner carries
// a defers entry's named owner, verbatim (026 §5.3); every other relation
// leaves it empty.
type docEdgeRef struct {
	fromAnchor string
	typ        string
	ref        string
	owner      string
}

// docEdgeRow is one edge after resolution — exactly the tuple
// doc_edges_unique keys, so equality here is the collision the index would
// report. A covers edge sets toRule, every other edge toDoc; both are 0 and
// toExternal non-empty for an unresolved reference. The from end is always
// the writing document, so it is not a field.
type docEdgeRow struct {
	fromAnchor string
	typ        string
	toDoc      int64
	toRule     int64
	toAnchor   string
	toExternal string
}

// ownerRef is a resolved defers owner: a doc id when it resolved, or the
// verbatim reference in toExternal when it did not, same as an unresolved
// doc_edges target.
type ownerRef struct {
	toDoc      int64
	toExternal string
}

// resolveOwner resolves a defers entry's owner against project.
func resolveOwner(tx *sql.Tx, project, ref string) (ownerRef, error) {
	base, _ := designdoc.SplitFragment(ref) // an owner carries no fragment
	id, resolved, err := resolveDocRef(tx, project, base)
	if err != nil || !resolved {
		return ownerRef{toExternal: ref}, err
	}
	return ownerRef{toDoc: id}, nil
}

// rebuildEdges replaces the edges a document's frontmatter declares. It
// deletes and re-inserts, so doc_edges_unique is satisfied across calls.
// Every row it writes runs from this document, so it clears exactly the rows
// whose from end is this document (WL-SPEC-77 §8).
//
// A header carrying an inverse spelling (`blocks`, `isRequiredBy`) is
// refused: only the acting direction is stored, and the header naming it is
// the one on the from end (`blockedBy`, `requires`).
//
// Within one frontmatter it dedupes on the *resolved* row rather than on the
// reference: two spellings of one target ("004-x.md" and
// "docs/specs/004-x.md", or a filename and its <KEY>-SPEC-<n> shorthand) are
// one edge, and inserting both would abort a legal document on a raw unique
// violation.
//
// A covers entry is a plain reference (WL-SPEC-78 §4.1, §4.5): the retired
// `coverage:` and `fullCoverageWith:` keys are refused. It is stored as one
// edge per rule it resolves to (coversRules, WL-SPEC-77 §4), so a section
// entry writes an edge for the rule at its anchor and each rule under it, and
// a whole-document entry one for each rule the document contains, requirements
// only. Nested entries overlap: the plan's covered set is their union. An
// entry naming no rule keeps its reference in to_external.
//
// A defers edge (026 §5.3) is checked, not merely written: the from end must
// be a plan, the `spec` reference must carry a `#sec-N` fragment (a
// whole-document deferral would silently defer sections not yet written), the
// owner must be named, must carry no fragment (an owner is a document), and
// must not resolve to the deferring plan itself. The owner is stored on the
// edge row (owner_doc, or owner_external when it did not resolve). The same
// section deferred to two different owners is refused as ErrInvalidInput.
func rebuildEdges(tx *sql.Tx, now time.Time, docID int64, kind, project string, fm *designdoc.Frontmatter) error {
	if err := declareDocArtifacts(tx, now, docID, fm); err != nil {
		return err
	}
	return writeEdges(tx, docID, kind, project, fm, false)
}

// declareDocArtifacts records a header's artifact key. It is not an edge — it
// declares the catalog address(es) this document is verified by (029 §3.1),
// which is what routes a /hooks/catalog delivery to it (WL-255). Declarations
// are additive and idempotent: removing the key from a later body does not
// undeclare, the same as every other declaration surface.
func declareDocArtifacts(tx *sql.Tx, now time.Time, docID int64, fm *designdoc.Frontmatter) error {
	if fm == nil {
		return nil
	}
	for _, a := range fm.Artifact {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if utf8.RuneCountInString(a) > maxArtifactURI {
			return fmt.Errorf("doc %d artifact %q is too long: %w", docID, a[:40]+"…", ErrInvalidInput)
		}
		if err := DeclareArtifact(tx, now, "doc", strconv.FormatInt(docID, 10), "address", a); err != nil {
			return err
		}
	}
	return nil
}

// writeEdges replaces docID's edge set with the one fm declares, through the
// guards rebuildEdges documents. candidate selects the table: false writes
// the live doc_edges, true the open candidate revision's doc_revision_edges.
func writeEdges(tx *sql.Tx, docID int64, kind, project string, fm *designdoc.Frontmatter, candidate bool) error {
	// The covers defects a single header settles on its own. They are checked
	// here rather than in the loop below: designdoc.Refs drops an entry naming
	// no spec, so it never reaches frontmatterEdges.
	if fm != nil {
		if len(fm.Blocks) > 0 {
			return fmt.Errorf(
				"doc %d carries blocks, which is not stored: declare the ordering on the later plan as blockedBy (WL-SPEC-77 §8): %w",
				docID, ErrInvalidInput)
		}
		if len(fm.IsRequiredBy) > 0 {
			return fmt.Errorf(
				"doc %d carries isRequiredBy, which is not stored: declare it on the other document as requires (WL-SPEC-77 §8): %w",
				docID, ErrInvalidInput)
		}
		if fm.Covers != nil && fm.Implements != nil {
			return fmt.Errorf(
				"doc %d carries both covers and implements, which are one key under two names (026 §5.1): %w",
				docID, ErrInvalidInput)
		}
		for i, c := range fm.CoverageEntries() {
			if strings.TrimSpace(c.Spec) == "" {
				return fmt.Errorf("doc %d covers[%d] names no spec (026 §5.1): %w",
					docID, i, ErrInvalidInput)
			}
			if !c.Plain() {
				return fmt.Errorf(
					"doc %d covers %q is not a plain reference: coverage levels are retired, a covers entry means the plan builds the whole rule (WL-SPEC-78 §4.1, §4.5): %w",
					docID, c.Spec, ErrInvalidInput)
			}
		}
	}
	return replaceEdgeRefs(tx, docID, kind, project, frontmatterEdges(fm), candidate)
}

// replaceEdgeRefs clears docID's live or candidate edge set and writes edges.
func replaceEdgeRefs(tx *sql.Tx, docID int64, kind, project string, edges []docEdgeRef, candidate bool) error {
	clear := `DELETE FROM doc_edges WHERE from_doc = $1`
	if candidate {
		clear = `DELETE FROM doc_revision_edges WHERE doc_id = $1`
	}
	if _, err := tx.Exec(clear, docID); err != nil {
		return fmt.Errorf("clear edges of doc %d: %w", docID, err)
	}
	return insertEdgeRefs(tx, docID, kind, project, edges, candidate)
}

// insertEdgeRefs resolves and validates edges and adds them to docID's live
// or candidate edge set: the one path every edge write goes through
// (rebuildEdges documents the guards).
func insertEdgeRefs(tx *sql.Tx, docID int64, kind, project string, edges []docEdgeRef, candidate bool) error {
	resolvedCovers, err := resolveCoversRefs(tx, kind, project, edges)
	if err != nil {
		return err
	}
	seen := map[docEdgeRow]*ownerRef{}
	for _, e := range edges {
		rows, owner, err := resolveEdgeRef(tx, docID, kind, project, e, resolvedCovers)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := insertDocEdge(tx, docID, e, row, owner, seen, candidate); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveCoversRefs resolves every covers entry of a plan's edges to its
// rules, once per distinct reference.
func resolveCoversRefs(tx *sql.Tx, kind, project string, edges []docEdgeRef) (map[string][]int64, error) {
	resolvedCovers := map[string][]int64{}
	for _, e := range edges {
		if e.typ != "covers" || kind != "plan" {
			continue
		}
		if _, ok := resolvedCovers[e.ref]; ok {
			continue
		}
		rules, named, err := coversRules(tx, project, e.ref)
		if err != nil {
			return nil, err
		}
		if named && rules == nil {
			rules = []int64{} // names only rules a plan does not cover: no edge
		}
		resolvedCovers[e.ref] = rules
	}
	return resolvedCovers, nil
}

// resolveEdgeRef resolves one edge to the rows it stores, with a defers
// edge's resolved owner, refusing what the guards in rebuildEdges refuse.
func resolveEdgeRef(tx *sql.Tx, docID int64, kind, project string, e docEdgeRef,
	resolvedCovers map[string][]int64) ([]docEdgeRow, *ownerRef, error) {
	base, fragment := designdoc.SplitFragment(e.ref)
	var toDoc int64
	var resolved bool
	var err error
	if e.typ != "covers" {
		// A covers entry resolves to rules instead (coversRules below).
		if toDoc, resolved, err = resolveDocRef(tx, project, base); err != nil {
			return nil, nil, err
		}
	}
	if e.typ == "blockedBy" {
		if err := checkPlanOrdering(tx, docID, kind, e.ref, toDoc, resolved); err != nil {
			return nil, nil, err
		}
	}
	var owner *ownerRef
	if e.typ == "defers" {
		if kind != "plan" {
			return nil, nil, fmt.Errorf("doc %d defers %q, but defers is plan-only and doc %d is a %s (026 §5.3): %w",
				docID, e.ref, docID, kind, ErrInvalidInput)
		}
		if fragment == "" {
			return nil, nil, fmt.Errorf(
				"doc %d defers %q with no #sec-N fragment: defers is section-scoped, unlike covers (026 §5.3): %w",
				docID, e.ref, ErrInvalidInput)
		}
		if strings.TrimSpace(e.owner) == "" {
			return nil, nil, fmt.Errorf("doc %d defers %q with no owner: a deferral names its owner (026 §5.3): %w",
				docID, e.ref, ErrInvalidInput)
		}
		if _, ownerFragment := designdoc.SplitFragment(e.owner); ownerFragment != "" {
			return nil, nil, fmt.Errorf(
				"doc %d defers %q to %q: the owner is a document, no fragment (026 §5.3): %w",
				docID, e.ref, e.owner, ErrInvalidInput)
		}
		o, err := resolveOwner(tx, project, e.owner)
		if err != nil {
			return nil, nil, err
		}
		if o.toDoc == docID {
			return nil, nil, fmt.Errorf(
				"doc %d defers %q to itself: a plan cannot defer a section to itself (026 §5.3): %w",
				docID, e.ref, ErrInvalidInput)
		}
		owner = &o
	}
	if e.typ == "covers" && kind != "plan" {
		return nil, nil, fmt.Errorf("doc %d covers %q, but covers is plan-only and doc %d is a %s (026 §5.1): %w",
			docID, e.ref, docID, kind, ErrInvalidInput)
	}

	row := docEdgeRow{fromAnchor: e.fromAnchor, typ: e.typ}
	var rows []docEdgeRow
	switch {
	case e.typ == "covers":
		// A covers edge runs from the plan to each rule the entry
		// resolves to (WL-SPEC-77 §4); an entry naming no rule keeps its
		// reference verbatim.
		rules := resolvedCovers[e.ref]
		for _, r := range rules {
			rr := row
			rr.toRule = r
			rows = append(rows, rr)
		}
		if rules == nil {
			row.toExternal = e.ref
			rows = append(rows, row)
		}
	case resolved:
		row.toDoc, row.toAnchor = toDoc, fragment
		rows = append(rows, row)
	default:
		// Unresolvable: the whole reference is kept verbatim, fragment
		// included, since nothing here can say what its anchor names.
		row.toExternal = e.ref
		rows = append(rows, row)
	}
	return rows, owner, nil
}

// insertDocEdge writes one resolved row of writeEdges into the live
// doc_edges or the candidate's doc_revision_edges. A row already seen in this
// write is one edge, unless it is a deferral naming a different owner.
func insertDocEdge(tx *sql.Tx, docID int64, e docEdgeRef, row docEdgeRow, owner *ownerRef, seen map[docEdgeRow]*ownerRef, candidate bool) error {
	if prior, ok := seen[row]; ok {
		if e.typ == "defers" && *prior != *owner {
			return fmt.Errorf("doc %d defers %q twice, deferred to two different owners (026 §5.3): %w",
				docID, e.ref, ErrInvalidInput)
		}
		return nil
	}
	seen[row] = owner

	var ownerDoc sql.NullInt64
	var ownerExternal sql.NullString
	if owner != nil {
		ownerDoc, ownerExternal = nullID(owner.toDoc), nullText(owner.toExternal)
	}
	table, from := "doc_edges", "from_doc"
	if candidate {
		table, from = "doc_revision_edges", "doc_id"
	}
	if _, err := tx.Exec(
		`INSERT INTO `+table+`
		   (`+from+`, from_anchor, type, to_doc, to_rule, to_anchor, to_external, owner_doc, owner_external)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		docID, nullText(row.fromAnchor), row.typ, nullID(row.toDoc), nullID(row.toRule),
		nullText(row.toAnchor), nullText(row.toExternal), ownerDoc, ownerExternal,
	); err != nil {
		return fmt.Errorf("insert %s edge from doc %d to %q: %w", e.typ, docID, e.ref, err)
	}
	return nil
}

// repointExternalEdges re-points the project's already-stored unresolved
// references that name newDocID, in both doc_edges targets and defers
// owners. rebuildEdges resolves a reference once,
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
// holds is deleted instead of updated. Where the surviving row and the deleted
// one disagree on a defers owner, the lower-id row wins — which rebuildEdges
// would instead have refused as a contradiction (026 §5.3). That disagreement
// is deliberately not ErrInvalidInput here: it lives in *another* document's
// frontmatter, and failing this document's creation for it would wedge an
// import on an unrelated defect.
//
// The re-point is attributed to the creating document's event and logged as an
// edges change on each referring document whose rows moved.
func repointExternalEdges(tx *sql.Tx, project string, newDocID, eventID int64) error {
	// Distinct referring documents whose rows changed, logged once each below.
	touched := map[int64]bool{}
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
		if c.typ == "covers" {
			moved, err := repointCovers(tx, project, c.id, c.fromDoc, c.ref)
			if err != nil {
				return err
			}
			if moved {
				touched[c.fromDoc] = true
			}
			continue
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

	// Second pass, after the first: a defers edge's unresolved owner. The
	// owner is not part of doc_edges_unique, so there is no collision case.
	type ownerRow struct {
		edgeID  int64
		fromDoc int64
		ref     string
	}
	ownerRows, err := tx.Query(
		`SELECT e.id, e.from_doc, e.owner_external
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc
		  WHERE d.project_id = $1 AND d.deleted_at IS NULL AND e.owner_external IS NOT NULL
		  ORDER BY e.id`, project)
	if err != nil {
		return fmt.Errorf("read unresolved defers owners of project %s: %w", project, err)
	}
	owners, err := collectRows(ownerRows, "read unresolved defers owners of project "+project,
		func(r rowScanner) (ownerRow, error) {
			var row ownerRow
			err := r.Scan(&row.edgeID, &row.fromDoc, &row.ref)
			return row, err
		})
	if err != nil {
		return err
	}

	for _, r := range owners {
		o, err := resolveOwner(tx, project, r.ref)
		if err != nil {
			return err
		}
		if o.toDoc != newDocID {
			continue
		}
		if _, err := tx.Exec(
			`UPDATE doc_edges SET owner_doc = $1, owner_external = NULL WHERE id = $2`,
			newDocID, r.edgeID,
		); err != nil {
			return fmt.Errorf("re-point owner of edge %d to doc %d: %w", r.edgeID, newDocID, err)
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
	return nil
}

// repointCovers resolves an unresolved covers edge whose document has just
// arrived: it is replaced by one edge per rule the reference now names. An
// edge the plan already holds is kept as it is. A reference that still names no
// rule is left in place and reports false; one naming no requirement is
// dropped.
func repointCovers(tx *sql.Tx, project string, edgeID, fromDoc int64, ref string) (bool, error) {
	rules, named, err := coversRules(tx, project, ref)
	if err != nil || !named {
		return false, err
	}
	for _, r := range rules {
		if _, err := tx.Exec(
			`INSERT INTO doc_edges (from_doc, from_anchor, type, to_rule)
			 SELECT from_doc, from_anchor, type, $2 FROM doc_edges WHERE id = $1
			 ON CONFLICT (from_doc, coalesce(from_anchor,''), type, coalesce(to_doc, 0),
			              coalesce(to_rule, 0), coalesce(to_anchor,''), coalesce(to_external,''))
			 DO NOTHING`, edgeID, r); err != nil {
			return false, fmt.Errorf("re-point covers edge %d of doc %d to rule %d: %w", edgeID, fromDoc, r, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM doc_edges WHERE id = $1`, edgeID); err != nil {
		return false, fmt.Errorf("drop re-pointed covers edge %d of doc %d: %w", edgeID, fromDoc, err)
	}
	return true, nil
}

// frontmatterEdges reads the recorded relations out of fm — the walk is
// designdoc.Frontmatter.Refs, the rel set designdoc.StoredRels — in the
// deterministic order that walk fixes. rebuildEdges dedupes what comes back,
// on the resolved row rather than on the reference text.
//
// The inverse spellings (designdoc.InverseOf) are what StoredRels leaves out:
// one row read backward is the inverse (025 §14), and rebuildEdges refuses a
// header carrying one.
//
// Plan ordering is `blockedBy`, written by the later plan: a numbered plan
// series is authored forward, so part 3 knows it follows part 2 while part 2
// may be accepted and spent by then (WL-SPEC-77 §8).
//
// covers reads the retired `implements` spelling too (026 §5.1).
//
// defers carries its named owner beside the ref as docEdgeRef.owner rather
// than a separate walk; rebuildEdges resolves it and stores it on the edge
// row (026 §5.3).
//
// The implements edge *type* is a different subject: a component's evidence
// about its own code (026 §6.2), declared in `.worklode/implements.yaml`. That
// is 025 §11 machinery, and it is not built — so no writer emits the type here
// or anywhere else. The doc_edges CHECK admitting a value is not the same as
// something producing it; TestDocEdgeTypesWithoutWriter pins that gap so it is
// not re-diagnosed as a defect (WL-132).
//
// blockedBy orders whole plan documents (025 §5, §9.3); it projects as
// wl:blockedByPlan.
func frontmatterEdges(fm *designdoc.Frontmatter) []docEdgeRef {
	var out []docEdgeRef
	for _, r := range fm.RefsFor(designdoc.StoredRels...) {
		e := docEdgeRef{fromAnchor: r.SrcAnchor, typ: r.Rel, ref: r.Ref}
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

// LintDocs reports the corpus's dangling frontmatter references (055 §4.1):
// every doc_edges target or defers owner that stayed unresolved
// (to_external or owner_external set) after rebuildEdges last ran, plus every doc_edges row
// that resolved but whose to_anchor names no row in the target document's
// doc_sections. project narrows the answer to the referring document's
// project; "" answers over every project.
//
// Both cases are stored deliberately — rebuildEdges resolves a reference
// once, at write time, and repointExternalEdges (WL-133) repairs an
// unresolved one once its target is created — so this is a read of what
// stands right now, not a defect in what was written. A reference is never
// refused for staying unresolved; it went unreported before this, which is
// the gap 055 §4.1 closes.
//
// The `NO-SPEC` sentinel (026 §4.3) is not a finding: every other resolver
// in this codebase (designdoc.PlanTasks, designdoc.Coverage,
// designdoc.ResolveRef) treats a base ref of exactly "NO-SPEC" as the
// deliberate "no governing spec" marker rather than a dangling reference,
// and this filters it out the same way rather than inventing a second rule.
//
// Deleted documents (docs.deleted_at IS NOT NULL) are excluded on both
// ends: a tombstoned referrer's edges are hidden the way ListDocEdges
// already hides them, and a tombstoned target is not reported as a
// missing-anchor defect — 044 §4 leaves it addressable by id, not by a
// section that still has to resolve.
func (s *Store) LintDocs(ctx context.Context, project string) ([]model.DocLintFinding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT 'unresolved', d.id, d.project_id, coalesce(d.number,0), d.slug, d.kind,
		        coalesce(e.from_anchor,''), e.type, e.to_external,
		        0::bigint, '', ''
		   FROM doc_edges e
		   JOIN docs d ON d.id = e.from_doc
		  WHERE e.to_external IS NOT NULL AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)

		 UNION ALL

		 SELECT 'unresolved', d.id, d.project_id, coalesce(d.number,0), d.slug, d.kind,
		        coalesce(e.from_anchor,''), e.type, e.owner_external,
		        0::bigint, '', ''
		   FROM doc_edges e
		   JOIN docs d ON d.id = e.from_doc
		  WHERE e.owner_external IS NOT NULL AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)

		 UNION ALL

		 SELECT 'missing-anchor', d.id, d.project_id, coalesce(d.number,0), d.slug, d.kind,
		        coalesce(e.from_anchor,''), e.type, '',
		        e.to_doc, td.slug, e.to_anchor
		   FROM doc_edges e
		   JOIN docs d ON d.id = e.from_doc
		   JOIN docs td ON td.id = e.to_doc
		  WHERE e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		    AND d.deleted_at IS NULL AND td.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		    AND NOT EXISTS (SELECT 1 FROM doc_sections sec
		                      WHERE sec.doc_id = e.to_doc AND sec.anchor = e.to_anchor)

		  ORDER BY 3, 4, 5, 1, 7, 8, 9`, project)
	if err != nil {
		return nil, fmt.Errorf("lint docs: %w", err)
	}
	findings, err := collectRows(rows, "lint docs", func(r rowScanner) (model.DocLintFinding, error) {
		var f model.DocLintFinding
		err := r.Scan(&f.Kind, &f.Doc, &f.Project, &f.Number, &f.Slug, &f.DocKind,
			&f.FromAnchor, &f.Type, &f.Ref, &f.ToDoc, &f.ToSlug, &f.ToAnchor)
		return f, err
	})
	if err != nil {
		return nil, err
	}
	out := findings[:0]
	for _, f := range findings {
		if f.Kind == "unresolved" {
			base, _ := designdoc.SplitFragment(f.Ref)
			if base == "NO-SPEC" {
				continue
			}
		}
		out = append(out, f)
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
	"requires":       "isRequiredBy",
	"wasDerivedFrom": "hadDerivation",
	"blockedBy":      "blocks",
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
// Outbound edges are the ones this document's frontmatter declares; inbound
// ones are declared by the far end.
//
// Each resolved far end is named as well as identified: one join carries the
// other document's project, slug, kind and number back with its id, so a
// caller can render "spec 25" instead of "document 42", or address the far
// document by project and slug, without a query per edge. The project is part
// of that because an edge can cross one — resolveDocRef matches a shorthand
// reference on a project key — so a slug alone does not name a document. An
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
		`SELECT e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc, ra.doc_id, 0),
		        coalesce(e.to_anchor, ra.anchor, ''), coalesce(e.to_external,''),
		        coalesce(d.project_id,''), coalesce(d.slug,''), coalesce(d.kind,''),
		        coalesce(d.number,0), coalesce(d.status,''), coalesce(od.slug, e.owner_external, ''),
		        coalesce(`+ruleRefSQL("rp", "r")+`, '')
		   FROM doc_edges e
		   LEFT JOIN rules r ON r.id = e.to_rule
		   LEFT JOIN projects rp ON rp.id = r.project_id
		   LEFT JOIN LATERAL (
		            SELECT dr.doc_id, dr.anchor FROM doc_rules dr
		             WHERE dr.rule_id = e.to_rule
		             ORDER BY dr.doc_id, dr.position LIMIT 1
		        ) ra ON true
		   LEFT JOIN docs d ON d.id = coalesce(e.to_doc, ra.doc_id)
		   LEFT JOIN docs od ON od.id = e.owner_doc
		  WHERE e.from_doc = $1
		  ORDER BY e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc, ra.doc_id, 0),
		           coalesce(e.to_anchor, ra.anchor, ''), coalesce(e.to_external,''), r.number`, docID)
	if err != nil {
		return nil, nil, fmt.Errorf("list edges out of doc %d: %w", docID, err)
	}
	out, err = scanDocEdges(outRows)
	if err != nil {
		return nil, nil, fmt.Errorf("list edges out of doc %d: %w", docID, err)
	}

	// from_doc and to_anchor swap into the reader's frame: the row is read
	// from docID's end, so what the writer called its target anchor is the
	// anchor here, and its source anchor is the far one. A defers owner is
	// read the same way regardless of direction — it describes the stored
	// row itself, not which end docID sits at.
	//
	// A covers edge runs to a rule, so it lands here at each section of docID
	// arranging a rule it reaches (covered_sections), once per section.
	inRows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT e.type, coalesce(e.to_anchor,''), e.from_doc, coalesce(e.from_anchor,''), '',
		        d.project_id, d.slug, d.kind, coalesce(d.number,0), d.status,
		        coalesce(od.slug, e.owner_external, ''), ''
		   FROM (SELECT type, from_doc, from_anchor, to_anchor, owner_doc, owner_external
		           FROM doc_edges WHERE to_doc = $1
		         UNION ALL
		         SELECT 'covers', plan_id, NULL, anchor, NULL::bigint, NULL::text
		           FROM covered_sections WHERE doc_id = $1) e
		   JOIN docs d ON d.id = e.from_doc
		   LEFT JOIN docs od ON od.id = e.owner_doc
		  WHERE d.deleted_at IS NULL
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
// five stored ones, the joined far end's project, slug, kind, number and
// status, the defers owner, then the rule ref.
func scanDocEdges(rows *sql.Rows) ([]model.DocEdge, error) {
	defer rows.Close()
	var out []model.DocEdge
	for rows.Next() {
		var e model.DocEdge
		if err := rows.Scan(&e.Type, &e.FromAnchor, &e.ToDoc, &e.ToAnchor, &e.ToExternal,
			&e.ToProject, &e.ToSlug, &e.ToKind, &e.ToNumber, &e.ToStatus,
			&e.Owner, &e.ToRule); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
