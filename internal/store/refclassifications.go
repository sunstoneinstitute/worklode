package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/inlineref"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
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
	row, err := json.Marshal(c)
	if err != nil {
		return 0, 0, fmt.Errorf("marshal classification event: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(row, &payload); err != nil {
		return 0, 0, err
	}
	payload["@type"] = "wl:ReferenceClassified"
	if eventID != 0 {
		payload["prov:wasInformedBy"] = iri.CURIE(iri.Event(eventID))
	}
	b, err := EventPayload(payload)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('cli', $1, 'wl:ReferenceClassified', $2, $3) RETURNING id`,
		fmt.Sprintf("ref_classification:%d", c.ID), b, c.CreatedAt.UTC()).Scan(&classEvent); err != nil {
		return 0, 0, fmt.Errorf("record classification event: %w", err)
	}
	return c.ID, classEvent, nil
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
// classification_id set (WL-REQ-1858). A pair that already has that edge is
// skipped, so re-deriving unchanged text records nothing. A hit the edge's
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
		var exists bool
		if err := tx.QueryRow(typedEdgeExists[[2]model.NodeKind{from, r.ToKind}], fromID, r.ToID, predicate).Scan(&exists); err != nil {
			return fmt.Errorf("check %s %s %s %s: %w", from, fromID, predicate, r.ToID, err)
		}
		if exists {
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
