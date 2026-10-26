package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// recordClassification inserts c as a ref_classifications row and makes the
// already-inserted event eventID (type wl:ReferenceClassified) carry the row as
// its payload, in the caller's transaction (WL-REQ-1853, WL-REQ-1655).
// It returns the new row id. c.Outcome defaults to "kept".
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
	payload, err := json.Marshal(struct {
		Type string `json:"@type"`
		model.RefClassification
	}{"wl:ReferenceClassified", c})
	if err != nil {
		return 0, fmt.Errorf("marshal classification event: %w", err)
	}
	if err := SetEventPayload(tx, eventID, payload); err != nil {
		return 0, err
	}
	return c.ID, nil
}
