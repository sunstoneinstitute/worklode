package model

// Idea is one row of the ideas table (WL-REQ-1799): private to its author
// until shared, when it gains an intake task.
type Idea struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	Author       string     `json:"author"`
	Type         string     `json:"type"`       // general or story-pitch
	StoryType    string     `json:"story_type"` // data-story, short-story or ""
	LitmusTestID string     `json:"litmus_test_id"`
	Status       string     `json:"status"`    // private or intake
	Shared       string     `json:"shared"`    // YYYY-MM-DD or ""
	Task         string     `json:"task"`      // the intake task id or ""
	Submitted    string     `json:"submitted"` // created_at as YYYY-MM-DD
	Intake       IdeaIntake `json:"intake"`
}

// IdeaIntake is the intake side of a shared idea. Decision is accept,
// narrow, park, stop or "" until made.
type IdeaIntake struct {
	PreResearchAuthorized bool   `json:"pre_research_authorized"`
	Decision              string `json:"decision"`
}

// CreateIdeaInput is the request body of POST /api/v1/ideas. The author is
// the caller, never the body.
type CreateIdeaInput struct {
	Title        string `json:"title"`
	Body         string `json:"body"`
	Type         string `json:"type"`
	StoryType    string `json:"story_type"`
	LitmusTestID string `json:"litmus_test_id"`
}

// PatchIdeaInput is the request body of PATCH /api/v1/ideas/{id}. A nil
// field is left unchanged.
type PatchIdeaInput struct {
	Title        *string `json:"title,omitempty"`
	Body         *string `json:"body,omitempty"`
	Type         *string `json:"type,omitempty"`
	StoryType    *string `json:"story_type,omitempty"`
	LitmusTestID *string `json:"litmus_test_id,omitempty"`
}

// PatchIdeaIntakeInput is the request body of PATCH
// /api/v1/ideas/{id}/intake; it replaces both fields.
type PatchIdeaIntakeInput struct {
	PreResearchAuthorized bool   `json:"pre_research_authorized"`
	Decision              string `json:"decision"`
}

// IdeaListResponse is the response body of GET /api/v1/ideas.
type IdeaListResponse struct {
	Ideas []Idea `json:"ideas"`
}
