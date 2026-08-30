package model

// BlockerNode is one task in a blocker tree: the blocking task, the task it
// holds up (Via), and how many blocker hops from the root it sits. Only ID,
// Title, State and Project are read from the task row — the same slim
// projection BriefBlocker carries, for the same reason.
type BlockerNode struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Project string `json:"project"`
	// Via is the task this one blocks — the root at depth 1, another
	// blocker below that. It is what makes the flat slice a tree.
	Via   string `json:"via"`
	Depth int    `json:"depth"`
	// Cycle marks a node whose expansion stopped because its id already
	// appeared on its own path: the blocker graph cycles through it. Its
	// blockers are not listed under it, they are the ones above it.
	Cycle bool `json:"cycle,omitempty"`
}

// BlockerTree is the wire form of GET /api/v1/tasks/{id}/blockers: every open
// task transitively holding the root, plus the unfinished plans ordered
// before the root's own plan (025 §9.3) — the same two halves a Brief reports
// for one hop, walked to the bottom.
//
// Blockers and BlockingPlans are always arrays (never null). BlockingPlans is
// the root's alone: a draft plan mints no task, so it has no chain to walk,
// and repeating the query per node would report the same document many times.
type BlockerTree struct {
	Root          string        `json:"root"`
	Blockers      []BlockerNode `json:"blockers"`
	BlockingPlans []DocRef      `json:"blocking_plans"`
}

// BlockerForest is the wire form of GET /api/v1/blockers?project=<id>: one
// tree per blocked task in scope that nothing else in scope already shows as
// a blocker, so a chain is printed once from its top rather than once per
// task on it.
//
// Trees is always an array (never null) and is empty when nothing in scope is
// blocked.
type BlockerForest struct {
	Trees []BlockerTree `json:"trees"`
}
