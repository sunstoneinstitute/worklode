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

// RefStatsParams is the query of GET /api/v1/projects/{id}/refs/stats
// (WL-REQ-1859): the nodes whose classifications are counted, selected as
// RefDeriveParams selects them. Kind is "doc", "task" or "rule".
type RefStatsParams struct {
	Kind     NodeKind `query:"kind,omitempty"`
	Refs     []string `query:"ref,omitempty"`
	Status   []string `query:"status,omitempty"`
	DocKind  string   `query:"doc_kind,omitempty"`
	TaskKind string   `query:"task_kind,omitempty"`
	All      bool     `query:"all,omitempty"`
}

// RefStatsRow counts the classifications of one classifier version and
// predicate whose confidence falls in one tenth. ConfidenceBucket is the
// bucket's lower bound: 0.9 holds 0.9 up to and including 1.0. Each rate is
// its count over the row's four counts together (WL-REQ-1859).
type RefStatsRow struct {
	Classifier        string  `json:"classifier"`
	ClassifierVersion int     `json:"classifier_version"`
	Predicate         string  `json:"predicate"`
	ConfidenceBucket  float64 `json:"confidence_bucket"`
	Kept              int     `json:"kept"`
	Adjusted          int     `json:"adjusted"`
	Rejected          int     `json:"rejected"`
	Missed            int     `json:"missed"`
	KeptRate          float64 `json:"kept_rate"`
	AdjustedRate      float64 `json:"adjusted_rate"`
	RejectedRate      float64 `json:"rejected_rate"`
	MissedRate        float64 `json:"missed_rate"`
}

// RefStats is the response of GET /api/v1/projects/{id}/refs/stats, ordered
// by classifier, version, predicate and bucket.
type RefStats struct {
	Rows []RefStatsRow `json:"rows"`
}
