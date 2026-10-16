package model

// SummaryItem is one line of the project summary: a task or document that
// needs attention (WL-REQ-1797).
type SummaryItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Due   string `json:"due,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Workload is one roster member's share of the project's work.
type Workload struct {
	Actor          string        `json:"actor"`
	Availability   string        `json:"availability"`
	Done           int           `json:"done"`
	Total          int           `json:"total"`
	PastDue        []SummaryItem `json:"past_due"`
	Upcoming       []SummaryItem `json:"upcoming"`
	AwaitingReview []SummaryItem `json:"awaiting_review"`
}

// ProjectSummary is the body of the project summary (WL-REQ-1797).
type ProjectSummary struct {
	PastDue        []SummaryItem `json:"past_due"`
	AwaitingReview []SummaryItem `json:"awaiting_review"`
	Workloads      []Workload    `json:"workloads"`
}
