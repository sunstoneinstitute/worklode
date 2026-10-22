package model

// NodeKind names the kind of node at either end of an inline reference
// (WL-REQ-1852): "doc", "rule", "task", "milestone" or "deliverable".
type NodeKind string

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
	Lines       []int    `json:"lines"`
	Source      string   `json:"source"` // manual | derived
}
