package model

// Skill is the wire form of a synced org skill.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourceRepo  string `json:"source_repo"`
	Hash        string `json:"hash"`
	Deleted     bool   `json:"deleted"`
}

// SkillsListResponse is the response body of GET /api/v1/skills.
type SkillsListResponse struct {
	Skills []Skill `json:"skills"`
}

// SkillMatch is one embedding-recommendation hit.
type SkillMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Hash        string  `json:"hash"`
	Score       float64 `json:"score"`
}

// PinnedSkill is a task-pinned skill with its content inlined, so a caller
// never needs a second round trip to read it.
type PinnedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hash        string `json:"hash"`
	Content     string `json:"content"`
}

// SkillRecommendation is the response body of the skills recommend endpoint.
type SkillRecommendation struct {
	Pinned   []PinnedSkill `json:"pinned"`
	Matches  []SkillMatch  `json:"matches"`
	Warnings []string      `json:"warnings"`
	Provider string        `json:"provider"`
}

// SkillSyncReport is the response body of POST /api/v1/skills/sync:
// skillsync.Summary plus, on a partial failure, the per-source error
// messages — the counts are real work done and must not be thrown away just
// because another source in the same request failed.
type SkillSyncReport struct {
	Synced  int      `json:"synced"`
	Changed int      `json:"changed"`
	Deleted int      `json:"deleted"`
	Errors  []string `json:"errors,omitempty"`
}

// RecommendInput is the request body for RecommendSkills (POST
// /api/v1/skills/recommend). Exactly one of TaskID or Text is required.
type RecommendInput struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
	Limit  int    `json:"limit"`
}
