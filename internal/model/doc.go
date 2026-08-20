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

// DocEdge is one typed link between documents (025 §14), always stated from
// the point of view of the document being read: FromAnchor is the near end,
// ToDoc and ToAnchor the far end — in DocDetail's Edges and EdgesIn alike. An
// anchor is "" when that end of the edge is the whole document rather than one
// of its sections.
//
// One stored row carries both directions, so an edge in EdgesIn is that row
// read backward: near and far ends swap, and Type is the inverse spelling —
// covers/isCoveredBy, implements/isImplementedBy, amends/amendedBy,
// replaces/isReplacedBy, requires/isRequiredBy, wasDerivedFrom/hadDerivation,
// blocks/blockedBy.
//
// ToExternal is outbound-only: it carries a reference this backbone cannot
// resolve, which by definition names no document that could point back. In
// Edges exactly one of ToDoc and ToExternal is set; in EdgesIn ToDoc always
// is and ToExternal is always "".
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
// Status is the corpus importer's field: a caller holding doc.import (admin)
// may state draft, accepted or superseded and have it honoured, because
// imported history predates the accept gate it would otherwise pass through.
// Every other caller is refused with a 422 naming the field — a document is
// created as a draft and accepted through POST /api/v1/docs/{id}/accept.
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

// DocRef is a minimal reference to a document — enough to name and link it
// without carrying its body. It is how a task's brief reports the plans
// ordered before that task's plan (025 §9.3).
type DocRef struct {
	ID     int64  `json:"id"`
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// DocSectionGap is one section of a spec that no accepted plan discharges,
// with the reason 026 §2.1 gives: "partial" when a plan covers part of it and
// no fullCoverageWith set closes it, "bound-only" when every accepted plan
// naming it claims `none`, "unplanned" when none names it at all.
type DocSectionGap struct {
	Anchor   string `json:"anchor"`
	Coverage string `json:"coverage"` // partial | bound-only | unplanned
}

// DocPlanningGap names the sections of one accepted spec that no accepted
// plan discharges, each classified by why (026 §2.1). It is keyed by
// document id rather than embedding the document, so GET /api/v1/docs
// answers with one listing shape whatever selector produced it.
//
// Sections is the spec's current section count, so a caller can render the
// "2/9" ratio 026 §2.1 shows without a second request.
type DocPlanningGap struct {
	Doc      int64           `json:"doc"`
	Sections int             `json:"sections"`
	Gaps     []DocSectionGap `json:"gaps"` // in document order
}

// DocSupersessionGap names the sections of one superseded document that
// nothing explains — 025 §6 rule 2's "bare superseded section" (026 §2.4). It
// is keyed by document id for the same reason DocPlanningGap is: one listing
// shape serves every selector.
//
// Sections is the document's whole section count, so a caller can render the
// "1/3" ratio without a second request.
type DocSupersessionGap struct {
	Doc         int64    `json:"doc"`
	Sections    int      `json:"sections"`
	Unexplained []string `json:"unexplained"` // anchors, in document order
}

// DocListResponse is the response body of GET /api/v1/docs. PlanningGaps is
// populated only for ?needs_planning=true, one entry per document in Docs;
// SupersessionGaps only for ?bare_superseded=true.
type DocListResponse struct {
	Docs             []Doc                `json:"docs"`
	PlanningGaps     []DocPlanningGap     `json:"planning_gaps,omitempty"`
	SupersessionGaps []DocSupersessionGap `json:"supersession_gaps,omitempty"`
}

// AcceptDocResponse is the response body of POST /api/v1/docs/{id}/accept.
// It embeds Doc so every existing field stays at the top level — a spec or
// ADR accept is byte-identical to before — and adds Tasks, the tasks a
// plan's acceptance minted (025 §9.2); omitted and nil for a spec or ADR.
type AcceptDocResponse struct {
	Doc
	Tasks []Task `json:"tasks,omitempty"`
}
