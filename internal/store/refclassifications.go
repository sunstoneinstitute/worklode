package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/inlineref"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/typesafe"
)

// recordClassification inserts c as a ref_classifications row and records its
// own wl:ReferenceClassified event, with the row as payload, in the caller's
// transaction (WL-REQ-1853, WL-REQ-1856, WL-REQ-1655). eventID is the event
// that caused the classification (a derive run, say): it stays untouched and the
// new event names it under prov:wasInformedBy. A write with no event of its own
// passes 0, and the new event names none. It returns the new row's id and the
// new event's id. c.Outcome defaults to "kept".
func recordClassification(tx *sql.Tx, c model.RefClassification, eventID int64) (id, classEvent int64, err error) {
	if c.Outcome == "" {
		c.Outcome = "kept"
	}
	var probs any
	if len(c.Probabilities) > 0 {
		probs = []byte(c.Probabilities)
	}
	err = tx.QueryRow(
		`INSERT INTO ref_classifications
		   (from_kind, from_id, field, from_version, line, line_text, to_kind, to_id,
		    classifier, classifier_version, model, predicate, confidence, probabilities,
		    outcome, adjusted_to, outcome_by, outcome_at, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,NULLIF($16,''),NULLIF($17,''),$18,$19)
		 RETURNING id`,
		c.FromKind, c.FromID, c.Field, c.FromVersion, c.Line, c.LineText, c.ToKind, c.ToID,
		c.Classifier, c.ClassifierVersion, c.Model, c.Predicate, c.Confidence, probs,
		c.Outcome, c.AdjustedTo, c.OutcomeBy, c.OutcomeAt, c.CreatedAt,
	).Scan(&c.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("insert ref classification: %w", err)
	}
	classEvent, err = classificationEvent(tx, c, "wl:ReferenceClassified",
		fmt.Sprintf("ref_classification:%d", c.ID), eventID, c.CreatedAt)
	if err != nil {
		return 0, 0, err
	}
	return c.ID, classEvent, nil
}

// classificationEvent records classification c as its own event of type typ,
// with the row as payload and prov:wasInformedBy naming eventID when it is
// non-zero (WL-REQ-1856).
func classificationEvent(tx *sql.Tx, c model.RefClassification, typ, externalID string, eventID int64, at time.Time) (int64, error) {
	row, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("marshal classification event: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(row, &payload); err != nil {
		return 0, err
	}
	payload["@type"] = typ
	if eventID != 0 {
		payload["prov:wasInformedBy"] = iri.CURIE(iri.Event(eventID))
	}
	b, err := EventPayload(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('cli', $1, $2, $3, $4) RETURNING id`,
		externalID, typ, b, at.UTC()).Scan(&id); err != nil {
		return 0, fmt.Errorf("record %s event: %w", typ, err)
	}
	return id, nil
}

// classifiedPairs names, per kind pair a classifier writes, the typed table,
// how one pair's rows match ($1 from, $2 to) and the row's predicate.
var classifiedPairs = map[[2]model.NodeKind]struct{ table, pair, typ string }{
	{"task", "task"}: {"task_edges", "from_task = $1 AND to_task = $2", "type"},
	{"rule", "rule"}: {"rule_edges", "from_rule = $1::bigint AND to_rule = $2::bigint", "type"},
	{"doc", "doc"}:   {"doc_edges", "from_doc = $1::bigint AND to_doc = $2::bigint", "type"},
	{"task", "rule"}: {"task_governed_by", "task_id = $1 AND rule_id = $2::bigint", "'governedBy'"},
}

// RepairClassifications records a person's typed edge write (removed false)
// or removal (removed true) of fromID predicate toID as an outcome on the
// classifier's verdicts for that pair (WL-REQ-1858). A person's writer calls
// it in its own transaction before it writes or removes the edge; the
// classifier's writes and pruneClassifiedEdges never do.
//
//   - Removing a classified edge sets its classification rejected.
//   - Writing another predicate beside a classified edge sets that
//     classification adjusted to predicate and clears the edge's
//     classification_id, so the edge is the person's from then on.
//   - Writing on a pair whose newest classification says references sets it
//     missed.
//
// actor is the writer's actor and eventID its event, 0 for none.
func RepairClassifications(tx *sql.Tx, now time.Time, fromKind model.NodeKind, fromID string,
	toKind model.NodeKind, toID, predicate string, removed bool, actor string, eventID int64) error {
	p, ok := classifiedPairs[[2]model.NodeKind{fromKind, toKind}]
	if !ok || predicate == "references" {
		return nil
	}
	op, outcome, adjustedTo := "=", "rejected", ""
	if !removed {
		op, outcome, adjustedTo = "<>", "adjusted", predicate
	}
	rows, err := tx.Query(`WITH old AS (SELECT ctid, classification_id FROM `+p.table+`
		   WHERE `+p.pair+` AND `+p.typ+` `+op+` $3 AND classification_id IS NOT NULL FOR UPDATE)
		 UPDATE `+p.table+` e SET classification_id = NULL FROM old WHERE e.ctid = old.ctid
		 RETURNING old.classification_id`, fromID, toID, predicate)
	if err != nil {
		return fmt.Errorf("clear classification of %s %s %s: %w", fromID, predicate, toID, err)
	}
	ids, err := collectRows(rows, "repaired classifications", func(r rowScanner) (int64, error) {
		var id int64
		return id, r.Scan(&id)
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := setClassificationOutcome(tx, now, id, outcome, adjustedTo, actor, eventID); err != nil {
			return err
		}
	}
	if removed {
		return nil
	}
	var missed int64
	err = tx.QueryRow(`SELECT id FROM (SELECT id, predicate, outcome FROM ref_classifications
		 WHERE from_kind = $1 AND from_id = $2 AND to_kind = $3 AND to_id = $4
		 ORDER BY created_at DESC, id DESC LIMIT 1) n WHERE predicate = 'references' AND outcome = 'kept'`,
		fromKind, fromID, toKind, toID).Scan(&missed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read newest classification of %s %s: %w", fromID, toID, err)
	}
	return setClassificationOutcome(tx, now, missed, "missed", "", actor, eventID)
}

// setClassificationOutcome sets classification id's outcome, by actor at now,
// and records its own wl:ClassificationRepaired event with the row as payload
// (WL-REQ-1856).
func setClassificationOutcome(tx *sql.Tx, now time.Time, id int64, outcome, adjustedTo, actor string, eventID int64) error {
	var row []byte
	if err := tx.QueryRow(`UPDATE ref_classifications c
		 SET outcome = $2, adjusted_to = NULLIF($3, ''), outcome_by = NULLIF($4, ''), outcome_at = $5
		 WHERE id = $1 RETURNING to_jsonb(c)`, id, outcome, adjustedTo, actor, now.UTC()).Scan(&row); err != nil {
		return fmt.Errorf("set outcome of classification %d: %w", id, err)
	}
	var c model.RefClassification
	if err := json.Unmarshal(row, &c); err != nil {
		return fmt.Errorf("read classification %d: %w", id, err)
	}
	if string(c.Probabilities) == "null" {
		c.Probabilities = nil
	}
	_, err := classificationEvent(tx, c, "wl:ClassificationRepaired",
		fmt.Sprintf("ref_classification:%d:%s", id, outcome), eventID, now)
	return err
}

// typedEdgeExists asks whether from already holds a typed edge of predicate
// to to, whoever wrote it, for each kind pair inlineref.Candidates allows.
var typedEdgeExists = map[[2]model.NodeKind]string{
	{"task", "task"}: `SELECT EXISTS (SELECT 1 FROM task_edges WHERE from_task = $1 AND to_task = $2 AND type = $3)`,
	{"rule", "rule"}: `SELECT EXISTS (SELECT 1 FROM rule_edges WHERE from_rule = $1::bigint AND to_rule = $2::bigint AND type = $3)`,
	{"doc", "doc"}:   `SELECT EXISTS (SELECT 1 FROM doc_edges WHERE from_doc = $1::bigint AND to_doc = $2::bigint AND type = $3)`,
	{"task", "rule"}: `SELECT EXISTS (SELECT 1 FROM task_governed_by WHERE task_id = $1 AND rule_id = $2::bigint AND $3 = 'governedBy')`,
}

// cueInlineRefs runs the cue classifier over one field's refs (WL-REQ-1857).
// A hit records a cue classification and writes its typed edge with
// classification_id set (WL-REQ-1858). A pair that already has that edge, or
// whose newest classification a person repaired, is skipped, so re-deriving
// unchanged text records nothing and a repair stands. A hit the edge's
// writer refuses (a second parent, a cycle, a rally target) is dropped with its
// classification: the text stays a plain reference. eventID is the write's
// event, 0 when it has none.
func cueInlineRefs(tx *sql.Tx, from model.NodeKind, fromID, field string, version *int, text string, refs []model.InlineRef, eventID int64) error {
	lines := strings.Split(text, "\n")
	for _, r := range refs {
		var fromDocKind, toDocKind string
		if from == "doc" && r.ToKind == "doc" {
			if err := tx.QueryRow(`SELECT (SELECT kind FROM docs WHERE id = $1::bigint), (SELECT kind FROM docs WHERE id = $2::bigint)`,
				fromID, r.ToID).Scan(&fromDocKind, &toDocKind); err != nil {
				return fmt.Errorf("read doc kinds of %s and %s: %w", fromID, r.ToID, err)
			}
		}
		candidates := inlineref.Candidates(from, r.ToKind, fromDocKind, toDocKind)
		if candidates == nil {
			continue
		}
		var predicate string
		var line int
		for _, l := range r.Lines {
			if l >= 1 && l <= len(lines) {
				if predicate = inlineref.Cue(lines[l-1], candidates); predicate != "" {
					line = l
					break
				}
			}
		}
		if predicate == "" {
			continue
		}
		var exists, repaired bool
		if err := tx.QueryRow(typedEdgeExists[[2]model.NodeKind{from, r.ToKind}], fromID, r.ToID, predicate).Scan(&exists); err != nil {
			return fmt.Errorf("check %s %s %s %s: %w", from, fromID, predicate, r.ToID, err)
		}
		// A pair a person repaired keeps the person's verdict (WL-REQ-1858).
		if err := tx.QueryRow(`SELECT coalesce((SELECT outcome <> 'kept' FROM ref_classifications
			 WHERE from_kind = $1 AND from_id = $2 AND to_kind = $3 AND to_id = $4
			 ORDER BY created_at DESC, id DESC LIMIT 1), false)`, from, fromID, r.ToKind, r.ToID).Scan(&repaired); err != nil {
			return fmt.Errorf("check repair of %s %s %s: %w", from, fromID, r.ToID, err)
		}
		if exists || repaired {
			continue
		}
		c := model.RefClassification{
			FromKind: from, FromID: fromID, Field: field, FromVersion: version, Line: line,
			LineText: lines[line-1], ToKind: r.ToKind, ToID: r.ToID, Classifier: "cue",
			ClassifierVersion: inlineref.CueVersion, Predicate: predicate, Confidence: 1,
			CreatedAt: time.Now().UTC(),
		}
		if err := classifiedEdge(tx, c, eventID); err != nil {
			return err
		}
	}
	return nil
}

// classifiedEdge records c and writes its typed edge through the edge's own
// writer, then marks the row with c's id (WL-REQ-1853). Both happen under a
// savepoint, so a writer's refusal undoes the pair and leaves the enclosing
// write intact.
func classifiedEdge(tx *sql.Tx, c model.RefClassification, eventID int64) error {
	if _, err := tx.Exec(`SAVEPOINT classified_edge`); err != nil {
		return fmt.Errorf("savepoint classified edge: %w", err)
	}
	err := writeClassifiedEdge(tx, c, eventID)
	if errors.Is(err, ErrEdgeExists) || errors.Is(err, ErrCycle) || errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrNotFound) {
		if _, err := tx.Exec(`ROLLBACK TO SAVEPOINT classified_edge`); err != nil {
			return fmt.Errorf("roll back classified edge: %w", err)
		}
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`RELEASE SAVEPOINT classified_edge`); err != nil {
		return fmt.Errorf("release classified edge: %w", err)
	}
	return nil
}

func writeClassifiedEdge(tx *sql.Tx, c model.RefClassification, eventID int64) error {
	// A write that cleared the edge (a doc's frontmatter rebuild) reuses the
	// newest kept classification of the same verdict instead of counting it
	// again.
	var id, classEvent int64
	err := tx.QueryRow(
		`SELECT c.id, e.id FROM ref_classifications c
		   JOIN events e ON e.type = 'wl:ReferenceClassified' AND e.external_id = 'ref_classification:' || c.id
		  WHERE c.from_kind = $1 AND c.from_id = $2 AND c.to_kind = $3 AND c.to_id = $4 AND c.classifier = $5
		    AND c.classifier_version = $6 AND c.predicate = $7 AND c.outcome = 'kept'
		  ORDER BY c.created_at DESC, c.id DESC LIMIT 1`,
		c.FromKind, c.FromID, c.ToKind, c.ToID, c.Classifier, c.ClassifierVersion, c.Predicate).Scan(&id, &classEvent)
	if errors.Is(err, sql.ErrNoRows) {
		id, classEvent, err = recordClassification(tx, c, eventID)
	}
	if err != nil {
		return err
	}
	var mark string
	switch c.FromKind {
	case "task":
		if c.ToKind == "rule" {
			ruleID, _ := strconv.ParseInt(c.ToID, 10, 64)
			err = Govern(tx, c.FromID, ruleID, "manual", false)
			mark = `UPDATE task_governed_by SET classification_id = $4 WHERE task_id = $1 AND rule_id = $2::bigint AND $3 = 'governedBy'`
			break
		}
		// The classification's own event stands for the write in state_log.
		err = AddEdge(tx, c.CreatedAt, c.FromID, c.ToID, c.Predicate, classEvent)
		mark = `UPDATE task_edges SET classification_id = $4 WHERE from_task = $1 AND to_task = $2 AND type = $3`
	case "rule":
		fromID, _ := strconv.ParseInt(c.FromID, 10, 64)
		toID, _ := strconv.ParseInt(c.ToID, 10, 64)
		err = LinkRules(tx, fromID, toID, c.Predicate)
		mark = `UPDATE rule_edges SET classification_id = $4 WHERE from_rule = $1::bigint AND to_rule = $2::bigint AND type = $3`
	case "doc":
		fromID, _ := strconv.ParseInt(c.FromID, 10, 64)
		toID, _ := strconv.ParseInt(c.ToID, 10, 64)
		return AddDocEdge(tx, fromID, toID, c.Predicate, id)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(mark, c.FromID, c.ToID, c.Predicate, id); err != nil {
		return fmt.Errorf("mark %s %s %s %s classified: %w", c.FromKind, c.FromID, c.Predicate, c.ToID, err)
	}
	return nil
}

// pruneClassifiedEdges removes the node's typed edges a classification wrote
// whose target no longer appears in any of its fields (WL-REQ-1853): no
// inline_refs row of the node's current version, or unversioned, names it on a
// line. version is a rule's current version (unused for other kinds); a doc's
// is read from docs. An
// edge a person wrote has no classification_id and stays.
func pruneClassifiedEdges(tx *sql.Tx, kind model.NodeKind, id string, version *int) error {
	const named = `SELECT 1 FROM inline_refs r WHERE r.from_kind = $1 AND r.from_id = $2::text AND cardinality(r.lines) > 0`
	args := []any{kind, id}
	var stmts []string
	switch kind {
	case "task":
		stmts = []string{
			`DELETE FROM task_edges e WHERE e.from_task = $2 AND e.classification_id IS NOT NULL
			   AND NOT EXISTS (` + named + ` AND r.to_kind = 'task' AND r.to_id = e.to_task)`,
			`DELETE FROM task_governed_by g WHERE g.task_id = $2 AND g.classification_id IS NOT NULL
			   AND NOT EXISTS (` + named + ` AND r.to_kind = 'rule' AND r.to_id = g.rule_id::text)`,
		}
	case "rule":
		args = append(args, version)
		stmts = []string{`DELETE FROM rule_edges e WHERE e.from_rule = $2::text::bigint AND e.classification_id IS NOT NULL
			   AND NOT EXISTS (` + named + ` AND r.to_kind = 'rule' AND r.to_id = e.to_rule::text
			                   AND (r.from_version IS NULL OR r.from_version = $3::int))`}
	case "doc":
		stmts = []string{`DELETE FROM doc_edges e WHERE e.from_doc = $2::text::bigint AND e.classification_id IS NOT NULL
			   AND NOT EXISTS (` + named + ` AND r.to_kind = 'doc' AND r.to_id = e.to_doc::text
			                   AND (r.from_version IS NULL OR r.from_version = (SELECT version FROM docs WHERE id = $2::text::bigint)))`}
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("prune classified edges of %s %s: %w", kind, id, err)
		}
	}
	return nil
}

// JevBatch is one node's references the jev classifier is to answer
// (WL-REQ-1857): Node and Refs go to inlineref.JevQuestions, whose question
// "r<i>" asks about Refs[i]. Node.Text is the node's fields joined by blank
// lines, so a Refs line counts into that text.
type JevBatch struct {
	Node inlineref.JevNode
	Refs []inlineref.JevRef
	refs []jevRef
}

// jevRef is where one JevBatch ref sits in its field.
type jevRef struct {
	field      string
	version    *int
	line       int
	lineText   string
	toKind     model.NodeKind
	toID       string
	candidates []string
}

// JevPending collects the references of node kind/id the jev classifier is to
// answer: those whose pair has candidates, no cue phrase on any of their
// lines, and no classification yet. With reclassify it also takes a pair whose
// newest classification is a kept jev row of an older classifier_version; a
// pair a person repaired is never taken (WL-REQ-1858). A target named in
// several fields is asked about once, at its first line. It only reads, so it
// runs in the derive's transaction and the Jev call happens after.
func JevPending(tx *sql.Tx, project string, kind model.NodeKind, id string, reclassify bool) (JevBatch, error) {
	ctx := context.Background()
	b := JevBatch{Node: inlineref.JevNode{Kind: kind}}
	_, fields, err := nodeFields(tx, project, kind, id)
	if err != nil {
		return b, err
	}
	b.Node.Title = fields[0].text
	if kind == "doc" {
		if err := tx.QueryRow(`SELECT kind FROM docs WHERE id = $1::bigint`, id).Scan(&b.Node.DocKind); err != nil {
			return b, fmt.Errorf("read kind of doc %s: %w", id, err)
		}
	}
	offset := map[string]int{}
	texts := make([]string, len(fields))
	n := 0
	for i, f := range fields {
		offset[f.name], texts[i] = n, f.text
		n += strings.Count(f.text, "\n") + 2
	}
	b.Node.Text = strings.Join(texts, "\n\n")
	all := strings.Split(b.Node.Text, "\n")
	out, err := listInlineRefs(ctx, tx, kind, id, "out")
	if err != nil {
		return b, err
	}
	seen := map[string]bool{}
	for _, r := range out {
		if len(r.Lines) == 0 || seen[string(r.ToKind)+"|"+r.ToID] {
			continue
		}
		var toDocKind string
		if r.ToKind == "doc" {
			if err := tx.QueryRow(`SELECT kind FROM docs WHERE id = $1::bigint`, r.ToID).Scan(&toDocKind); err != nil {
				return b, fmt.Errorf("read kind of doc %s: %w", r.ToID, err)
			}
		}
		cands := inlineref.Candidates(kind, r.ToKind, b.Node.DocKind, toDocKind)
		if cands == nil {
			continue
		}
		cued := false
		for _, l := range r.Lines {
			if at := offset[r.Field] + l; at <= len(all) && inlineref.Cue(all[at-1], cands) != "" {
				cued = true
			}
		}
		if cued {
			continue
		}
		var take bool
		err := tx.QueryRow(`SELECT $5 AND classifier = 'jev' AND classifier_version < $6 AND outcome = 'kept'
			 FROM ref_classifications WHERE from_kind = $1 AND from_id = $2 AND to_kind = $3 AND to_id = $4
			 ORDER BY created_at DESC, id DESC LIMIT 1`,
			kind, id, r.ToKind, r.ToID, reclassify, inlineref.JevVersion).Scan(&take)
		if errors.Is(err, sql.ErrNoRows) {
			take, err = true, nil
		}
		if err != nil {
			return b, fmt.Errorf("read newest classification of %s %s %s: %w", kind, id, r.ToID, err)
		}
		at := offset[r.Field] + r.Lines[0]
		if !take || at > len(all) {
			continue
		}
		seen[string(r.ToKind)+"|"+r.ToID] = true
		b.Refs = append(b.Refs, inlineref.JevRef{Kind: r.ToKind, Title: r.ToTitle, DocKind: toDocKind, Line: at})
		b.refs = append(b.refs, jevRef{field: r.Field, version: r.FromVersion, line: r.Lines[0],
			lineText: all[at-1], toKind: r.ToKind, toID: r.ToID, candidates: cands})
	}
	return b, nil
}

// ClassifyNode records the jev answers to b's questions, keyed as
// inlineref.JevQuestions keys them, and writes their typed edges
// (WL-REQ-1858). An answer replaces the pair's typed edges that still carry a
// classification_id; an edge a person wrote or repaired stays. A references
// answer is recorded and writes no edge; so is an answer whose edge a person
// already wrote. It returns the classifications recorded.
func ClassifyNode(tx *sql.Tx, now time.Time, kind model.NodeKind, id string, b JevBatch,
	answers map[string]typesafe.Answer, modelName string, eventID int64) ([]model.RefClassification, error) {
	since, err := maxClassificationID(tx)
	if err != nil {
		return nil, err
	}
	for i, r := range b.refs {
		a, ok := answers[fmt.Sprintf("r%d", i)]
		if !ok {
			return nil, fmt.Errorf("jev gave no answer for %s %s", r.toKind, r.toID)
		}
		if a.Choice != "references" && !slices.Contains(r.candidates, a.Choice) {
			return nil, fmt.Errorf("jev chose %q for %s %s, not a candidate", a.Choice, r.toKind, r.toID)
		}
		probs, err := json.Marshal(a.Probabilities)
		if err != nil {
			return nil, fmt.Errorf("marshal probabilities: %w", err)
		}
		c := model.RefClassification{
			FromKind: kind, FromID: id, Field: r.field, FromVersion: r.version, Line: r.line,
			LineText: r.lineText, ToKind: r.toKind, ToID: r.toID, Classifier: "jev",
			ClassifierVersion: inlineref.JevVersion, Model: modelName, Predicate: a.Choice,
			Confidence: a.Confidence, Probabilities: probs, CreatedAt: now.UTC(),
		}
		p := classifiedPairs[[2]model.NodeKind{kind, r.toKind}]
		if _, err := tx.Exec(`DELETE FROM `+p.table+` WHERE `+p.pair+` AND classification_id IS NOT NULL`, id, r.toID); err != nil {
			return nil, fmt.Errorf("replace classified edges of %s %s %s: %w", kind, id, r.toID, err)
		}
		exists := false
		if c.Predicate != "references" {
			if err := tx.QueryRow(typedEdgeExists[[2]model.NodeKind{kind, r.toKind}], id, r.toID, c.Predicate).Scan(&exists); err != nil {
				return nil, fmt.Errorf("check %s %s %s %s: %w", kind, id, c.Predicate, r.toID, err)
			}
		}
		if c.Predicate == "references" || exists {
			_, _, err = recordClassification(tx, c, eventID)
		} else {
			err = classifiedEdge(tx, c, eventID)
		}
		if err != nil {
			return nil, err
		}
	}
	return classificationsSince(tx, kind, id, since)
}

// maxClassificationID is the newest ref_classifications id, 0 for none, so
// classificationsSince can list what a run recorded after it.
func maxClassificationID(tx *sql.Tx) (int64, error) {
	var id int64
	if err := tx.QueryRow(`SELECT coalesce(max(id), 0) FROM ref_classifications`).Scan(&id); err != nil {
		return 0, fmt.Errorf("read newest classification id: %w", err)
	}
	return id, nil
}

// classificationsSince lists node kind/id's classifications with an id after
// since, oldest first; never nil.
func classificationsSince(tx *sql.Tx, kind model.NodeKind, id string, since int64) ([]model.RefClassification, error) {
	rows, err := tx.Query(`SELECT to_jsonb(c) FROM ref_classifications c
		 WHERE from_kind = $1 AND from_id = $2 AND id > $3 ORDER BY id`, kind, id, since)
	if err != nil {
		return nil, fmt.Errorf("list classifications of %s %s: %w", kind, id, err)
	}
	out, err := collectRows(rows, "classifications of "+id, func(r rowScanner) (model.RefClassification, error) {
		var raw []byte
		var c model.RefClassification
		if err := r.Scan(&raw); err != nil {
			return c, err
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, err
		}
		if string(c.Probabilities) == "null" {
			c.Probabilities = nil
		}
		return c, nil
	})
	if out == nil {
		out = []model.RefClassification{}
	}
	return out, err
}

// RefStats counts the classifications of the nodes p selects, one row per
// (classifier, classifier_version, predicate, confidence bucket)
// (WL-REQ-1859). The bucket is the lower bound floor(confidence*10)/10, with
// confidence 1 clamped into 0.9. Each rate is the outcome's count over the
// row's total.
func (s *Store) RefStats(ctx context.Context, project string, p model.RefStatsParams) (model.RefStats, error) {
	ids, _, err := s.DeriveTargets(ctx, project, model.RefDeriveParams{
		Kind: p.Kind, Refs: p.Refs, Status: p.Status, DocKind: p.DocKind, TaskKind: p.TaskKind, All: p.All})
	if err != nil {
		return model.RefStats{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT classifier, classifier_version, predicate, LEAST(floor(confidence * 10)::int, 9),
		       count(*) FILTER (WHERE outcome = 'kept'), count(*) FILTER (WHERE outcome = 'adjusted'),
		       count(*) FILTER (WHERE outcome = 'rejected'), count(*) FILTER (WHERE outcome = 'missed')
		  FROM ref_classifications
		 WHERE from_kind = $1 AND from_id = ANY($2)
		 GROUP BY 1, 2, 3, 4
		 ORDER BY 1, 2, 3, 4`, string(p.Kind), ids)
	if err != nil {
		return model.RefStats{}, fmt.Errorf("ref stats: %w", err)
	}
	defer rows.Close()
	out := model.RefStats{Rows: []model.RefStatsRow{}}
	for rows.Next() {
		var r model.RefStatsRow
		var tenth int
		if err := rows.Scan(&r.Classifier, &r.ClassifierVersion, &r.Predicate, &tenth, &r.Kept, &r.Adjusted, &r.Rejected, &r.Missed); err != nil {
			return model.RefStats{}, fmt.Errorf("scan ref stats: %w", err)
		}
		r.ConfidenceBucket = float64(tenth) / 10
		total := float64(r.Kept + r.Adjusted + r.Rejected + r.Missed)
		r.KeptRate, r.AdjustedRate = float64(r.Kept)/total, float64(r.Adjusted)/total
		r.RejectedRate, r.MissedRate = float64(r.Rejected)/total, float64(r.Missed)/total
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}
