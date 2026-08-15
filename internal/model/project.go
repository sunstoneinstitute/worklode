package model

// RepoMapping is a repo mapped to a project, with the terminal delivery state
// that counts as fully delivered for it (merged, deployed_prod, or released).
type RepoMapping struct {
	Repo      string `json:"repo"`
	DoneState string `json:"done_state"`
}

// Project is the wire form of a project, including its mapped repos and
// ranking focus (the ordered list of concerns claim-next should prioritize).
type Project struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Key   string        `json:"key"`
	Repos []RepoMapping `json:"repos"`
	Focus []string      `json:"focus"`
}
