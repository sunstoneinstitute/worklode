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
		`SELECT e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc,0),
		        coalesce(e.to_anchor,''), coalesce(e.to_external,''),
		        coalesce(d.project_id,''), coalesce(d.slug,''), coalesce(d.kind,''),
		        coalesce(d.number,0), cw.items
		   FROM doc_edges e
		   LEFT JOIN docs d ON d.id = e.to_doc
		   LEFT JOIN LATERAL (
		            SELECT coalesce(json_agg(coalesce(wd.slug, w.to_external)
		                             ORDER BY w.position), '[]')::text AS items
		              FROM doc_coverage_completed_with w
		              LEFT JOIN docs wd ON wd.id = w.to_doc
		             WHERE w.edge_id = e.id
		        ) cw ON true
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
	// anchor here, and its source anchor is the far one. completedWith is
	// read the same way regardless of direction — it describes the stored
	// row itself (e.id), not which end docID sits at.
	inRows, err := s.db.QueryContext(ctx,
		`SELECT e.type, coalesce(e.to_anchor,''), e.from_doc, coalesce(e.from_anchor,''), '',
		        d.project_id, d.slug, d.kind, coalesce(d.number,0), cw.items
		   FROM doc_edges e
		   JOIN docs d ON d.id = e.from_doc
		   LEFT JOIN LATERAL (
		            SELECT coalesce(json_agg(coalesce(wd.slug, w.to_external)
		                             ORDER BY w.position), '[]')::text AS items
		              FROM doc_coverage_completed_with w
		              LEFT JOIN docs wd ON wd.id = w.to_doc
		             WHERE w.edge_id = e.id
		        ) cw ON true
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
// five stored ones, the joined far end's project, slug, kind and number,
// then the completedWith closure as a JSON array of strings (never NULL —
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
			&e.ToProject, &e.ToSlug, &e.ToKind, &e.ToNumber, &completedWithJSON); err != nil {
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
