package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- docs ---------------------------------------------------------------

// CreateDoc calls POST /api/v1/docs. The whole markdown artifact goes in
// in.Body, frontmatter included: the server parses it, so the body is the
// authority for the document's title, issued date, sections and edges.
func (c *Client) CreateDoc(ctx context.Context, in model.CreateDocInput) (model.Doc, []byte, error) {
	return doJSON[model.Doc](ctx, c, http.MethodPost, "/api/v1/docs", in, "doc")
}

// DocListFilter narrows ListDocs. Zero-valued fields do not filter.
//
// NeedsPlanning, NeedsExecution and BareSuperseded are 026 §2's derived
// selectors, not plain filters: each implies a kind and a status, and the
// server refuses a Kind or Status that contradicts it, or more than one
// selector at once.
type DocListFilter struct {
	Project        string
	Kind           string // spec | adr | plan
	Status         string // draft | accepted | superseded
	Owner          string
	NeedsPlanning  bool
	NeedsExecution bool
	BareSuperseded bool
	// Deleted switches the list to tombstoned documents (044 §5): they
	// replace the live ones rather than joining them, so a list never mixes
	// the two.
	Deleted bool
}

// ListDocs calls GET /api/v1/docs.
func (c *Client) ListDocs(ctx context.Context, f DocListFilter) (model.DocListResponse, []byte, error) {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.Owner != "" {
		q.Set("owner", f.Owner)
	}
	if f.NeedsPlanning {
		q.Set("needs_planning", "true")
	}
	if f.NeedsExecution {
		q.Set("needs_execution", "true")
	}
	if f.BareSuperseded {
		q.Set("bare_superseded", "true")
	}
	if f.Deleted {
		q.Set("deleted", "true")
	}
	return doJSON[model.DocListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/docs", q), nil, "doc list")
}

// ResolveDoc calls GET /api/v1/docs/resolve?ref=, returning the document a
// reference names — an id or an exact slug (025 §14.3). The server owns the
// grammar and its ambiguity rule, so a ref costs one indexed lookup rather
// than a listing of the whole corpus, and a grammar extension needs no client
// upgrade. A *ClientError with Status 404 means no document holds that ref;
// 422 means a slug that names more than one. The response carries no body
// text — fetch the document with GetDoc when the text is wanted.
func (c *Client) ResolveDoc(ctx context.Context, ref string) (model.Doc, error) {
	q := url.Values{}
	q.Set("ref", ref)
	d, _, err := doJSON[model.Doc](ctx, c, http.MethodGet, withQuery("/api/v1/docs/resolve", q), nil, "doc")
	return d, err
}

// GetDoc calls GET /api/v1/docs/{id}: the document plus its sections, its
// edges in both directions, and its open candidate revision if it has one.
func (c *Client) GetDoc(ctx context.Context, id int64) (model.DocDetail, []byte, error) {
	return doJSON[model.DocDetail](ctx, c, http.MethodGet, docPath(id, ""), nil, "doc")
}

// ListDocVersions calls GET /api/v1/docs/{id}/versions: every version of a
// document, newest first (025 §4.5).
func (c *Client) ListDocVersions(ctx context.Context, id int64) ([]model.DocVersionSummary, []byte, error) {
	return doJSON[[]model.DocVersionSummary](ctx, c, http.MethodGet, docPath(id, "/versions"), nil, "doc versions")
}

// GetDocVersion calls GET /api/v1/docs/{id}/versions/{n}: one version of a
// document, current or superseded, with its full body.
func (c *Client) GetDocVersion(ctx context.Context, id int64, version int) (model.DocVersion, []byte, error) {
	return doJSON[model.DocVersion](ctx, c, http.MethodGet, docPath(id, "/versions/"+strconv.Itoa(version)), nil, "doc version")
}

// UpdateDocBody calls PUT /api/v1/docs/{id}/body: an in-place edit, which the
// server allows on a draft and on a plan at any status. An accepted spec or
// ADR is revised instead (see ReviseDoc).
func (c *Client) UpdateDocBody(ctx context.Context, id int64, body string) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPut, docPath(id, "/body"), model.UpdateDocBodyInput{Body: body})
}

// ReplaceDocEdges calls PUT /api/v1/docs/{id}/edges: re-resolve the document's
// frontmatter references against the documents that exist now. It is the
// corpus import's second pass — the first cannot resolve a reference to a
// document it has not created yet — and needs the admin-only doc.import
// permission. Nothing else about the document changes; the response is the
// same detail GET serves, so the caller reads back the resolved edge set.
func (c *Client) ReplaceDocEdges(ctx context.Context, id int64) (model.DocDetail, []byte, error) {
	return doJSON[model.DocDetail](ctx, c, http.MethodPut, docPath(id, "/edges"), nil, "doc")
}

// SubmitDoc calls POST /api/v1/docs/{id}/submit: the document enters review.
// Submission is an event, not a status (025 §15.4), so nothing about the
// document changes and the response is the document as it stands. Submitting
// the same version twice records one event and still answers 200.
func (c *Client) SubmitDoc(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/submit"), nil)
}

// AcceptDoc calls POST /api/v1/docs/{id}/accept. Only the document's owner
// may accept it (025 §7); anyone else gets 403. The response also carries the
// tasks a plan's acceptance minted (025 §9.2); Tasks is empty for a spec or
// ADR.
func (c *Client) AcceptDoc(ctx context.Context, id int64) (model.AcceptDocResponse, []byte, error) {
	return doJSON[model.AcceptDocResponse](ctx, c, http.MethodPost, docPath(id, "/accept"), nil, "doc accept")
}

// ReviseDoc calls POST /api/v1/docs/{id}/revise, opening the one candidate
// revision an accepted spec or ADR may carry, and returns it.
func (c *Client) ReviseDoc(ctx context.Context, id int64) (model.DocRevision, []byte, error) {
	return c.docRevisionWrite(ctx, http.MethodPost, docPath(id, "/revise"), nil)
}

// UpdateDocRevision calls PUT /api/v1/docs/{id}/revision, replacing the open
// candidate's body.
func (c *Client) UpdateDocRevision(ctx context.Context, id int64, body string) (model.DocRevision, []byte, error) {
	return c.docRevisionWrite(ctx, http.MethodPut, docPath(id, "/revision"),
		model.UpdateDocBodyInput{Body: body})
}

// DiscardDocRevision calls DELETE /api/v1/docs/{id}/revision, withdrawing the
// open candidate without landing it and freeing the document's one candidate
// slot. Either the document's owner or the revision's author may (025
// §7.2); anyone else gets 403. The document itself is unchanged, and is what
// the response carries.
func (c *Client) DiscardDocRevision(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodDelete, docPath(id, "/revision"), nil)
}

// AcceptDocRevision calls POST /api/v1/docs/{id}/revision/accept, landing the
// open candidate as the document's next version. A candidate that breaks the
// 025 §6 anchor rules is refused with the violations named.
func (c *Client) AcceptDocRevision(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/revision/accept"), nil)
}

// DeleteDoc calls DELETE /api/v1/docs/{id}: tombstone the document (044 §2).
// Like DeleteTask, the body goes out even with an empty justification; the
// server owns the instance-environment rule (044 §3).
func (c *Client) DeleteDoc(ctx context.Context, id int64, justification string) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodDelete, docPath(id, ""),
		model.DeleteInput{Justification: justification})
}

// UndeleteDoc calls POST /api/v1/docs/{id}/undelete: clear the tombstone. No
// justification on either instance environment (044 §3).
func (c *Client) UndeleteDoc(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/undelete"), nil)
}

// TransferDocOwner calls POST /api/v1/docs/{id}/owner: hands the document to
// another actor (025 §7.3). The current owner or an admin may call it;
// transferring to the actor that already owns the document is a legal no-op
// that still answers 200 — what makes TransferDocs' retry-after-failure safe.
func (c *Client) TransferDocOwner(ctx context.Context, id int64, owner string) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/owner"), model.TransferDocOwnerInput{Owner: owner})
}

// SetDocReviewers calls POST /api/v1/docs/{id}/reviewers: replaces the
// document's durable reviewer set wholesale (025 §7.3, WL-359). The current
// owner or an admin may call it, the same authority TransferDocOwner checks.
func (c *Client) SetDocReviewers(ctx context.Context, id int64, reviewers []string) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/reviewers"), model.SetDocReviewersInput{Reviewers: reviewers})
}

// DocTransferOutcome is one document's result from TransferDocs: the document
// (body cleared — the loop can cover hundreds of documents and none of them
// need it) and the error transferring it hit, "" on success including the
// already-owns-it no-op. No json tags: this crosses no HTTP boundary (ADR
// 036 §2), so it carries no wire contract of its own — `lode doc transfer
// --json`'s contract is internal/cmd's docTransferResult, built from this.
type DocTransferOutcome struct {
	Doc model.Doc
	Err string
}

// TransferDocs is `lode doc transfer`'s loop: one TransferDocOwner call per
// document, continuing past a failure so one bad document does not stop the
// rest. There is no bulk transfer endpoint — TransferDocOwner's no-op-on-
// same-owner rule is what makes looping safe to simply run again after a
// partial failure, rather than needing one.
//
// On success the outcome carries the endpoint's response, not the pre-
// transfer document docs[i] still holds — otherwise a successful --json
// transfer would report the actor the document used to belong to. On
// failure there is no updated document, so the input one is reported as-is.
func (c *Client) TransferDocs(ctx context.Context, docs []model.Doc, owner string) []DocTransferOutcome {
	out := make([]DocTransferOutcome, len(docs))
	for i, d := range docs {
		d.Body = ""
		if updated, _, err := c.TransferDocOwner(ctx, d.ID, owner); err != nil {
			out[i] = DocTransferOutcome{Doc: d, Err: err.Error()}
		} else {
			updated.Body = ""
			// The owner endpoint's response carries no ProjectKey (it skips
			// the withProjectKey stamp GetDoc/ListDocs apply) — keep the one
			// already resolved so DocRef still renders the "WL-" prefix.
			updated.ProjectKey = d.ProjectKey
			out[i] = DocTransferOutcome{Doc: updated}
		}
	}
	return out
}

// docPath builds a document endpoint path.
func docPath(id int64, suffix string) string {
	return "/api/v1/docs/" + strconv.FormatInt(id, 10) + suffix
}

// docWrite is the shared decode for the document endpoints answering with the
// document itself.
func (c *Client) docWrite(ctx context.Context, method, path string, body any) (model.Doc, []byte, error) {
	return doJSON[model.Doc](ctx, c, method, path, body, "doc")
}

// docRevisionWrite is the same for the two endpoints answering with the open
// candidate revision.
func (c *Client) docRevisionWrite(ctx context.Context, method, path string, body any) (model.DocRevision, []byte, error) {
	return doJSON[model.DocRevision](ctx, c, method, path, body, "doc revision")
}

// DocTable prints one row per document: ref, status, title.
//
// Three columns, because the title is what a reader scans for and every other
// candidate spends width that the title is worth more than. REF carries the
// kind and the number, so neither gets a column. The slug is the file name a
// document is saved under, useful when writing the corpus to disk and noise in
// a listing. The integer id and the version stay in --json, where the id is the
// API's handle.
func DocTable(w io.Writer, docs []model.Doc) {
	tbl := newTable(
		column{header: "REF"},
		column{header: "STATUS"},
		titleColumn("TITLE"),
	)
	for _, d := range docs {
		tbl.add(DocRef(d), d.Status, d.Title)
	}
	tbl.flush(w)
}

// DocTransferTable prints `lode doc transfer`'s per-document result: one row
// per document and whether it moved. A failed transfer's error is the whole
// point of the row — it is what tells a re-run what still needs doing.
func DocTransferTable(w io.Writer, outcomes []DocTransferOutcome) {
	tbl := newTable(
		column{header: "REF"},
		titleColumn("TITLE"),
		column{header: "RESULT"},
	)
	for _, o := range outcomes {
		result := "moved"
		if o.Err != "" {
			result = "FAILED: " + o.Err
		}
		tbl.add(DocRef(o.Doc), o.Doc.Title, result)
	}
	tbl.flush(w)
}

// DocPlanningTable prints the `lode doc list --needs-planning` view: one row
// per accepted spec, with the gap ratio 026 §2.1 shows and each undischarged
// anchor annotated with why it is still a gap — "sec-2.4(partial)
// sec-4(unplanned)" (026 §2.1's sample output), or "sec-4(deferred:OWNER)"
// when a defers entry names who it is owed to (026 §5.3). gaps is keyed by
// document id, so a document without one renders as no gap rather than
// misaligning the table.
func DocPlanningTable(w io.Writer, docs []model.Doc, gaps []model.DocPlanningGap) {
	byDoc := make(map[int64]model.DocPlanningGap, len(gaps))
	for _, g := range gaps {
		byDoc[g.Doc] = g
	}
	docGapTable(w, "GAPS", "ANCHORS", docs, func(d model.Doc) (int, []string) {
		g := byDoc[d.ID]
		anchors := make([]string, len(g.Gaps))
		for i, s := range g.Gaps {
			if s.Coverage == "deferred" && s.Owner != "" {
				anchors[i] = fmt.Sprintf("%s(deferred:%s)", s.Anchor, s.Owner)
			} else {
				anchors[i] = fmt.Sprintf("%s(%s)", s.Anchor, s.Coverage)
			}
		}
		return g.Sections, anchors
	})
}

// DocSupersessionTable prints the `lode doc list --bare-superseded` view: one
// row per superseded document that has a section nothing explains — 025 §6
// rule 2 (026 §2.4) — with the bare ratio and the anchors that need it. gaps
// is keyed by document id, mirroring DocPlanningTable.
func DocSupersessionTable(w io.Writer, docs []model.Doc, gaps []model.DocSupersessionGap) {
	byDoc := make(map[int64]model.DocSupersessionGap, len(gaps))
	for _, g := range gaps {
		byDoc[g.Doc] = g
	}
	docGapTable(w, "BARE", "UNEXPLAINED", docs, func(d model.Doc) (int, []string) {
		g := byDoc[d.ID]
		return g.Sections, g.Unexplained
	})
}

// docGapTable is the shape both gap views share: the document's identity, the
// undischarged-over-total ratio, and the anchors behind it. gapsOf returns the
// document's section total and its outstanding anchors; a document with no gap
// row renders as no gap rather than misaligning the table.
func docGapTable(w io.Writer, ratioHeader, anchorHeader string, docs []model.Doc,
	gapsOf func(model.Doc) (sections int, anchors []string)) {
	tbl := newTable(
		column{header: "REF"},
		titleColumn("TITLE"),
		column{header: ratioHeader},
		column{header: anchorHeader},
	)
	for _, d := range docs {
		sections, anchors := gapsOf(d)
		tbl.add(DocRef(d), d.Title,
			fmt.Sprintf("%d/%d", len(anchors), sections),
			strings.Join(anchors, " "))
	}
	tbl.flush(w)
}

// DocVersionsTable prints one row per version of a document, newest first:
// the `lode doc versions` view.
func DocVersionsTable(w io.Writer, versions []model.DocVersionSummary) {
	tbl := newTable(
		column{header: "VERSION"},
		titleColumn("TITLE"),
		column{header: "ISSUED"},
		column{header: "CREATED AT"},
	)
	for _, v := range versions {
		tbl.add(strconv.Itoa(v.Version), v.Title, dash(v.Issued), LocalTime(v.CreatedAt))
	}
	tbl.flush(w)
}

// DocVersionRender prints one version of a document: its identity and body —
// the `lode doc get --version` view. current is the document's live version;
// when it differs from v.Version, a line says the rendered version is not it.
func DocVersionRender(w io.Writer, v model.DocVersion, current int) {
	fmt.Fprintf(w, "%d  %s\n", v.Doc, v.Title)
	fmt.Fprintf(w, "  version:  %d\n", v.Version)
	if v.Issued != "" {
		fmt.Fprintf(w, "  issued:   %s\n", v.Issued)
	}
	fmt.Fprintf(w, "  created:  %s\n", LocalTime(v.CreatedAt))
	if v.Version != current {
		fmt.Fprintf(w, "  (not the current version; current is %d)\n", current)
	}
	if v.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, v.Body)
	}
}

// DocDetailRender prints one document: its metadata, body, sections, and
// edges both ways — the `lode doc get` view.
func DocDetailRender(w io.Writer, d model.DocDetail) {
	fmt.Fprintf(w, "%d  %s\n", d.ID, d.Title)
	fmt.Fprintf(w, "  project:  %s\n", d.Project)
	fmt.Fprintf(w, "  kind:     %s\n", d.Kind)
	if d.Number != 0 {
		fmt.Fprintf(w, "  number:   %d\n", d.Number)
	}
	fmt.Fprintf(w, "  slug:     %s\n", d.Slug)
	fmt.Fprintf(w, "  status:   %s\n", d.Status)
	fmt.Fprintf(w, "  version:  %d\n", d.Version)
	if d.Issued != "" {
		fmt.Fprintf(w, "  issued:   %s\n", d.Issued)
	}
	fmt.Fprintf(w, "  owner:    %s\n", dash(d.Owner))
	// Only when set: most documents predate the column or were authored
	// outside a worktree, and a row of dashes is not worth the line (025 §12).
	if d.GeneratedByTask != "" {
		fmt.Fprintf(w, "  written by task: %s\n", d.GeneratedByTask)
	}
	if d.Revision != nil {
		fmt.Fprintf(w, "  open revision: by %s at %s\n", d.Revision.CreatedBy, LocalTime(d.Revision.CreatedAt))
	}
	if len(d.Sections) > 0 {
		fmt.Fprintln(w, "\n  sections:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "    ANCHOR\tNUMBER\tHEADING")
		for _, s := range d.Sections {
			fmt.Fprintf(tw, "    %s\t%s\t%s\n", s.Anchor, s.Number, s.Heading)
		}
		tw.Flush()
	}
	if d.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, d.Body)
	}
	if len(d.Edges) > 0 || len(d.EdgesIn) > 0 {
		fmt.Fprintln(w, "\nedges:")
		for _, e := range d.Edges {
			fmt.Fprintf(w, "  %s %s %s\n", d.Slug, e.Type, docEdgeTarget(e))
		}
		for _, e := range d.EdgesIn {
			fmt.Fprintf(w, "  %s %s %s\n", docEdgeTarget(e), e.Type, d.Slug)
		}
	}
}

// docEdgeTarget renders one edge's far end: the document's slug and optional
// anchor — the id only when a read did not resolve the slug — or the external
// reference an unresolved edge carries.
func docEdgeTarget(e model.DocEdge) string {
	if e.ToDoc == 0 {
		return e.ToExternal
	}
	name := e.ToSlug
	if name == "" {
		name = strconv.FormatInt(e.ToDoc, 10)
	}
	if e.ToAnchor != "" {
		return name + "#" + e.ToAnchor
	}
	return name
}
