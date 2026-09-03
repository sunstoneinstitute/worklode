package model

// Search modes (040 §9). hybrid fuses both arms; dense and lexical run one
// arm each and return its own ranking, so the two can be compared on a real
// query rather than argued about.
const (
	SearchHybrid  = "hybrid"
	SearchDense   = "dense"
	SearchLexical = "lexical"
)

// SearchHit is one ranked result from the corpus index (040 §6). Exactly one
// of DocID, TaskID and SkillID is set, and Kind says which.
//
// DenseRank and LexicalRank are the per-arm ranks that produced Score, 0
// meaning the arm did not return this subject at all. They are part of the
// response rather than an implementation detail: a fused score alone makes a
// bad result disappointing, and the two ranks make it diagnosable (§9).
type SearchHit struct {
	Kind    string `json:"kind"` // doc | task | skill
	DocID   int64  `json:"doc_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	SkillID int64  `json:"skill_id,omitempty"`
	// Anchor is the frozen section anchor for a doc hit (025 §3.2), so one
	// spec can return two sections as two results. "" for tasks and skills.
	Anchor string `json:"anchor,omitempty"`
	// Title is the doc or task title, or the skill's qualified name.
	Title string `json:"title"`
	// Excerpt comes from the matching chunk's chunk_text, never its context
	// header: the header is indexing scaffolding (040 §4.3), not text the
	// subject actually contains.
	Excerpt string `json:"excerpt"`
	// Score is the fused reciprocal-rank sum, not a similarity. Comparing it
	// to a similarity floor is a category error (§6.1).
	Score       float64 `json:"score"`
	DenseRank   int     `json:"dense_rank"`
	LexicalRank int     `json:"lexical_rank"`
}

// SearchResponse is one search result page (040 §9). Provider and Mode are
// the response's own report of how it was answered: a degraded instance with
// no embedding provider answers provider "none", mode "lexical" and returns
// real results rather than an empty set (§11), and a caller can tell that
// apart from a well-configured instance without log-diving.
type SearchResponse struct {
	// Provider is "none" on an instance with no embedding provider
	// configured, "openai-compatible" otherwise.
	Provider string `json:"provider"`
	// Mode is the mode the search actually ran in, which is not always the
	// one asked for: with no usable provider every mode degrades to lexical.
	Mode string      `json:"mode"`
	Hits []SearchHit `json:"hits"`
}
