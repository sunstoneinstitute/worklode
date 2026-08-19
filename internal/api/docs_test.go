package api_test

// docs_test.go covers the document surface of spec 025: the /api/v1/docs
// endpoints and the two read-only cockpit pages. The lifecycle rules
// themselves are the store's (internal/store/docs_test.go); what is checked
// here is that each rule reaches the caller as the right status code and a
// message naming what to fix.

import (
	"context"
	"database/sql"
	"net/http"
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
	ctx := context.Background()
	if err := st.CreateActor(ctx, id, "human", id, false); err != nil {
		t.Fatalf("create actor %s: %v", id, err)
	}
	token, err := st.CreateToken(ctx, id, "test token", nil)
	if err != nil {
		t.Fatalf("create token for %s: %v", id, err)
	}
	return token
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

// TestCreateDocRejectsStatus: status is import-only. The field is declared so
// the refusal can name it rather than silently dropping it.
func TestCreateDocRejectsStatus(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/docs", token, map[string]any{
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
	spec := acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	rr := doReq(t, h, "GET", "/docs", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		"Documents in the backbone",
		"025-part-2",
		docPageURL(spec.ID),
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

	rr := doReq(t, h, "GET", docPageURL(spec.ID), "", nil)
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
		docPageURL(plan.ID), // linked to the other end
		"Model body.",       // the body, rendered verbatim in a <pre>
	)

	if rr := doReq(t, h, "GET", "/docs/4711", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/docs/025-x", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("non-numeric id status = %d, want 404", rr.Code)
	}
}

// docPageURL is the cockpit page path for a document id.
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
	if gap.Doc != spec.ID || gap.Sections != 2 || len(gap.Unplanned) != 1 || gap.Unplanned[0] != "sec-2" {
		t.Fatalf("gap = %+v, want doc %d, 2 sections, [sec-2] unplanned", gap, spec.ID)
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
		"both selectors":           {"needs_planning=true&needs_execution=true", "disjoint"},
		"planning with draft":      {"needs_planning=true&status=draft", "accepted"},
		"planning with plan kind":  {"needs_planning=true&kind=plan", "spec"},
		"execution with draft":     {"needs_execution=true&status=draft", "accepted"},
		"execution with spec kind": {"needs_execution=true&kind=spec", "plan"},
		"unparseable selector":     {"needs_planning=maybe", "needs_planning"},
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
