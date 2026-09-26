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
)

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
// carries (rebuildEdges).
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
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "edges"})
}

// docEdgeRef is one frontmatter reference before resolution. ref is verbatim,
// fragment included; fromAnchor is "" for a document-level edge. coverage and
// completedWith carry a covers entry's authored level and, for a partial
// entry, its fullCoverageWith closure (026 §2.1, §5); owner carries a defers
// entry's named owner, verbatim, the same way (026 §5.3). Every other
// relation leaves all three zero.
type docEdgeRef struct {
	fromAnchor    string
	typ           string
	ref           string
	coverage      string
	completedWith []string
	owner         string
}

// docEdgeRow is one edge after resolution — exactly the tuple
// doc_edges_unique keys, so equality here is the collision the index would
// report. A covers edge sets toRule, every other edge toDoc; both are 0 and
// toExternal non-empty for an unresolved reference. The from end is always
// the writing document, so it is not a field.
// The coverage level is not part of this tuple — doc_edges_unique does not
// cover it — so rebuildEdges tracks it alongside the row in its dedupe map.
type docEdgeRow struct {
	fromAnchor string
	typ        string
	toDoc      int64
	toRule     int64
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
// clears it too. Every row it writes runs from this document, so it clears
// exactly the rows whose from end is this document (WL-SPEC-77 §8).
//
// A header carrying an inverse spelling (`blocks`, `isRequiredBy`) is
// refused: only the acting direction is stored, and the header naming it is
// the one on the from end (`blockedBy`, `requires`).
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
// A covers entry is stored as one edge per rule it resolves to (coversRules,
// WL-SPEC-77 §4), so a section entry writes an edge for the rule at its
// anchor and each rule under it, and a whole-document entry one for each rule
// the document contains. An entry naming no rule keeps its reference in
// to_external.
//
// A covers edge is checked against 026 §5.1: the key is plan-only, the level
// is one of full/partial/none, and a qualified entry carries both required
// keys. An empty level reaches here only from the object form with
// `coverage:` absent — the bare-string form decodes straight to "full"
// (designdoc.Coverage.UnmarshalYAML) — so it is that missing key, not a
// document with nothing to say. A partial edge's fullCoverageWith
// closure is resolved the same way doc_edges resolves its own targets and
// stored in doc_coverage_completed_with, in authored order.
//
// A defers edge (026 §5.3) is checked, not merely written: the from end must
// be a plan, the `spec` reference must carry a `#sec-N` fragment (unlike
// covers, which tolerates a whole-document claim — a whole-document deferral
// would silently defer sections not yet written), the owner must be named,
// must carry no fragment (an owner is a document, 026 §5.3), and must not
// resolve to the deferring plan itself. The
// owner is
// then resolved exactly as a fullCoverageWith target and stored as the
// edge's sole doc_coverage_completed_with row, at position 0. coverage stays
// NULL for a defers edge — a deferral is not a level. The same entry
// authored twice is one edge, same as covers; the same section deferred to
// two different owners is the contradiction covers refuses for two
// disagreeing levels, refused here as ErrInvalidInput too.
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
// the live doc_edges (closures in doc_coverage_completed_with), true the open
// candidate revision's doc_revision_edges (closures as completed_with JSON).
func writeEdges(tx *sql.Tx, docID int64, kind, project string, fm *designdoc.Frontmatter, candidate bool) error {
	clear := `DELETE FROM doc_edges WHERE from_doc = $1`
	if candidate {
		clear = `DELETE FROM doc_revision_edges WHERE doc_id = $1`
	}
	if _, err := tx.Exec(clear, docID); err != nil {
		return fmt.Errorf("clear edges of doc %d: %w", docID, err)
	}
	// The two covers defects a single header settles on its own (026 §5.1,
	// §7). Both are checked here rather than in the loop below: designdoc.Refs
	// reads one coverage key and drops an entry naming no spec, so neither
	// reaches frontmatterEdges.
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
		}
	}
	// Resolve every covers entry first: when two entries reach the same rule,
	// the more specific one (coveredRule.Depth) writes its edge and the other
	// skips that rule, so `sec-3: none` with `sec-3.1: full` is not a
	// contradiction. Entries of equal depth still meet the level check below.
	edges := frontmatterEdges(fm)
	resolvedCovers := map[string][]coveredRule{}
	deepest := map[int64]int{}
	for _, e := range edges {
		if e.typ != "covers" || kind != "plan" {
			continue
		}
		if _, ok := resolvedCovers[e.ref]; ok {
			continue
		}
		rules, err := coversRules(tx, project, e.ref)
		if err != nil {
			return err
		}
		resolvedCovers[e.ref] = rules
		for _, r := range rules {
			if d, ok := deepest[r.ID]; !ok || r.Depth > d {
				deepest[r.ID] = r.Depth
			}
		}
	}
	seen := map[docEdgeRow]docEdgeSeen{}
	for _, e := range edges {
		base, fragment := designdoc.SplitFragment(e.ref)
		var toDoc int64
		var resolved bool
		var err error
		if e.typ != "covers" {
			// A covers entry resolves to rules instead (coversRules below).
			if toDoc, resolved, err = resolveDocRef(tx, project, base); err != nil {
				return err
			}
		}
		if e.typ == "blockedBy" {
			if err := checkPlanOrdering(tx, docID, kind, e.ref, toDoc, resolved); err != nil {
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
			if kind != "plan" {
				return fmt.Errorf("doc %d covers %q, but covers is plan-only and doc %d is a %s (026 §5.1): %w",
					docID, e.ref, docID, kind, ErrInvalidInput)
			}
			level = strings.TrimSpace(e.coverage)
			if level == "" {
				// Only the mapping form arrives empty: the bare form decodes
				// straight to "full" (designdoc.Coverage.UnmarshalYAML), and
				// `coverage` is required on a qualified entry (026 §5.1).
				return fmt.Errorf("doc %d covers %q with no coverage level (026 §5.1): %w",
					docID, e.ref, ErrInvalidInput)
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

		row := docEdgeRow{fromAnchor: e.fromAnchor, typ: e.typ}
		var rows []docEdgeRow
		switch {
		case e.typ == "covers":
			// A covers edge runs from the plan to each rule the entry
			// resolves to (WL-SPEC-77 §4); an entry naming no rule keeps its
			// reference verbatim.
			rules := resolvedCovers[e.ref]
			for _, r := range rules {
				if r.Depth < deepest[r.ID] {
					continue
				}
				rr := row
				rr.toRule = r.ID
				rows = append(rows, rr)
			}
			if len(rules) == 0 {
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
		for _, row := range rows {
			if err := insertDocEdge(tx, docID, e, row, level, closure, seen, candidate); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertDocEdge writes one resolved row of writeEdges: the dedupe and
// contradiction checks against rows already seen in this frontmatter, the
// row itself, and its closure — doc_coverage_completed_with rows for a live
// edge, completed_with JSON for a candidate's.
func insertDocEdge(tx *sql.Tx, docID int64, e docEdgeRef, row docEdgeRow, level string, closure []closureRef, seen map[docEdgeRow]docEdgeSeen, candidate bool) error {
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
		return nil
	}
	seen[row] = docEdgeSeen{level: level, closure: closure}

	var coverageCol sql.NullString
	if e.typ == "covers" {
		coverageCol = sql.NullString{String: level, Valid: true}
	}
	if candidate {
		var completedWith any // NULL unless the edge carries a closure
		if level == "partial" || e.typ == "defers" {
			items := make([]map[string]any, 0, len(closure))
			for _, c := range closure {
				if c.resolved {
					items = append(items, map[string]any{"to_doc": c.toDoc})
				} else {
					items = append(items, map[string]any{"to_external": c.toExternal})
				}
			}
			b, err := json.Marshal(items)
			if err != nil {
				return err
			}
			completedWith = string(b)
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_revision_edges
			   (doc_id, from_anchor, type, to_doc, to_rule, to_anchor, to_external, coverage, completed_with)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			docID, nullText(row.fromAnchor), row.typ, nullID(row.toDoc), nullID(row.toRule),
			nullText(row.toAnchor), nullText(row.toExternal), coverageCol, completedWith,
		); err != nil {
			return fmt.Errorf("insert candidate %s edge of doc %d to %q: %w", e.typ, docID, e.ref, err)
		}
		return nil
	}
	var edgeID int64
	if err := tx.QueryRow(
		`INSERT INTO doc_edges
		   (from_doc, from_anchor, type, to_doc, to_rule, to_anchor, to_external, coverage)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		docID, nullText(row.fromAnchor), row.typ, nullID(row.toDoc), nullID(row.toRule),
		nullText(row.toAnchor), nullText(row.toExternal), coverageCol,
	).Scan(&edgeID); err != nil {
		return fmt.Errorf("insert %s edge from doc %d to %q: %w", e.typ, docID, e.ref, err)
	}

	if level != "partial" && e.typ != "defers" {
		return nil
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
	return nil
}

// repointCovers resolves an unresolved covers edge whose document has just
// arrived: it is replaced by one edge per rule the reference now names,
// each carrying the old edge's level and fullCoverageWith closure. An edge
// the plan already holds is kept as it is. A reference that still names no
// rule is left in place and reports false.
func repointCovers(tx *sql.Tx, project string, edgeID, fromDoc int64, ref string) (bool, error) {
	rules, err := coversRules(tx, project, ref)
	if err != nil || len(rules) == 0 {
		return false, err
	}
	for _, rule := range rules {
		r := rule.ID
		var newID int64
		err := tx.QueryRow(
			`INSERT INTO doc_edges (from_doc, from_anchor, type, to_rule, coverage)
			 SELECT from_doc, from_anchor, type, $2, coverage FROM doc_edges WHERE id = $1
			 ON CONFLICT (from_doc, coalesce(from_anchor,''), type, coalesce(to_doc, 0),
			              coalesce(to_rule, 0), coalesce(to_anchor,''), coalesce(to_external,''))
			 DO NOTHING
			 RETURNING id`, edgeID, r).Scan(&newID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("re-point covers edge %d of doc %d to rule %d: %w", edgeID, fromDoc, r, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_coverage_completed_with (edge_id, position, to_doc, to_external)
			 SELECT $2, position, to_doc, to_external FROM doc_coverage_completed_with WHERE edge_id = $1`,
			edgeID, newID); err != nil {
			return false, fmt.Errorf("copy fullCoverageWith of covers edge %d: %w", edgeID, err)
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
// blockedBy orders whole plan documents (025 §5, §9.3); it projects as
// wl:blockedByPlan.
func frontmatterEdges(fm *designdoc.Frontmatter) []docEdgeRef {
	var out []docEdgeRef
	for _, r := range fm.RefsFor(designdoc.StoredRels...) {
		e := docEdgeRef{fromAnchor: r.SrcAnchor, typ: r.Rel, ref: r.Ref}
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
// every doc_edges/doc_coverage_completed_with row that stayed unresolved
// (to_external set) after rebuildEdges last ran, plus every doc_edges row
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
		        coalesce(e.from_anchor,''), e.type, w.to_external,
		        0::bigint, '', ''
		   FROM doc_coverage_completed_with w
		   JOIN doc_edges e ON e.id = w.edge_id
		   JOIN docs d ON d.id = e.from_doc
		  WHERE w.to_external IS NOT NULL AND d.deleted_at IS NULL
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
		        coalesce(d.number,0), coalesce(d.status,''), cw.items,
		        coalesce(rp.key || '-RULE-' || r.number, '')
		   FROM doc_edges e
		   LEFT JOIN rules r ON r.id = e.to_rule
		   LEFT JOIN projects rp ON rp.id = r.project_id
		   LEFT JOIN LATERAL (
		            SELECT dr.doc_id, dr.anchor FROM doc_rules dr
		             WHERE dr.rule_id = e.to_rule
		             ORDER BY dr.doc_id, dr.position LIMIT 1
		        ) ra ON true
		   LEFT JOIN docs d ON d.id = coalesce(e.to_doc, ra.doc_id)
		   LEFT JOIN LATERAL (
		            SELECT coalesce(json_agg(coalesce(wd.slug, w.to_external)
		                             ORDER BY w.position), '[]')::text AS items
		              FROM doc_coverage_completed_with w
		              LEFT JOIN docs wd ON wd.id = w.to_doc
		             WHERE w.edge_id = e.id
		        ) cw ON true
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
	// anchor here, and its source anchor is the far one. completedWith is
	// read the same way regardless of direction — it describes the stored
	// row itself (e.id), not which end docID sits at.
	//
	// A covers edge runs to a rule, so it lands here at each section of docID
	// arranging a rule it reaches (covered_sections), once per section.
	inRows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT e.type, coalesce(e.to_anchor,''), e.from_doc, coalesce(e.from_anchor,''), '',
		        d.project_id, d.slug, d.kind, coalesce(d.number,0), d.status, cw.items, ''
		   FROM (SELECT id, type, from_doc, from_anchor, to_anchor
		           FROM doc_edges WHERE to_doc = $1
		         UNION ALL
		         SELECT edge_id, 'covers', plan_id, NULL, anchor
		           FROM covered_sections WHERE doc_id = $1) e
		   JOIN docs d ON d.id = e.from_doc
		   LEFT JOIN LATERAL (
		            SELECT coalesce(json_agg(coalesce(wd.slug, w.to_external)
		                             ORDER BY w.position), '[]')::text AS items
		              FROM doc_coverage_completed_with w
		              LEFT JOIN docs wd ON wd.id = w.to_doc
		             WHERE w.edge_id = e.id
		        ) cw ON true
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
// status, the completedWith closure, then the rule ref. The closure is a
// JSON array of strings (never NULL —
// the caller's lateral join coalesces an edge with no
// doc_coverage_completed_with rows to "[]", following NeedsPlanning's
// json_agg-to-text convention rather than scanning a native Postgres array).
func scanDocEdges(rows *sql.Rows) ([]model.DocEdge, error) {
	defer rows.Close()
	var out []model.DocEdge
	for rows.Next() {
		var e model.DocEdge
		var completedWithJSON string
		if err := rows.Scan(&e.Type, &e.FromAnchor, &e.ToDoc, &e.ToAnchor, &e.ToExternal,
			&e.ToProject, &e.ToSlug, &e.ToKind, &e.ToNumber, &e.ToStatus,
			&completedWithJSON, &e.ToRule); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(completedWithJSON), &e.CompletedWith); err != nil {
			return nil, fmt.Errorf("decode completedWith of doc edge: %w", err)
		}
		// "[]" unmarshals to a non-nil empty slice; nil is the DocEdge zero
		// value for "no completedWith", so every equality check downstream —
		// tests included — sees one consistent absence rather than two.
		if len(e.CompletedWith) == 0 {
			e.CompletedWith = nil
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
