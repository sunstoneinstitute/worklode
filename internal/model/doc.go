package model

import "time"

// Doc is a backbone design document (025 §5): a spec, an ADR, or a plan.
// Number is 0 for plans, which carry no corpus number (025 §14.3); Issued is
// the frontmatter's ISO date of first publication (dct:issued, 025 §14) and is
// "" when unset, the body being the authority for it as for Title; Assignee
// defaults to the creator and is what the accept gate checks.
type Doc struct {
	ID        int64     `json:"id"`
	Project   string    `json:"project"`
	Kind      string    `json:"kind"`   // spec | adr | plan
	Number    int       `json:"number"` // 0 for plans
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Body      string    `json:"body"` // the full markdown, frontmatter included
	Status    string    `json:"status"`
	Version   int       `json:"version"`
	Issued    string    `json:"issued"` // YYYY-MM-DD, "" when unset
	Assignee  string    `json:"assignee"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DocSection is one addressable section of a spec or ADR (025 §3). Plans have
// none. Anchor is the identity and is frozen once Published; LastRevisedIn is
// the document version whose accept last changed the section (025 §4.4).
type DocSection struct {
	Anchor        string `json:"anchor"`
	Number        string `json:"number"` // "4.1a", "" for an unnumbered heading
	Heading       string `json:"heading"`
	Depth         int    `json:"depth"`
	Position      int    `json:"position"` // 0-based document order
	LastRevisedIn int    `json:"last_revised_in"`
	Published     bool   `json:"published"`
}

// DocRevision is a document's open candidate revision (025 §7.2): a copy of
// the accepted body being edited against a stable document identity. At most
// one exists per document, and the accepted version stays authoritative until
// it lands.
type DocRevision struct {
	Doc       int64     `json:"doc"`
	Body      string    `json:"body"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// DocEdge is one typed link out of a document (025 §14). Exactly one of ToDoc
// and ToExternal is set: ToExternal carries a reference this backbone cannot
// resolve. FromAnchor and ToAnchor are "" for a document-level edge.
//
// One row carries both directions, so an *inbound* edge is the same row read
// backward: Type is the inverse spelling ("amends" read backward is
// "amendedBy"), ToDoc names the document the edge came from, FromAnchor is the
// anchor in the document being read, and ToAnchor the anchor at the other end.
type DocEdge struct {
	Type       string `json:"type"`
	FromAnchor string `json:"from_anchor"`
	ToDoc      int64  `json:"to_doc"`
	ToAnchor   string `json:"to_anchor"`
	ToExternal string `json:"to_external"`
}

// CreateDocInput is the request body for POST /api/v1/docs. Number is omitted
// for a plan, which carries no corpus number (025 §14.3); Assignee defaults to
// the caller, who is then the only actor that can accept the document.
//
// Status is declared but refused: it exists so the field a corpus importer
// would set is named on the wire and rejected with a message, rather than
// silently ignored. A document is created as a draft and accepted through
// POST /api/v1/docs/{id}/accept.
type CreateDocInput struct {
	Project  string `json:"project"`
	Kind     string `json:"kind"` // spec | adr | plan
	Number   int    `json:"number,omitempty"`
	Slug     string `json:"slug"`
	Body     string `json:"body"`
	Assignee string `json:"assignee,omitempty"`
	Status   string `json:"status,omitempty"`
}

// UpdateDocBodyInput is the request body for PUT /api/v1/docs/{id}/body and
// PUT /api/v1/docs/{id}/revision: the whole markdown source, frontmatter
// included, since the body is the authority for title, issued and edges.
type UpdateDocBodyInput struct {
	Body string `json:"body"`
}

// DocDetail is the wire form of GET /api/v1/docs/{id}: the document plus the
// rows derived from its body. Sections is empty for a plan (025 §9); Edges
// leaves the document and EdgesIn points at it, each carrying its inverse
// type. Revision is the open candidate revision, null when none is open.
type DocDetail struct {
	Doc
	Sections []DocSection `json:"sections"`
	Edges    []DocEdge    `json:"edges"`
	EdgesIn  []DocEdge    `json:"edges_in"`
	Revision *DocRevision `json:"revision"`
}

// DocListResponse is the response body of GET /api/v1/docs.
type DocListResponse struct {
	Docs []Doc `json:"docs"`
}
