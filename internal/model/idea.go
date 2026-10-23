package model

// Idea is one row of the ideas table (WL-REQ-1799): private to its author
// until shared, when it gains an intake task.
type Idea struct {
	ID           string     `json:"id"`
	Title        Markdown   `json:"title"`
	Body         Markdown   `json:"body"`
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
	Title        Markdown `json:"title"`
	Body         Markdown `json:"body"`
	Type         string   `json:"type"`
	StoryType    string   `json:"story_type"`
	LitmusTestID string   `json:"litmus_test_id"`
}

// PatchIdeaInput is the request body of PATCH /api/v1/ideas/{id}. A nil
// field is left unchanged.
type PatchIdeaInput struct {
	Title        *Markdown `json:"title,omitempty"`
	Body         *Markdown `json:"body,omitempty"`
	Type         *string   `json:"type,omitempty"`
	StoryType    *string   `json:"story_type,omitempty"`
	LitmusTestID *string   `json:"litmus_test_id,omitempty"`
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

// LitmusTest is one litmus-test result recorded on a shared idea
// (WL-REQ-1800). Date is YYYY-MM-DD.
type LitmusTest struct {
	ID              string   `json:"id"`
	Date            string   `json:"date"`
	Summary         string   `json:"summary"`
	Recommendations []string `json:"recommendations"`
}

// LitmusTestListResponse is the response body of GET
// /api/v1/ideas/{id}/litmus-tests, newest date first.
type LitmusTestListResponse struct {
	LitmusTests []LitmusTest `json:"litmus_tests"`
}

// CreateLitmusTestInput is the request body of POST
// /api/v1/ideas/{id}/litmus-tests. Date is YYYY-MM-DD.
type CreateLitmusTestInput struct {
	Date            string   `json:"date"`
	Summary         string   `json:"summary"`
	Recommendations []string `json:"recommendations"`
}

// PreResearch is the response body of GET and PUT
// /api/v1/ideas/{id}/pre-research: the newest run's findings.
type PreResearch struct {
	Findings []string `json:"findings"`
}

// PutPreResearchInput is the request body of PUT
// /api/v1/ideas/{id}/pre-research; it records a new run. Date is YYYY-MM-DD.
type PutPreResearchInput struct {
	Date     string   `json:"date"`
	Findings []string `json:"findings"`
}
