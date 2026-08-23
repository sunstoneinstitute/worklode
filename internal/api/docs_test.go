package api_test

// docs_test.go covers the document surface of spec 025: the /api/v1/docs
// endpoints and the two read-only cockpit pages. The lifecycle rules
// themselves are the store's (internal/store/docs_test.go); what is checked
// here is that each rule reaches the caller as the right status code and a
// message naming what to fix.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// docSpecBody is a well-formed spec: frontmatter, an H1 title, and two
// anchored sections whose anchors agree with their numbers.
const docSpecBody = `---
status: draft
issued: 2026-08-01
requires: 004-execution-backbone.md#sec-6
---

# Documents in the backbone

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// docPlanBody covers one section of the spec above, by slug.
const docPlanBody = `---
status: draft
covers:
  - 025-documents-in-the-backbone.md#sec-1
---

# Documents in the backbone, part 2

## Task 1

Do the thing.
`

// docPlanMintBody is a well-formed plan in the mintable ## Tasks format
// (025 §9.1): two definitions, no blockers, for the accept-response tests.
const docPlanMintBody = `---
status: draft
---

# A mintable plan

## Tasks

### Task 1 — First task

` + "```yaml" + `
kind: feature
priority: high
` + "```" + `

Do the first thing.

### Task 2 — Second task

` + "```yaml" + `
kind: bug
priority: medium
` + "```" + `

Do the second thing.
`

// docActor registers an actor and returns a bearer token for it, for the
// cases that need a second identity (the accept gate is assignee-only).
func docActor(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	return seedActor(t, st, id, "human", id, false)
}

// createDocViaAPI posts a document and fails unless it lands.
func createDocViaAPI(t *testing.T, h http.Handler, token string, in model.CreateDocInput) model.Doc {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/docs", token, in)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create doc status = %d, body %s", rr.Code, rr.Body.String())
	}
	var d model.Doc
	decodeInto(t, rr, &d)
	return d
}

// seedDoc writes a document straight through the store, for the one state the
// API deliberately refuses to create: an accepted plan (POST /accept is
// stubbed for plans until minting exists, 025 §9.2).
func seedDoc(t *testing.T, st *store.Store, in store.DocInput) *model.Doc {
	t.Helper()
	var out *model.Doc
	_, _, err := st.RecordDocEvent(context.Background(), "create", "cli",
		"seed-"+in.Project+"-"+in.Slug, "doc.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = store.CreateDoc(tx, st.Now(), in, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("seed doc %s: %v", in.Slug, err)
	}
	return out
}

// docPath builds the /api/v1/docs path for a document id.
func docPath(id int64, suffix string) string {
	return "/api/v1/docs/" + strconv.FormatInt(id, 10) + suffix
}

// acceptedSpec creates a spec assigned to the caller and accepts it, the
// starting point for every revision case.
func acceptedSpec(t *testing.T, h http.Handler, token, project, slug string, number int) model.Doc {
	t.Helper()
	d := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: project, Kind: "spec", Number: number, Slug: slug, Body: docSpecBody,
	})
	rr := doReq(t, h, "POST", docPath(d.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, body %s", rr.Code, rr.Body.String())
	}
	var accepted model.Doc
	decodeInto(t, rr, &accepted)
	return accepted
}

func TestCreateDoc(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	got := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: docSpecBody,
	})
	if got.ID == 0 {
		t.Error("id = 0, want a generated id")
	}
	if got.Title != "Documents in the backbone" {
		t.Errorf("title = %q, want the body's H1", got.Title)
	}
	for _, c := range []struct{ name, got, want string }{
		{"project", got.Project, "proj"},
		{"kind", got.Kind, "spec"},
		{"slug", got.Slug, "025-documents-in-the-backbone"},
		{"status", got.Status, "draft"},
		{"issued", got.Issued, "2026-08-01"},
		{"assignee", got.Assignee, "alice"},
		{"created_by", got.CreatedBy, "alice"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got.Number != 25 || got.Version != 1 {
		t.Errorf("number/version = %d/%d, want 25/1", got.Number, got.Version)
	}
}

// TestCreateDocRejectsStatus: status is the corpus importer's field, and an
// ordinary actor holds no doc.import. The field is declared so the refusal can
// name it rather than silently dropping it.
func TestCreateDocRejectsStatus(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")
	bobToken := docActor(t, st, "bob")

	rr := doReq(t, h, "POST", "/api/v1/docs", bobToken, map[string]any{
		"project": "proj", "kind": "spec", "number": 25,
		"slug": "025-x", "body": docSpecBody, "status": "accepted",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "status") {
		t.Errorf("error = %q, want it to name the status field", msg)
	}
}

// TestCreateDocAtAcceptedForAnImporter: an admin holds doc.import, so a stated
// status is honoured — and creating a spec accepted must establish everything
// the accept gate would have, its anchors published at version 1.
func TestCreateDocAtAcceptedForAnImporter(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	got := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x",
		Body: docSpecBody, Status: "accepted",
	})
	if got.Status != "accepted" {
		t.Fatalf("status = %q, want accepted", got.Status)
	}
	rr := doReq(t, h, "GET", docPath(got.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if len(detail.Sections) != 2 {
		t.Fatalf("sections = %+v, want the two anchored headings", detail.Sections)
	}
	for _, sec := range detail.Sections {
		if !sec.Published {
			t.Errorf("#%s is unpublished; a spec created accepted has its anchors frozen", sec.Anchor)
		}
		if sec.LastRevisedIn != 1 {
			t.Errorf("#%s last_revised_in = %d, want 1: history is not reconstructed",
				sec.Anchor, sec.LastRevisedIn)
		}
	}

	// The value is checked too: the importer may state a status, not invent one.
	rr = doReq(t, h, "POST", "/api/v1/docs", token, map[string]any{
		"project": "proj", "kind": "spec", "number": 26,
		"slug": "026-x", "body": docSpecBody, "status": "proposed",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "superseded") {
		t.Errorf("error = %q, want it to name the three statuses", msg)
	}
}

// TestReplaceDocEdges is the corpus import's second pass: a reference that
// resolved to nothing at create time becomes a real edge once its target
// exists, and nothing else about the document moves.
func TestReplaceDocEdges(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	// The plan covers a spec that does not exist yet, so the reference is kept
	// verbatim — the state the import's first pass leaves behind.
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	rr := doReq(t, h, "GET", docPath(plan.ID, ""), token, nil)
	var before model.DocDetail
	decodeInto(t, rr, &before)
	if len(before.Edges) != 1 || before.Edges[0].ToExternal == "" {
		t.Fatalf("edges before = %+v, want one unresolved covers edge", before.Edges)
	}

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: docSpecBody,
	})

	rr = doReq(t, h, "PUT", docPath(plan.ID, "/edges"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var after model.DocDetail
	decodeInto(t, rr, &after)
	if len(after.Edges) != 1 {
		t.Fatalf("edges after = %+v, want the one covers edge", after.Edges)
	}
	got := after.Edges[0]
	if got.ToDoc != spec.ID || got.ToAnchor != "sec-1" || got.ToExternal != "" {
		t.Errorf("edge = %+v, want it resolved to doc %d #sec-1", got, spec.ID)
	}
	for _, c := range []struct{ name, got, want string }{
		{"body", after.Body, before.Body},
		{"status", after.Status, before.Status},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want it untouched (%q)", c.name, c.got, c.want)
		}
	}
	if after.Version != before.Version {
		t.Errorf("version = %d, want it untouched (%d): the source is unchanged",
			after.Version, before.Version)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at moved to %s from %s; re-resolution authors nothing",
			after.UpdatedAt, before.UpdatedAt)
	}

	// Import authority, not authoring authority.
	bobToken := docActor(t, st, "bob")
	if rr := doReq(t, h, "PUT", docPath(plan.ID, "/edges"), bobToken, nil); rr.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "PUT", "/api/v1/docs/4711/edges", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", rr.Code)
	}
}

// TestDocDetailEdgesIncludeCompletedWith: GET /api/v1/docs/{id} (what
// `lode doc get --json` shows) must carry a partial covers entry's
// fullCoverageWith closure and a defers entry's owner (026 §5, §5.3) — both
// live in the doc_coverage_completed_with side-table, not doc_edges itself,
// so a document's own edge listing previously understated what its
// frontmatter asserted (WL-291).
func TestDocDetailEdgesIncludeCompletedWith(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: docSpecBody,
	})
	owner := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 26, Slug: "026-owner", Body: docSpecBody,
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "closer-plan",
		Body: "---\nstatus: draft\ncovers:\n  - " + spec.Slug + ".md#sec-1\n---\n\n# Closer\n",
	})
	partial := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "partial-plan",
		Body: `---
status: draft
covers:
  - spec: ` + spec.Slug + `.md#sec-1
    coverage: partial
    fullCoverageWith:
      - closer-plan.md
---

# Partial
`,
	})
	deferrer := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "deferring-plan",
		Body: `---
status: draft
defers:
  - spec: ` + spec.Slug + `.md#sec-2
    to: ` + owner.Slug + `.md
---

# Deferring
`,
	})

	rr := doReq(t, h, "GET", docPath(partial.ID, ""), token, nil)
	var partialDetail model.DocDetail
	decodeInto(t, rr, &partialDetail)
	if len(partialDetail.Edges) != 1 || partialDetail.Edges[0].Type != "covers" {
		t.Fatalf("partial plan edges = %+v, want one covers edge", partialDetail.Edges)
	}
	if want := []string{"closer-plan"}; !slices.Equal(partialDetail.Edges[0].CompletedWith, want) {
		t.Errorf("covers edge completed_with = %v, want %v", partialDetail.Edges[0].CompletedWith, want)
	}

	rr = doReq(t, h, "GET", docPath(deferrer.ID, ""), token, nil)
	var deferrerDetail model.DocDetail
	decodeInto(t, rr, &deferrerDetail)
	if len(deferrerDetail.Edges) != 1 || deferrerDetail.Edges[0].Type != "defers" {
		t.Fatalf("deferring plan edges = %+v, want one defers edge", deferrerDetail.Edges)
	}
	if want := []string{owner.Slug}; !slices.Equal(deferrerDetail.Edges[0].CompletedWith, want) {
		t.Errorf("defers edge completed_with = %v, want %v", deferrerDetail.Edges[0].CompletedWith, want)
	}
}

// TestCreateDocRejectsParseDefect: an anchor defect makes a section
// unaddressable, so the document never lands — and the 422 names the anchor.
func TestCreateDocRejectsParseDefect(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	body := strings.Replace(docSpecBody, "## 2. Model {#sec-2}", "## 1. Model {#sec-1}", 1)
	rr := doReq(t, h, "POST", "/api/v1/docs", token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: body,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "sec-1") {
		t.Errorf("error = %q, want it to name the duplicated anchor", msg)
	}
}

func TestCreateDocRejectsBadInput(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	// msg is asserted, not just the status: the first two are refused by the
	// handler's own pre-checks ahead of the transaction, and the store would
	// answer 422 for them too — so a status-only assertion would pass with the
	// pre-checks deleted and the better message lost.
	cases := map[string]struct {
		in   model.CreateDocInput
		code int
		msg  string
	}{
		"unknown kind": {model.CreateDocInput{
			Project: "proj", Kind: "memo", Number: 1, Slug: "s", Body: docSpecBody,
		}, http.StatusUnprocessableEntity, "must be spec, adr, or plan"},
		"missing slug": {model.CreateDocInput{
			Project: "proj", Kind: "spec", Number: 1, Body: docSpecBody,
		}, http.StatusUnprocessableEntity, "slug is required"},
		"spec without a number": {model.CreateDocInput{
			Project: "proj", Kind: "spec", Slug: "s", Body: docSpecBody,
		}, http.StatusUnprocessableEntity, "corpus number"},
		"plan with a number": {model.CreateDocInput{
			Project: "proj", Kind: "plan", Number: 3, Slug: "s", Body: docPlanBody,
		}, http.StatusUnprocessableEntity, "carries no number"},
		"unknown project": {model.CreateDocInput{
			Project: "nope", Kind: "spec", Number: 1, Slug: "s", Body: docSpecBody,
		}, http.StatusNotFound, "not found"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/docs", token, tc.in)
			if rr.Code != tc.code {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.code, rr.Body.String())
			}
			if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, tc.msg) {
				t.Errorf("error = %q, want it to contain %q", msg, tc.msg)
			}
		})
	}
}

// TestCreateDocDuplicateSlugConflicts: the identity rules of 025 §5 reach the
// caller as a 409 naming the collision, not as a raw database error.
func TestCreateDocDuplicateSlugConflicts(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	in := model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	}
	createDocViaAPI(t, h, token, in)

	in.Number = 26
	rr := doReq(t, h, "POST", "/api/v1/docs", token, in)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "025-x") {
		t.Errorf("error = %q, want it to name the slug", msg)
	}
}

func TestListDocs(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: docSpecBody,
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "other", Kind: "spec", Number: 1, Slug: "001-elsewhere", Body: docSpecBody,
	})
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, body %s", rr.Code, rr.Body.String())
	}

	cases := map[string]struct {
		query string
		want  []string
	}{
		// Corpus order: project, then kind, then number and slug — so a
		// project's plans (no number) sort ahead of its specs.
		"all":         {"", []string{"001-elsewhere", "025-part-2", "025-documents-in-the-backbone"}},
		"by project":  {"?project=proj", []string{"025-part-2", "025-documents-in-the-backbone"}},
		"by kind":     {"?kind=plan", []string{"025-part-2"}},
		"by status":   {"?status=accepted", []string{"025-documents-in-the-backbone"}},
		"combined":    {"?project=proj&kind=spec&status=draft", nil},
		"no matches":  {"?project=proj&status=superseded", nil},
		"kind+status": {"?kind=spec&status=accepted", []string{"025-documents-in-the-backbone"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "GET", "/api/v1/docs"+tc.query, token, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var resp model.DocListResponse
			decodeInto(t, rr, &resp)
			var got []string
			for _, d := range resp.Docs {
				got = append(got, d.Slug)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("slugs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResolveDocRef: GET /api/v1/docs/resolve answers the one document a ref
// names, so a `lode doc <verb> <slug>` costs an indexed lookup rather than a
// listing of the corpus. The route sits in front of GET /api/v1/docs/{id} and
// must not be read as an id.
func TestResolveDocRef(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})

	for _, ref := range []string{"025-x", strconv.FormatInt(spec.ID, 10)} {
		rr := doReq(t, h, "GET", "/api/v1/docs/resolve?ref="+ref, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("resolve %q: status = %d, body %s", ref, rr.Code, rr.Body.String())
		}
		var got model.Doc
		decodeInto(t, rr, &got)
		if got.ID != spec.ID {
			t.Errorf("resolve %q = %d, want %d", ref, got.ID, spec.ID)
		}
		// The resolver answers an id, not a corpus text.
		if got.Body != "" {
			t.Errorf("resolve %q carries a body of %d bytes, want none", ref, len(got.Body))
		}
	}

	if rr := doReq(t, h, "GET", "/api/v1/docs/resolve", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("resolve with no ref: status = %d, want 422", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/api/v1/docs/resolve?ref=nope", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("resolve unmatched: status = %d, want 404", rr.Code)
	}

	// Slugs are unique per project, not globally: the server refuses rather
	// than picking one, and says so in a way the caller can act on.
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "other", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	rr := doReq(t, h, "GET", "/api/v1/docs/resolve?ref=025-x", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("resolve ambiguous: status = %d, want 422", rr.Code)
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "025-x") || !strings.Contains(msg, "numeric id") {
		t.Errorf("error = %q, want it to name the slug and the way out", msg)
	}
}

// TestListDocsOmitsBodies: a corpus is many documents of many kilobytes each
// and no list consumer reads the markdown, so the list projection carries none
// of it. GET /api/v1/docs/{id} is where a body comes from.
func TestListDocsOmitsBodies(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})

	rr := doReq(t, h, "GET", "/api/v1/docs", token, nil)
	var resp model.DocListResponse
	decodeInto(t, rr, &resp)
	if len(resp.Docs) != 1 {
		t.Fatalf("docs = %+v, want one", resp.Docs)
	}
	if resp.Docs[0].Body != "" {
		t.Errorf("list body = %q, want it omitted", resp.Docs[0].Body)
	}
	// Everything else a list row needs is still there.
	if resp.Docs[0].Title == "" || resp.Docs[0].Slug != "025-x" {
		t.Errorf("list row = %+v, want the identifying fields kept", resp.Docs[0])
	}
	if strings.Contains(rr.Body.String(), "Scope body.") {
		t.Error("list response carries markdown from a document body")
	}

	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if detail.Body != docSpecBody {
		t.Errorf("detail body = %q, want the whole source", detail.Body)
	}

	// The index page renders no markdown either.
	page := doReq(t, h, "GET", "/docs", "", nil)
	if strings.Contains(page.Body.String(), "Scope body.") {
		t.Error("/docs carries markdown from a document body")
	}
}

// TestGetDocDetail: the detail endpoint carries what a reader cannot
// reconstruct from the body alone — the section rows with their accept-time
// state, and the edges in both directions.
func TestGetDocDetail(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: docSpecBody,
	})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	rr := doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.DocDetail
	decodeInto(t, rr, &got)

	if got.ID != spec.ID || got.Slug != spec.Slug {
		t.Errorf("detail = %+v, want the spec itself embedded", got.Doc)
	}
	if len(got.Sections) != 2 ||
		got.Sections[0].Anchor != "sec-1" || got.Sections[1].Anchor != "sec-2" {
		t.Fatalf("sections = %+v, want sec-1 then sec-2", got.Sections)
	}
	if got.Sections[0].Published {
		t.Error("a draft's sections are not published yet")
	}
	if len(got.Edges) != 1 || got.Edges[0].Type != "requires" ||
		got.Edges[0].ToExternal != "004-execution-backbone.md#sec-6" {
		t.Errorf("edges = %+v, want the one unresolved requires", got.Edges)
	}
	if len(got.EdgesIn) != 1 || got.EdgesIn[0].Type != "isCoveredBy" ||
		got.EdgesIn[0].ToDoc != plan.ID || got.EdgesIn[0].FromAnchor != "sec-1" {
		t.Errorf("edges_in = %+v, want the plan's covers read backward", got.EdgesIn)
	}
	if got.Revision != nil {
		t.Errorf("revision = %+v, want null with none open", got.Revision)
	}

	// A plan carries no sections (025 §9) and its covers edge points out.
	rr = doReq(t, h, "GET", docPath(plan.ID, ""), token, nil)
	decodeInto(t, rr, &got)
	if len(got.Sections) != 0 {
		t.Errorf("plan sections = %+v, want none", got.Sections)
	}
	if len(got.Edges) != 1 || got.Edges[0].Type != "covers" || got.Edges[0].ToDoc != spec.ID {
		t.Errorf("plan edges = %+v, want one covers edge at the spec", got.Edges)
	}
}

func TestGetDocNotFound(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	if rr := doReq(t, h, "GET", "/api/v1/docs/4711", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", rr.Code)
	}
	// A non-numeric id is a malformed request, not a missing document: saying
	// so beats a 404 that reads like "this document was deleted".
	rr := doReq(t, h, "GET", "/api/v1/docs/025-x", token, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric id status = %d, want 400, body %s", rr.Code, rr.Body.String())
	}
}

func TestDocsRequireAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	if rr := doReq(t, h, "GET", "/api/v1/docs", "", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestUpdateDocBody: a draft spec and a plan at any status are edited in
// place; an accepted spec is revised instead, and the refusal says so.
func TestUpdateDocBody(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	edited := strings.Replace(docSpecBody, "# Documents in the backbone", "# Retitled", 1)
	rr := doReq(t, h, "PUT", docPath(spec.ID, "/body"), token, model.UpdateDocBodyInput{Body: edited})
	if rr.Code != http.StatusOK {
		t.Fatalf("draft spec status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.Doc
	decodeInto(t, rr, &got)
	if got.Title != "Retitled" || got.Body != edited {
		t.Errorf("doc = %+v, want the new title and body", got)
	}

	if rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PUT", docPath(spec.ID, "/body"), token, model.UpdateDocBodyInput{Body: docSpecBody})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("accepted spec status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "revise") {
		t.Errorf("error = %q, want it to point at revise", msg)
	}

	// A plan stays freely mutable at any status (025 §9). Seeded accepted:
	// accepting a plan over HTTP is the stub below.
	plan := seedDoc(t, st, store.DocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
		CreatedBy: "alice", Status: "accepted",
	})
	rr = doReq(t, h, "PUT", docPath(plan.ID, "/body"), token,
		model.UpdateDocBodyInput{Body: strings.Replace(docPlanBody, "Do the thing.", "Do it twice.", 1)})
	if rr.Code != http.StatusOK {
		t.Fatalf("accepted plan status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}

	if rr := doReq(t, h, "PUT", "/api/v1/docs/4711/body", token,
		model.UpdateDocBodyInput{Body: docSpecBody}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", rr.Code)
	}
}

// TestAcceptDoc: acceptance is the assignee's deliberate act (025 §7), so
// another authenticated actor is refused with 403 — an authorization refusal
// about this document, not about the endpoint.
func TestAcceptDoc(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	bobToken := docActor(t, st, "bob")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), bobToken, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("other actor status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("assignee status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.Doc
	decodeInto(t, rr, &got)
	if got.Status != "accepted" {
		t.Errorf("status = %q, want accepted", got.Status)
	}

	// Acceptance freezes the published anchor set.
	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	for _, sec := range detail.Sections {
		if !sec.Published {
			t.Errorf("section %s is unpublished after accept", sec.Anchor)
		}
	}

	// Accepting a plan mints its tasks in the same transaction (025 §9.2),
	// which is not built yet, so it is refused rather than half-done.
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	rr = doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("plan accept status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "plan") {
		t.Errorf("error = %q, want it to say a plan cannot be accepted yet", msg)
	}
}

// TestReAcceptPlanMintsAddedDeclaration: the whole re-accept path through the
// endpoint (025 §9.2). A plan edited after acceptance carries a new version,
// so its accept event is a new event and the mint runs, returning only the
// declaration that had no row; re-accepting the same version again mints
// nothing and still answers 200 with the document.
func TestReAcceptPlanMintsAddedDeclaration(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "remint-plan", Body: docPlanMintBody,
	})
	rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("first accept status = %d, body %s", rr.Code, rr.Body.String())
	}
	var first model.AcceptDocResponse
	decodeInto(t, rr, &first)
	if len(first.Tasks) != 2 {
		t.Fatalf("first accept minted %d tasks, want 2", len(first.Tasks))
	}

	// The no-op path below must not swallow the assignee gate: another actor
	// re-accepting an accepted plan is still 403.
	bobToken := docActor(t, st, "bob")
	if rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), bobToken, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("other actor re-accept status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}

	// Re-accepting at the same version: the event id collides, apply is
	// skipped, and the endpoint says so by answering with the document and no
	// minted tasks rather than by refusing.
	rr = doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("unedited re-accept status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	var settled model.AcceptDocResponse
	decodeInto(t, rr, &settled)
	if settled.Status != "accepted" || len(settled.Tasks) != 0 {
		t.Errorf("unedited re-accept = %+v, want the accepted doc and no minted tasks", settled)
	}

	edited := docPlanMintBody + `
### Task 3 — Third task

` + "```yaml" + `
kind: chore
` + "```" + `

Do the third thing.
`
	rr = doReq(t, h, "PUT", docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Body: edited})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-accept status = %d, body %s", rr.Code, rr.Body.String())
	}
	var second model.AcceptDocResponse
	decodeInto(t, rr, &second)
	if len(second.Tasks) != 1 {
		t.Fatalf("re-accept minted %d tasks, want 1", len(second.Tasks))
	}
	if second.Tasks[0].Title != "Third task" {
		t.Errorf("minted %q, want the added declaration", second.Tasks[0].Title)
	}
	for _, task := range first.Tasks {
		if second.Tasks[0].ID == task.ID {
			t.Errorf("re-accept returned an already-minted task %s", task.ID)
		}
	}

	// The event says what actually happened: the second acceptance left
	// "accepted", not "draft" — an append-only log may not record a transition
	// the document never made. Read from the table rather than through
	// /api/v1/events, whose commit-horizon predicate (025 §15) may not have
	// caught up with an event committed a moment ago.
	rows, err := st.DBForTests().Query(
		`SELECT payload->>'wl:fromStatus' FROM events WHERE type = 'wl:DocumentAccepted' ORDER BY id`)
	if err != nil {
		t.Fatalf("read acceptance events: %v", err)
	}
	defer rows.Close()
	var fromStatuses []string
	for rows.Next() {
		var from string
		if err := rows.Scan(&from); err != nil {
			t.Fatalf("scan wl:fromStatus: %v", err)
		}
		fromStatuses = append(fromStatuses, from)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read acceptance events: %v", err)
	}
	if !slices.Equal(fromStatuses, []string{"wlc:draft", "wlc:accepted"}) {
		t.Errorf("wl:fromStatus values = %v, want [wlc:draft wlc:accepted]", fromStatuses)
	}
}

// TestAcceptPlanReturnsMintedTasks: accepting a plan returns the doc and the
// tasks it minted in one response (025 §9.2); accepting a spec or ADR
// carries no "tasks" key at all, so the response stays byte-identical to
// before this field existed.
func TestAcceptPlanReturnsMintedTasks(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "mint-plan", Body: docPlanMintBody,
	})
	rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept plan status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resp model.AcceptDocResponse
	decodeInto(t, rr, &resp)
	if resp.Status != "accepted" {
		t.Errorf("status = %q, want accepted", resp.Status)
	}
	if len(resp.Tasks) != 2 {
		t.Fatalf("tasks = %v, want 2 minted tasks", resp.Tasks)
	}
	for _, task := range resp.Tasks {
		if task.PlanDoc != plan.ID {
			t.Errorf("task %s plan_doc = %d, want %d", task.ID, task.PlanDoc, plan.ID)
		}
	}

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 1, Slug: "spec-1", Body: docSpecBody,
	})
	rr = doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept spec status = %d, body %s", rr.Code, rr.Body.String())
	}
	if _, present := decodeMap(t, rr)["tasks"]; present {
		t.Errorf(`spec accept response carries a "tasks" key, want none`)
	}
}

// TestDocRevisionLifecycle walks 025 §7.2 over HTTP: open a candidate, edit
// it, be refused for a violation, fix it, land it.
func TestDocRevisionLifecycle(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-x", 25)

	rr := doReq(t, h, "POST", docPath(spec.ID, "/revise"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("revise status = %d, body %s", rr.Code, rr.Body.String())
	}
	var rev model.DocRevision
	decodeInto(t, rr, &rev)
	if rev.Doc != spec.ID || rev.Body != docSpecBody || rev.CreatedBy != "alice" {
		t.Errorf("revision = %+v, want a copy of the accepted body", rev)
	}

	// One candidate at a time.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revise"), token, nil); rr.Code != http.StatusConflict {
		t.Fatalf("second revise status = %d, want 409, body %s", rr.Code, rr.Body.String())
	}

	// The open candidate shows up on the detail endpoint.
	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if detail.Revision == nil || detail.Revision.Body != docSpecBody {
		t.Fatalf("detail revision = %+v, want the open candidate", detail.Revision)
	}

	// A candidate that drops a published anchor is refused at the gate, and
	// the 422 lists the violation.
	dropped := strings.Replace(docSpecBody, "## 1. Scope {#sec-1}\n\nScope body.\n\n", "", 1)
	if rr := doReq(t, h, "PUT", docPath(spec.ID, "/revision"), token,
		model.UpdateDocBodyInput{Body: dropped}); rr.Code != http.StatusOK {
		t.Fatalf("update revision status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", docPath(spec.ID, "/revision/accept"), token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("violating accept status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	msg, _ := decodeMap(t, rr)["error"].(string)
	if !strings.Contains(msg, "sec-1") || !strings.Contains(msg, "section removed") {
		t.Errorf("error = %q, want it to list the removed anchor", msg)
	}

	// Appending a section is legal; landing it bumps the version and moves
	// last_revised_in on exactly the changed anchor.
	added := docSpecBody + "\n## 3. Added {#sec-3}\n\nAdded body.\n"
	if rr := doReq(t, h, "PUT", docPath(spec.ID, "/revision"), token,
		model.UpdateDocBodyInput{Body: added}); rr.Code != http.StatusOK {
		t.Fatalf("update revision status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", docPath(spec.ID, "/revision/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept revision status = %d, body %s", rr.Code, rr.Body.String())
	}
	var landed model.Doc
	decodeInto(t, rr, &landed)
	if landed.Version != 2 || landed.Body != added {
		t.Errorf("doc = version %d, want 2 with the candidate body", landed.Version)
	}

	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	decodeInto(t, rr, &detail)
	if detail.Revision != nil {
		t.Errorf("revision = %+v, want it consumed by the accept", detail.Revision)
	}
	if len(detail.Sections) != 3 {
		t.Fatalf("sections = %+v, want three", detail.Sections)
	}
	for _, sec := range detail.Sections {
		want := 1
		if sec.Anchor == "sec-3" {
			want = 2
		}
		if sec.LastRevisedIn != want {
			t.Errorf("%s last_revised_in = %d, want %d", sec.Anchor, sec.LastRevisedIn, want)
		}
	}
}

// TestDocRevisionDiscard walks DELETE /api/v1/docs/{id}/revision: a third
// party is refused, the proposer withdraws their own candidate, and the slot
// the withdrawal frees takes a fresh one straight away (025 §7.2).
//
// The token identity is alice, who is the spec's assignee; bob proposes.
func TestDocRevisionDiscard(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	bobToken := docActor(t, st, "bob")
	carolToken := docActor(t, st, "carol")
	spec := acceptedSpec(t, h, token, "proj", "025-x", 25)

	// Nothing open yet: 404, not a silent success.
	if rr := doReq(t, h, "DELETE", docPath(spec.ID, "/revision"), token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("discard with no open revision status = %d, want 404", rr.Code)
	}

	// Proposing stays open to any doc.write holder, so bob may open one on
	// alice's document.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revise"), bobToken, nil); rr.Code != http.StatusOK {
		t.Fatalf("revise as bob status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Carol is neither the assignee nor the proposer.
	if rr := doReq(t, h, "DELETE", docPath(spec.ID, "/revision"), carolToken, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("third-party discard status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}
	rr := doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if detail.Revision == nil {
		t.Fatal("the refused discard removed the candidate anyway")
	}

	// The proposer withdraws their own, and the response is the document,
	// unchanged.
	rr = doReq(t, h, "DELETE", docPath(spec.ID, "/revision"), bobToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("author discard status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	var after model.Doc
	decodeInto(t, rr, &after)
	if after.Version != 1 || after.Body != docSpecBody {
		t.Errorf("doc = version %d, want the accepted version untouched by a discard", after.Version)
	}

	// The slot is free: a fresh candidate opens rather than 409ing. This one
	// is bob's again, and the assignee withdraws it — the other half of the
	// gate.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revise"), bobToken, nil); rr.Code != http.StatusOK {
		t.Fatalf("revise after a discard status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "DELETE", docPath(spec.ID, "/revision"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("assignee discard status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	decodeInto(t, rr, &detail)
	if detail.Revision != nil {
		t.Errorf("revision = %+v, want it withdrawn", detail.Revision)
	}

	// Two discards happened, and each recorded its own event: the type string
	// is the handler's, so nothing else in the suite would catch a typo in it.
	// Polled, not read once — the commit horizon is cluster-wide.
	pollEvents(t, h, token, "?type=doc.revision_discarded", 2)
}

// TestDocRevisionRefusals covers the states with nothing to revise: a draft
// (edited in place), a plan (edited in place), and a document with no open
// candidate.
func TestDocRevisionRefusals(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	bobToken := docActor(t, st, "bob")

	draft := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	if rr := doReq(t, h, "POST", docPath(draft.ID, "/revise"), token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("revise draft status = %d, want 422", rr.Code)
	}
	if rr := doReq(t, h, "PUT", docPath(draft.ID, "/revision"), token,
		model.UpdateDocBodyInput{Body: docSpecBody}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("update revision of a draft status = %d, want 422", rr.Code)
	}

	spec := acceptedSpec(t, h, token, "proj", "026-x", 26)
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revision/accept"), token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("accept with no open revision status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "PUT", docPath(spec.ID, "/revision"), token,
		model.UpdateDocBodyInput{Body: docSpecBody}); rr.Code != http.StatusNotFound {
		t.Errorf("update with no open revision status = %d, want 404", rr.Code)
	}
	// Landing a revision is assignee-gated like the first accept.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revise"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("revise status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revision/accept"), bobToken, nil); rr.Code != http.StatusForbidden {
		t.Errorf("other actor accept status = %d, want 403", rr.Code)
	}
}

// --- cockpit pages -----------------------------------------------------------

func TestDocsPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	rr := doReq(t, h, "GET", "/docs", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		`href="/docs/WL-SPEC-25">WL-SPEC-25</a>`,
		`href="/docs/WL-SPEC-25">Documents in the backbone</a>`,
		`href="`+docPageURL(plan.ID)+`">Documents in the backbone, part 2</a>`,
		"accepted",
		"draft",
	)
}

func TestDocPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	rr := doReq(t, h, "GET", "/docs/WL-SPEC-25", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		"Documents in the backbone", // title
		"accepted",                  // status chip
		"sec-1",                     // section table
		"Scope",
		"isCoveredBy",       // the plan's covers, read backward
		docPageURL(plan.ID), // plans have no shorthand
		"025-part-2",        // named by slug, not as "document 42"
		"Model body.",       // the body, rendered verbatim in a <pre>
	)
	if strings.Contains(body, "document "+strconv.FormatInt(plan.ID, 10)) {
		t.Errorf("relation names the far end by id rather than by slug:\n%s", body)
	}

	// The far end's corpus reference tells a plan from the spec it covers.
	rr = doReq(t, h, "GET", docPageURL(plan.ID), "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("plan page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(),
		"025-documents-in-the-backbone#sec-1", // the covered section, by slug
		">spec 25<",                           // and what kind of document that is
	)

	if rr := doReq(t, h, "GET", "/docs/"+strconv.FormatInt(spec.ID, 10), "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("numeric URL status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/docs/025-x", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("non-numeric id status = %d, want 404", rr.Code)
	}
}

// docPageURL is the cockpit page path retained for plans, which have no
// cross-corpus shorthand.
func docPageURL(id int64) string { return "/docs/" + strconv.FormatInt(id, 10) }

// --- list selectors (026 §2) --------------------------------------------

// docPlanCoveringSec1Body is a mintable plan whose covers edge names sec-1 of
// docSpecBody's spec, so accepting it discharges exactly that section.
const docPlanCoveringSec1Body = `---
status: draft
covers:
  - 025-documents-in-the-backbone.md#sec-1
---

# Part one

## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: chore
` + "```" + `

Do it.
`

// acceptDocViaAPI accepts a document and fails unless it lands.
func acceptDocViaAPI(t *testing.T, h http.Handler, token string, id int64) {
	t.Helper()
	rr := doReq(t, h, "POST", docPath(id, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept doc %d status = %d, body %s", id, rr.Code, rr.Body.String())
	}
}

// listDocs performs GET /api/v1/docs with the given query and fails unless it
// answers 200.
func listDocs(t *testing.T, h http.Handler, token, query string) model.DocListResponse {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/docs?"+query, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list docs ?%s status = %d, body %s", query, rr.Code, rr.Body.String())
	}
	var resp model.DocListResponse
	decodeInto(t, rr, &resp)
	return resp
}

// TestListDocsNeedsPlanning: the selector answers accepted specs with an
// uncovered section, and carries the gap detail alongside the documents.
func TestListDocsNeedsPlanning(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "part-one", Body: docPlanCoveringSec1Body,
	})
	acceptDocViaAPI(t, h, token, plan.ID)

	resp := listDocs(t, h, token, "needs_planning=true")
	if len(resp.Docs) != 1 || resp.Docs[0].ID != spec.ID {
		t.Fatalf("docs = %+v, want the spec alone", resp.Docs)
	}
	if len(resp.PlanningGaps) != 1 {
		t.Fatalf("planning_gaps = %+v, want one entry", resp.PlanningGaps)
	}
	gap := resp.PlanningGaps[0]
	if gap.Doc != spec.ID || gap.Sections != 2 || len(gap.Gaps) != 1 ||
		gap.Gaps[0] != (model.DocSectionGap{Anchor: "sec-2", Coverage: "unplanned"}) {
		t.Fatalf("gap = %+v, want doc %d, 2 sections, sec-2 unplanned", gap, spec.ID)
	}
	// The listing stays body-free, like every other doc list response.
	if resp.Docs[0].Body != "" {
		t.Errorf("docs[0].body = %q, want it blanked on a list", resp.Docs[0].Body)
	}
}

// TestListDocsNeedsExecution: the selector answers accepted plans with an open
// task, and carries no planning-gap detail.
func TestListDocsNeedsExecution(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "part-one", Body: docPlanCoveringSec1Body,
	})
	acceptDocViaAPI(t, h, token, plan.ID)

	resp := listDocs(t, h, token, "needs_execution=true")
	if len(resp.Docs) != 1 || resp.Docs[0].ID != plan.ID {
		t.Fatalf("docs = %+v, want the plan alone", resp.Docs)
	}
	if resp.PlanningGaps != nil {
		t.Errorf("planning_gaps = %+v, want it omitted for needs_execution", resp.PlanningGaps)
	}
}

// TestListDocsSelectorConflicts: each derived selector implies a kind and a
// status, so a contradicting filter is refused rather than answered with an
// empty list, which would read as "nothing to plan" (026 §2.1).
func TestListDocsSelectorConflicts(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	for name, c := range map[string]struct{ query, want string }{
		"both selectors":               {"needs_planning=true&needs_execution=true", "disjoint"},
		"planning with draft":          {"needs_planning=true&status=draft", "accepted"},
		"planning with plan kind":      {"needs_planning=true&kind=plan", "spec"},
		"execution with draft":         {"needs_execution=true&status=draft", "accepted"},
		"execution with spec kind":     {"needs_execution=true&kind=spec", "plan"},
		"unparseable selector":         {"needs_planning=maybe", "needs_planning"},
		"bare superseded with draft":   {"bare_superseded=true&status=draft", "superseded"},
		"bare superseded with plan":    {"bare_superseded=true&kind=plan", "spec or adr"},
		"bare superseded and planning": {"bare_superseded=true&needs_planning=true", "mutually exclusive"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "GET", "/api/v1/docs?"+c.query, token, nil)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			msg, _ := decodeMap(t, rr)["error"].(string)
			if !strings.Contains(msg, c.want) {
				t.Errorf("error = %q, want it to mention %q", msg, c.want)
			}
		})
	}
}

// TestListDocsSelectorRedundantFiltersAllowed: the implied kind and status may
// be restated — only a contradiction is an error.
func TestListDocsSelectorRedundantFiltersAllowed(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)

	resp := listDocs(t, h, token, "needs_planning=true&kind=spec&status=accepted&project=proj")
	if len(resp.Docs) != 1 || resp.Docs[0].ID != spec.ID {
		t.Fatalf("docs = %+v, want the spec alone", resp.Docs)
	}
}

// TestListDocsBareSuperseded: the selector answers a superseded document
// nothing explains — 025 §6 rule 2 — and carries the gap detail, not the
// planning-gap shape, alongside it.
func TestListDocsBareSuperseded(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	old := seedDoc(t, st, store.DocInput{
		Project: "proj", Kind: "spec", Number: 6, Slug: "006-old", Body: docSpecBody,
		CreatedBy: "alice", Status: "superseded",
	})

	resp := listDocs(t, h, token, "bare_superseded=true")
	if len(resp.Docs) != 1 || resp.Docs[0].ID != old.ID {
		t.Fatalf("docs = %+v, want the superseded doc alone", resp.Docs)
	}
	if len(resp.SupersessionGaps) != 1 {
		t.Fatalf("supersession_gaps = %+v, want one entry", resp.SupersessionGaps)
	}
	gap := resp.SupersessionGaps[0]
	if gap.Doc != old.ID || gap.Sections != 2 || len(gap.Unexplained) != 2 {
		t.Fatalf("gap = %+v, want doc %d, 2 sections, both unexplained", gap, old.ID)
	}
	if resp.PlanningGaps != nil {
		t.Errorf("planning_gaps = %+v, want it omitted for bare_superseded", resp.PlanningGaps)
	}
}

// eventPayload reads one event's decoded payload object out of the /api/v1/events
// projection, failing the test if it carries none.
func eventPayload(t *testing.T, ev map[string]any) map[string]any {
	t.Helper()
	p, ok := ev["payload"].(map[string]any)
	if !ok {
		t.Fatalf("event %v carries no payload object", ev)
	}
	return p
}

// checkPayloadProps fails for every property whose value is not what 025 §15.3
// mandates, naming all of them rather than stopping at the first.
func checkPayloadProps(t *testing.T, payload map[string]any, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got := payload[k]; got != v {
			t.Errorf("payload[%q] = %v, want %q", k, got, v)
		}
	}
}

// eventsOfType reads GET /api/v1/events?type=... without polling: for a type
// whose rows this test has already observed, the horizon has passed them, so a
// short read is a real absence.
func eventsOfType(t *testing.T, h http.Handler, token, typ string) []any {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/events?type="+typ, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/events?type=%s status = %d, body %s", typ, rr.Code, rr.Body.String())
	}
	events, _ := decodeMap(t, rr)["events"].([]any)
	return events
}

// TestAcceptDocEmitsTypedEvent: acceptance is one of the two events the
// doc-lifecycle watcher consumes, so it is recorded in 025 §15.3's typed
// JSON-LD form — wl:DocumentAccepted, with the deterministic external id that
// makes a retry idempotent at the log — and not as the dotted doc.accepted the
// other document verbs still write.
func TestAcceptDocEmitsTypedEvent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := pollEvents(t, h, token, "?type=wl:DocumentAccepted", 1)
	if len(events) != 1 {
		t.Fatalf("got %d wl:DocumentAccepted events, want exactly 1", len(events))
	}
	ev, _ := events[0].(map[string]any)
	iri := store.DocIRI(spec)
	wantExtID := "wl:DocumentAccepted:" + iri + ":" + strconv.Itoa(spec.Version)
	if ev["external_id"] != wantExtID {
		t.Errorf("external_id = %v, want %q", ev["external_id"], wantExtID)
	}
	payload := eventPayload(t, ev)
	id, _ := ev["id"].(float64)
	checkPayloadProps(t, payload, map[string]string{
		"@type":                  "wl:DocumentAccepted",
		"@id":                    fmt.Sprintf("wlid:event/%d", int64(id)),
		"wl:subject":             iri,
		"wl:fromStatus":          "wlc:draft",
		"wl:toStatus":            "wlc:accepted",
		"prov:wasAssociatedWith": "wlid:actor/alice",
	})

	if dotted := eventsOfType(t, h, token, "doc.accepted"); len(dotted) != 0 {
		t.Errorf("doc.accepted events = %v, want none: accept is typed now", dotted)
	}

	// A second accept records no second event — the deterministic external id
	// collapses it — and still answers 422, exactly as it did before the event
	// was typed. Emit skips apply on that conflict, so the store's draft-only
	// gate never runs and the handler raises the refusal in its place;
	// answering 200 would report an accept that did not happen.
	rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("second accept status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if again := eventsOfType(t, h, token, "wl:DocumentAccepted"); len(again) != 1 {
		t.Errorf("wl:DocumentAccepted events after a second accept = %d, want 1", len(again))
	}
}

// TestSubmitDoc walks POST /api/v1/docs/{id}/submit, 025 §15.4's "submission is
// an event, not a status": the log gains a wl:DocumentSubmitted row and the
// document itself does not move at all.
func TestSubmitDoc(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})

	rr := doReq(t, h, "POST", docPath(spec.ID, "/submit"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body %s", rr.Code, rr.Body.String())
	}
	var submitted model.Doc
	decodeInto(t, rr, &submitted)
	if submitted.Status != "draft" {
		t.Errorf("response status = %q, want draft: submission moves no column", submitted.Status)
	}

	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	var after model.DocDetail
	decodeInto(t, rr, &after)
	if after.Status != spec.Status || !after.UpdatedAt.Equal(spec.UpdatedAt) {
		t.Errorf("doc after submit = %q/%v, want it unchanged at %q/%v",
			after.Status, after.UpdatedAt, spec.Status, spec.UpdatedAt)
	}

	events := pollEvents(t, h, token, "?type=wl:DocumentSubmitted", 1)
	if len(events) != 1 {
		t.Fatalf("got %d wl:DocumentSubmitted events, want exactly 1", len(events))
	}
	ev, _ := events[0].(map[string]any)
	iri := store.DocIRI(spec)
	wantExtID := "wl:DocumentSubmitted:" + iri + ":" + strconv.Itoa(spec.Version)
	if ev["external_id"] != wantExtID {
		t.Errorf("external_id = %v, want %q", ev["external_id"], wantExtID)
	}
	payload := eventPayload(t, ev)
	id, _ := ev["id"].(float64)
	checkPayloadProps(t, payload, map[string]string{
		"@type":                  "wl:DocumentSubmitted",
		"@id":                    fmt.Sprintf("wlid:event/%d", int64(id)),
		"wl:subject":             iri,
		"prov:wasAssociatedWith": "wlid:actor/alice",
	})

	// Submitting the same version twice is one fact: the deterministic
	// external id collapses the retry at the log, and the caller still gets a
	// 200 rather than a conflict about a document that is exactly where it was.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/submit"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("second submit status = %d, body %s", rr.Code, rr.Body.String())
	}
	if again := eventsOfType(t, h, token, "wl:DocumentSubmitted"); len(again) != 1 {
		t.Errorf("wl:DocumentSubmitted events after a second submit = %d, want 1", len(again))
	}

	if rr := doReq(t, h, "POST", "/api/v1/docs/4711/submit", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "POST", "/api/v1/docs/025-x/submit", token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id status = %d, want 400", rr.Code)
	}
}

// TestCreateDocRecordsAuthoringTask covers 025 §12: the document a task wrote
// points back at that task, so "which task produced this spec?" is answerable.
// Both halves are checked, because both are the design: a create carrying the
// caller's worktree task records it, and a create carrying none is a document
// with no authoring task rather than a refusal (migration 0044).
func TestCreateDocRecordsAuthoringTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Write spec 025", "kind": "design",
		"priority": "medium",
	})
	taskID, _ := task["id"].(string)
	if taskID == "" {
		t.Fatalf("task = %v, want one with an id", task)
	}

	authored := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x",
		Body: docSpecBody, GeneratedByTask: taskID,
	})
	if authored.GeneratedByTask != taskID {
		t.Errorf("generated_by_task = %q, want %q", authored.GeneratedByTask, taskID)
	}

	// Read back, not just echoed from the create response: the column is what
	// answers the question later.
	rr := doReq(t, h, "GET", docPath(authored.ID, ""), token, nil)
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if detail.GeneratedByTask != taskID {
		t.Errorf("detail generated_by_task = %q, want %q", detail.GeneratedByTask, taskID)
	}

	// The event log carries it too, inside the recorded request — provenance
	// survives even if the row is later tombstoned.
	events := pollEvents(t, h, token, "?type=doc.created", 1)
	payload := eventPayload(t, events[0].(map[string]any))
	req, _ := payload["request"].(map[string]any)
	if req["generated_by_task"] != taskID {
		t.Errorf("doc.created payload request = %v, want generated_by_task %q", req, taskID)
	}

	// No worktree, no authoring task, and the create still lands: a human in
	// the cockpit and an agent working ad hoc both author documents.
	unauthored := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "adr", Number: 51, Slug: "051-x", Body: docSpecBody,
	})
	if unauthored.GeneratedByTask != "" {
		t.Errorf("generated_by_task = %q, want empty for a create carrying no task",
			unauthored.GeneratedByTask)
	}

	// A worktree can outlive the task it was named for, so the id is checked
	// rather than reaching the caller as an anonymous constraint failure.
	rr = doReq(t, h, "POST", "/api/v1/docs", token, map[string]any{
		"project": "proj", "kind": "spec", "number": 26, "slug": "026-x",
		"body": docSpecBody, "generated_by_task": "WL-9999",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown authoring task status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "WL-9999") {
		t.Errorf("error = %q, want it to name the task", msg)
	}
}
