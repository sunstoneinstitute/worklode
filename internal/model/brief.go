package model

// BriefBlocker is the slim projection of an open blocker in a Brief: just
// the fields an agent needs to see why a task is blocked.
type BriefBlocker struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// Brief is the wire form of GET /api/v1/tasks/{id}/brief: the bounded
// start-of-work payload for a task. Lease is null when the task has no
// active lease. OpenBlockers and BlockingPlans are always arrays (never
// null); BlockingPlans names the unfinished plans ordered before this task's
// plan (025 §9.3), which is the only thing holding a task whose blocking plan
// is still draft and has minted no task to name. Parent is
// null for a root task (no omitempty, so the key is always present — see
// TaskHierarchy.Parent for the same convention on task detail). The three
// reserved fields serialize as JSON null in v1: GoverningDesign and
// DefinitionOfDone are *string, AffectedComponents is a nil []string
// (marshals to null, not []) — see store.Brief. Skills carries the task's
// pinned skills (content inline) plus embedding-matched suggestions, in the
// same shape as POST /api/v1/skills/recommend. Blobs is always an array
// (never null) and carries absolute URLs (spec 021 §10): an agent fetching a
// brief is not same-origin with the server and has nothing to resolve a
// root-relative reference against.
type Brief struct {
	Task               Task                `json:"task"`
	Body               string              `json:"body"`
	Branch             string              `json:"branch"`
	OpenBlockers       []BriefBlocker      `json:"open_blockers"`
	BlockingPlans      []DocRef            `json:"blocking_plans"`
	Parent             *TaskParent         `json:"parent"`
	Lease              *Lease              `json:"lease"`
	GoverningDesign    *string             `json:"governing_design"`
	AffectedComponents []string            `json:"affected_components"`
	DefinitionOfDone   *string             `json:"definition_of_done"`
	Skills             SkillRecommendation `json:"skills"`
	Blobs              []TaskBlob          `json:"blobs"`
}
