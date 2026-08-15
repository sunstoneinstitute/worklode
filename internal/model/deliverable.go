package model

import "time"

// Deliverable is a declared, checkable output of a project (a datapackage, a
// report PDF, a CMS post). There is no state field, and its absence is the
// point — spec 029 §3.2 makes deliverable state a reported fact, so nothing
// here may look like a stored status.
type Deliverable struct {
	ID          string    `json:"id"`
	Project     string    `json:"project"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
