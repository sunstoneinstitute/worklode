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
	"github.com/sunstoneinstitute/worklode/internal/ns"
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

// syncRules makes a document's rules agree with its parsed source.
// Every anchored section is a design rule or, when isSpecHeading holds and no
// prior rule matches it, a spec heading row (WL-REQ-1295); changed text rewrites the rule's draft version or, once
// that version is accepted, becomes its next version; anything else is a new
// rule numbered from the project's RULE counter. The arrangement
// (doc_rules) is rewritten in section order, each entry storing its depth
// and, when unnumbered, its slug (WL-REQ-165). Plans never reach here:
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
// and write doc_rules positions. minted reports whether a new rule was
// inserted.
func syncRules(tx *sql.Tx, docID int64, doc *designdoc.Document, priorTable string) (minted bool, derive func() error, err error) {
	var project string
	if err := tx.QueryRow(`SELECT project_id FROM docs WHERE id = $1`, docID).Scan(&project); err != nil {
		return false, nil, fmt.Errorf("project of doc %d: %w", docID, err)
	}
	prior, err := arrangedRules(tx, priorTable, docID)
	if err != nil {
		return false, nil, err
	}
	byAnchor := map[string]*ruleRow{}
	byHeading := map[string][]*ruleRow{}
	for i := range prior {
		byAnchor[prior[i].anchor] = &prior[i]
		byHeading[prior[i].heading] = append(byHeading[prior[i].heading], &prior[i])
	}
	claimed := map[int64]bool{}
	match := make([]*ruleRow, len(doc.Sections))

	// Pass 0: a heading naming its rule (the editable form's rule=, or the
	// ref a rendered heading prints, WL-REQ-1299) arranges that rule, from
	// this spec or any other.
	for i, sec := range doc.Sections {
		ref := namedRef(sec)
		if sec.Anchor == "" || ref == "" {
			continue
		}
		c, err := namedRule(tx, prior, ref)
		if err != nil {
			return false, nil, err
		}
		if !slices.ContainsFunc(prior, func(r ruleRow) bool { return r.id == c.id }) || c.heading != sec.Title || !sameRuleBody(c.body, sec.Body) {
			if err := refuseWithdrawnRule(tx, c.id, ref); err != nil {
				return false, nil, err
			}
		}
		if claimed[c.id] {
			return false, nil, fmt.Errorf("%s names %s, which an earlier heading already arranges: %w", sec.Anchor, ref, ErrInvalidInput)
		}
		match[i], claimed[c.id] = c, true
	}
	// Pass 1: anchor and heading both match.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" || match[i] != nil {
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
		return false, nil, fmt.Errorf("clear arrangement of doc %d: %w", docID, err)
	}
	// changed collects the rules this write inserted or revised, so their
	// references can be derived once the whole arrangement and its section
	// anchors are written (derive, which the caller runs). Deriving inline in
	// this loop would miss a ref into the same document: the section it
	// names has no arrangement row yet (forward) or its own derivation
	// already ran (backward), so it would resolve through an empty or stale
	// arrangement and drop the edge every time (S26).
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
		if m == nil && isSpecHeading(doc.Sections, i) {
			if _, err := tx.Exec(
				`INSERT INTO doc_rules (doc_id, position, heading, depth, slug) VALUES ($1, $2, $3, $4, $5)`,
				docID, position, sec.Title, sec.Level, entrySlug(sec)); err != nil {
				return false, nil, fmt.Errorf("arrange heading %s in doc %d: %w", sec.Anchor, docID, err)
			}
			position++
			continue
		}
		var id int64
		var version int
		switch {
		case m == nil:
			id, version, err = insertRule(tx, project, sec.Title, sec.Body)
			minted = true
		case m.heading != sec.Title || !sameRuleBody(m.body, sec.Body):
			id, version, err = reviseRule(tx, m.id, sec.Title, sec.Body)
		default:
			id, version = m.id, m.version
		}
		if err != nil {
			return false, nil, err
		}
		if m == nil || m.heading != sec.Title || !sameRuleBody(m.body, sec.Body) {
			changed = append(changed, changedRule{id, sec.Title + "\n" + sec.Body})
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, slug)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, position, id, version, sec.Level, entrySlug(sec)); err != nil {
			return false, nil, fmt.Errorf("arrange rule %d in doc %d: %w", id, docID, err)
		}
		position++
	}
	// A rule this write left unchanged shows its newest accepted version,
	// which may have moved past the text the write was based on: a
	// candidate revision opened before `lode rule accept` holds the older
	// text (WL-REQ-1298).
	if _, err := tx.Exec(
		`UPDATE doc_rules dr
		    SET rule_version = CASE WHEN r.status = 'draft' THEN r.version - 1 ELSE r.version END
		   FROM rules r
		  WHERE dr.doc_id = $1 AND r.id = dr.rule_id
		    AND dr.rule_version < CASE WHEN r.status = 'draft' THEN r.version - 1 ELSE r.version END`,
		docID); err != nil {
		return false, nil, fmt.Errorf("move doc %d to accepted rule versions: %w", docID, err)
	}
	return minted, func() error {
		for _, c := range changed {
			if err := deriveReferences(tx, project, c.id, c.text); err != nil {
				return err
			}
			if err := checkTermSlug(tx, c.id); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// namedRef is the rule a heading names: its rule= attribute, else the ref
// a rendered heading prints.
func namedRef(sec *designdoc.Section) string {
	if sec.Rule != "" {
		return sec.Rule
	}
	return sec.Ref
}

// entrySlug is the slug an entry stores for sec: its anchor when that is
// not a section number, which makes the entry unnumbered (WL-REQ-165).
func entrySlug(sec *designdoc.Section) any {
	if designdoc.IsSlug(sec.Anchor) {
		return sec.Anchor
	}
	return nil
}

// namedRule is the rule a rule= heading names: the prior arrangement's row
// when the spec already arranges it, else the rule at the version a newly
// arranged rule shows (ArrangeRule).
func namedRule(tx *sql.Tx, prior []ruleRow, ref string) (*ruleRow, error) {
	id, err := ruleByRefString(tx, ref)
	if err != nil {
		return nil, err
	}
	for i := range prior {
		if prior[i].id == id {
			return &prior[i], nil
		}
	}
	c := &ruleRow{id: id}
	if err := tx.QueryRow(
		`SELECT v.version, v.heading, v.body FROM rules r
		   JOIN rule_versions v ON v.rule_id = r.id
		    AND v.version = CASE WHEN r.status = 'draft' AND r.version > 1 THEN r.version - 1 ELSE r.version END
		  WHERE r.id = $1`, id).Scan(&c.version, &c.heading, &c.body); err != nil {
		return nil, fmt.Errorf("read rule %s: %w", ref, err)
	}
	return c, nil
}

// refuseWithdrawnRule refuses a rule= heading that would arrange or edit a
// withdrawn rule, which would undo its supersession. The error names the
// rule's successors so the author can point at one instead. A spec that
// already arranges it may keep it there unchanged.
func refuseWithdrawnRule(tx *sql.Tx, id int64, ref string) error {
	var status string
	if err := tx.QueryRow(`SELECT status FROM rules WHERE id = $1`, id).Scan(&status); err != nil {
		return fmt.Errorf("read rule %s: %w", ref, err)
	}
	if status != "withdrawn" && status != "superseded" {
		return nil
	}
	var succ string
	if err := tx.QueryRow(
		`SELECT coalesce(string_agg(`+ruleRefSQL("p", "r")+`, ', ' ORDER BY r.id), '')
		   FROM rule_edges e JOIN rules r ON r.id = e.from_rule JOIN projects p ON p.id = r.project_id
		  WHERE e.to_rule = $1 AND e.type = 'supersedes'`, id).Scan(&succ); err != nil {
		return fmt.Errorf("successors of rule %s: %w", ref, err)
	}
	if succ == "" {
		return fmt.Errorf("%s is %s and has no successor: %w", ref, status, ErrInvalidInput)
	}
	return fmt.Errorf("%s is %s; name a successor instead: %s: %w", ref, status, succ, ErrInvalidInput)
}

// isSpecHeading reports whether section i is a spec heading (WL-SPEC-77
// §19.1): anchored, no text of its own, and followed directly by a deeper
// anchored heading. syncRules writes one as a heading row only when no prior
// rule matched it, so a heading-only rule written before spec headings
// existed stays arranged until the §19.7 migration converts it.
func isSpecHeading(secs []*designdoc.Section, i int) bool {
	return i+1 < len(secs) && strings.TrimSpace(secs[i].Body) == "" &&
		secs[i+1].Anchor != "" && secs[i+1].Level > secs[i].Level
}

// arrangedRules reads a document's arrangement from table (doc_entries, the
// current one, or doc_revision_rules, its candidate's) with each rule's
// arranged version text and anchor, in position order. Spec headings carry
// no rule and are left out.
func arrangedRules(tx *sql.Tx, table string, docID int64) ([]ruleRow, error) {
	rows, err := tx.Query(
		`SELECT dc.rule_id, dc.rule_version, cv.heading, cv.body, coalesce(dc.anchor, ''), dc.depth
		   FROM `+table+` dc
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
// behind its number, shared by every kind (WL-REQ-165).
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
// arranges and writes nothing else (S11). It returns acceptDocRules' result.
func publishDocSections(tx *sql.Tx, docID int64) ([]int64, map[int64]int, error) {
	if _, err := tx.Exec(`UPDATE doc_sections SET published = true WHERE doc_id = $1`, docID); err != nil {
		return nil, nil, fmt.Errorf("publish sections of doc %d: %w", docID, err)
	}
	return acceptDocRules(tx, docID)
}

// acceptDocRules accepts every draft rule a document's current
// arrangement holds (S11). Called both when a document is accepted and when
// ensureRules backfills the arrangement of a document that was already
// accepted before the rule tables existed. A plan contains no rules
// (WL-REQ-165); the d.kind <> 'plan' guard keeps accepting a plan from
// ever flipping a rule.
//
// Every other spec arranging a rule this accepts moves to the accepted
// version, an accepted one through a version bump (WL-REQ-1298). It
// returns the rules it accepted and the specs it bumped, with their new
// versions.
func acceptDocRules(tx *sql.Tx, docID int64) ([]int64, map[int64]int, error) {
	rows, err := tx.Query(
		`UPDATE rules SET status = 'accepted', updated_at = now()
		  WHERE status = 'draft'
		    AND id IN (
		      SELECT dc.rule_id FROM doc_rules dc JOIN docs d ON d.id = dc.doc_id
		       WHERE dc.doc_id = $1 AND d.kind <> 'plan')
		 RETURNING id`, docID)
	if err != nil {
		return nil, nil, fmt.Errorf("accept rules of doc %d: %w", docID, err)
	}
	ids, err := scanColumn[int64](rows, fmt.Sprintf("accept rules of doc %d", docID))
	if err != nil || len(ids) == 0 {
		return nil, nil, err
	}
	bumped, err := moveSpecsToAcceptedRules(tx, ids, docID)
	return ids, bumped, err
}

// GetRule reads one rule by its project key and number, with the current
// version's text and every document whose arrangement holds it.
func (s *Store) GetRule(ctx context.Context, projectKey string, number int64) (*model.Rule, error) {
	c := &model.Rule{}
	var owner sql.NullString
	var tagsRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.project_id, p.key, c.number, c.kind, c.status, c.version,
		        cv.heading, cv.body, coalesce(c.concept_iri, ''), c.owner, c.tags, c.created_at, c.updated_at
		   FROM rules c
		   JOIN projects p ON p.id = c.project_id
		   JOIN rule_versions cv ON cv.rule_id = c.id AND cv.version = c.version
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number,
	).Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Kind, &c.Status, &c.Version,
		&c.Heading, &c.Body, &c.ConceptIRI, &owner, &tagsRaw, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), err)
	}
	c.Owner = owner.String
	tags, err := scanTextArray(tagsRaw)
	if err != nil {
		return nil, fmt.Errorf("scan tags of rule %d: %w", c.ID, err)
	}
	c.Tags = nonNil(tags)
	c.Ref = designdoc.FormatRuleRef(c.ProjectKey, c.Number, c.Kind)

	arr, err := s.ruleArrangements(ctx, []int64{c.ID})
	if err != nil {
		return nil, err
	}
	c.ArrangedIn = nonNil(arr[c.ID])

	prows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT d.id, p.key || '-PLAN-' || coalesce(d.number::text, d.slug), d.status
		   FROM covered_rules c
		   JOIN docs d ON d.id = c.plan_id AND d.deleted_at IS NULL
		   JOIN projects p ON p.id = d.project_id
		  WHERE c.rule_id = $1
		  ORDER BY d.id`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read plans covering rule %d: %w", c.ID, err)
	}
	cov, err := collectRows(prows, "plans covering rule", func(r rowScanner) (model.RulePlan, error) {
		var rp model.RulePlan
		err := r.Scan(&rp.Doc, &rp.DocRef, &rp.Status)
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
	q := `SELECT c.id, c.project_id, p.key, c.number, c.kind, c.status, c.version,
	             cv.heading, coalesce(c.concept_iri, ''), c.owner, c.tags, c.created_at, c.updated_at
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
		if err := rows.Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Kind, &c.Status, &c.Version,
			&c.Heading, &c.ConceptIRI, &owner, &tagsRaw, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		tags, err := scanTextArray(tagsRaw)
		if err != nil {
			return nil, fmt.Errorf("scan tags of rule %d: %w", c.ID, err)
		}
		c.Owner, c.Tags = owner.String, nonNil(tags)
		c.Ref = designdoc.FormatRuleRef(c.ProjectKey, c.Number, c.Kind)
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
		`SELECT dc.rule_id, dc.doc_id, p.key, d.kind, d.number, coalesce(dc.anchor, ''), dc.position, dc.depth, dc.rule_version
		   FROM doc_entries dc
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
		return nil, fmt.Errorf("list versions of rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), err)
	}
	defer rows.Close()
	out := []model.RuleVersion{}
	for rows.Next() {
		var v model.RuleVersion
		if err := rows.Scan(&v.Version, &v.Heading, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan version of rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), ErrNotFound)
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

// EditRule writes a rule's heading and body as its next draft version
// (WL-REQ-1298): it rewrites the newest version in place while that is a
// draft, else adds the next version as a draft. It writes only rule_versions,
// whichever specs arrange the rule, none or several; each spec keeps showing
// the version its arrangement holds until the draft is accepted. A withdrawn
// or superseded rule is ErrBadTransition. One rule.edited event records it.
func (s *Store) EditRule(ctx context.Context, projectKey string, number int64, in model.EditRuleInput, actor string) (r *model.Rule, err error) {
	defer func() { s.metrics.ruleOp("edit", err) }()
	in.Heading = strings.TrimSpace(in.Heading)
	if in.Heading == "" {
		return nil, fmt.Errorf("rule heading is required: %w", ErrInvalidInput)
	}
	ref := designdoc.FormatRuleRef(projectKey, number, "")
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"actor": actor, "rule": ref, "heading": in.Heading})
	if err != nil {
		return nil, err
	}
	_, _, err = s.RecordEvent(ctx, "cli", extID, "rule.edited", payload, func(tx *sql.Tx, _ int64) error {
		id, err := RuleIDByRef(tx, projectKey, number)
		if err != nil {
			return err
		}
		var status, project string
		if err := tx.QueryRow(`SELECT status, project_id FROM rules WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&status, &project); err != nil {
			return fmt.Errorf("read rule %s: %w", ref, err)
		}
		if status != "draft" && status != "accepted" {
			return fmt.Errorf("rule %s is %s; only a live rule is edited: %w", ref, status, ErrBadTransition)
		}
		body := sectionBody(in.Body, true)
		var heading, current string
		if err := tx.QueryRow(
			`SELECT v.heading, v.body FROM rule_versions v JOIN rules r ON r.id = v.rule_id AND r.version = v.version
			  WHERE r.id = $1`, id).Scan(&heading, &current); err != nil {
			return fmt.Errorf("read rule %s text: %w", ref, err)
		}
		if heading == in.Heading && sameRuleBody(current, body) {
			return nil
		}
		if _, _, err := reviseRule(tx, id, in.Heading, body); err != nil {
			return err
		}
		return deriveReferences(tx, project, id, in.Heading+"\n"+body)
	})
	if err != nil {
		return nil, err
	}
	return s.GetRule(ctx, projectKey, number)
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

// SetRuleMeta sets a rule's owner, tags, kind and concept IRI (S15,
// WL-REQ-165, WL-REQ-1368). A nil field is left alone. Only a definition carries
// a concept IRI: a rule that stops being one loses it, and naming one on
// any other kind is refused. Dates never live on a rule; they reach it
// through its tasks.
func SetRuleMeta(tx *sql.Tx, ruleID int64, in model.RuleMetaInput) error {
	if in.Owner == nil && in.Tags == nil && in.Kind == nil && in.Concept == nil {
		return fmt.Errorf("nothing to set: %w", ErrInvalidInput)
	}
	if c := deref(in.Concept); c != "" && !ns.IsConceptIRI(c) {
		return fmt.Errorf("concept %q is not a scheme or concept of ns/concept.ttl (%s<name>): %w", c, ns.ConceptNS, ErrInvalidInput)
	}
	if in.Kind != nil && !slices.Contains(ns.Schemes["RuleKind"], *in.Kind) {
		return fmt.Errorf("rule kind %q: must be one of %s: %w",
			*in.Kind, strings.Join(ns.Schemes["RuleKind"], ", "), ErrInvalidInput)
	}
	if in.Kind != nil && !designdoc.RuleKindCovered(*in.Kind) {
		plans, err := acceptedPlansCovering(tx, []int64{ruleID})
		if err != nil {
			return err
		}
		if len(plans) > 0 {
			return fmt.Errorf("%s is covered by an accepted plan and stays a requirement or catalogue (WL-SPEC-77 §4): %w",
				ruleRefOf(tx, ruleID), ErrRuleCovered)
		}
	}
	var kind string
	err := tx.QueryRow(
		`UPDATE rules
		    SET owner = CASE WHEN $2::boolean THEN nullif($3, '') ELSE owner END,
		        tags  = CASE WHEN $4::boolean THEN $5::text[] ELSE tags END,
		        kind  = CASE WHEN $6::boolean THEN $7 ELSE kind END,
		        concept_iri = CASE WHEN (CASE WHEN $6::boolean THEN $7 ELSE kind END) <> 'definition' THEN NULL
		                           WHEN $8::boolean THEN nullif($9, '') ELSE concept_iri END,
		        updated_at = now()
		  WHERE id = $1
		  RETURNING kind`,
		ruleID, in.Owner != nil, deref(in.Owner), in.Tags != nil, deref(in.Tags), in.Kind != nil, deref(in.Kind),
		in.Concept != nil, deref(in.Concept)).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rule %d: %w", ruleID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("set meta of rule %d: %w", ruleID, err)
	}
	if deref(in.Concept) != "" && kind != designdoc.RuleKindDefinition {
		return fmt.Errorf("%s is a %s; only a definition carries a concept IRI: %w", ruleRefOf(tx, ruleID), kind, ErrInvalidInput)
	}
	if in.Kind != nil {
		return checkTermSlug(tx, ruleID)
	}
	return nil
}

// ruleRefSQL is designdoc.FormatRuleRef as a SQL expression over a projects
// alias p and a rules alias r, for queries that build the ref in the row.
func ruleRefSQL(p, r string) string {
	return p + `.key || CASE WHEN ` + r + `.kind IN ('` + designdoc.RuleKindRequirement + `', '` +
		designdoc.RuleKindCatalogue + `') THEN '-REQ-' ELSE '-RULE-' END || ` + r + `.number`
}

// ruleRefOf is a rule's printed ref by row id, for an error message; the
// bare id when the read fails.
func ruleRefOf(tx *sql.Tx, ruleID int64) string {
	var ref string
	if err := tx.QueryRow(`SELECT `+ruleRefSQL("p", "r")+`
		   FROM rules r JOIN projects p ON p.id = r.project_id WHERE r.id = $1`, ruleID).Scan(&ref); err != nil {
		return fmt.Sprintf("%d", ruleID)
	}
	return ref
}

// RuleIDByRef resolves a rule's project key and number to its row id
// inside a transaction, or ErrNotFound.
func RuleIDByRef(tx *sql.Tx, projectKey string, number int64) (int64, error) {
	var id int64
	err := tx.QueryRow(
		`SELECT c.id FROM rules c JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), err)
	}
	return id, nil
}

// ruleStatuses mirrors the rules.status CHECK in migration 0082.
var ruleStatuses = map[string]bool{"draft": true, "accepted": true, "superseded": true, "withdrawn": true}

// SetRuleStatus is the one writer of a rule's status outside document
// acceptance (increment 3 R7). Withdrawing a rule marks every accepted plan
// covering it stale (S23, WL-REQ-170): the plan undertook text that no
// longer holds. Increment 4's split and merge lineage is the caller that
// withdraws. The rule row lock is NO KEY UPDATE so it does not wait on a
// concurrent Govern's FK KEY SHARE (see resolveSupersede).
func SetRuleStatus(tx *sql.Tx, now time.Time, ruleID int64, status string, eventID int64) error {
	if !ruleStatuses[status] {
		return fmt.Errorf("rule status %q: %w", status, ErrInvalidInput)
	}
	var old, key, kind string
	var number int64
	err := tx.QueryRow(
		`SELECT c.status, p.key, c.number, c.kind FROM rules c JOIN projects p ON p.id = c.project_id
		  WHERE c.id = $1 FOR NO KEY UPDATE OF c`, ruleID).Scan(&old, &key, &number, &kind)
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
	ref := designdoc.FormatRuleRef(key, number, kind)
	_, err = MarkPlansStale(tx, now, plans, "rule_withdrawn", ref, nil, eventID)
	return err
}
