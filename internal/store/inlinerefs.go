package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/inlineref"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/refkind"
)

// projectKeys reads every project key on the instance, the set a bare
// <KEY>-<n> counts as a task id for (WL-REQ-1851).
func projectKeys(tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT key FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("read project keys: %w", err)
	}
	keys, err := collectRows(rows, "read project keys", func(r rowScanner) (string, error) {
		var k string
		return k, r.Scan(&k)
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

// resolveHits turns scanner hits into targets; unresolved hits are returned
// separately for --dry-run (WL-REQ-1851). Rows are grouped by target with
// one line entry per hit. A hit naming the node itself is dropped.
func resolveHits(tx *sql.Tx, project string, from model.NodeKind, fromID string, hits []inlineref.Hit) (refs []model.InlineRef, unresolved []inlineref.Hit, err error) {
	// An unshared idea has no project, so a keyless hit cannot resolve.
	var key string
	if project != "" {
		if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, project).Scan(&key); err != nil {
			return nil, nil, fmt.Errorf("read key of project %q: %w", project, err)
		}
	}
	at := map[[2]string]int{}
	for _, h := range hits {
		kind, id, ok, err := resolveHit(tx, project, key, h)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unresolved = append(unresolved, h)
			continue
		}
		if kind == from && id == fromID {
			continue
		}
		k := [2]string{string(kind), id}
		i, seen := at[k]
		if !seen {
			i = len(refs)
			at[k] = i
			refs = append(refs, model.InlineRef{FromKind: from, FromID: fromID, ToKind: kind, ToID: id, ToRef: hitRef(h, key), Source: "derived"})
		}
		refs[i].Lines = append(refs[i].Lines, h.Line)
	}
	return refs, unresolved, nil
}

// hitRef spells the ref a hit names, for display.
func hitRef(h inlineref.Hit, key string) string {
	if h.Key != "" {
		key = h.Key
	}
	ref := key + "-" + strconv.FormatInt(h.Number, 10)
	if h.Infix != "" {
		ref = key + "-" + h.Infix + "-" + strconv.FormatInt(h.Number, 10)
	}
	if h.Anchor != "" {
		ref += "#" + h.Anchor
	}
	return ref
}

// resolveHit resolves one hit (WL-REQ-1357). A section ref resolves to the
// rule arranged at that anchor, else to the document; a plan arranges no
// rules of its own, so its anchors resolve to the plan (WL-REQ-1295).
func resolveHit(tx *sql.Tx, project, key string, h inlineref.Hit) (model.NodeKind, string, bool, error) {
	if h.Key == "" && key == "" {
		return "", "", false, nil
	}
	if h.Infix == "" {
		var id string
		err := tx.QueryRow(`SELECT id FROM tasks WHERE id = $1`, hitRef(h, key)).Scan(&id)
		return found("task", id, err)
	}
	target, _ := refkind.Lookup(h.Infix)
	switch target {
	case refkind.Rule:
		id, err := RuleIDByRef(tx, h.Key, h.Number)
		if errors.Is(err, ErrNotFound) {
			return "", "", false, nil
		}
		return found("rule", strconv.FormatInt(id, 10), err)
	case refkind.Milestone:
		var id string
		err := tx.QueryRow(`SELECT id FROM milestones WHERE id = $1`, hitRef(h, key)).Scan(&id)
		return found("milestone", id, err)
	case refkind.Deliverable:
		var id string
		err := tx.QueryRow(`SELECT id FROM deliverables WHERE id = $1`, hitRef(h, key)).Scan(&id)
		return found("deliverable", id, err)
	}
	k := h.Key
	if k == "" {
		k = key
	}
	docID, ok, err := resolveDocRef(tx, project, k+"-"+h.Infix+"-"+strconv.FormatInt(h.Number, 10))
	if err != nil || !ok {
		return "", "", false, err
	}
	if h.Anchor != "" {
		var ruleID int64
		err := tx.QueryRow(
			`SELECT dc.rule_id FROM doc_entries dc JOIN docs d ON d.id = dc.doc_id
			  WHERE dc.doc_id = $1 AND dc.anchor = $2 AND d.kind <> 'plan' AND dc.rule_id IS NOT NULL`,
			docID, h.Anchor).Scan(&ruleID)
		if err == nil {
			return "rule", strconv.FormatInt(ruleID, 10), true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", "", false, fmt.Errorf("resolve %s: %w", hitRef(h, k), err)
		}
	}
	return "doc", strconv.FormatInt(docID, 10), true, nil
}

// found maps a single-row lookup to resolveHit's result.
func found(kind model.NodeKind, id string, err error) (model.NodeKind, string, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("resolve %s %s: %w", kind, id, err)
	}
	return kind, id, true, nil
}

// deriveInlineRefs re-extracts one field and replaces its derived rows
// (WL-REQ-1852). version is nil for an unversioned field. Manual rows stay;
// a derived row that duplicates one records its lines on the manual row.
func deriveInlineRefs(tx *sql.Tx, project string, from model.NodeKind, fromID, field string, version *int, text string) error {
	keys, err := projectKeys(tx)
	if err != nil {
		return err
	}
	refs, _, err := resolveHits(tx, project, from, fromID, inlineref.Scan(text, keys))
	if err != nil {
		return err
	}
	const match = `from_kind = $1 AND from_id = $2 AND field = $3 AND from_version IS NOT DISTINCT FROM $4::int`
	if _, err := tx.Exec(`DELETE FROM inline_refs WHERE `+match+` AND source = 'derived'`, from, fromID, field, version); err != nil {
		return fmt.Errorf("clear inline refs of %s %s %s: %w", from, fromID, field, err)
	}
	if _, err := tx.Exec(`UPDATE inline_refs SET lines = '{}' WHERE `+match, from, fromID, field, version); err != nil {
		return fmt.Errorf("reset inline ref lines of %s %s %s: %w", from, fromID, field, err)
	}
	for _, r := range refs {
		if _, err := tx.Exec(
			`INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, 'derived')
			 ON CONFLICT (from_kind, from_id, field, COALESCE(from_version, -1), to_kind, to_id)
			 DO UPDATE SET lines = EXCLUDED.lines`,
			from, fromID, field, version, r.ToKind, r.ToID, r.Lines); err != nil {
			return fmt.Errorf("derive inline ref %s %s -> %s %s: %w", from, fromID, r.ToKind, r.ToID, err)
		}
	}
	return nil
}

// deriveRuleInlineRefs re-extracts a rule's heading and body at version.
func deriveRuleInlineRefs(tx *sql.Tx, project string, ruleID int64, version int, heading, body string) error {
	id := strconv.FormatInt(ruleID, 10)
	if err := deriveInlineRefs(tx, project, "rule", id, "heading", &version, heading); err != nil {
		return err
	}
	return deriveInlineRefs(tx, project, "rule", id, "body", &version, body)
}

// markdownWriters names, for every model.Markdown field, the store function
// whose writes re-extract it (WL-RULE-1850). internal/model's
// markdownrule_test.go reads this table.
var markdownWriters = map[string]string{
	"Doc.Title":                          "SetDocColumns",
	"Doc.Body":                           "UpdateDocBody",
	"CreateDocInput.Body":                "CreateDoc",
	"UpdateDocBodyInput.Body":            "UpdateDocBody",
	"DocColumnsInput.Title":              "SetDocColumns",
	"PatchDocInput.Body":                 "PatchDoc",
	"PatchDocInput.Note":                 "AddDocNote",
	"DocNote.Body":                       "AddDocNote",
	"AddDocNoteInput.Body":               "AddDocNote",
	"Rule.Heading":                       "EditRule",
	"Rule.Body":                          "EditRule",
	"AddRuleInput.Heading":               "AddRule",
	"AddRuleInput.Body":                  "AddRule",
	"EditRuleInput.Heading":              "EditRule",
	"EditRuleInput.Body":                 "EditRule",
	"Task.Title":                         "UpdateTaskFields",
	"Task.Body":                          "UpdateTaskFields",
	"CreateTaskInput.Title":              "CreateTask",
	"CreateTaskInput.Body":               "CreateTask",
	"EditTaskInput.Title":                "UpdateTaskFields",
	"EditTaskInput.Body":                 "UpdateTaskFields",
	"Milestone.Title":                    "CreateMilestone",
	"CreateMilestoneInput.Title":         "CreateMilestone",
	"Deliverable.Description":            "CreateDeliverable",
	"CreateDeliverableInput.Description": "CreateDeliverable",
	"Idea.Title":                         "PatchIdea",
	"Idea.Body":                          "PatchIdea",
	"CreateIdeaInput.Title":              "CreateIdea",
	"CreateIdeaInput.Body":               "CreateIdea",
	"PatchIdeaInput.Title":               "PatchIdea",
	"PatchIdeaInput.Body":                "PatchIdea",
	"Decision.Question":                  "InsertDecision",
	"Decision.Context":                   "InsertDecision",
	"DecisionInput.Question":             "EditDecision",
	"DecisionInput.Context":              "EditDecision",
	"Approval.Note":                      "SetImpactNote",
}

// deriveStored re-extracts the unversioned Markdown fields of one node from
// its stored row. query takes the id and returns the node's project, then one
// text per field.
func deriveStored(tx *sql.Tx, from model.NodeKind, id, query string, fields ...string) error {
	vals := make([]string, len(fields)+1)
	dst := make([]any, len(vals))
	for i := range vals {
		dst[i] = &vals[i]
	}
	if err := tx.QueryRow(query, id).Scan(dst...); err != nil {
		return fmt.Errorf("read %s %s for inline refs: %w", from, id, err)
	}
	for i, f := range fields {
		if err := deriveInlineRefs(tx, vals[0], from, id, f, nil, vals[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// deriveDocRefs re-extracts a doc's title (unversioned) and its stored body
// at the current version.
func deriveDocRefs(tx *sql.Tx, docID int64) error {
	var project, title, body string
	var version int
	if err := tx.QueryRow(`SELECT project_id, title, body, version FROM docs WHERE id = $1`, docID).
		Scan(&project, &title, &body, &version); err != nil {
		return fmt.Errorf("read doc %d for inline refs: %w", docID, err)
	}
	id := strconv.FormatInt(docID, 10)
	if err := deriveInlineRefs(tx, project, "doc", id, "title", nil, title); err != nil {
		return err
	}
	return deriveInlineRefs(tx, project, "doc", id, "body", &version, body)
}

// deriveDocNoteRefs re-extracts one doc note, held by its doc as note/<id>.
func deriveDocNoteRefs(tx *sql.Tx, noteID int64) error {
	var project, body string
	var docID int64
	if err := tx.QueryRow(
		`SELECT d.project_id, d.id, n.body FROM doc_notes n JOIN docs d ON d.id = n.doc_id WHERE n.id = $1`, noteID,
	).Scan(&project, &docID, &body); err != nil {
		return fmt.Errorf("read doc note %d for inline refs: %w", noteID, err)
	}
	return deriveInlineRefs(tx, project, "doc", strconv.FormatInt(docID, 10),
		"note/"+strconv.FormatInt(noteID, 10), nil, body)
}

func deriveTaskRefs(tx *sql.Tx, id string) error {
	return deriveStored(tx, "task", id,
		`SELECT project_id, title, coalesce(body, '') FROM tasks WHERE id = $1`, "title", "body")
}

func deriveMilestoneRefs(tx *sql.Tx, id string) error {
	return deriveStored(tx, "milestone", id, `SELECT project_id, title FROM milestones WHERE id = $1`, "title")
}

func deriveDeliverableRefs(tx *sql.Tx, id string) error {
	return deriveStored(tx, "deliverable", id,
		`SELECT project_id, coalesce(description, '') FROM deliverables WHERE id = $1`, "description")
}

// deriveIdeaRefs re-extracts an idea; it has a project only once shared.
func deriveIdeaRefs(tx *sql.Tx, id string) error {
	return deriveStored(tx, model.NodeKindIdea, id,
		`SELECT coalesce(t.project_id, ''), i.title, i.body
		   FROM ideas i LEFT JOIN tasks t ON t.id = i.task WHERE i.id = $1`, "title", "body")
}

func deriveDecisionRefs(tx *sql.Tx, id int64) error {
	return deriveStored(tx, model.NodeKindDecision, strconv.FormatInt(id, 10),
		`SELECT t.project_id, d.question, d.context
		   FROM decisions d JOIN tasks t ON t.id = d.task_id WHERE d.id = $1::bigint`, "question", "context")
}

// deriveApprovalRefs re-extracts an approval's note, resolved in the project
// of the entity it governs ("" for a PR).
func deriveApprovalRefs(tx *sql.Tx, id int64) error {
	return deriveStored(tx, model.NodeKindApproval, strconv.FormatInt(id, 10),
		`SELECT coalesce(CASE a.entity_kind
		            WHEN 'doc' THEN (SELECT project_id FROM docs WHERE 'doc:' || id = a.entity_id)
		            WHEN 'task' THEN (SELECT project_id FROM tasks WHERE id = a.entity_id)
		            WHEN 'deliverable' THEN (SELECT project_id FROM deliverables WHERE id = a.entity_id)
		        END, ''), coalesce(a.note, '')
		   FROM approvals a WHERE a.id = $1::bigint`, "note")
}

// deleteInlineRefs removes every inline_refs row on either end of a deleted
// node (WL-REQ-1852).
func deleteInlineRefs(tx *sql.Tx, kind model.NodeKind, id string) error {
	if _, err := tx.Exec(
		`DELETE FROM inline_refs WHERE (from_kind = $1 AND from_id = $2) OR (to_kind = $1 AND to_id = $2)`,
		kind, id); err != nil {
		return fmt.Errorf("delete inline refs of %s %s: %w", kind, id, err)
	}
	return nil
}

// normalizer rewrites a project's inline references into their plain form
// (WL-REQ-1854). The first resolution error is kept in err, because
// inlineref.Verify's resolver cannot return one.
type normalizer struct {
	tx      *sql.Tx
	project string
	key     string
	keys    map[string]bool
	err     error
}

func newNormalizer(tx *sql.Tx, project string) (*normalizer, error) {
	keys, err := projectKeys(tx)
	if err != nil {
		return nil, err
	}
	n := &normalizer{tx: tx, project: project, keys: keys}
	if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, project).Scan(&n.key); err != nil {
		return nil, fmt.Errorf("read key of project %q: %w", project, err)
	}
	return n, nil
}

// target is the plain form of what h resolves to; nil leaves h as written.
func (n *normalizer) target(h inlineref.Hit) *inlineref.Target {
	if n.err != nil {
		return nil
	}
	kind, id, ok, err := resolveHit(n.tx, n.project, n.key, h)
	if err != nil || !ok {
		n.err = err
		return nil
	}
	var ref sql.NullString
	switch kind {
	case "rule":
		err = n.tx.QueryRow(`SELECT `+ruleRefSQL("p", "r")+` FROM rules r JOIN projects p ON p.id = r.project_id
		  WHERE r.id = $1::bigint`, id).Scan(&ref)
	case "doc":
		if h.Anchor != "" {
			return &inlineref.Target{Ref: "doc:" + id, DocAnchor: true}
		}
		err = n.tx.QueryRow(`SELECT `+docRefSQL("p", "d")+` FROM docs d JOIN projects p ON p.id = d.project_id
		  WHERE d.id = $1::bigint`, id).Scan(&ref)
	default:
		return &inlineref.Target{Ref: id}
	}
	if err != nil {
		n.err = fmt.Errorf("plain ref of %s %s: %w", kind, id, err)
		return nil
	}
	if !ref.Valid { // a doc with no number has no shorthand
		return nil
	}
	return &inlineref.Target{Ref: ref.String}
}

// text is text with every resolvable reference outside code in plain form.
func (n *normalizer) text(text string) (string, error) {
	hits := inlineref.Scan(text, n.keys)
	targets := make([]*inlineref.Target, len(hits))
	for i, h := range hits {
		targets[i] = n.target(h)
	}
	return inlineref.Normalize(text, hits, targets), n.err
}

// verify refuses newText unless it is a pure normalization of oldText
// (WL-REQ-1855), naming the node in the error.
func (n *normalizer) verify(node, field, oldText, newText string) error {
	err := inlineref.Verify(oldText, newText, n.keys, n.target)
	if n.err != nil {
		return n.err
	}
	if err != nil {
		return fmt.Errorf("%s %s is not a pure normalization: %v: %w", node, field, err, ErrInvalidInput)
	}
	return nil
}

// lineDiff appends the lines of one field a normalization changed. A
// normalization never adds or removes a line (WL-REQ-1854), so lines pair up
// by number.
func lineDiff(b *strings.Builder, label, oldText, newText string) {
	ol, nl := strings.Split(oldText, "\n"), strings.Split(newText, "\n")
	for i := range min(len(ol), len(nl)) {
		if ol[i] != nl[i] {
			fmt.Fprintf(b, "%s line %d\n-%s\n+%s\n", label, i+1, ol[i], nl[i])
		}
	}
}

// NormalizeNode rewrites the inline references of one doc, rule or task of
// project into their plain form (WL-REQ-1854) and reports whether anything
// changed, with a line diff. A draft doc or rule, a plan and an unversioned
// field are written through their normal write paths. An accepted doc or rule
// gets a new version marked normalize that stays accepted, with no review
// task, once inlineref.Verify passes; otherwise nothing is written and the
// error names the node (WL-REQ-1855).
func NormalizeNode(tx *sql.Tx, now time.Time, project string, kind model.NodeKind, id string, eventID int64) (bool, string, error) {
	n, err := newNormalizer(tx, project)
	if err != nil {
		return false, "", err
	}
	var b strings.Builder
	switch kind {
	case "task":
		err = normalizeTask(tx, now, n, id, &b)
	case "rule":
		err = normalizeRuleNode(tx, n, id, &b)
	case "doc":
		err = normalizeDoc(tx, now, n, id, eventID, &b)
	default:
		err = fmt.Errorf("normalize %s %s: only a doc, rule or task: %w", kind, id, ErrInvalidInput)
	}
	if err != nil {
		return false, "", err
	}
	return b.Len() > 0, b.String(), nil
}

func normalizeTask(tx *sql.Tx, now time.Time, n *normalizer, id string, b *strings.Builder) error {
	var title, body string
	err := tx.QueryRow(`SELECT title, coalesce(body, '') FROM tasks WHERE id = $1 AND project_id = $2 FOR UPDATE`,
		id, n.project).Scan(&title, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s in %s: %w", id, n.project, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read task %s: %w", id, err)
	}
	var newTitle, newBody *model.Markdown
	for _, f := range []struct {
		name string
		old  string
		dst  **model.Markdown
	}{{"title", title, &newTitle}, {"body", body, &newBody}} {
		t, err := n.text(f.old)
		if err != nil {
			return err
		}
		if t != f.old {
			m := model.Markdown(t)
			*f.dst = &m
			lineDiff(b, id+" "+f.name, f.old, t)
		}
	}
	if newTitle == nil && newBody == nil {
		return nil
	}
	return UpdateTaskFields(tx, now, id, newTitle, newBody, nil, nil, nil, nil, nil, nil, nil, nil)
}

func normalizeRuleNode(tx *sql.Tx, n *normalizer, id string, b *strings.Builder) error {
	ruleID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("rule id %q: %w", id, ErrInvalidInput)
	}
	var heading, body string
	err = tx.QueryRow(
		`SELECT v.heading, v.body FROM rules r JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version
		  WHERE r.id = $1 AND r.project_id = $2`, ruleID, n.project).Scan(&heading, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rule %s in %s: %w", id, n.project, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read rule %s: %w", id, err)
	}
	newHeading, err := n.text(heading)
	if err != nil {
		return err
	}
	newBody, err := n.text(body)
	if err != nil {
		return err
	}
	if newHeading == heading && newBody == body {
		return nil
	}
	ref := ruleRefOf(tx, ruleID)
	lineDiff(b, ref+" heading", heading, newHeading)
	lineDiff(b, ref+" body", body, newBody)
	return normalizeRule(tx, n.project, ruleID, newHeading, newBody)
}

// normalizeRule writes heading and body as rule ruleID's normalized text: a
// draft is rewritten in place, as lode rule edit would; an accepted rule gets
// the next version, accepted and marked normalize, when the text is a pure
// normalization, and every spec arranging it moves to that version
// (WL-REQ-1298, WL-REQ-1855).
func normalizeRule(tx *sql.Tx, project string, ruleID int64, heading, body string) error {
	var status, oldHeading, oldBody string
	if err := tx.QueryRow(
		`SELECT r.status, v.heading, v.body FROM rules r JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version
		  WHERE r.id = $1 FOR NO KEY UPDATE OF r`, ruleID).Scan(&status, &oldHeading, &oldBody); err != nil {
		return fmt.Errorf("read rule %d: %w", ruleID, err)
	}
	ref := ruleRefOf(tx, ruleID)
	var version int
	var err error
	switch status {
	case "draft":
		_, version, err = reviseRule(tx, ruleID, heading, body)
		if err != nil {
			return err
		}
		return deriveRuleInlineRefs(tx, project, ruleID, version, heading, body)
	case "accepted":
		n, err := newNormalizer(tx, project)
		if err != nil {
			return err
		}
		if err := n.verify(ref, "heading", oldHeading, heading); err != nil {
			return err
		}
		if err := n.verify(ref, "body", oldBody, body); err != nil {
			return err
		}
		if version, err = bumpRuleVersion(tx, ruleID, heading, body, "normalize"); err != nil {
			return err
		}
		if err := deriveRuleInlineRefs(tx, project, ruleID, version, heading, body); err != nil {
			return err
		}
		_, err = moveSpecsToAcceptedRules(tx, []int64{ruleID}, 0, "normalize")
		return err
	}
	return fmt.Errorf("rule %s is %s; only a live rule is normalized: %w", ref, status, ErrBadTransition)
}

// normalizeDoc normalizes a doc's title and its own stored text, which for a
// spec is the template around its rules; the rules normalize on their own.
// ponytail: a spec heading also lives in doc_rules and doc_sections, which
// keep the old spelling of a ref in a heading; rewrite them when a heading
// ever holds one.
func normalizeDoc(tx *sql.Tx, now time.Time, n *normalizer, id string, eventID int64, b *strings.Builder) error {
	docID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("doc id %q: %w", id, ErrInvalidInput)
	}
	var kind, status, ref, title, body string
	err = tx.QueryRow(
		`SELECT d.kind, d.status, coalesce(`+docRefSQL("p", "d")+`, d.slug), d.title, d.body
		   FROM docs d JOIN projects p ON p.id = d.project_id
		  WHERE d.id = $1 AND d.project_id = $2 AND d.deleted_at IS NULL FOR UPDATE OF d`, docID, n.project).
		Scan(&kind, &status, &ref, &title, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("doc %s in %s: %w", id, n.project, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read doc %s: %w", id, err)
	}
	newTitle, err := n.text(title)
	if err != nil {
		return err
	}
	newBody, err := n.text(body)
	if err != nil {
		return err
	}
	if newTitle != title {
		lineDiff(b, ref+" title", title, newTitle)
		t := model.Markdown(newTitle)
		if _, err := SetDocColumns(tx, now, docID, model.DocColumnsInput{Title: &t}, eventID); err != nil {
			return err
		}
	}
	if newBody == body {
		return nil
	}
	lineDiff(b, ref+" body", body, newBody)
	switch {
	case kind == "plan" || status == "draft":
		// UpdateDocBody takes a spec's editable form: the template with its
		// rules' text filled in.
		texts, err := arrangedTexts(context.Background(), tx, "doc_rules", "x.doc_id = $1", docID)
		if err != nil {
			return err
		}
		_, err = UpdateDocBody(tx, now, docID, renderBody(newBody, texts[docID]), 0, eventID)
		return err
	case status == "accepted":
		if err := n.verify(ref, "body", body, newBody); err != nil {
			return err
		}
		if _, err := bumpDocVersion(tx, docID, "normalize"); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE docs SET body = $2, updated_at = $3 WHERE id = $1`,
			docID, newBody, now.UTC().Truncate(time.Second)); err != nil {
			return fmt.Errorf("normalize doc %s body: %w", ref, err)
		}
		if err := logDocChange(tx, docID, eventID, map[string]string{"field": "body", "change": "normalize"}); err != nil {
			return err
		}
		return deriveDocRefs(tx, docID)
	}
	return fmt.Errorf("doc %s is %s; only a draft, an accepted doc or a plan is normalized: %w", ref, status, ErrBadTransition)
}

// ListInlineRefs lists the current inline references of a node (WL-REQ-1852):
// dir "out" are the rows its own fields make, "in" the rows other nodes make to
// it. A row is current when it is unversioned or at its source's current
// version. Each row carries the display ref and title of the node at the far
// end. Never nil.
func (s *Store) ListInlineRefs(ctx context.Context, kind model.NodeKind, id, dir string) ([]model.InlineRef, error) {
	return listInlineRefs(ctx, s.db, kind, id, dir)
}

// rowsQueryer is what *sql.DB and *sql.Tx share, so a read runs on either.
type rowsQueryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func listInlineRefs(ctx context.Context, db rowsQueryer, kind model.NodeKind, id, dir string) ([]model.InlineRef, error) {
	var where string
	switch dir {
	case "out":
		where = `from_kind = $1 AND from_id = $2`
	case "in":
		where = `to_kind = $1 AND to_id = $2`
	default:
		return nil, fmt.Errorf("inline ref direction %q must be out or in: %w", dir, ErrInvalidInput)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT from_kind, from_id, field, from_version, to_kind, to_id, lines::text, source
		   FROM inline_refs WHERE `+where+` ORDER BY from_kind, from_id, field, to_kind, to_id`, kind, id)
	if err != nil {
		return nil, fmt.Errorf("list inline refs of %s %s: %w", kind, id, err)
	}
	all, err := collectRows(rows, "list inline refs of "+string(kind)+" "+id, func(r rowScanner) (model.InlineRef, error) {
		var x model.InlineRef
		var v sql.NullInt64
		var lines string
		if err := r.Scan(&x.FromKind, &x.FromID, &x.Field, &v, &x.ToKind, &x.ToID, &lines, &x.Source); err != nil {
			return x, err
		}
		if v.Valid {
			n := int(v.Int64)
			x.FromVersion = &n
		}
		for _, f := range strings.Split(strings.Trim(lines, "{}"), ",") {
			if f == "" {
				continue
			}
			n, err := strconv.Atoi(f)
			if err != nil {
				return x, fmt.Errorf("parse lines %q: %w", lines, err)
			}
			x.Lines = append(x.Lines, n)
		}
		return x, nil
	})
	if err != nil {
		return nil, err
	}
	out := []model.InlineRef{}
	for _, x := range all {
		from, err := nodeLabel(ctx, db, x.FromKind, x.FromID)
		if err != nil {
			return nil, err
		}
		if x.FromVersion != nil && (from.version == nil || *from.version != *x.FromVersion) {
			continue // a superseded version's row
		}
		to, err := nodeLabel(ctx, db, x.ToKind, x.ToID)
		if err != nil {
			return nil, err
		}
		x.FromRef, x.ToRef, x.ToTitle = from.ref, to.ref, to.title
		out = append(out, x)
	}
	return out, nil
}

// label is how a node is shown at the end of an inline reference.
type label struct {
	ref, title string
	version    *int // the current version of a versioned node, else nil
}

// nodeLabel reads a node's display ref, title and current version. A node
// that no longer exists keeps its id as its ref. ponytail: one query per row,
// batch by kind if a node ever shows hundreds.
func nodeLabel(ctx context.Context, db rowsQueryer, kind model.NodeKind, id string) (label, error) {
	var q string
	switch kind {
	case "doc":
		q = `SELECT coalesce(` + docRefSQL("p", "d") + `, d.slug), d.title, d.version
		       FROM docs d JOIN projects p ON p.id = d.project_id WHERE d.id = $1::bigint`
	case "rule":
		q = `SELECT ` + ruleRefSQL("p", "r") + `, rv.heading, r.version
		       FROM rules r JOIN projects p ON p.id = r.project_id
		       JOIN rule_versions rv ON rv.rule_id = r.id AND rv.version = r.version WHERE r.id = $1::bigint`
	case "task":
		q = `SELECT id, title, NULL::int FROM tasks WHERE id = $1`
	case "milestone":
		q = `SELECT id, title, NULL::int FROM milestones WHERE id = $1`
	case "deliverable":
		q = `SELECT id, name, NULL::int FROM deliverables WHERE id = $1`
	case model.NodeKindIdea:
		q = `SELECT id, title, NULL::int FROM ideas WHERE id = $1`
	case model.NodeKindDecision:
		q = `SELECT id::text, question, NULL::int FROM decisions WHERE id = $1::bigint`
	default: // approval: no title of its own
		return label{ref: string(kind) + " " + id}, nil
	}
	l := label{ref: id}
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, q, id).Scan(&l.ref, &l.title, &v)
	if errors.Is(err, sql.ErrNoRows) {
		return label{ref: id}, nil
	}
	if err != nil {
		return label{}, fmt.Errorf("label %s %s: %w", kind, id, err)
	}
	if v.Valid {
		n := int(v.Int64)
		l.version = &n
	}
	return l, nil
}

// InlineRefsOf is both directions of ListInlineRefs, for a node's detail.
func (s *Store) InlineRefsOf(ctx context.Context, kind model.NodeKind, id string) (model.InlineRefs, error) {
	out, err := s.ListInlineRefs(ctx, kind, id, "out")
	if err != nil {
		return model.InlineRefs{}, err
	}
	in, err := s.ListInlineRefs(ctx, kind, id, "in")
	return model.InlineRefs{Out: out, In: in}, err
}
