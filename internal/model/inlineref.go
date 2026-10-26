package model

// NodeKind names the kind of node at either end of an inline reference
// (WL-REQ-1852): "doc", "rule", "task", "milestone" or "deliverable".
type NodeKind string

// Ideas, decisions and approvals hold Markdown fields but are not nodes: these
// kinds are valid only as an InlineRef's FromKind (WL-RULE-1848).
const (
	NodeKindIdea     NodeKind = "idea"
	NodeKindDecision NodeKind = "decision"
	NodeKindApproval NodeKind = "approval"
)

// InlineRef is one row of inline_refs: a node's field names another node on
// the listed lines (WL-REQ-1852). FromVersion is nil for an unversioned field.
// Lines holds one entry per occurrence, so a line naming the target twice
// appears twice.
type InlineRef struct {
	FromKind    NodeKind `json:"from_kind"`
	FromID      string   `json:"from_id"`
	FromRef     string   `json:"from_ref"` // display ref, e.g. a rule's WL-REQ-<n>
	Field       string   `json:"field"`
	FromVersion *int     `json:"from_version,omitempty"`
	ToKind      NodeKind `json:"to_kind"`
	ToID        string   `json:"to_id"`
	ToRef       string   `json:"to_ref"`
	ToTitle     string   `json:"to_title,omitempty"`
	// ViaDoc is the doc id a rule was reached through by a doc fragment,
	// ViaAnchor the section anchor as written (WL-REQ-1852).
	ViaDoc    string `json:"via_doc,omitempty"`
	ViaAnchor string `json:"via_anchor,omitempty"`
	Lines     []int  `json:"lines"`
	Source    string `json:"source"` // manual | derived
}

// InlineRefs is the inline references a node shows (WL-REQ-1852): Out are the
// ones its own fields make, In the ones other nodes' fields make to it. Both
// are arrays, empty when there are none. It is embedded in the detail of a doc,
// task, milestone and deliverable.
type InlineRefs struct {
	Out []InlineRef `json:"inline_refs_out"`
	In  []InlineRef `json:"inline_refs_in"`
}

// DeliverableDetail is GET /api/v1/deliverables/{id}: the deliverable plus the
// inline references it makes and receives.
type DeliverableDetail struct {
	Deliverable
	InlineRefs
}

// RefDeriveParams is the body of POST /api/v1/projects/{id}/refs/derive
// (WL-REQ-1860). Kind is "doc", "task" or "rule". The nodes come from Refs,
// else from the kind's own filters (Status, DocKind, TaskKind), else from All;
// selecting nothing is a 400. DryRun and Diff compute the report and write
// nothing. Limit pages the selection: the nodes sort by id, After skips
// those up to and including it, and the report's Next resumes the rest.
type RefDeriveParams struct {
	Kind     NodeKind `json:"kind"`
	Refs     []string `json:"refs,omitempty"`
	Status   []string `json:"status,omitempty"`
	DocKind  string   `json:"doc_kind,omitempty"`
	TaskKind string   `json:"task_kind,omitempty"`
	All      bool     `json:"all,omitempty"`
	DryRun   bool     `json:"dry_run,omitempty"`
	Diff     bool     `json:"diff,omitempty"`
	After    string   `json:"after,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// RefDeriveNode is what deriving one node did, or would do under DryRun.
// Added and Removed are its outgoing inline references that appeared and
// disappeared, Unresolved the references in its text that name nothing, and
// Rewrites the lines normalization rewrote. Error is set when the node failed
// and nothing of it was written.
type RefDeriveNode struct {
	Ref        string      `json:"ref"`
	Added      []InlineRef `json:"added"`
	Removed    []InlineRef `json:"removed"`
	Unresolved []string    `json:"unresolved"`
	Rewrites   int         `json:"rewrites"`
	Diff       string      `json:"diff,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// RefDeriveReport is the response of POST /api/v1/projects/{id}/refs/derive.
// Next is the After of the following page, empty on the last one.
type RefDeriveReport struct {
	Nodes []RefDeriveNode `json:"nodes"`
	Next  string          `json:"next,omitempty"`
}
