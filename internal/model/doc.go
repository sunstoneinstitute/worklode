package model

import (
	"encoding/json"
	"time"
)

// DocSection is one anchored heading extracted from a synced document's body.
type DocSection struct {
	Anchor   string `json:"anchor"`
	Heading  string `json:"heading"`
	Depth    int    `json:"depth"`
	Position int    `json:"position"`
}

// DocEdge is one frontmatter-derived cross-reference extracted from a synced
// document's body (spec 025 §5.1).
type DocEdge struct {
	SrcAnchor    string `json:"src_anchor"`
	Rel          string `json:"rel"`
	Target       string `json:"target"`
	TargetAnchor string `json:"target_anchor"`
}

// DocUpsert is one document in a SyncDocs request body (spec 025 §5.1).
type DocUpsert struct {
	Kind        string          `json:"kind"`
	Ordinal     string          `json:"ordinal"`
	Status      string          `json:"status"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Frontmatter json.RawMessage `json:"frontmatter"`
	Sections    []DocSection    `json:"sections,omitempty"`
	Edges       []DocEdge       `json:"edges,omitempty"`
}

// Doc is the wire form of a stored document. Body and Frontmatter are omitted
// from list responses.
type Doc struct {
	ID           string          `json:"id"`
	Project      string          `json:"project"`
	Kind         string          `json:"kind"`
	Ordinal      string          `json:"ordinal"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	Version      int             `json:"version"`
	SourceBranch string          `json:"source_branch"`
	SourceDirty  bool            `json:"source_dirty"`
	SyncedAt     time.Time       `json:"synced_at"`
	Body         string          `json:"body,omitempty"`
	Frontmatter  json.RawMessage `json:"frontmatter,omitempty"`
	Sections     []DocSection    `json:"sections,omitempty"`
	Edges        []DocEdge       `json:"edges,omitempty"`
}
