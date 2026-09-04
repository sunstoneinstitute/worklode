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

// --- search -------------------------------------------------------------

// SearchFilter is one hybrid-search request (040 §9). Zero-valued fields let
// the server pick: no kinds means all three, an empty mode means hybrid, and
// a zero limit means the server's default page.
type SearchFilter struct {
	Query   string
	Kinds   []string // doc | task | skill, repeatable
	Mode    string   // hybrid | dense | lexical
	Limit   int
	Project string
}

// Search calls GET /api/v1/search. The response reports how it was actually
// answered: an instance with no embedding provider answers provider "none"
// and real lexical hits rather than an error (040 §11).
func (c *Client) Search(ctx context.Context, f SearchFilter) (model.SearchResponse, []byte, error) {
	q := url.Values{}
	q.Set("q", f.Query)
	for _, k := range f.Kinds {
		q.Add("kind", k)
	}
	if f.Mode != "" {
		q.Set("mode", f.Mode)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	return doJSON[model.SearchResponse](ctx, c, http.MethodGet, withQuery("/api/v1/search", q), nil, "search results")
}

// DocRefs maps document id to the reference a reader cites — "WL-SPEC-40" —
// for every document in project ("" for all projects). A search hit carries
// the document's row id, which is not an address anyone can act on, so the
// human rendering resolves it here.
//
// The lookup is one list request, made only when a result set actually holds
// document hits. An unreachable or unreadable corpus yields a nil map rather
// than an error: a missing reference degrades one column of one line.
func (c *Client) DocRefs(ctx context.Context, project string) map[int64]string {
	resp, _, err := c.ListDocs(ctx, DocListFilter{Project: project})
	if err != nil {
		return nil
	}
	refs := make(map[int64]string, len(resp.Docs))
	for _, d := range resp.Docs {
		refs[d.ID] = DocRef(d)
	}
	return refs
}

// SearchTable prints one line per hit in the 040 §9 form:
//
//	WL-SPEC-25 §15.2  0.032  The ordered log
//
// The first column is an address the reader can act on — a document
// reference and its frozen section anchor, a task id, a qualified skill name
// — the second the fused score, the third the subject's title. A skill's
// title is its qualified name, so it is not repeated.
//
// docRefs resolves document ids to references (DocRefs); a nil or incomplete
// map falls back to the raw id.
func SearchTable(w io.Writer, hits []model.SearchHit, docRefs map[int64]string) {
	tw := newTabwriter(w)
	for _, h := range hits {
		addr := searchAddress(h, docRefs)
		fmt.Fprintf(tw, "%s\t%.3f", addr, h.Score)
		// A skill's title is its qualified name, which is already the
		// address: the cell is dropped rather than repeated, and dropping it
		// rather than emptying it keeps the line free of trailing padding.
		if h.Title != addr {
			fmt.Fprintf(tw, "\t%s", h.Title)
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()
}

// searchAddress renders one hit's address column.
func searchAddress(h model.SearchHit, docRefs map[int64]string) string {
	var addr string
	switch h.Kind {
	case "task":
		addr = h.TaskID
	case "doc":
		addr = docRefs[h.DocID]
		if addr == "" {
			addr = "doc:" + strconv.FormatInt(h.DocID, 10)
		}
	default:
		// A skill's qualified name is its address, and the store puts it in
		// the title column of the response.
		addr = h.Title
	}
	if h.Anchor != "" {
		// Anchors are stored as "sec-15.2" (025 §3.2) and cited as §15.2.
		addr += " §" + strings.TrimPrefix(h.Anchor, "sec-")
	}
	return addr
}

// SearchNotice writes the one line a degraded instance owes the caller: with
// no embedding provider every mode falls back to the lexical arm, and the
// results are real but narrower (040 §11). Callers put it on stderr, since
// the search itself succeeded. Nothing is written for a healthy instance.
func SearchNotice(w io.Writer, resp model.SearchResponse) {
	if resp.Provider != "none" {
		return
	}
	fmt.Fprintf(w, "note: no embedding provider configured on this server; answered with the %s arm only\n", resp.Mode)
}
