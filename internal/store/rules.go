package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ruleRow is one rule as a document's current arrangement holds it.
type ruleRow struct {
	id      int64
	version int
	heading string
	body    string
	anchor  string
	depth   int
}

// syncRules makes a document's rules agree with its parsed source
// (12-spec-refactoring-design-tree.md S8 to S11, S20). Every anchored section
// is a design rule; changed text rewrites the rule's draft version or, once
// that version is accepted, becomes its next version; anything else is a new
// rule numbered from the project's RULE counter. The arrangement
// (doc_rules) is rewritten in section order. Plans never reach here:
// rebuildSectionsFrom returns before calling it.
//
// Matching a section to a prior rule runs in three priority passes over
// the whole document, not per section in document order: anchors are
// positional (`--update-section-anchors` rewrites every `{#sec-N}` from tree
// position, and authors renumber drafts by hand), so resolving matches
// section-by-section can let a later section steal an earlier one's identity
// through the heading fallback once its own anchor match is already taken.
// Pass 1 claims by anchor and heading both matching; pass 2 claims the
// remainder by heading; pass 3 claims what's left by anchor alone. Each
// prior rule is claimed at most once. When two sections share a heading,
// candidates are claimed in document order, one per section: the first
// unclaimed rule with that heading wins. Only after every section has its
// match (or none) does the second walk, in document order, bump/keep rules
// and write doc_rules positions.
func syncRules(tx *sql.Tx, docID int64, doc *designdoc.Document) error {
	var project string
	if err := tx.QueryRow(`SELECT project_id FROM docs WHERE id = $1`, docID).Scan(&project); err != nil {
		return fmt.Errorf("project of doc %d: %w", docID, err)
	}
	prior, err := arrangedRules(tx, docID)
	if err != nil {
		return err
	}
	byAnchor := map[string]*ruleRow{}
	byHeading := map[string][]*ruleRow{}
	for i := range prior {
		byAnchor[prior[i].anchor] = &prior[i]
		byHeading[prior[i].heading] = append(byHeading[prior[i].heading], &prior[i])
	}
	claimed := map[int64]bool{}
	match := make([]*ruleRow, len(doc.Sections))

	// Pass 1: anchor and heading both match.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		if c := byAnchor[sec.Anchor]; c != nil && !claimed[c.id] && c.heading == sec.Title {
			match[i], claimed[c.id] = c, true
		}
	}
	// Pass 2: heading matches a still-unclaimed prior rule.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" || match[i] != nil {
			continue
		}
		for _, c := range byHeading[sec.Title] {
			if !claimed[c.id] {
				match[i], claimed[c.id] = c, true
				break
			}
		}
	}
	// Pass 3: anchor matches a still-unclaimed prior rule.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" || match[i] != nil {
			continue
		}
		if c := byAnchor[sec.Anchor]; c != nil && !claimed[c.id] {
			match[i], claimed[c.id] = c, true
		}
	}

	if _, err := tx.Exec(`DELETE FROM doc_rules WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("clear arrangement of doc %d: %w", docID, err)
	}
	// changed collects the rules this write inserted or revised, so their
	// references can be derived once every doc_rules row below has been
	// written. Deriving inline in this loop would miss a ref into the same
	// document: the section it names has no arrangement row yet (forward) or
	// its own derivation already ran (backward), so it would resolve through
	// an empty or stale doc_rules and drop the edge every time (S26).
	type changedRule struct {
		id   int64
		text string
	}
	var changed []changedRule
	position := 0
	for i, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		m := match[i]
		var id int64
		var version int
		switch {
		case m == nil:
			id, version, err = insertRule(tx, project, sec.Title, sec.Body)
		case m.heading != sec.Title || m.body != sec.Body:
			id, version, err = reviseRule(tx, m.id, sec.Title, sec.Body)
		default:
			id, version = m.id, m.version
		}
		if err != nil {
			return err
		}
		if m == nil || m.heading != sec.Title || m.body != sec.Body {
			changed = append(changed, changedRule{id, sec.Title + "\n" + sec.Body})
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, position, id, version, sec.Level, sec.Anchor); err != nil {
			return fmt.Errorf("arrange rule %d in doc %d: %w", id, docID, err)
		}
		position++
	}
	for _, c := range changed {
		if err := deriveReferences(tx, project, c.id, c.text); err != nil {
			return err
		}
	}
	return nil
}

// arrangedRules reads a document's current arrangement with each rule's
// arranged version text, in position order.
func arrangedRules(tx *sql.Tx, docID int64) ([]ruleRow, error) {
	rows, err := tx.Query(
		`SELECT dc.rule_id, dc.rule_version, cv.heading, cv.body, dc.anchor, dc.depth
		   FROM doc_rules dc
		   JOIN rule_versions cv ON cv.rule_id = dc.rule_id AND cv.version = dc.rule_version
		  WHERE dc.doc_id = $1
		  ORDER BY dc.position`, docID)
	if err != nil {
		return nil, fmt.Errorf("read arrangement of doc %d: %w", docID, err)
	}
	defer rows.Close()
	var out []ruleRow
	for rows.Next() {
		var c ruleRow
		if err := rows.Scan(&c.id, &c.version, &c.heading, &c.body, &c.anchor, &c.depth); err != nil {
			return nil, fmt.Errorf("scan arrangement of doc %d: %w", docID, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ruleSeqKind is the rule's row key in project_entity_seq — the counter
// behind its WL-RULE-<n> number.
const ruleSeqKind = "RULE"

// insertRule mints a rule at version 1 with the project's next RULE number.
// The upsert is the same counter CreateDoc uses for document numbers, held
// under the row lock for the rest of the transaction.
func insertRule(tx *sql.Tx, project, heading, body string) (int64, int, error) {
	var number int64
	if err := tx.QueryRow(
		`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, $2, 2)
		 ON CONFLICT (project_id, kind) DO UPDATE SET next = project_entity_seq.next + 1
		 RETURNING next - 1`, project, ruleSeqKind).Scan(&number); err != nil {
		return 0, 0, fmt.Errorf("allocate rule number in %s: %w", project, err)
	}
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO rules (project_id, number) VALUES ($1, $2) RETURNING id`,
		project, number).Scan(&id); err != nil {
		return 0, 0, fmt.Errorf("insert rule %s RULE %d: %w", project, number, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, 1, $2, $3)`,
		id, heading, body); err != nil {
		return 0, 0, fmt.Errorf("insert rule %d v1: %w", id, err)
	}
	return id, 1, nil
}

// reviseRule records changed text on a rule (S10, S35). While the
// rule's newest version is a draft, the text is rewritten in place: an
// autosaving editor may write hundreds of times before anything is accepted,
// and those intermediate states have no reader. Once the newest version is
// accepted it is locked, and the change becomes the next version, itself a
// draft until the arranging document is accepted.
func reviseRule(tx *sql.Tx, id int64, heading, body string) (int64, int, error) {
	var status string
	var version int
	if err := tx.QueryRow(`SELECT status, version FROM rules WHERE id = $1`, id).Scan(&status, &version); err != nil {
		return 0, 0, fmt.Errorf("read rule %d: %w", id, err)
	}
	if status == "draft" {
		if _, err := tx.Exec(
			`UPDATE rule_versions SET heading = $3, body = $4 WHERE rule_id = $1 AND version = $2`,
			id, version, heading, body); err != nil {
			return 0, 0, fmt.Errorf("rewrite rule %d v%d: %w", id, version, err)
		}
		if _, err := tx.Exec(`UPDATE rules SET updated_at = now() WHERE id = $1`, id); err != nil {
			return 0, 0, fmt.Errorf("touch rule %d: %w", id, err)
		}
		return id, version, nil
	}
	version, err := bumpRuleVersion(tx, id, heading, body)
	if err != nil {
		return 0, 0, err
	}
	return id, version, nil
}

// publishDocSections marks a document's sections published and its arranged
// rules accepted. Accepting a document accepts every draft rule it
// arranges and writes nothing else (S11).
func publishDocSections(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(`UPDATE doc_sections SET published = true WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("publish sections of doc %d: %w", docID, err)
	}
	return acceptDocRules(tx, docID)
}

// acceptDocRules accepts every draft rule a document's current
// arrangement holds (S11). Called both when a document is accepted and when
// ensureRules backfills the arrangement of a document that was already
// accepted before the rule tables existed. A plan contains no rules
// (WL-SPEC-77 §4); the d.kind <> 'plan' guard keeps accepting a plan from
// ever flipping a rule.
func acceptDocRules(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(
		`UPDATE rules SET status = 'accepted', updated_at = now()
		  WHERE status = 'draft'
		    AND id IN (
		      SELECT dc.rule_id FROM doc_rules dc JOIN docs d ON d.id = dc.doc_id
		       WHERE dc.doc_id = $1 AND d.kind <> 'plan')`, docID); err != nil {
		return fmt.Errorf("accept rules of doc %d: %w", docID, err)
	}
	return nil
}

// GetRule reads one rule by its project key and number, with the current
// version's text and every document whose arrangement holds it.
func (s *Store) GetRule(ctx context.Context, projectKey string, number int64) (*model.Rule, error) {
	c := &model.Rule{}
	var owner sql.NullString
	var tagsRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.project_id, p.key, c.number, c.status, c.version,
		        cv.heading, cv.body, c.owner, c.tags, c.created_at, c.updated_at
		   FROM rules c
		   JOIN projects p ON p.id = c.project_id
		   JOIN rule_versions cv ON cv.rule_id = c.id AND cv.version = c.version
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number,
	).Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Status, &c.Version,
		&c.Heading, &c.Body, &owner, &tagsRaw, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read rule %s-RULE-%d: %w", projectKey, number, err)
	}
	c.Owner = owner.String
	tags, err := scanTextArray(tagsRaw)
	if err != nil {
		return nil, fmt.Errorf("scan tags of rule %d: %w", c.ID, err)
	}
	c.Tags = nonNil(tags)
	c.Ref = fmt.Sprintf("%s-RULE-%d", c.ProjectKey, c.Number)

	arr, err := s.ruleArrangements(ctx, []int64{c.ID})
	if err != nil {
		return nil, err
	}
	c.ArrangedIn = nonNil(arr[c.ID])

	prows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT d.id, p.key || '-PLAN-' || coalesce(d.number::text, d.slug), d.status, c.coverage
		   FROM covered_rules c
		   JOIN docs d ON d.id = c.plan_id AND d.deleted_at IS NULL
		   JOIN projects p ON p.id = d.project_id
		  WHERE c.rule_id = $1
		  ORDER BY d.id, c.coverage`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read plans covering rule %d: %w", c.ID, err)
	}
	cov, err := collectRows(prows, "plans covering rule", func(r rowScanner) (model.RulePlan, error) {
		var rp model.RulePlan
		err := r.Scan(&rp.Doc, &rp.DocRef, &rp.Status, &rp.Coverage)
		return rp, err
	})
	if err != nil {
		return nil, err
	}
	c.CoveredBy = nonNil(cov)

	c.GovernedTasks = []model.RuleTask{}
	trows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.state, g.source, g.rule_version
		   FROM task_governed_by g JOIN tasks t ON t.id = g.task_id
		  WHERE g.rule_id = $1
		  ORDER BY g.created_at DESC, t.id`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read governed tasks of rule %d: %w", c.ID, err)
	}
	defer trows.Close()
	for trows.Next() {
		var gt model.RuleTask
		if err := trows.Scan(&gt.ID, &gt.Title, &gt.State, &gt.Source, &gt.RuleVersion); err != nil {
			return nil, fmt.Errorf("scan governed task of rule %d: %w", c.ID, err)
		}
		c.GovernedTasks = append(c.GovernedTasks, gt)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	erows, err := s.db.QueryContext(ctx, ruleEdgesSQL, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read edges of rule %d: %w", c.ID, err)
	}
	defer erows.Close()
	if c.Edges, err = scanRuleEdges(erows); err != nil {
		return nil, err
	}
	return c, nil
}

// RuleFilter narrows ListRules. Zero-valued fields do not filter.
type RuleFilter struct {
	Project string
	// Doc narrows to the rules in this document's current arrangement,
	// returned in arrangement order.
	Doc    int64
	Status string
}

// ListRules reads rules with their current heading, owner, tags and every
// arrangement holding them. Body, governed tasks and edges are left empty, as
// a document list leaves its bodies: GetRule reads those. Without a Doc
// filter the order is by project key and number.
func (s *Store) ListRules(ctx context.Context, f RuleFilter) ([]model.Rule, error) {
	if f.Status != "" && !ruleStatuses[f.Status] {
		return nil, fmt.Errorf("rule status %q: must be draft, accepted, superseded or withdrawn: %w", f.Status, ErrInvalidInput)
	}
	q := `SELECT c.id, c.project_id, p.key, c.number, c.status, c.version,
	             cv.heading, c.owner, c.tags, c.created_at, c.updated_at
	        FROM rules c
	        JOIN projects p ON p.id = c.project_id
	        JOIN rule_versions cv ON cv.rule_id = c.id AND cv.version = c.version`
	args := []any{f.Project, f.Status}
	order := ` ORDER BY p.key, c.number`
	if f.Doc != 0 {
		q += ` JOIN doc_rules dc ON dc.rule_id = c.id AND dc.doc_id = $3`
		args = append(args, f.Doc)
		order = ` ORDER BY dc.position`
	}
	q += ` WHERE ($1 = '' OR c.project_id = $1) AND ($2 = '' OR c.status = $2)` + order
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	out := []model.Rule{}
	var ids []int64
	for rows.Next() {
		var c model.Rule
		var owner sql.NullString
		var tagsRaw string
		if err := rows.Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Status, &c.Version,
			&c.Heading, &owner, &tagsRaw, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		tags, err := scanTextArray(tagsRaw)
		if err != nil {
			return nil, fmt.Errorf("scan tags of rule %d: %w", c.ID, err)
		}
		c.Owner, c.Tags = owner.String, nonNil(tags)
		c.Ref = fmt.Sprintf("%s-RULE-%d", c.ProjectKey, c.Number)
		c.GovernedTasks, c.Edges, c.CoveredBy = []model.RuleTask{}, []model.RuleEdge{}, []model.RulePlan{}
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	arr, err := s.ruleArrangements(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ArrangedIn = nonNil(arr[out[i].ID])
	}
	return out, nil
}

// ruleArrangements reads every current arrangement of the given rules,
// keyed by rule id, each rule's placements ordered by document and position.
func (s *Store) ruleArrangements(ctx context.Context, ids []int64) (map[int64][]model.RuleArrangement, error) {
	out := map[int64][]model.RuleArrangement{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT dc.rule_id, dc.doc_id, p.key, d.kind, d.number, dc.anchor, dc.position, dc.depth, dc.rule_version
		   FROM doc_rules dc
		   JOIN docs d ON d.id = dc.doc_id
		   JOIN projects p ON p.id = d.project_id
		  WHERE dc.rule_id = ANY($1)
		  ORDER BY dc.rule_id, dc.doc_id, dc.position`, ids)
	if err != nil {
		return nil, fmt.Errorf("read rule arrangements: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a model.RuleArrangement
		var ruleID int64
		var key, kind string
		var docNumber sql.NullInt64
		if err := rows.Scan(&ruleID, &a.Doc, &key, &kind, &docNumber, &a.Anchor, &a.Position, &a.Depth, &a.RuleVersion); err != nil {
			return nil, fmt.Errorf("scan rule arrangement: %w", err)
		}
		a.DocRef = fmt.Sprintf("%s-%s-%d", key, strings.ToUpper(kind), docNumber.Int64)
		out[ruleID] = append(out[ruleID], a)
	}
	return out, rows.Err()
}

// ListRuleVersions is a rule's version history, newest first.
func (s *Store) ListRuleVersions(ctx context.Context, projectKey string, number int64) ([]model.RuleVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cv.version, cv.heading, cv.created_at
		   FROM rule_versions cv
		   JOIN rules c ON c.id = cv.rule_id
		   JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2
		  ORDER BY cv.version DESC`, projectKey, number)
	if err != nil {
		return nil, fmt.Errorf("list versions of rule %s-RULE-%d: %w", projectKey, number, err)
	}
	defer rows.Close()
	out := []model.RuleVersion{}
	for rows.Next() {
		var v model.RuleVersion
		if err := rows.Scan(&v.Version, &v.Heading, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan version of rule %s-RULE-%d: %w", projectKey, number, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rule %s-RULE-%d: %w", projectKey, number, ErrNotFound)
	}
	return out, nil
}

// GetRuleVersion reads a rule as it stood at one version: the detail
// with Version, Heading and Body taken from that version's row. ArrangedIn
// and GovernedTasks are the current ones. For a superseded version the
// outgoing edges are the ones snapshotted at that version
// (rule_edge_versions); inbound edges are the current ones, since they are
// owned by the other rule.
func (s *Store) GetRuleVersion(ctx context.Context, projectKey string, number int64, version int) (*model.Rule, error) {
	c, err := s.GetRule(ctx, projectKey, number)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT heading, body FROM rule_versions WHERE rule_id = $1 AND version = $2`,
		c.ID, version).Scan(&c.Heading, &c.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("rule %s v%d: %w", c.Ref, version, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read rule %s v%d: %w", c.Ref, version, err)
	}
	if version != c.Version {
		inbound := slices.DeleteFunc(c.Edges, func(e model.RuleEdge) bool { return e.From == c.Ref })
		rows, err := s.db.QueryContext(ctx, ruleEdgeSnapshotSQL, c.ID, version)
		if err != nil {
			return nil, fmt.Errorf("read edges of rule %s v%d: %w", c.Ref, version, err)
		}
		defer rows.Close()
		out, err := scanRuleEdges(rows)
		if err != nil {
			return nil, err
		}
		c.Edges = append(out, inbound...)
	}
	c.Version = version
	return c, nil
}

// EditRule writes a rule's new heading and body by regenerating the
// arranging document's body around it (12-spec-refactoring-design-tree.md
// S13, S14, S35). A draft document is written through UpdateDocBody, so
// syncRules rewrites the rule's draft version in place. An accepted
// document is written through its candidate revision, opened here when none
// is open; the rule's next version appears when the revision lands. A
// plan contains no rules, so the rule writes through the spec or ADR that
// arranges it, never through a covering plan. A rule arranged in
// no spec or ADR, or in more than one, is refused: editing is document-first
// in this stage. Returns the arranging document's id.
func EditRule(tx *sql.Tx, now time.Time, projectKey string, number int64, in model.EditRuleInput, actorID string, eventID int64) (int64, error) {
	if strings.TrimSpace(in.Heading) == "" {
		return 0, fmt.Errorf("rule heading is required: %w", ErrInvalidInput)
	}
	ruleID, err := RuleIDByRef(tx, projectKey, number)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(
		`SELECT dc.doc_id, dc.anchor FROM doc_rules dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.rule_id = $1 AND d.kind <> 'plan' AND d.deleted_at IS NULL
		  ORDER BY dc.doc_id`, ruleID)
	if err != nil {
		return 0, fmt.Errorf("read arrangements of rule %d: %w", ruleID, err)
	}
	var docIDs []int64
	var anchors []string
	for rows.Next() {
		var id int64
		var anchor string
		if err := rows.Scan(&id, &anchor); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan arrangement of rule %d: %w", ruleID, err)
		}
		docIDs, anchors = append(docIDs, id), append(anchors, anchor)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read arrangements of rule %d: %w", ruleID, err)
	}
	switch len(docIDs) {
	case 0:
		return 0, fmt.Errorf("rule %s-RULE-%d is arranged in no spec or ADR; edit the document instead: %w", projectKey, number, ErrInvalidInput)
	case 1:
	default:
		return 0, fmt.Errorf("rule %s-RULE-%d is arranged in %d specs or ADRs; editing a rule shared between them is not supported: %w", projectKey, number, len(docIDs), ErrInvalidInput)
	}
	docID, anchor := docIDs[0], anchors[0]

	var kind, status, body string
	if err := tx.QueryRow(`SELECT kind, status, body FROM docs WHERE id = $1 FOR UPDATE`, docID).Scan(&kind, &status, &body); err != nil {
		return 0, fmt.Errorf("load doc %d: %w", docID, err)
	}
	editable := body
	revising := kind != "plan" && status != "draft"
	if revising {
		var candidate sql.NullString
		if err := tx.QueryRow(`SELECT body FROM doc_revisions WHERE doc_id = $1`, docID).Scan(&candidate); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("load revision of doc %d: %w", docID, err)
		}
		if candidate.Valid {
			editable = candidate.String
		} else if err := ReviseDoc(tx, now, docID, actorID, eventID); err != nil {
			return 0, err
		}
	}
	parsed, err := designdoc.Parse([]byte(editable))
	if err != nil {
		return 0, fmt.Errorf("parse doc %d: %w", docID, err)
	}
	sec := parsed.SectionByAnchor(anchor)
	if sec == nil {
		return 0, fmt.Errorf("rule %s-RULE-%d: its section %s is not in the editable body of doc %d: %w", projectKey, number, anchor, docID, ErrInvalidInput)
	}
	sec.Title = in.Heading
	sec.Body = sectionBody(in.Body, sec.Index == len(parsed.Sections)-1)
	next := string(parsed.Bytes())
	if revising {
		return docID, UpdateRevision(tx, now, docID, next, eventID)
	}
	_, err = UpdateDocBody(tx, now, docID, next, 0, eventID)
	return docID, err
}

// sectionBody normalises a submitted rule body to the shape designdoc.Parse
// would have produced for it, so re-parsing the regenerated document yields
// the sections and anchors it had before. Document.Bytes writes the heading
// line and the body with nothing between them, so a body that does not start
// on a fresh line after the heading, or does not end in one, swallows the
// next heading and orphans that rule. A parsed body carries a blank line
// before the next heading; the last section's runs to EOF and ends in a
// single newline, so a trailing blank line is added only when a heading
// follows.
func sectionBody(body string, last bool) string {
	body = "\n" + strings.Trim(body, "\r\n") + "\n"
	if !last {
		body += "\n"
	}
	return body
}

// textArrayMap decodes a text[] column's raw Postgres array literal (e.g.
// "{storage,search}") into a []string. pgx's stdlib database/sql driver
// hands back that literal as a plain string rather than converting it, so
// reading a native array column needs this one explicit decode step; no
// existing helper in the package does it, because every other []string
// column here (project focus, actor groups) is stored as jsonb instead.
var textArrayMap = pgtype.NewMap()

// scanTextArray decodes raw as read from a text[] column. An empty literal
// yields nil; rules.tags is NOT NULL so "" cannot occur.
func scanTextArray(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := textArrayMap.Scan(pgtype.TextArrayOID, pgtype.TextFormatCode, []byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode text[] literal %q: %w", raw, err)
	}
	return out, nil
}

// deref returns the pointer's value, or T's zero value for nil. SetRuleMeta
// pairs it with a boolean flag per field, so the zero value it returns for a
// nil field is never actually written: nullif on the empty string, or a
// false CASE branch, discards it.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// SetRuleMeta sets a rule's owner and tags (S15). A nil field is left
// alone. Dates never live on a rule; they reach it through its tasks.
func SetRuleMeta(tx *sql.Tx, ruleID int64, in model.RuleMetaInput) error {
	if in.Owner == nil && in.Tags == nil {
		return fmt.Errorf("nothing to set: %w", ErrInvalidInput)
	}
	res, err := tx.Exec(
		`UPDATE rules
		    SET owner = CASE WHEN $2::boolean THEN nullif($3, '') ELSE owner END,
		        tags  = CASE WHEN $4::boolean THEN $5::text[] ELSE tags END,
		        updated_at = now()
		  WHERE id = $1`,
		ruleID, in.Owner != nil, deref(in.Owner), in.Tags != nil, deref(in.Tags))
	if err != nil {
		return fmt.Errorf("set meta of rule %d: %w", ruleID, err)
	}
	return requireOneAffected(res, fmt.Sprintf("rule %d", ruleID), ErrNotFound)
}

// RuleIDByRef resolves a rule's project key and number to its row id
// inside a transaction, or ErrNotFound.
func RuleIDByRef(tx *sql.Tx, projectKey string, number int64) (int64, error) {
	var id int64
	err := tx.QueryRow(
		`SELECT c.id FROM rules c JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("rule %s-RULE-%d: %w", projectKey, number, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve rule %s-RULE-%d: %w", projectKey, number, err)
	}
	return id, nil
}

// ruleStatuses mirrors the rules.status CHECK in migration 0082.
var ruleStatuses = map[string]bool{"draft": true, "accepted": true, "superseded": true, "withdrawn": true}

// SetRuleStatus is the one writer of a rule's status outside document
// acceptance (increment 3 R7). Withdrawing a rule marks every accepted plan
// covering it stale (S23, WL-SPEC-77 §9): the plan undertook text that no
// longer holds. Increment 4's split and merge lineage is the caller that
// withdraws. The rule row lock is NO KEY UPDATE so it does not wait on a
// concurrent Govern's FK KEY SHARE (see resolveSupersede).
func SetRuleStatus(tx *sql.Tx, now time.Time, ruleID int64, status string, eventID int64) error {
	if !ruleStatuses[status] {
		return fmt.Errorf("rule status %q: %w", status, ErrInvalidInput)
	}
	var old, key string
	var number int64
	err := tx.QueryRow(
		`SELECT c.status, p.key, c.number FROM rules c JOIN projects p ON p.id = c.project_id
		  WHERE c.id = $1 FOR NO KEY UPDATE OF c`, ruleID).Scan(&old, &key, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rule %d: %w", ruleID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read rule %d: %w", ruleID, err)
	}
	if old == status {
		return nil
	}
	if _, err := tx.Exec(`UPDATE rules SET status = $2, updated_at = $3 WHERE id = $1`, ruleID, status, now.UTC()); err != nil {
		return fmt.Errorf("set rule %d status: %w", ruleID, err)
	}
	if status != "withdrawn" {
		return nil
	}
	plans, err := acceptedPlansCovering(tx, []int64{ruleID})
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("%s-RULE-%d", key, number)
	_, err = MarkPlansStale(tx, now, plans, "rule_withdrawn", ref, nil, eventID)
	return err
}
