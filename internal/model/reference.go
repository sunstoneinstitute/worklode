package model

import "time"

// EntityEdge is one typed reference between entities of different kinds
// (029 §5), the only edges that may cross a project boundary.
type EntityEdge struct {
	FromKind  string    `json:"from_kind"`
	From      string    `json:"from"`
	ToKind    string    `json:"to_kind"`
	To        string    `json:"to"`
	Rel       string    `json:"rel"` // depends_on | seeded_by
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// ReferenceListResponse is the response body of GET /api/v1/references.
type ReferenceListResponse struct {
	References []EntityEdge `json:"references"`
}

// ReferenceListParams is the query string of GET /api/v1/references. Both
// fields are required; the handler answers 422 when either is empty.
type ReferenceListParams struct {
	Kind string `query:"kind,omitempty"`
	ID   string `query:"id,omitempty"`
}
