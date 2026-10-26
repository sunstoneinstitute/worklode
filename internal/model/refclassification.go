package model

import (
	"encoding/json"
	"time"
)

// RefClassification is one row of ref_classifications: a classifier's verdict
// on the relation an inline reference states (WL-REQ-1853). Outcome starts as
// "kept"; a person's later verdict rewrites it (WL-REQ-1856).
type RefClassification struct {
	ID                int64           `json:"id"`
	FromKind          NodeKind        `json:"from_kind"`
	FromID            string          `json:"from_id"`
	Field             string          `json:"field"`
	FromVersion       *int            `json:"from_version,omitempty"`
	Line              int             `json:"line"`
	LineText          string          `json:"line_text"`
	ToKind            NodeKind        `json:"to_kind"`
	ToID              string          `json:"to_id"`
	Classifier        string          `json:"classifier"` // cue | jev
	ClassifierVersion int             `json:"classifier_version"`
	Model             string          `json:"model,omitempty"`
	Predicate         string          `json:"predicate"`
	Confidence        float64         `json:"confidence"`
	Probabilities     json.RawMessage `json:"probabilities,omitempty"`
	Outcome           string          `json:"outcome"` // kept | adjusted | rejected | missed
	AdjustedTo        string          `json:"adjusted_to,omitempty"`
	OutcomeBy         string          `json:"outcome_by,omitempty"`
	OutcomeAt         *time.Time      `json:"outcome_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}
