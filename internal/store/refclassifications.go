package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// recordClassification inserts c as a ref_classifications row and records its
// own wl:ReferenceClassified event, with the row as payload, in the caller's
// transaction (WL-REQ-1853, WL-REQ-1856, WL-REQ-1655). eventID is the event
// that caused the classification (a derive run, say): it stays untouched and the
// new event names it under prov:wasInformedBy. It returns the new row id.
// c.Outcome defaults to "kept".
func recordClassification(tx *sql.Tx, c model.RefClassification, eventID int64) (int64, error) {
	if c.Outcome == "" {
		c.Outcome = "kept"
	}
	var probs any
	if len(c.Probabilities) > 0 {
		probs = []byte(c.Probabilities)
	}
	err := tx.QueryRow(
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
		return 0, fmt.Errorf("insert ref classification: %w", err)
	}
	row, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("marshal classification event: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(row, &payload); err != nil {
		return 0, err
	}
	payload["@type"] = "wl:ReferenceClassified"
	payload["prov:wasInformedBy"] = iri.CURIE(iri.Event(eventID))
	b, err := EventPayload(payload)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('cli', $1, 'wl:ReferenceClassified', $2, $3)`,
		fmt.Sprintf("ref_classification:%d", c.ID), b, c.CreatedAt.UTC()); err != nil {
		return 0, fmt.Errorf("record classification event: %w", err)
	}
	return c.ID, nil
}
