package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

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
