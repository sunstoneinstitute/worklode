package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docTestBody is a minimal well-formed document: an H1 title and no
// frontmatter, sufficient for parseDocBody (internal/store/docs.go) on any
// kind — a plan needs no sections, and a spec/ADR with no numbered headings
// has nothing for the anchor lint to reject.
const docTestBody = "# Test Document\n\nSome body text.\n"

// docPlanMintBody is a well-formed plan in the mintable ## Tasks format
// (025 §9.1): two definitions, no blockers, for the CLI's plan-accept and
// `task list --plan` tests.
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

// writeDocFile writes content to a temp file and returns its path, for
// commands that read a document body via --file.
func writeDocFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write doc file: %v", err)
	}
	return path
}

// --- flag validation (no server needed) --------------------------------

func TestDocNewUnknownKind(t *testing.T) {
	file := writeDocFile(t, docTestBody)
	cmd := newDocNewCmd()
	cmd.SetArgs([]string{"--kind", "bogus", "--slug", "s", "--file", file, "--project", "proj"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("err = %v; want it to say unknown kind", err)
	}
}

func TestDocNewMissingFile(t *testing.T) {
	cmd := newDocNewCmd()
	cmd.SetArgs([]string{"--kind", "spec", "--slug", "s", "--project", "proj"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"file"`) {
		t.Fatalf("err = %v; want cobra's required-flag error naming \"file\"", err)
	}
}

func TestDocNewMissingSlug(t *testing.T) {
	file := writeDocFile(t, docTestBody)
	cmd := newDocNewCmd()
	cmd.SetArgs([]string{"--kind", "spec", "--file", file, "--project", "proj"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"slug"`) {
		t.Fatalf("err = %v; want cobra's required-flag error naming \"slug\"", err)
	}
}

func TestDocEditMissingFile(t *testing.T) {
	cmd := newDocEditCmd()
	cmd.SetArgs([]string{"5"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"file"`) {
		t.Fatalf("err = %v; want cobra's required-flag error naming \"file\"", err)
	}
}

func TestDocReviseFileAndAcceptMutuallyExclusive(t *testing.T) {
	cmd := newDocReviseCmd()
	cmd.SetArgs([]string{"5", "--file", "whatever.md", "--accept"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("err = %v; want cobra mutual-exclusion error", err)
	}
}

// --- doc ref resolution (025 §14.3; needs a real server) -----------------

func TestResolveDocIDNumeric(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	d, _, err := c.CreateDoc(context.Background(), model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 1, Slug: "my-spec", Body: docTestBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	id, err := resolveDocID(context.Background(), c, strconv.FormatInt(d.ID, 10))
	if err != nil {
		t.Fatalf("resolveDocID numeric: %v", err)
	}
	if id != d.ID {
		t.Fatalf("resolveDocID numeric = %d, want %d", id, d.ID)
	}
}

func TestResolveDocIDBySlug(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	d, _, err := c.CreateDoc(context.Background(), model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 1, Slug: "my-spec", Body: docTestBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	id, err := resolveDocID(context.Background(), c, "my-spec")
	if err != nil {
		t.Fatalf("resolveDocID by slug: %v", err)
	}
	if id != d.ID {
		t.Fatalf("resolveDocID by slug = %d, want %d", id, d.ID)
	}
}

func TestResolveDocIDUnmatchedRef(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	if _, err := resolveDocID(context.Background(), c, "no-such-slug"); err == nil ||
		!strings.Contains(err.Error(), "no-such-slug") {
		t.Fatalf("resolveDocID unmatched ref: err = %v, want it to name the ref", err)
	}
}

// TestResolveDocIDAmbiguousSlug: the same slug in two projects (slugs are
// unique per project, not globally) is refused rather than picking one.
func TestResolveDocIDAmbiguousSlug(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	if _, _, err := c.CreateProject(context.Background(),
		model.CreateProjectInput{ID: "proj2", Name: "Proj2", Key: "PROJ2"}); err != nil {
		t.Fatalf("create project 2: %v", err)
	}
	if _, _, err := c.CreateDoc(context.Background(), model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 1, Slug: "dup", Body: docTestBody,
	}); err != nil {
		t.Fatalf("create doc 1: %v", err)
	}
	if _, _, err := c.CreateDoc(context.Background(), model.CreateDocInput{
		Project: "proj2", Kind: "spec", Number: 1, Slug: "dup", Body: docTestBody,
	}); err != nil {
		t.Fatalf("create doc 2: %v", err)
	}
	if _, err := resolveDocID(context.Background(), c, "dup"); err == nil || !strings.Contains(err.Error(), "dup") {
		t.Fatalf("resolveDocID ambiguous slug: err = %v, want it to name the ref", err)
	}
}

// TestDocAcceptBySlugPrintsMintedTasks: `lode doc accept <slug>` resolves the
// ref and, for a plan, reports the minted task ids (025 §9.2).
func TestDocAcceptBySlugPrintsMintedTasks(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	planFile := writeDocFile(t, docPlanMintBody)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "plan",
		"--slug", "mint-plan", "--file", planFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}
	out, err := runLode(t, "doc", "accept", "mint-plan")
	if err != nil {
		t.Fatalf("doc accept mint-plan: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "accepted doc") || !strings.Contains(out, "minted tasks:") {
		t.Fatalf("doc accept output = %q, want it to report the minted tasks", out)
	}
}

// --- happy paths, against a real store + server -------------------------

// docJSON decodes a `--json` command's stdout into a model.Doc.
func docJSON(t *testing.T, out string) model.Doc {
	t.Helper()
	var d model.Doc
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("decode doc %q: %v", out, err)
	}
	return d
}

func TestDocLifecycle(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	specFile := writeDocFile(t, docTestBody)

	// new
	out, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--number", "1", "--slug", "test-spec", "--file", specFile, "--json")
	if err != nil {
		t.Fatalf("doc new: %v\noutput: %s", err, out)
	}
	created := docJSON(t, out)
	if created.ID == 0 {
		t.Fatalf("doc new: id = 0, want a generated id")
	}
	if created.Title != "Test Document" {
		t.Errorf("doc new: title = %q, want the body's H1", created.Title)
	}
	if created.Status != "draft" {
		t.Errorf("doc new: status = %q, want draft", created.Status)
	}
	id := created.ID
	idArg := strconv.FormatInt(id, 10)

	// new: non-json prints a table with the id and title.
	out, err = runLode(t, "doc", "new", "--project", "proj", "--kind", "adr",
		"--number", "1", "--slug", "test-adr", "--file", specFile)
	if err != nil {
		t.Fatalf("doc new (table): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Test Document") || !strings.Contains(out, "adr") {
		t.Errorf("doc new table output = %q, want it to mention the title and kind", out)
	}

	// list
	out, err = runLode(t, "doc", "list", "--project", "proj", "--json")
	if err != nil {
		t.Fatalf("doc list: %v\noutput: %s", err, out)
	}
	var listed struct {
		Docs []model.Doc `json:"docs"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode doc list %q: %v", out, err)
	}
	if len(listed.Docs) != 2 {
		t.Fatalf("doc list: got %d docs, want 2", len(listed.Docs))
	}

	// get
	out, err = runLode(t, "doc", "get", idArg, "--json")
	if err != nil {
		t.Fatalf("doc get: %v\noutput: %s", err, out)
	}
	var detail model.DocDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("decode doc detail %q: %v", out, err)
	}
	if !strings.Contains(detail.Body, "Some body text.") {
		t.Errorf("doc get: body = %q, want it to contain the source body", detail.Body)
	}

	// get: non-json renders the body.
	out, err = runLode(t, "doc", "get", idArg)
	if err != nil {
		t.Fatalf("doc get (rendered): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Some body text.") {
		t.Errorf("doc get rendered output = %q, want it to contain the body", out)
	}

	// edit
	editedBody := "# Test Document\n\nEdited body text.\n"
	editFile := writeDocFile(t, editedBody)
	out, err = runLode(t, "doc", "edit", idArg, "--file", editFile, "--json")
	if err != nil {
		t.Fatalf("doc edit: %v\noutput: %s", err, out)
	}
	edited := docJSON(t, out)
	if edited.Body != editedBody {
		t.Errorf("doc edit: body = %q, want %q", edited.Body, editedBody)
	}

	// accept
	out, err = runLode(t, "doc", "accept", idArg, "--json")
	if err != nil {
		t.Fatalf("doc accept: %v\noutput: %s", err, out)
	}
	accepted := docJSON(t, out)
	if accepted.Status != "accepted" {
		t.Errorf("doc accept: status = %q, want accepted", accepted.Status)
	}

	// accept: non-json reports the new status.
	// (accept is a one-shot transition; re-derive a fresh accepted doc for
	// the table-output check instead of accepting this one twice.)
	adrOut, err := runLode(t, "doc", "list", "--project", "proj", "--kind", "adr", "--json")
	if err != nil {
		t.Fatalf("doc list --kind adr: %v\noutput: %s", err, adrOut)
	}
	var adrListed struct {
		Docs []model.Doc `json:"docs"`
	}
	if err := json.Unmarshal([]byte(adrOut), &adrListed); err != nil {
		t.Fatalf("decode doc list %q: %v", adrOut, err)
	}
	if len(adrListed.Docs) != 1 {
		t.Fatalf("doc list --kind adr: got %d docs, want 1", len(adrListed.Docs))
	}
	adrID := strconv.FormatInt(adrListed.Docs[0].ID, 10)
	out, err = runLode(t, "doc", "accept", adrID)
	if err != nil {
		t.Fatalf("doc accept (table): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "accepted") || !strings.Contains(out, "status accepted") {
		t.Errorf("doc accept output = %q, want it to report the new status", out)
	}

	// revise: open
	out, err = runLode(t, "doc", "revise", idArg, "--json")
	if err != nil {
		t.Fatalf("doc revise (open): %v\noutput: %s", err, out)
	}
	var rev model.DocRevision
	if err := json.Unmarshal([]byte(out), &rev); err != nil {
		t.Fatalf("decode revision %q: %v", out, err)
	}
	if rev.Doc != id {
		t.Errorf("doc revise (open): doc = %d, want %d", rev.Doc, id)
	}

	// revise: update the candidate's body
	revisedBody := "# Test Document\n\nRevised candidate body.\n"
	revFile := writeDocFile(t, revisedBody)
	out, err = runLode(t, "doc", "revise", idArg, "--file", revFile, "--json")
	if err != nil {
		t.Fatalf("doc revise --file: %v\noutput: %s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &rev); err != nil {
		t.Fatalf("decode revision %q: %v", out, err)
	}
	if rev.Body != revisedBody {
		t.Errorf("doc revise --file: body = %q, want %q", rev.Body, revisedBody)
	}

	// revise: land the candidate
	out, err = runLode(t, "doc", "revise", idArg, "--accept", "--json")
	if err != nil {
		t.Fatalf("doc revise --accept: %v\noutput: %s", err, out)
	}
	landed := docJSON(t, out)
	if landed.Version != 2 {
		t.Errorf("doc revise --accept: version = %d, want 2", landed.Version)
	}
	if landed.Body != revisedBody {
		t.Errorf("doc revise --accept: body = %q, want %q", landed.Body, revisedBody)
	}
}
