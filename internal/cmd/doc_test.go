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
	"github.com/sunstoneinstitute/worklode/internal/worktree"
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

// TestDocSubmitBySlug: `lode doc submit <slug>` resolves the ref, reports the
// document it submitted, and leaves the document's status alone — submission
// is an event, not a status (025 §15.4).
func TestDocSubmitBySlug(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--number", "1", "--slug", "my-spec", "--file", specFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}

	out, err := runLode(t, "doc", "submit", "my-spec")
	if err != nil {
		t.Fatalf("doc submit my-spec: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "submitted doc") {
		t.Fatalf("doc submit output = %q, want it to report the submission", out)
	}

	out, err = runLode(t, "doc", "submit", "my-spec", "--json")
	if err != nil {
		t.Fatalf("doc submit --json: %v\noutput: %s", err, out)
	}
	if got := docJSON(t, out); got.Status != "draft" {
		t.Errorf("doc submit: status = %q, want draft (submission moves no column)", got.Status)
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

// TestDocFileFlagRejectsEmptyPath pins the --file flag against an empty value.
// MarkFlagRequired only checks that the flag was set, so `--file ""` reaches
// readBodyFile; resolving it to an empty body would create a doc with no
// content, and on `doc edit` would overwrite one that had some.
func TestDocFileFlagRejectsEmptyPath(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "adr",
		"--number", "902", "--slug", "empty-file-adr", "--file", ""); err == nil {
		t.Fatal(`doc new --file "": want error, got nil`)
	}
	out, err := runLode(t, "doc", "list", "--project", "proj", "--json")
	if err != nil {
		t.Fatalf("doc list: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "empty-file-adr") {
		t.Fatalf(`doc new --file "" created a document anyway: %s`, out)
	}

	// Same guard on the edit side: the body must survive a rejected --file.
	specFile := writeDocFile(t, docTestBody)
	out, err = runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--number", "902", "--slug", "keeps-its-body", "--file", specFile, "--json")
	if err != nil {
		t.Fatalf("doc new: %v\noutput: %s", err, out)
	}
	idArg := strconv.FormatInt(docJSON(t, out).ID, 10)

	if _, err := runLode(t, "doc", "edit", idArg, "--file", ""); err == nil {
		t.Fatal(`doc edit --file "": want error, got nil`)
	}
	out, err = runLode(t, "doc", "get", idArg, "--json")
	if err != nil {
		t.Fatalf("doc get: %v\noutput: %s", err, out)
	}
	if got := docJSON(t, out).Body; got != docTestBody {
		t.Fatalf(`body after rejected --file "" = %q, want it untouched`, got)
	}
}

// TestDocNewAutoAssignsNumber: omitting --number for a spec/ADR gets the next
// free number for its (project, kind) rather than refusing (025 §14.3).
func TestDocNewAutoAssignsNumber(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	specFile := writeDocFile(t, docTestBody)

	out, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--slug", "auto-numbered", "--file", specFile, "--json")
	if err != nil {
		t.Fatalf("doc new (no --number): %v\noutput: %s", err, out)
	}
	if got := docJSON(t, out); got.Number != 1 {
		t.Errorf("doc new (no --number): number = %d, want 1 (auto-assigned)", got.Number)
	}
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
	// The kind is in the ref now, not a column of its own.
	if !strings.Contains(out, "Test Document") || !strings.Contains(out, "PROJ-ADR-1") {
		t.Errorf("doc new table output = %q, want it to mention the title and the ref", out)
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

	// revise: --discard withdraws the candidate and frees the slot, so the
	// open above can be repeated and the accept below still has one to land.
	out, err = runLode(t, "doc", "revise", idArg, "--discard")
	if err != nil {
		t.Fatalf("doc revise --discard: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "discarded the candidate revision") {
		t.Errorf("doc revise --discard output = %q, want it to report the withdrawal", out)
	}
	if out, err := runLode(t, "doc", "revise", idArg, "--discard"); err == nil {
		t.Errorf("doc revise --discard with nothing open: want an error, got %q", out)
	}
	if out, err := runLode(t, "doc", "revise", idArg, "--json"); err != nil {
		t.Fatalf("doc revise (reopen after a discard): %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "doc", "revise", idArg, "--file", revFile, "--json"); err != nil {
		t.Fatalf("doc revise --file (after a discard): %v\noutput: %s", err, out)
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

// --- list selectors (026 §2) --------------------------------------------

// TestDocListSelectorConflicts: each derived selector implies a kind and a
// status, so a contradicting filter is refused locally, before any round trip
// (026 §2.1). The server refuses the same combinations; this just spares the
// request.
func TestDocListSelectorConflicts(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"both selectors":                 {[]string{"--needs-planning", "--needs-execution"}, "none of the others can be"},
		"planning with draft":            {[]string{"--needs-planning", "--status", "draft"}, "accepted"},
		"planning with plan kind":        {[]string{"--needs-planning", "--kind", "plan"}, "spec"},
		"execution with draft":           {[]string{"--needs-execution", "--status", "draft"}, "accepted"},
		"execution with spec kind":       {[]string{"--needs-execution", "--kind", "spec"}, "plan"},
		"bare-superseded with draft":     {[]string{"--bare-superseded", "--status", "draft"}, "superseded"},
		"bare-superseded with plan kind": {[]string{"--bare-superseded", "--kind", "plan"}, "spec or adr"},
		"bare-superseded and planning":   {[]string{"--bare-superseded", "--needs-planning"}, "none of the others can be"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := newDocListCmd()
			cmd.SetArgs(c.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v; want it to mention %q", err, c.want)
			}
		})
	}
}

// TestCheckDocSelectorsAllowsBareSuperseded: --bare-superseded accepts no
// restatement, or one that agrees with what it implies — status=superseded
// and kind spec or adr, unlike --needs-planning/--needs-execution's single
// implied kind.
func TestCheckDocSelectorsAllowsBareSuperseded(t *testing.T) {
	for name, c := range map[string]struct{ kind, status string }{
		"no restatement":    {"", ""},
		"kind spec":         {"spec", ""},
		"kind adr":          {"adr", ""},
		"status superseded": {"", "superseded"},
		"both restated":     {"spec", "superseded"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkDocSelectors(c.kind, c.status, false, false, true); err != nil {
				t.Fatalf("checkDocSelectors(%q, %q, bareSuperseded=true) = %v, want nil", c.kind, c.status, err)
			}
		})
	}
}

// docSpecTwoSections is a spec with two anchored sections, so a plan covering
// one leaves exactly one planning gap.
const docSpecTwoSections = `---
status: draft
---

# A spec

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// docPlanCoveringSec1 is a mintable plan whose covers edge names sec-1 of the
// spec above.
const docPlanCoveringSec1 = `---
status: draft
covers:
  - my-spec#sec-1
---

# Part one

## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: chore
` + "```" + `

Do it.
`

// docOwnerSpec is a minimal spec, the named owner of docPlanCoveringSec1's
// defers entry below.
const docOwnerSpec = `---
status: draft
---

# Owner spec
`

// docPlanCoveringSec1DefersSec2 covers sec-1 and defers sec-2 to owner-spec
// (026 §5.3), for the deferred-gap rendering case in
// TestDocListNeedsPlanningAndExecution.
const docPlanCoveringSec1DefersSec2 = `---
status: draft
covers:
  - my-spec#sec-1
defers:
  - spec: my-spec#sec-2
    to: owner-spec
---

# Part one

## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: chore
` + "```" + `

Do it.
`

// TestDocListNeedsPlanningAndExecution: both selectors reach the server and
// render — the spec with its gap anchors, including a deferred one carrying
// its owner (026 §5.3), and the plan with its open task.
func TestDocListNeedsPlanningAndExecution(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	ownerFile := writeDocFile(t, docOwnerSpec)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--number", "2", "--slug", "owner-spec", "--file", ownerFile); err != nil {
		t.Fatalf("doc new owner spec: %v", err)
	}
	specFile := writeDocFile(t, docSpecTwoSections)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--number", "1", "--slug", "my-spec", "--file", specFile); err != nil {
		t.Fatalf("doc new spec: %v", err)
	}
	if _, err := runLode(t, "doc", "accept", "my-spec"); err != nil {
		t.Fatalf("doc accept spec: %v", err)
	}
	planFile := writeDocFile(t, docPlanCoveringSec1DefersSec2)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "plan",
		"--slug", "part-one", "--file", planFile); err != nil {
		t.Fatalf("doc new plan: %v", err)
	}
	if _, err := runLode(t, "doc", "accept", "part-one"); err != nil {
		t.Fatalf("doc accept plan: %v", err)
	}

	out, err := runLode(t, "doc", "list", "--project", "proj", "--needs-planning")
	if err != nil {
		t.Fatalf("doc list --needs-planning: %v\noutput: %s", err, out)
	}
	// Identified by ref, not slug: the slug is the file name and left to --json.
	if !strings.Contains(out, "PROJ-SPEC-1") || !strings.Contains(out, "sec-2(deferred:owner-spec)") {
		t.Errorf("needs-planning output = %q, want the spec and its deferred anchor with its owner", out)
	}
	if strings.Contains(out, "sec-1") {
		t.Errorf("needs-planning output = %q, want sec-1 omitted: an accepted plan covers it", out)
	}

	out, err = runLode(t, "doc", "list", "--project", "proj", "--needs-planning", "--json")
	if err != nil {
		t.Fatalf("doc list --needs-planning --json: %v\noutput: %s", err, out)
	}
	var resp model.DocListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode doc list %q: %v", out, err)
	}
	if len(resp.Docs) != 1 || len(resp.PlanningGaps) != 1 {
		t.Fatalf("json = %+v, want one doc and one gap", resp)
	}
	if resp.PlanningGaps[0].Sections != 2 || len(resp.PlanningGaps[0].Gaps) != 1 ||
		resp.PlanningGaps[0].Gaps[0] != (model.DocSectionGap{Anchor: "sec-2", Coverage: "deferred", Owner: "owner-spec"}) {
		t.Errorf("gap = %+v, want 2 sections with sec-2 deferred to owner-spec", resp.PlanningGaps[0])
	}

	out, err = runLode(t, "doc", "list", "--project", "proj", "--needs-execution")
	if err != nil {
		t.Fatalf("doc list --needs-execution: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "PROJ-PLAN-1") {
		t.Errorf("needs-execution output = %q, want the accepted plan", out)
	}
}

// --- lode doc anchors (the local pre-accept lint, 025 §18) ---------------

func TestDocAnchors(t *testing.T) {
	cases := map[string]struct {
		body     string
		wantErr  bool
		contains []string
	}{
		"clean spec": {
			body:     docSpecTwoSections,
			contains: []string{"no problems"},
		},
		"duplicate anchor": {
			body:    "# T\n\n## A {#sec-1}\n\nx\n\n## B {#sec-1}\n\ny\n",
			wantErr: true, contains: []string{"#sec-1 is claimed by both"},
		},
		"anchor disagrees with number": {
			body:    "# T\n\n## 1. A {#sec-9}\n\nx\n",
			wantErr: true, contains: []string{"numbered 1 but anchored #sec-9"},
		},
		"section too deep": {
			body: "# T\n\n## 1. A {#sec-1}\n\nx\n\n### 1.1 B {#sec-1.1}\n\ny\n" +
				"\n#### 1.1.1 C {#sec-1.1.1}\n\nz\n\n##### 1.1.1.1 D {#sec-1.1.1.1}\n\nw\n",
			wantErr: true, contains: []string{"sec-1.1.1.1", "depth"},
		},
		"every finding is reported": {
			body:    "# T\n\n## 1. A {#sec-9}\n\nx\n\n## B {#sec-2}\n\ny\n\n## C {#sec-2}\n\nz\n",
			wantErr: true, contains: []string{"numbered 1 but anchored #sec-9", "#sec-2 is claimed by both"},
		},
		"well-formed plan": {
			body:     docPlanCoveringSec1,
			contains: []string{"no problems"},
		},
		"plan with an unparseable task": {
			body: "---\nstatus: draft\ncovers: NO-SPEC\n---\n\n# P\n\n## Tasks\n\n" +
				"### Task 1 — No fence\n\nprose only\n",
			wantErr: true, contains: []string{"task 1", "kind is required"},
		},
		// A spec is not a plan, so its "## Tasks" section is prose: the
		// plan-task check is skipped rather than guessed at.
		"tasks section in a non-plan is not linted as a plan": {
			body:     "# T\n\n## Tasks\n\n### Not a task heading\n\nprose\n",
			contains: []string{"no problems"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file := writeDocFile(t, tc.body)
			cmd := newDocAnchorsCmd()
			out := &strings.Builder{}
			cmd.SetArgs([]string{file})
			cmd.SetOut(out)
			cmd.SetErr(out)
			err := cmd.Execute()
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v\noutput: %s", err, tc.wantErr, out)
			}
			for _, want := range tc.contains {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output = %q, want it to contain %q", out.String(), want)
				}
			}
		})
	}
}

func TestDocAnchorsMissingFile(t *testing.T) {
	cmd := newDocAnchorsCmd()
	cmd.SetArgs([]string{filepath.Join(t.TempDir(), "nope.md")})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("err = nil, want the read to fail")
	}
}

// TestDocNewRecordsWorktreeTask walks the whole chain 025 §12 needs: `lode
// next` binds a worktree to a task, and a `lode doc new` run from inside that
// worktree records the binding on the document. The CLI reads the task the
// same way every other worktree-aware command does, so claiming into a
// worktree is the only setup a document author does.
func TestDocNewRecordsWorktreeTask(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Write the spec")

	root := initGitRepo(t)
	t.Chdir(root)
	if out, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v\noutput: %s", err, out)
	}
	t.Chdir(filepath.Join(root, worktree.DefaultBase, task.ID+"-write-the-spec"))

	file := writeDocFile(t, docTestBody)
	out, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--number", "1", "--slug", "test-spec", "--file", file, "--json")
	if err != nil {
		t.Fatalf("doc new: %v\noutput: %s", err, out)
	}
	if got := docJSON(t, out).GeneratedByTask; got != task.ID {
		t.Errorf("generated_by_task = %q, want %q: a document written under a "+
			"leased worktree records the task that wrote it", got, task.ID)
	}

	// The same command outside any worktree records no task and still creates
	// the document — an ad hoc author is not refused (migration 0044).
	t.Chdir(t.TempDir())
	out, err = runLode(t, "doc", "new", "--project", "proj", "--kind", "adr",
		"--number", "1", "--slug", "test-adr", "--file", file, "--json")
	if err != nil {
		t.Fatalf("doc new outside a worktree: %v\noutput: %s", err, out)
	}
	if got := docJSON(t, out).GeneratedByTask; got != "" {
		t.Errorf("generated_by_task = %q, want empty outside a bound worktree", got)
	}
}
