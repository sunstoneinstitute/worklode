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
// active lease. OpenBlockers is always an array (never null). Parent is
// null for a root task (no omitempty, so the key is always present — see
// TaskHierarchy.Parent for the same convention on task detail). The three
// reserved fields serialize as JSON null in v1: GoverningDesign and
// DefinitionOfDone are *string, AffectedComponents is a nil []string
// (marshals to null, not []) — see store.Brief. Skills carries the task's
// pinned skills (content inline) plus embedding-matched suggestions, in the
// same shape as POST /api/v1/skills/recommend.
type Brief struct {
	Task               Task                `json:"task"`
	Body               string              `json:"body"`
	Branch             string              `json:"branch"`
	OpenBlockers       []BriefBlocker      `json:"open_blockers"`
	Parent             *TaskParent         `json:"parent"`
	Lease              *Lease              `json:"lease"`
	GoverningDesign    *string             `json:"governing_design"`
	AffectedComponents []string            `json:"affected_components"`
	DefinitionOfDone   *string             `json:"definition_of_done"`
	Skills             SkillRecommendation `json:"skills"`
}
