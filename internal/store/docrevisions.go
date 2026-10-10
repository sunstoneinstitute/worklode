package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ReviseDoc opens a candidate revision against an accepted spec or ADR: a copy
// of the current body to edit while the accepted version stays authoritative
// (WL-REQ-170). One candidate at a time.
//
// Plans are edited in place with UpdateDocBody (WL-REQ-172) and drafts are edited
// in place because there is nothing to revise against; both are
// ErrInvalidInput.
func ReviseDoc(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	if d.kind == "plan" {
		return fmt.Errorf("doc %d is a plan: plans are edited in place (WL-SPEC-77 §11): %w", id, ErrInvalidInput)
	}
	if d.status != "accepted" {
		return fmt.Errorf("doc %d is %s: only an accepted document is revised (WL-SPEC-77 §9): %w",
			id, d.status, ErrInvalidInput)
	}
	if err := openRevision(tx, now, id, d.body, actorID); err != nil {
		return err
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "revision", "new": "open"})
}

// openRevision inserts document id's candidate revision, starting from body
// and the live edge set (WL-REQ-164). ErrRevisionExists when one is open.
func openRevision(tx *sql.Tx, now time.Time, id int64, body, actorID string) error {
	if _, err := tx.Exec(
		`INSERT INTO doc_revisions (doc_id, body, created_by, created_at) VALUES ($1, $2, $3, $4)`,
		id, body, nullText(actorID), now.UTC().Truncate(time.Second),
	); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("doc %d already has an open revision: %w", id, ErrRevisionExists)
		}
		return fmt.Errorf("open revision of doc %d: %w", id, err)
	}
	// The candidate starts from the live edge set (WL-REQ-164).
	if _, err := tx.Exec(
		`INSERT INTO doc_revision_edges
		   (doc_id, from_anchor, type, to_doc, to_anchor, to_external, to_rule, owner_doc, owner_external)
		 SELECT from_doc, from_anchor, type, to_doc, to_anchor, to_external, to_rule, owner_doc, owner_external
		   FROM doc_edges WHERE from_doc = $1`, id); err != nil {
		return fmt.Errorf("copy edges of doc %d into its revision: %w", id, err)
	}
	// And from the live arrangement (WL-REQ-1297). A candidate's anchor is
	// where its text holds the entry, so it starts as the derived one.
	if _, err := tx.Exec(
		`INSERT INTO doc_revision_rules (doc_id, position, rule_id, rule_version, heading, depth, slug, anchor)
		 SELECT doc_id, position, rule_id, rule_version, heading, depth, slug, anchor
		   FROM doc_entries WHERE doc_id = $1`, id); err != nil {
		return fmt.Errorf("copy arrangement of doc %d into its revision: %w", id, err)
	}
	return nil
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
	parsed, err := parseSpecWrite(d.kind, body)
	if err != nil {
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
	if err := dropOmittedRevisionRules(tx, id, parsed.doc); err != nil {
		return err
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "revision", "new": "updated"})
}

// DiscardRevision withdraws a document's open candidate revision without
// landing it — the close-without-merging half of the pull request WL-REQ-170
// says a revision structurally is. Deleting the row frees the
// one-candidate-per-document slot, so the next ReviseDoc succeeds immediately
// instead of hitting ErrRevisionExists. ErrNotFound if no revision is open.
//
// Gated on the document's owner or the revision's created_by: anyone with
// doc.write may propose a revision, and either its author or the document's
// owner may withdraw it. That pairing is what keeps ReviseDoc open — an
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
	if err := checkRevisionDiscarder(id, d.owner, createdBy.String, actorID); err != nil {
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
// WL-REQ-167 constraint check against the accepted version and, when clean, swaps
// the body, bumps the version, rebuilds sections, lands the candidate's own
// edge set (title and issued are columns, untouched by the body), stamps
// last_revised_in on exactly the changed anchors, publishes every anchor the
// new version carries, supersedes the documents its rules retire
// (supersedeRetiredDocs), and consumes the candidate — one transaction, owner-gated like AcceptDoc.
//
// The append-only rule protects the anchors the accepted version *published*
// (WL-REQ-170), so a never-published row that disappears is legal; renumbering
// and excess depth are violations regardless.
func AcceptRevision(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*model.Doc, error) {
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, err
	}
	if err := checkDocOwner(id, d.owner, actorID); err != nil {
		return nil, err
	}
	if d.kind == "plan" {
		return nil, fmt.Errorf("doc %d is a plan: plans are edited in place (WL-SPEC-77 §11): %w", id, ErrInvalidInput)
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
	// A rule the candidate unarranged leaves on purpose (WL-REQ-1297); any
	// other rule leaving is refused once the arrangement lands.
	dropped, err := unarrangedRules(tx, id)
	if err != nil {
		return nil, err
	}
	diff := designdoc.CompareSections(accepted.doc, candidate.doc, docDepthLimit)
	if err := checkAnchorFreeze(fmt.Sprintf("revision of doc %d cannot be accepted", id),
		&diff, prior); err != nil {
		return nil, err
	}
	var accKeys []string
	for _, sec := range accepted.doc.Sections {
		if sec.Anchor != "" {
			accKeys = append(accKeys, prior[sec.Anchor].key)
		}
	}

	ts := now.UTC().Truncate(time.Second)
	// Snapshot the version this accept replaces (WL-REQ-164) and move to the
	// next one, before any other write.
	version, err := bumpDocVersion(tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, updated_at = $3 WHERE id = $1`, id, storedBody(d.kind, candidateBody), ts,
	); err != nil {
		return nil, fmt.Errorf("land revision of doc %d: %w", id, err)
	}
	// The section rebuild matches the candidate body against the rules the
	// candidate arranged, a rule arranged from another spec included.
	after, err := rebuildSectionsFrom(tx, id, d.kind, candidate.doc, version, prior, "doc_revision_rules", eventID)
	if err != nil {
		return nil, err
	}
	if err := landRevisionEdges(tx, id); err != nil {
		return nil, err
	}
	if err := checkRulesLeft(tx, id, prior, after, dropped); err != nil {
		return nil, err
	}

	// WL-REQ-167 rule 3: last_revised_in moves on exactly the sections whose
	// content changed. Touching it elsewhere invalidates valid claims.
	candKeys, err := entryKeys(tx, id)
	if err != nil {
		return nil, err
	}
	if changed := designdoc.ChangedEntries(accepted.doc, candidate.doc, accKeys, candKeys); len(changed) > 0 {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET last_revised_in = $3
			  WHERE doc_id = $1 AND position = ANY($2::int[])`,
			id, changed, version,
		); err != nil {
			return nil, fmt.Errorf("stamp last_revised_in on doc %d: %w", id, err)
		}
	}
	// Unarranging a rule is substantive for the spec (WL-REQ-1297): one
	// review task for its reviewers.
	if len(dropped) > 0 {
		if err := reviewUnarranged(tx, now, id, version, d.owner, actorID, dropped, eventID); err != nil {
			return nil, err
		}
	}

	// WL-REQ-170: a landed revision is a reviewed replacement for the text the
	// WL-REQ-171 marks were on, so nothing here is still "approved text, modified
	// since". The section rebuild carries the flag forward deliberately — one
	// patch must not clear another's mark — so this is where it ends.
	if err := ClearPatchedSections(tx, id, version); err != nil {
		return nil, err
	}
	ruleIDs, bumped, err := publishDocSections(tx, id)
	if err != nil {
		return nil, err
	}
	if err := gateDocRuleVersions(tx, now, id, ruleIDs, bumped, d.owner, actorID, eventID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM doc_revisions WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("consume revision of doc %d: %w", id, err)
	}
	if err := supersedeRetiredDocs(tx, ts, id, eventID); err != nil {
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

// dropOmittedRevisionRules unarranges from document id's candidate every rule
// the candidate text leaves out: no heading at its anchor, none with its
// heading text, and none naming it by rule= or a printed ref (WL-REQ-1299).
// The candidate body keeps its rule=
// attributes until it lands, where syncRules reads them. A rule= heading
// naming a withdrawn rule the candidate does not arrange yet is refused.
func dropOmittedRevisionRules(tx *sql.Tx, id int64, doc *designdoc.Document) error {
	anchors, headings, named := []string{}, []string{}, []int64{}
	for _, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		anchors, headings = append(anchors, sec.Anchor), append(headings, sec.Title)
		if ref := namedRef(sec); ref != "" {
			rid, err := ruleByRefString(tx, ref)
			if err != nil {
				return err
			}
			var arranged bool
			if err := tx.QueryRow(
				`SELECT EXISTS (SELECT 1 FROM doc_revision_rules WHERE doc_id = $1 AND rule_id = $2)`,
				id, rid).Scan(&arranged); err != nil {
				return fmt.Errorf("read revision arrangement of doc %d: %w", id, err)
			}
			if !arranged {
				if err := refuseWithdrawnRule(tx, rid, ref); err != nil {
					return err
				}
			}
			named = append(named, rid)
		}
	}
	if _, err := tx.Exec(
		`DELETE FROM doc_revision_rules r
		  WHERE r.doc_id = $1 AND r.rule_id IS NOT NULL
		    AND r.anchor <> ALL($2::text[]) AND r.rule_id <> ALL($3::bigint[])
		    AND NOT EXISTS (SELECT 1 FROM rule_versions v
		                     WHERE v.rule_id = r.rule_id AND v.version = r.rule_version AND v.heading = ANY($4::text[]))`,
		id, anchors, named, headings); err != nil {
		return fmt.Errorf("unarrange omitted rules from the revision of doc %d: %w", id, err)
	}
	return nil
}

// unarrangedRules is the rules document id arranges that its candidate
// revision no longer arranges, by row id with each one's ref.
func unarrangedRules(tx *sql.Tx, id int64) (map[int64]string, error) {
	rows, err := tx.Query(
		`SELECT r.id, `+ruleRefSQL("p", "r")+`
		   FROM doc_rules a JOIN rules r ON r.id = a.rule_id JOIN projects p ON p.id = r.project_id
		  WHERE a.doc_id = $1
		    AND a.rule_id NOT IN (SELECT rule_id FROM doc_revision_rules WHERE doc_id = $1 AND rule_id IS NOT NULL)`, id)
	if err != nil {
		return nil, fmt.Errorf("read unarranged rules of doc %d: %w", id, err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var rid int64
		var ref string
		if err := rows.Scan(&rid, &ref); err != nil {
			return nil, fmt.Errorf("scan unarranged rule of doc %d: %w", id, err)
		}
		out[rid] = ref
	}
	return out, rows.Err()
}

// checkRulesLeft is WL-REQ-167 rule 1 over an accepted document's rebuild:
// a rule leaves it only through `lode rule unarrange` (dropped) or `lode
// rule supersede` (withdrawn). Renumbering and changed anchors are accepted.
func checkRulesLeft(tx *sql.Tx, id int64, prior, after map[string]priorSection, dropped map[int64]string) error {
	var gone []int64
	for _, p := range prior {
		rid, ok := strings.CutPrefix(p.key, "rule:")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(rid, 10, 64)
		if err != nil {
			continue
		}
		if _, still := after[p.key]; !still && dropped[n] == "" {
			gone = append(gone, n)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	rows, err := tx.Query(
		`SELECT `+ruleRefSQL("p", "r")+` FROM rules r JOIN projects p ON p.id = r.project_id
		  WHERE r.id = ANY($1) AND r.status NOT IN ('withdrawn', 'superseded') ORDER BY r.number`, gone)
	if err != nil {
		return fmt.Errorf("read rules leaving doc %d: %w", id, err)
	}
	refs, err := scanColumn[string](rows, fmt.Sprintf("rules leaving doc %d", id))
	if err != nil || len(refs) == 0 {
		return err
	}
	return fmt.Errorf("doc %d: %s would leave it other than by `lode rule unarrange` or `lode rule supersede` (WL-REQ-167): %w",
		id, strings.Join(refs, ", "), ErrInvalidInput)
}

// reviewUnarranged mints the one review task landing a revision that
// unarranged rules owes the spec's reviewers (WL-REQ-1297, WL-REQ-1666).
func reviewUnarranged(tx *sql.Tx, now time.Time, id int64, version int, owner, actorID string, dropped map[int64]string, eventID int64) error {
	var project, ref string
	if err := tx.QueryRow(
		`SELECT d.project_id, p.key || '-' || upper(d.kind) || '-' || d.number
		   FROM docs d JOIN projects p ON p.id = d.project_id WHERE d.id = $1`, id).Scan(&project, &ref); err != nil {
		return fmt.Errorf("read doc %d: %w", id, err)
	}
	reviewers, err := docReviewers(tx, id)
	if err != nil {
		return err
	}
	if owner != "" && !slices.Contains(reviewers, owner) {
		reviewers = append([]string{owner}, reviewers...)
	}
	refs := slices.Sorted(maps.Values(dropped))
	task, err := CreateTask(tx, now, TaskInput{
		ProjectID: project,
		Kind:      "review",
		Priority:  "medium",
		Title:     fmt.Sprintf("Review %s v%d: unarranged %s", ref, version, strings.Join(refs, ", ")),
		Body: fmt.Sprintf("Version %d of %s unarranged %s, which is substantive (WL-REQ-1297).\n\nReviewers: %s.\n",
			version, ref, strings.Join(refs, ", "), strings.Join(reviewers, ", ")),
		AboutDoc:  id,
		CreatedBy: actorID,
	}, eventID)
	if err != nil {
		return err
	}
	return MergeEventPayload(tx, eventID, map[string]any{"unarranged": refs, "review_task": task.ID})
}

// landRevisionEdges replaces document id's live edges with its candidate's.
// The candidate row goes when the revision is consumed; its edges cascade.
func landRevisionEdges(tx *sql.Tx, id int64) error {
	if _, err := tx.Exec(`DELETE FROM doc_edges WHERE from_doc = $1`, id); err != nil {
		return fmt.Errorf("clear edges of doc %d: %w", id, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_edges (from_doc, from_anchor, type, to_doc, to_anchor, to_external, to_rule, owner_doc, owner_external)
		 SELECT doc_id, from_anchor, type, to_doc, to_anchor, to_external, to_rule, owner_doc, owner_external
		   FROM doc_revision_edges WHERE doc_id = $1 ORDER BY id`, id); err != nil {
		return fmt.Errorf("land revision edges of doc %d: %w", id, err)
	}
	return nil
}

// checkAnchorFreeze applies WL-REQ-167 rule 4 to a diff between a
// document's accepted text and the text about to replace it: no anchor past
// the depth limit. Both publication paths run it — an accepted revision and
// a WL-REQ-171 in-place patch. Numbers and anchors are derived (WL-REQ-165),
// so a renumbered or dropped anchor is accepted; a rule leaving is
// checkRulesLeft's. what names the act in the refusal message (e.g.
// "revision of doc 25 cannot be accepted").
func checkAnchorFreeze(what string, diff *designdoc.SectionDiff, prior map[string]priorSection) error {
	// Lowering the limit is one-way-safe by construction (WL-REQ-165): the check
	// re-runs at every publication, so a too-deep anchor the accepted version
	// already published is refused here. It gets its own wording — the fix is
	// to raise the limit back, not to restructure the document.
	var orphaned []string
	for _, anchor := range diff.TooDeep {
		if prior[anchor].published {
			orphaned = append(orphaned, anchor)
		}
	}
	if len(orphaned) > 0 {
		return fmt.Errorf(
			"%s: depth limit %d orphans accepted anchors: %s "+
				"(§6.1: lower the limit only for documents never accepted): %w",
			what, docDepthLimit, strings.Join(orphaned, ", "), ErrInvalidInput)
	}
	diff.Removed, diff.Renumbered = nil, nil
	if v := diff.Violations(); len(v) > 0 {
		return fmt.Errorf("%s: %s: %w", what, strings.Join(v, "; "), ErrInvalidInput)
	}
	return nil
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
	r.Body = designdoc.StripRuleRefs(r.Body)
	r.CreatedBy = createdBy.String
	r.CreatedAt = r.CreatedAt.UTC()
	if r.Edges, err = s.revisionEdges(ctx, id); err != nil {
		return nil, err
	}
	return &r, nil
}

// checkRevisionDiscarder gates withdrawing an open candidate on the document's
// owner or the revision's author. Wider than checkDocOwner on purpose:
// accepting is the maintainer's act, but closing a proposal without merging it
// is also the proposer's, which is what lets ReviseDoc stay open to any
// doc.write holder (WL-REQ-170's pull-request analogy).
//
// An empty actorID matches nobody, including a revision or document whose own
// column is empty.
func checkRevisionDiscarder(id int64, owner, createdBy, actorID string) error {
	if actorID != "" && (actorID == owner || actorID == createdBy) {
		return nil
	}
	return fmt.Errorf(
		"revision of doc %d was opened by %q and the doc is owned by %q: %q may discard neither: %w",
		id, createdBy, owner, actorID, ErrForbidden)
}
