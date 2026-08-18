package model

// MergeReportRequest is the body of POST /api/v1/merges. Repo takes any git
// remote URL form; the server normalizes it.
type MergeReportRequest struct {
	Repo  string   `json:"repo"`
	SHA   string   `json:"sha"`
	Tasks []string `json:"tasks"`
}

// MergeResult is what recording one reported task did.
type MergeResult struct {
	Task   string `json:"task"`
	Result string `json:"result"`
}

// MergeReport is the response body of POST /api/v1/merges.
type MergeReport struct {
	Repo    string        `json:"repo"`
	SHA     string        `json:"sha"`
	Results []MergeResult `json:"results"`
}
