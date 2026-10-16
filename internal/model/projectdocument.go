package model

import "time"

// ProjectDocument is one row of a project's or idea's documents register
// (WL-REQ-1798): a pointer to a file, dataset or link held outside the
// backbone. Exactly one of Project and Idea is set.
type ProjectDocument struct {
	ID           string    `json:"id"`
	Project      string    `json:"project,omitempty"`
	Idea         string    `json:"idea,omitempty"`
	Name         string    `json:"name"`
	Category     string    `json:"category"`
	Type         string    `json:"type"`
	Status       string    `json:"status"`
	URL          string    `json:"url"`
	Source       string    `json:"source"`
	Milestone    string    `json:"milestone"`
	Due          string    `json:"due"`  // YYYY-MM-DD or ""
	Date         string    `json:"date"` // YYYY-MM-DD or ""
	Participants []string  `json:"participants"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	CreatedBy    string    `json:"created_by"`
}

// ProjectDocumentListResponse is the response body of GET
// /api/v1/projects/{id}/documents.
type ProjectDocumentListResponse struct {
	Documents []ProjectDocument `json:"documents"`
}

// CreateProjectDocumentInput is the request body for registering a document.
// The owner comes from the URL, never the body.
type CreateProjectDocumentInput struct {
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Type         string   `json:"type"`
	Status       string   `json:"status"`
	URL          string   `json:"url"`
	Source       string   `json:"source"`
	Milestone    string   `json:"milestone"`
	Due          string   `json:"due"`
	Date         string   `json:"date"`
	Participants []string `json:"participants"`
}

// PatchProjectDocumentInput is the request body for PATCH
// /api/v1/documents/{id}. A nil field is left unchanged.
type PatchProjectDocumentInput struct {
	Name         *string   `json:"name,omitempty"`
	Category     *string   `json:"category,omitempty"`
	Type         *string   `json:"type,omitempty"`
	Status       *string   `json:"status,omitempty"`
	URL          *string   `json:"url,omitempty"`
	Source       *string   `json:"source,omitempty"`
	Milestone    *string   `json:"milestone,omitempty"`
	Due          *string   `json:"due,omitempty"`
	Date         *string   `json:"date,omitempty"`
	Participants *[]string `json:"participants,omitempty"`
}
