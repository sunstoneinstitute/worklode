# Clause Model, Increment 1b Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A clause can be read in every form a reader needs (current text, one version, its history, the tasks it governs, a cockpit page, a canonical URL, a link wherever its ref appears in prose) and written directly, with the arranging document's body regenerated from the edit so the document stays a faithful reassembly of its clauses.

**Architecture:** Clause-first editing is composition over what increment 1 built: the store parses the document's editable body, replaces the one section the clause is arranged at, re-renders the body with `designdoc.Document.Bytes`, and hands it to the existing document write path (`UpdateDocBody` for a draft, `UpdateRevision` for an accepted document's candidate). `syncClauses` then does what it already does, so S35 holds without a second write path. Reading gains three store queries (versions, one version, governed tasks), two API routes, a `lode clause` command group, a `--version` flag on `lode show`, a templ page, and one arm in the autolinker. The canonical URL scheme from S20 ships for clauses and documents; task URLs and root-route redirects are deferred.

**Tech Stack:** Go, Postgres via `database/sql`, cobra CLI, templ for the cockpit, goldmark autolinker in `internal/mdrender`, the existing `internal/designdoc` parser.

**Spec:** `docs/specs2/12-spec-refactoring-design-tree.md` (S13, S14, S20, S35, A3) and GitHub issue #687's "Increment 1b" section. Executors read both.

**Stacked on:** branch `spec-refactor` (PR #686). This branch is `clause-increment-1b`; its PR targets `spec-refactor` until #686 lands.

## Rulings made while planning

The user was not available for brainstorming, so these are decided here and the plan argues from them. Each names what it costs if wrong.

- **R1 The edit verb is `PUT /api/v1/clauses/{id}` with `{heading, body}`, both required.** PUT because the call replaces the clause's whole text, the same shape as `PUT /api/v1/docs/{id}/body`. PATCH would invite partial writes that then need merge rules. Costs a second route later if a heading-only write turns out common.
- **R2 A clause edit is a document write in disguise.** The store finds the one document arranging the clause, parses that document's editable body, sets the section's `Title` and `Body`, re-renders with `Document.Bytes`, and calls the document write path. For a draft document that is `UpdateDocBody`. For an accepted document it is the candidate revision: `ReviseDoc` opens one when none is open, then `UpdateRevision` carries the composed body, and the clause's next version appears when the revision lands (S13: the frozen list is what review accepts; S35: the accepted version is locked until then). No new clause write path, no second source of truth. Costs nothing S35 promised; it means a clause edit on an accepted document is visible on the clause itself only after `lode doc revise --accept`.
- **R3 A clause arranged in more than one document refuses the edit.** Today the store never produces that (one arrangement per document body, each section its own clause); the shared-clause case arrives with plans as arrangements in increment 3. The refusal is `ErrInvalidInput` naming the documents. A clause arranged in no document also refuses: editing is document-first in this stage (S14). Costs one refusal message that increment 3 replaces with a rule.
- **R4 Governed tasks ride on the clause detail.** `model.Clause` gains `GovernedTasks []ClauseTask`, filled by `GetClause`, instead of a fourth route. Costs one extra query per clause read.
- **R5 The URL scheme ships for clauses and documents only.** `GET /projects/{proj}/clause/{n}` and `/{n}/{ver}` are the canonical clause page. `GET /projects/{proj}/{kind}/{n}` for `spec`, `adr`, `plan` redirects to the existing `/docs/<KEY>-<KIND>-<n>` page, which stays canonical for documents in this increment. An uppercase `{proj}` is a project key typed by habit and redirects to the lowercase id form. Task kinds, `/<ver>` on documents, and turning `/tasks/WL-456` and `/docs/WL-SPEC-4` into redirects are deferred and noted in 07 §10.2 (A3). `GET /clauses/{ref}` is a resolving redirect to the canonical clause page, the counterpart of `/docs/ref/{ref...}`, and is what the autolinker targets. Costs a second pass on the redirect layer later; nothing shipped here has to change for it.
- **R6 The CLI group is `lode clause` with `edit` and the `versions` view.** `edit` is L3's body-replacing verb; `versions` is an L6 noun view already used by `doc`. `lode show WL-CL-<n> --version N` reads one version, mirroring `lode show WL-SPEC-4 --version N` if that flag exists on show for documents, and otherwise introducing it for clauses only. Reading one clause stays `lode show`, so there is no `lode clause show`. Costs nothing.
- **R7 `parseClauseRef` moves into `internal/designdoc` as `ParseClauseRef`.** This retires the increment 1 deferred minor (the CL grammar written twice). `internal/api` and `internal/cmd/show.go`'s classifier both call it.

Deferred to later increments, stated so no reviewer reports them missing: the BlockNote editor saving to `PUT /api/v1/clauses/{id}` (the read-only spike behind `?editor=1` on the document page stays as it is), typed edges (S12, S26), plans as arrangements (S16, S25), the reconciler and gate trailer (S4, S18, S29, S30), split and merge lineage (S22), the migration of the old specs (A2), owner and tags (S15), version pinning on links (S10).

## Global Constraints

- Build and test with `-trimpath` only: `make build`, `make test`, or `go test -trimpath ./internal/<pkg> -run <Test>`. Never bare `go test`.
- Store and API tests need Postgres with pgvector at `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (override `TEST_POSTGRES_DSN`). They skip silently without it, so confirm the suite actually ran (`-v`, look for `--- PASS`, no `SKIP`).
- Every shape that crosses HTTP is declared once in `internal/model` with wire field names (ADR 036). `internal/model/rule_test.go` and `deps_test.go` enforce it.
- Every route appears in `internal/api/router.go`'s `routeGuards`; `NewServer` refuses to boot otherwise. Web routes use `guarded(permWebRead)`; API reads `guardedAny(permDocRead)`; API writes `guardedAny(permDocWrite)`.
- `internal/cmd` decides, `internal/cli` renders: a human view is a `cli.*Render` or `cli.*Table` function taking an `io.Writer`; no `tabwriter` or timestamp formatting in `internal/cmd`. `internal/cli` never imports cobra.
- New CLI names satisfy `internal/cmd/namerule_test.go`: an entity noun goes in its L1 list, a noun view in its L6 table, a verb in `l3DomainActions` unless it is one of L3's canonical verbs (`add`, `show`, `list`, `edit`, `set`, `remove`, `delete`). Spec 09 §1 mirrors the tables. Read that test before naming anything.
- `internal/ui` depends on nothing beyond stdlib, `internal/model` and the templ runtime; `internal/api` imports `internal/ui`, never the reverse. After editing a `.templ` file run `templ generate` from the repo root and commit the generated `_templ.go`.
- Feature-stem file naming: `model/clause.go`, `store/clauses.go`, `api/clauses.go`, `cli/clauses.go`, `cmd/clause.go`, `ui/clause.templ`. No file over 2000 lines (`filerule_test.go`).
- No migration in this increment. Highest on the branch is `0082_clauses`.
- Prose in `docs/specs2/` follows the `lode:anti-smartass` plain-language style: no em dashes, no "X, not Y" antithesis.
- Commit messages: imperative subject, no Co-authored-by or any self-reference.
- Refs follow the `<KEY>-<TYPE>-<n>` grammar of 025 §14.3; the clause type token is `CL`.

---

### Task 1: Clause ref grammar and section replacement in designdoc (R7)

**Files:**
- Modify: `internal/designdoc/resolve.go` (append `ClauseRef`, `ParseClauseRef`)
- Modify: `internal/designdoc/designdoc.go` (append `(*Document).SectionByAnchor`)
- Modify: `internal/designdoc/designdoc_test.go` (append two tests)
- Modify: `internal/api/clauses.go` (delete `clauseRef`, `parseClauseRef`; call `designdoc.ParseClauseRef`)
- Modify: `internal/api/governedby.go`, `internal/api/tasks.go` (every `parseClauseRef(` call site)

**Interfaces:**
- Produces: `designdoc.ClauseRef{Key string; Number int64}`, `designdoc.ParseClauseRef(base string) (ClauseRef, bool)`, `(*Document).SectionByAnchor(anchor string) *Section`.
- Consumes: `Document.Bytes()` (exists; re-renders a heading whose exported fields were assigned).

- [ ] **Step 1: Write the failing tests**

Append to `internal/designdoc/designdoc_test.go`:

```go
func TestParseClauseRef(t *testing.T) {
	cases := []struct {
		in   string
		key  string
		n    int64
		ok   bool
	}{
		{"WL-CL-12", "WL", 12, true},
		{"P1-CL-3", "P1", 3, true},
		{"WL-SPEC-12", "", 0, false},
		{"wl-cl-12", "", 0, false},
		{"WL-CL-", "", 0, false},
		{"WL-CL-12#sec-1", "", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseClauseRef(c.in)
		if ok != c.ok || got.Key != c.key || got.Number != c.n {
			t.Errorf("ParseClauseRef(%q) = %+v, %v; want {%s %d}, %v", c.in, got, ok, c.key, c.n, c.ok)
		}
	}
}

func TestSectionByAnchorEditRerendersHeading(t *testing.T) {
	src := []byte("---\nstatus: draft\n---\n\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n")
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if d.SectionByAnchor("sec-9") != nil {
		t.Error("unknown anchor should return nil")
	}
	sec := d.SectionByAnchor("sec-1.1")
	if sec == nil || sec.Title != "Sub" {
		t.Fatalf("sec-1.1 = %+v", sec)
	}
	sec.Title = "Subsection"
	sec.Body = "\nB changed.\n\n"
	want := "---\nstatus: draft\n---\n\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Subsection {#sec-1.1}\n\nB changed.\n\n## 2. Two {#sec-2}\n\nC.\n"
	if got := string(d.Bytes()); got != want {
		t.Errorf("Bytes after edit:\n%s\nwant:\n%s", got, want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -trimpath ./internal/designdoc -run 'TestParseClauseRef|TestSectionByAnchor' -v`
Expected: build failure, `undefined: ParseClauseRef` and `d.SectionByAnchor undefined`.

- [ ] **Step 3: Implement**

Append to `internal/designdoc/resolve.go`:

```go
// clauseRefPattern is the CL arm of 025 §14.3's <KEY>-<TYPE>-<n> grammar
// (12-spec-refactoring-design-tree.md S20): a design clause's citable ref.
var clauseRefPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-CL-(\d+)$`)

// ClauseRef is a parsed clause ref, e.g. "WL-CL-12".
type ClauseRef struct {
	Key    string
	Number int64
}

// ParseClauseRef parses base as a clause ref. It reports false for every
// other ref form, including document shorthand and a ref carrying a fragment.
func ParseClauseRef(base string) (ClauseRef, bool) {
	m := clauseRefPattern.FindStringSubmatch(base)
	if m == nil {
		return ClauseRef{}, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return ClauseRef{}, false
	}
	return ClauseRef{Key: m[1], Number: n}, true
}
```

Append to `internal/designdoc/designdoc.go`, after `Bytes`:

```go
// SectionByAnchor returns the section carrying anchor, or nil. The pointer
// aliases Document.Sections, so assigning its Title or Body and calling
// Bytes re-renders the document with that one section changed.
func (d *Document) SectionByAnchor(anchor string) *Section {
	if anchor == "" {
		return nil
	}
	for i := range d.Sections {
		if d.Sections[i].Anchor == anchor {
			return &d.Sections[i]
		}
	}
	return nil
}
```

If `Document.Sections` is `[]*Section` rather than `[]Section`, return `d.Sections[i]` directly; check the field's declared type before writing this.

In `internal/api/clauses.go` delete `clauseRef` and `parseClauseRef` and change every caller in `internal/api` to:

```go
ref, ok := designdoc.ParseClauseRef(r.PathValue("id"))
// ... ref.Key, ref.Number
```

`grep -n "parseClauseRef(" internal/api/*.go` lists the call sites (clauses.go, governedby.go, tasks.go). Keep the `400 "clause id must look like WL-CL-12"` messages as they are.

- [ ] **Step 4: Run the tests**

Run: `go test -trimpath ./internal/designdoc -run 'TestParseClauseRef|TestSectionByAnchor|TestClausesReassemble' -v && go build -trimpath ./... && go test -trimpath ./internal/api -run 'TestGetClause|TestGovernedBy|TestCreateTaskBadGovernedBy' -v`
Expected: PASS everywhere; the api tests confirm the call sites still parse refs.

- [ ] **Step 5: Commit**

```bash
git add internal/designdoc internal/api/clauses.go internal/api/governedby.go internal/api/tasks.go
git commit -m "Parse a clause ref in designdoc and find a section by anchor"
```

---

### Task 2: Store reads and the clause edit (R2, R3, R4)

**Files:**
- Modify: `internal/model/clause.go` (append `ClauseVersion`, `ClauseTask`, `EditClauseInput`; add `GovernedTasks` to `Clause`)
- Modify: `internal/store/clauses.go` (append `ListClauseVersions`, `GetClauseVersion`, `EditClause`; extend `GetClause`)
- Modify: `internal/store/clauses_test.go` (append tests)

**Interfaces:**
- Produces: `(*Store).ListClauseVersions(ctx, key string, number int64) ([]model.ClauseVersion, error)`; `(*Store).GetClauseVersion(ctx, key string, number int64, version int) (*model.Clause, error)`; `EditClause(tx *sql.Tx, now time.Time, key string, number int64, in model.EditClauseInput, actorID string, eventID int64) (docID int64, err error)`; `model.Clause.GovernedTasks`.
- Consumes: `UpdateDocBody(tx, now, id, body, ifVersion, eventID)`, `ReviseDoc(tx, now, id, actorID, eventID)`, `UpdateRevision(tx, now, id, body, eventID)`, `ErrRevisionExists`, `ClauseIDByRef`, `designdoc.Parse`, `(*Document).SectionByAnchor`, `(*Document).Bytes`.

- [ ] **Step 1: Model shapes**

In `internal/model/clause.go` add the field and types:

```go
	// GovernedTasks are the tasks this clause governs (S2), newest link first.
	GovernedTasks []ClauseTask `json:"governed_tasks"`
```

(inside `Clause`, after `ArrangedIn`), and append:

```go
// ClauseVersion is one entry of a clause's version history.
type ClauseVersion struct {
	Version   int       `json:"version"`
	Heading   string    `json:"heading"`
	CreatedAt time.Time `json:"created_at"`
}

// ClauseTask is one task a clause governs, as the clause detail lists it.
type ClauseTask struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	State         string `json:"state"`
	Source        string `json:"source"`         // plan | manual
	ClauseVersion int    `json:"clause_version"` // version current when the link was made
}

// EditClauseInput is the body of PUT /api/v1/clauses/{id}: the clause's new
// heading and body. Both replace what is stored (S35: a draft version is
// rewritten in place, an accepted version becomes the next version when the
// arranging document's revision lands).
type EditClauseInput struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}
```

Check `tasks` column names before writing the query in Step 3: `grep -n "title\|state" internal/store/tasks.go | head` and the `tasks` CREATE TABLE in the earliest migration. If the state column is `status`, use that and keep the wire name `state` only if `model.Task` calls it `State`; match whatever `model.Task` uses.

- [ ] **Step 2: Write the failing store tests**

Append to `internal/store/clauses_test.go`:

```go
// TestClauseVersionsAndGovernedTasks: the history lists every version newest
// first, one version reads with its own text, and the clause detail carries
// the tasks governed by it.
func TestClauseVersionsAndGovernedTasks(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := updateRevision(t, s, d.ID, strings.Replace(clauseDocV1, "C.\n", "C changed.\n", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	vs, err := s.ListClauseVersions(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 || vs[0].Heading != "Two" {
		t.Errorf("versions = %+v", vs)
	}
	v1, err := s.GetClauseVersion(context.Background(), "P1", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 || strings.Contains(v1.Body, "changed") || v1.Ref != "P1-CL-3" {
		t.Errorf("v1 = %+v", v1)
	}
	if _, err := s.GetClauseVersion(context.Background(), "P1", 3, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing version: %v, want ErrNotFound", err)
	}

	task := createTask(t, s, s.Now(), CreateTaskInput{Project: "p1", Title: "Governed", Kind: "feature", Priority: "medium", ExternalID: nextExt(t)})
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		id, err := ClauseIDByRef(tx, "P1", 3)
		if err != nil {
			return err
		}
		return Govern(tx, task.ID, id, "manual")
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetClause(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.GovernedTasks) != 1 || c.GovernedTasks[0].ID != task.ID || c.GovernedTasks[0].Source != "manual" || c.GovernedTasks[0].ClauseVersion != 2 {
		t.Errorf("governed tasks = %+v", c.GovernedTasks)
	}
}

// TestEditClauseDraftRewritesInPlace: editing a clause of a draft document
// regenerates the document body with only that section changed and rewrites
// the clause's draft version in place (S14, S35).
func TestEditClauseDraftRewritesInPlace(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	docID, err := editClause(t, s, "P1", 2, model.EditClauseInput{Heading: "Subsection", Body: "\nB changed.\n\n"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if docID != d.ID {
		t.Errorf("docID = %d, want %d", docID, d.ID)
	}
	got, err := s.GetDoc(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(clauseDocV1, "### 1.1 Sub {#sec-1.1}\n\nB.\n", "### 1.1 Subsection {#sec-1.1}\n\nB changed.\n", 1)
	if got.Body != want {
		t.Errorf("body after clause edit:\n%s\nwant:\n%s", got.Body, want)
	}
	c, err := s.GetClause(context.Background(), "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Heading != "Subsection" || c.Body != "\nB changed.\n\n" || c.Status != "draft" {
		t.Errorf("clause after edit = %+v", c)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
	})
}

// TestEditClauseAcceptedGoesThroughRevision: editing a clause of an accepted
// document opens (or updates) the candidate revision with the regenerated
// body; the clause itself does not move until the revision lands (R2, S13,
// S35).
func TestEditClauseAcceptedGoesThroughRevision(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := editClause(t, s, "P1", 3, model.EditClauseInput{Heading: "Two", Body: "\nC changed.\n\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	rev, err := s.GetDocRevision(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rev.Body, "C changed.") || strings.Contains(rev.Body, "\nC.\n") {
		t.Errorf("revision body:\n%s", rev.Body)
	}
	c, err := s.GetClause(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Body != "\nC.\n\n" || c.Status != "accepted" {
		t.Errorf("clause must not move before the revision lands: %+v", c)
	}
	// A second edit updates the same open revision rather than failing on
	// ErrRevisionExists.
	if _, err := editClause(t, s, "P1", 1, model.EditClauseInput{Heading: "One", Body: "\nA changed.\n\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	rev, _ = s.GetDocRevision(context.Background(), d.ID)
	if !strings.Contains(rev.Body, "A changed.") || !strings.Contains(rev.Body, "C changed.") {
		t.Errorf("second edit lost the first: %s", rev.Body)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetClause(context.Background(), "P1", 3)
	if c.Version != 2 || c.Body != "\nC changed.\n\n" || c.Status != "accepted" {
		t.Errorf("after landing: %+v", c)
	}
}

// TestEditClauseRefusals: an unknown clause is ErrNotFound; a clause arranged
// in no document is ErrInvalidInput (R3).
func TestEditClauseRefusals(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, err := editClause(t, s, "P1", 99, model.EditClauseInput{Heading: "x", Body: "y"}, "stig"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown clause: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_clauses WHERE doc_id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := editClause(t, s, "P1", 1, model.EditClauseInput{Heading: "x", Body: "y"}, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unarranged clause: %v, want ErrInvalidInput", err)
	}
}

// editClause runs EditClause through RecordDocEvent against the arranging
// document, the way the API handler does.
func editClause(t *testing.T, s *Store, key string, number int64, in model.EditClauseInput, actor string) (int64, error) {
	t.Helper()
	var docID int64
	_, _, err := s.RecordDocEvent(t.Context(), "clause", "cli",
		fmt.Sprintf("clause-edit-%d", docEventSeq.Add(1)), "doc.clause_edited", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			docID, err = EditClause(tx, s.Now(), key, number, in, actor, eventID)
			return err
		})
	return docID, err
}
```

Check the exact names of `createTask`, `CreateTaskInput` fields, `nextExt`, `s.Tx`, `s.GetDoc`, `RecordDocEvent`'s parameter order and `docEventSeq` in the existing store tests before running; adjust the helper calls to what exists, keeping the assertions.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -trimpath ./internal/store -run 'TestClauseVersions|TestEditClause' -v`
Expected: build failure on `ListClauseVersions`, `GetClauseVersion`, `EditClause`, `GovernedTasks`.

- [ ] **Step 4: Implement the reads**

In `internal/store/clauses.go`, extend `GetClause` after the arrangements loop (close `rows` first, or move the arrangements read into a helper) with:

```go
	c.GovernedTasks = []model.ClauseTask{}
	trows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.state, g.source, g.clause_version
		   FROM task_governed_by g JOIN tasks t ON t.id = g.task_id
		  WHERE g.clause_id = $1
		  ORDER BY g.created_at DESC, t.id`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read governed tasks of clause %d: %w", c.ID, err)
	}
	defer trows.Close()
	for trows.Next() {
		var gt model.ClauseTask
		if err := trows.Scan(&gt.ID, &gt.Title, &gt.State, &gt.Source, &gt.ClauseVersion); err != nil {
			return nil, fmt.Errorf("scan governed task of clause %d: %w", c.ID, err)
		}
		c.GovernedTasks = append(c.GovernedTasks, gt)
	}
	return c, trows.Err()
```

Append:

```go
// ListClauseVersions is a clause's version history, newest first.
func (s *Store) ListClauseVersions(ctx context.Context, projectKey string, number int64) ([]model.ClauseVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cv.version, cv.heading, cv.created_at
		   FROM clause_versions cv
		   JOIN clauses c ON c.id = cv.clause_id
		   JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2
		  ORDER BY cv.version DESC`, projectKey, number)
	if err != nil {
		return nil, fmt.Errorf("list versions of clause %s-CL-%d: %w", projectKey, number, err)
	}
	defer rows.Close()
	out := []model.ClauseVersion{}
	for rows.Next() {
		var v model.ClauseVersion
		if err := rows.Scan(&v.Version, &v.Heading, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan version of clause %s-CL-%d: %w", projectKey, number, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("clause %s-CL-%d: %w", projectKey, number, ErrNotFound)
	}
	return out, nil
}

// GetClauseVersion reads a clause as it stood at one version: the detail
// with Version, Heading and Body taken from that version's row. ArrangedIn
// and GovernedTasks are the current ones.
func (s *Store) GetClauseVersion(ctx context.Context, projectKey string, number int64, version int) (*model.Clause, error) {
	c, err := s.GetClause(ctx, projectKey, number)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT heading, body FROM clause_versions WHERE clause_id = $1 AND version = $2`,
		c.ID, version).Scan(&c.Heading, &c.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("clause %s v%d: %w", c.Ref, version, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read clause %s v%d: %w", c.Ref, version, err)
	}
	c.Version = version
	return c, nil
}
```

- [ ] **Step 5: Implement the edit**

Append to `internal/store/clauses.go`:

```go
// EditClause writes a clause's new heading and body by regenerating the
// arranging document's body around it (12-spec-refactoring-design-tree.md
// S13, S14, S35). A draft document is written through UpdateDocBody, so
// syncClauses rewrites the clause's draft version in place. An accepted
// document is written through its candidate revision, opened here when none
// is open; the clause's next version appears when the revision lands. A
// clause arranged in no document, or in more than one, is refused: editing
// is document-first in this stage, and a shared clause arrives with plans as
// arrangements. Returns the arranging document's id.
func EditClause(tx *sql.Tx, now time.Time, projectKey string, number int64, in model.EditClauseInput, actorID string, eventID int64) (int64, error) {
	if strings.TrimSpace(in.Heading) == "" {
		return 0, fmt.Errorf("clause heading is required: %w", ErrInvalidInput)
	}
	clauseID, err := ClauseIDByRef(tx, projectKey, number)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT doc_id, anchor FROM doc_clauses WHERE clause_id = $1 ORDER BY doc_id`, clauseID)
	if err != nil {
		return 0, fmt.Errorf("read arrangements of clause %d: %w", clauseID, err)
	}
	var docIDs []int64
	var anchors []string
	for rows.Next() {
		var id int64
		var anchor string
		if err := rows.Scan(&id, &anchor); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan arrangement of clause %d: %w", clauseID, err)
		}
		docIDs, anchors = append(docIDs, id), append(anchors, anchor)
	}
	rows.Close()
	switch len(docIDs) {
	case 0:
		return 0, fmt.Errorf("clause %s-CL-%d is arranged in no document; edit the document instead: %w", projectKey, number, ErrInvalidInput)
	case 1:
	default:
		return 0, fmt.Errorf("clause %s-CL-%d is arranged in %d documents; editing a shared clause is not supported yet: %w", projectKey, number, len(docIDs), ErrInvalidInput)
	}
	docID, anchor := docIDs[0], anchors[0]

	var kind, status, body string
	if err := tx.QueryRow(`SELECT kind, status, body FROM docs WHERE id = $1 FOR UPDATE`, docID).Scan(&kind, &status, &body); err != nil {
		return 0, fmt.Errorf("load doc %d: %w", docID, err)
	}
	editable := body
	revising := kind != "plan" && status != "draft"
	if revising {
		var candidate sql.NullString
		if err := tx.QueryRow(`SELECT body FROM doc_revisions WHERE doc_id = $1`, docID).Scan(&candidate); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("load revision of doc %d: %w", docID, err)
		}
		if candidate.Valid {
			editable = candidate.String
		} else if err := ReviseDoc(tx, now, docID, actorID, eventID); err != nil {
			return 0, err
		}
	}
	parsed, err := designdoc.Parse([]byte(editable))
	if err != nil {
		return 0, fmt.Errorf("parse doc %d: %w", docID, err)
	}
	sec := parsed.SectionByAnchor(anchor)
	if sec == nil {
		return 0, fmt.Errorf("clause %s-CL-%d: its section %s is not in the editable body of doc %d: %w", projectKey, number, anchor, docID, ErrInvalidInput)
	}
	sec.Title = in.Heading
	sec.Body = in.Body
	next := string(parsed.Bytes())
	if revising {
		return docID, UpdateRevision(tx, now, docID, next, eventID)
	}
	_, err = UpdateDocBody(tx, now, docID, next, 0, eventID)
	return docID, err
}
```

Check `doc_revisions`'s column names (`body`, one row per `doc_id`) in `internal/store/docrevisions.go` and `ReviseDoc`'s exact signature before relying on them; if `ReviseDoc` copies the body into the revision, the code above still holds because the following `UpdateRevision` replaces it. Add the `time` and `designdoc` imports if the file lacks them.

- [ ] **Step 6: Run the tests**

Run: `go test -trimpath ./internal/store -run 'TestClauseVersions|TestEditClause|TestSyncClauses|TestReviseClause|TestGetClause|TestGovern' -v`
Expected: PASS. Then `go test -trimpath ./internal/model` for the rule tests.

- [ ] **Step 7: Commit**

```bash
git add internal/model/clause.go internal/store/clauses.go internal/store/clauses_test.go
git commit -m "Edit a clause through its document and read its versions and governed tasks"
```

---

### Task 3: API routes for the edit and the history (R1)

**Files:**
- Modify: `internal/api/clauses.go` (append `editClause`, `listClauseVersions`, `getClauseVersion`)
- Modify: `internal/api/router.go` (three `routeGuards` entries)
- Modify: `internal/api/server.go` (three `r.api(...)` lines next to the existing clause route)
- Modify: `internal/api/clauses_test.go` (append tests)

**Interfaces:**
- Produces: `PUT /api/v1/clauses/{id}` body `model.EditClauseInput`, 200 with `model.Clause`; `GET /api/v1/clauses/{id}/versions` 200 `[]model.ClauseVersion`; `GET /api/v1/clauses/{id}/versions/{n}` 200 `model.Clause`.
- Consumes: Task 2's store functions; `s.recordDocEvent(w, r, op, eventType, id, req, apply)`; `s.mapStoreErr`; `readJSON`, `writeErr`, `writeJSON`.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/clauses_test.go`:

```go
func TestClauseEditAndVersionsAPI(t *testing.T) {
	st, h, token := newTestServer(t)
	seedProjectWithKey(t, st, "WL")
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: "worklode", Kind: "spec", Slug: "t", Body: clauseDocBody, CreatedBy: "stig"})

	rr := doReq(t, h, http.MethodPut, "/api/v1/clauses/WL-CL-2", token, model.EditClauseInput{Heading: "Subsection", Body: "\nB changed.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rr.Code, rr.Body)
	}
	var c model.Clause
	decodeInto(t, rr, &c)
	if c.Heading != "Subsection" || c.Version != 1 || c.Body != "\nB changed.\n\n" {
		t.Errorf("edited clause = %+v", c)
	}
	rr = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/docs/%d", doc.ID), token, nil)
	var d model.DocDetail
	decodeInto(t, rr, &d)
	if !strings.Contains(d.Body, "### 1.1 Subsection {#sec-1.1}\n\nB changed.\n") {
		t.Errorf("doc body not regenerated:\n%s", d.Body)
	}

	acceptDocViaAPI(t, h, token, doc.ID)
	rr = doReq(t, h, http.MethodPut, "/api/v1/clauses/WL-CL-2", token, model.EditClauseInput{Heading: "Subsection", Body: "\nB again.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT on accepted = %d %s", rr.Code, rr.Body)
	}
	decodeInto(t, rr, &c)
	if c.Body != "\nB changed.\n\n" || c.Status != "accepted" {
		t.Errorf("accepted clause moved before the revision landed: %+v", c)
	}
	rr = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/docs/%d/revision/accept", doc.ID), token, nil)
	if rr.Code/100 != 2 {
		t.Fatalf("accept revision = %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2/versions", token, nil)
	var vs []model.ClauseVersion
	decodeInto(t, rr, &vs)
	if rr.Code != http.StatusOK || len(vs) != 2 || vs[0].Version != 2 {
		t.Errorf("versions = %d %+v", rr.Code, vs)
	}
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/1", token, nil)
	decodeInto(t, rr, &c)
	if rr.Code != http.StatusOK || c.Version != 1 || c.Body != "\nB changed.\n\n" {
		t.Errorf("v1 = %d %+v", rr.Code, c)
	}

	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodPut, "/api/v1/clauses/WL-CL-99", model.EditClauseInput{Heading: "x", Body: "y"}, http.StatusNotFound},
		{http.MethodPut, "/api/v1/clauses/nope", model.EditClauseInput{Heading: "x", Body: "y"}, http.StatusBadRequest},
		{http.MethodPut, "/api/v1/clauses/WL-CL-2", model.EditClauseInput{Heading: "", Body: "y"}, http.StatusUnprocessableEntity},
		{http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/9", nil, http.StatusNotFound},
		{http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/x", nil, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/clauses/WL-CL-99/versions", nil, http.StatusNotFound},
	} {
		rr := doReq(t, h, tc.method, tc.path, token, tc.body)
		if rr.Code != tc.want {
			t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, rr.Code, tc.want, rr.Body)
		}
	}
}
```

`clauseDocBody` is whatever the existing `TestGetClause` API test uses as its document body; reuse its name (read the file). Confirm the `CreateDocInput` field names and `acceptDocViaAPI`'s signature against `internal/api/docs_test.go`. If accepting a revision in the API needs an owner or reviewer step the existing revision tests perform, copy that sequence.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -trimpath ./internal/api -run TestClauseEditAndVersionsAPI -v`
Expected: 404s (routes absent) or a `NewServer` boot refusal once the guards exist without handlers.

- [ ] **Step 3: Implement**

Append to `internal/api/clauses.go`:

```go
// editClause handles PUT /api/v1/clauses/{id}: a clause-first write of the
// heading and body (S14, S35). The store regenerates the arranging
// document's body and writes it through the document path, so the event is
// recorded against that document.
func (s *server) editClause(w http.ResponseWriter, r *http.Request) {
	ref, ok := designdoc.ParseClauseRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause id must look like WL-CL-12")
		return
	}
	var req model.EditClauseInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	current, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if len(current.ArrangedIn) != 1 {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("clause %s is arranged in %d documents; edit the document instead", current.Ref, len(current.ArrangedIn)))
		return
	}
	now := s.st.Now()
	err = s.recordDocEvent(w, r, "clause", "doc.clause_edited", current.ArrangedIn[0].Doc, req,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.EditClause(tx, now, ref.Key, ref.Number, req, s.actorID(r), eventID)
			return err
		})
	if err != nil {
		return
	}
	c, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// listClauseVersions handles GET /api/v1/clauses/{id}/versions.
func (s *server) listClauseVersions(w http.ResponseWriter, r *http.Request) {
	ref, ok := designdoc.ParseClauseRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause id must look like WL-CL-12")
		return
	}
	vs, err := s.st.ListClauseVersions(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

// getClauseVersion handles GET /api/v1/clauses/{id}/versions/{n}.
func (s *server) getClauseVersion(w http.ResponseWriter, r *http.Request) {
	ref, ok := designdoc.ParseClauseRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause id must look like WL-CL-12")
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n <= 0 {
		writeErr(w, http.StatusBadRequest, "version must be a positive integer")
		return
	}
	c, err := s.st.GetClauseVersion(r.Context(), ref.Key, ref.Number, n)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
```

How the handler learns the acting actor: read `recordDocEvent` and `reviseDoc` in `internal/api/docs.go` to see how the actor id reaches `store.ReviseDoc` there (a `Subject` on the request context, a helper such as `s.actorID(r)` or `subjectFrom(r)`), and use the same. `recordDocEvent` writes the error response itself when `apply` fails, so the handler returns on `err != nil` without writing again; confirm by reading it.

Router entries, next to `"GET /api/v1/clauses/{id}"`:

```go
	"PUT /api/v1/clauses/{id}":              guardedAny(permDocWrite),
	"GET /api/v1/clauses/{id}/versions":     guardedAny(permDocRead),
	"GET /api/v1/clauses/{id}/versions/{n}": guardedAny(permDocRead),
```

Server registrations next to the existing `r.api("GET /api/v1/clauses/{id}", s.getClause)`:

```go
	r.api("PUT /api/v1/clauses/{id}", s.editClause)
	r.api("GET /api/v1/clauses/{id}/versions", s.listClauseVersions)
	r.api("GET /api/v1/clauses/{id}/versions/{n}", s.getClauseVersion)
```

- [ ] **Step 4: Run the tests**

Run: `go test -trimpath ./internal/api -run 'TestClause|TestGovernedBy|TestRouteGuards|TestNewServer' -v`
Expected: PASS, including whatever test enforces the guards table.

- [ ] **Step 5: Commit**

```bash
git add internal/api/clauses.go internal/api/clauses_test.go internal/api/router.go internal/api/server.go
git commit -m "Edit a clause and read its versions over the API"
```

---

### Task 4: `lode clause edit`, `lode clause versions`, `lode show --version` (R6)

**Files:**
- Modify: `internal/cli/clauses.go` (append `EditClause`, `ListClauseVersions`, `GetClauseVersion`, `ClauseVersionsTable`; extend `ClauseRender` with governed tasks)
- Modify: `internal/cli/clauses_test.go` (render tests)
- Create: `internal/cmd/clause.go` (`newClauseCmd` with `edit` and `versions`)
- Modify: `internal/cmd/root.go` or wherever entity commands register (`grep -n "newDocCmd()" internal/cmd/*.go`)
- Modify: `internal/cmd/show.go` (`--version` flag routed to `runClauseShow`)
- Modify: `internal/cmd/namerule_test.go` (entity `clause`; view `versions` under it if the L6 table is per entity)
- Modify: `plugins/claude/lode/skills/worklode/references/commands.md` (regenerate; `TestCommandReference` says how)

**Interfaces:**
- Produces: `(c *Client) EditClause(ctx, ref string, in model.EditClauseInput) (model.Clause, []byte, error)`; `(c *Client) ListClauseVersions(ctx, ref string) ([]model.ClauseVersion, []byte, error)`; `(c *Client) GetClauseVersion(ctx, ref string, version int) (model.Clause, []byte, error)`; `ClauseVersionsTable(w io.Writer, vs []model.ClauseVersion)`.
- Consumes: Task 3's routes; `doJSON`; `newAPIClient`, `jsonOut`, `printRaw` in `internal/cmd`.

- [ ] **Step 1: Write the failing render tests**

Append to `internal/cli/clauses_test.go`:

```go
func TestClauseRenderGovernedTasks(t *testing.T) {
	var b strings.Builder
	ClauseRender(&b, model.Clause{Ref: "WL-CL-2", Heading: "Sub", Status: "accepted", Version: 2,
		GovernedTasks: []model.ClauseTask{{ID: "WL-7", Title: "Do it", State: "ready", Source: "plan", ClauseVersion: 1}}})
	if !strings.Contains(b.String(), "  governs:  WL-7 Do it (ready, plan, v1)\n") {
		t.Errorf("render:\n%s", b.String())
	}
}

func TestClauseVersionsTable(t *testing.T) {
	var b strings.Builder
	ClauseVersionsTable(&b, []model.ClauseVersion{
		{Version: 2, Heading: "Sub", CreatedAt: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)},
		{Version: 1, Heading: "Sub", CreatedAt: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)},
	})
	out := b.String()
	if !strings.HasPrefix(out, "VERSION") || !strings.Contains(out, "2") || !strings.Contains(out, "Sub") {
		t.Errorf("table:\n%s", out)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -trimpath ./internal/cli -run 'TestClauseRenderGovernedTasks|TestClauseVersionsTable' -v`
Expected: build failure on `ClauseVersionsTable` and the missing `governs:` line.

- [ ] **Step 3: Implement the client and renderers**

In `internal/cli/clauses.go` add, after the `arranged:` loop in `ClauseRender`:

```go
	for _, gt := range c.GovernedTasks {
		fmt.Fprintf(w, "  governs:  %s %s (%s, %s, v%d)\n", gt.ID, gt.Title, gt.State, gt.Source, gt.ClauseVersion)
	}
```

and append:

```go
// EditClause calls PUT /api/v1/clauses/{ref}.
func (c *Client) EditClause(ctx context.Context, ref string, in model.EditClauseInput) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodPut, "/api/v1/clauses/"+url.PathEscape(ref), in, "clause")
}

// ListClauseVersions calls GET /api/v1/clauses/{ref}/versions.
func (c *Client) ListClauseVersions(ctx context.Context, ref string) ([]model.ClauseVersion, []byte, error) {
	return doJSON[[]model.ClauseVersion](ctx, c, http.MethodGet, "/api/v1/clauses/"+url.PathEscape(ref)+"/versions", nil, "clause versions")
}

// GetClauseVersion calls GET /api/v1/clauses/{ref}/versions/{n}.
func (c *Client) GetClauseVersion(ctx context.Context, ref string, version int) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodGet, fmt.Sprintf("/api/v1/clauses/%s/versions/%d", url.PathEscape(ref), version), nil, "clause")
}

// ClauseVersionsTable lists a clause's versions, newest first.
func ClauseVersionsTable(w io.Writer, vs []model.ClauseVersion) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tHEADING\tCREATED")
	for _, v := range vs {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", v.Version, v.Heading, LocalTime(v.CreatedAt))
	}
	tw.Flush()
}
```

Read `DocVersionsTable` in `internal/cli/docs.go` first and match its tabwriter settings and column style exactly.

- [ ] **Step 4: The command group**

Create `internal/cmd/clause.go`:

```go
package cmd

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newClauseCmd is the design-clause entity group (12-spec-refactoring-design-tree.md
// S14, S35). Reading one clause is `lode show WL-CL-<n>`.
func newClauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clause",
		Short: "Design clauses: edit one, list its versions",
	}
	cmd.AddCommand(newClauseEditCmd(), newClauseVersionsCmd())
	return cmd
}

func newClauseEditCmd() *cobra.Command {
	var file, heading string
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Short: "Replace a clause's body (and heading) from a file; the document is regenerated around it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return errors.New("--file is required")
			}
			body, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			if heading == "" {
				current, _, err := c.GetClause(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				heading = current.Heading
			}
			clause, raw, err := c.EditClause(cmd.Context(), args[0], model.EditClauseInput{Heading: strings.TrimSpace(heading), Body: string(body)})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ClauseRender(cmd.OutOrStdout(), clause)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file holding the new body (the text under the heading)")
	cmd.Flags().StringVar(&heading, "heading", "", "new heading text; unchanged when omitted")
	return cmd
}

func newClauseVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <ref>",
		Short: "List a clause's versions, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			vs, raw, err := c.ListClauseVersions(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ClauseVersionsTable(cmd.OutOrStdout(), vs)
			return nil
		},
	}
}
```

Register `newClauseCmd()` where `newDocCmd()` is registered. Match the file's existing `Use`/`Short` conventions (read `internal/cmd/doc.go`'s `versions` subcommand for the exact shape, including how it resolves a ref and whether it has `ValidArgsFunction`).

In `internal/cmd/show.go` add a `--version` int flag. If a `--version` flag already exists for documents, reuse it: when the target classifies as `targetClause` and the flag is set, call `c.GetClauseVersion` instead of `c.GetClause` inside `runClauseShow` (add a `version int` parameter). If the flag does not exist, add `cmd.Flags().IntVar(&version, "version", 0, "show one version of a clause (WL-CL-<n>)")`, refuse it for other targets with `errors.New("--version applies only to clauses")`, and thread it through `dispatchShowPositional`.

Naming test: read `internal/cmd/namerule_test.go` and add `clause` to the L1 entity list and, if the L6 table is per entity, `versions` under `clause`. `edit` is an L3 canonical verb and needs no entry.

Regenerate the command reference the way `TestCommandReference`'s failure message says (Task 8 of increment 1 did the same).

- [ ] **Step 5: Run the tests**

Run: `go test -trimpath ./internal/cli ./internal/cmd`
Expected: PASS, including `TestNameRule*` and `TestCommandReference`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/clauses.go internal/cli/clauses_test.go internal/cmd/clause.go internal/cmd/show.go internal/cmd/namerule_test.go internal/cmd/root.go plugins/claude/lode/skills/worklode/references/commands.md
git commit -m "Add lode clause edit and versions, and show a clause at one version"
```

(`root.go` stands for whichever file registers entity commands.)

---

### Task 5: Cockpit clause page, canonical URLs and autolink (R5, S20, A3)

**Files:**
- Create: `internal/ui/clause.templ` (`templ Clause(v ClauseView)`) and its generated `clause_templ.go`
- Modify: `internal/ui/views.go` (append `ClauseView`, `ClauseArrangementRow`)
- Create: `internal/api/clausepage.go` (`clausePage`, `clauseRefRedirect`, `projectKindRedirect`)
- Modify: `internal/api/router.go`, `internal/api/server.go` (web routes)
- Modify: `internal/api/render.go` (append `clauseView`)
- Modify: `internal/mdrender/autolink.go` (`CL` arm), `internal/mdrender/autolink_test.go`
- Modify: `internal/api/clausepage_test.go` (new; page and redirect tests)

**Interfaces:**
- Produces: web routes `GET /projects/{proj}/clause/{n}`, `GET /projects/{proj}/clause/{n}/{ver}`, `GET /projects/{proj}/{kind}/{n}` (spec, adr, plan redirect), `GET /clauses/{ref}` (redirect); `ui.ClauseView`.
- Consumes: `s.st.GetClause`, `s.st.GetClauseVersion`, `s.st.ListClauseVersions`, `s.st.GetProject(ctx, id)`, a project-by-key reader (find it: `grep -n "func (s \*Store) .*Key" internal/store/projects.go`; if none exists, add `ProjectByKey(ctx, key) (*model.Project, error)` returning `ErrNotFound`), `s.renderWeb`, `webErr`, `s.mdcache.Body(keys, body)`, `s.projectKeys(ctx, project)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mdrender/autolink_test.go`, following the file's existing table style:

```go
	// A clause ref links to the resolving redirect, like a document shorthand.
	{"clause ref", "see WL-CL-12.", `see <a href="/clauses/WL-CL-12">WL-CL-12</a>.`},
```

(the exact expected HTML shape follows how the file's other shorthand cases are written; copy one and change the ref and href).

Create `internal/api/clausepage_test.go`:

```go
package api_test

func TestClausePageAndRedirects(t *testing.T) {
	st, h, token := newTestServer(t)
	seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: "worklode", Kind: "spec", Slug: "t", Body: clauseDocBody, CreatedBy: "stig"})

	rr := doReq(t, h, http.MethodGet, "/projects/worklode/clause/2", token, nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "WL-CL-2") || !strings.Contains(rr.Body.String(), "Sub") {
		t.Errorf("clause page = %d\n%s", rr.Code, rr.Body.String()[:min(400, rr.Body.Len())])
	}
	rr = doReq(t, h, http.MethodGet, "/projects/worklode/clause/2/1", token, nil)
	if rr.Code != http.StatusOK {
		t.Errorf("clause version page = %d", rr.Code)
	}
	for path, want := range map[string]string{
		"/clauses/WL-CL-2":         "/projects/worklode/clause/2",
		"/projects/WL/clause/2":    "/projects/worklode/clause/2",
		"/projects/worklode/spec/1": "/docs/WL-SPEC-1",
	} {
		rr := doReq(t, h, http.MethodGet, path, token, nil)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
			t.Errorf("%s = %d %q, want 302 %q", path, rr.Code, rr.Header().Get("Location"), want)
		}
	}
	for _, path := range []string{"/projects/worklode/clause/99", "/projects/worklode/feature/1", "/clauses/nope", "/projects/nowhere/clause/2"} {
		if rr := doReq(t, h, http.MethodGet, path, token, nil); rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rr.Code)
		}
	}
}
```

The web routes are session-gated; read how existing web page tests authenticate (`grep -n "GET /docs/" internal/api/*_test.go | head`) and use the same helper instead of the bearer token if `doReq` does not carry a session. Also check the project id the seed helper creates (`worklode` is assumed; read `seedProjectWithKey`).

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -trimpath ./internal/mdrender -run TestAutolink -v; go test -trimpath ./internal/api -run TestClausePageAndRedirects -v`
Expected: the mdrender case fails on the unlinked ref; the api test gets 404s.

- [ ] **Step 3: Autolink**

In `internal/mdrender/autolink.go`, add a pattern and a pass:

```go
	// clauseRefRe is the CL arm of the shorthand grammar (S20). It links to
	// the resolving redirect /clauses/<ref>, the counterpart of /docs/ref/.
	clauseRefRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-CL-\d+\b`)
```

and in `findRefs`, after the document shorthand loop and before the keyword loop:

```go
	for _, loc := range clauseRefRe.FindAllIndex(value, -1) {
		if taken(loc[0], loc[1]) {
			continue
		}
		out = append(out, refMatch{loc[0], loc[1], clauseRefPrefix + string(value[loc[0]:loc[1]])})
	}
```

with `const clauseRefPrefix = "/clauses/"` next to `docRefPrefix`. The task-id pass runs last already, so `WL-CL-12` is claimed before `taskRef` can read `WL-12` out of it; confirm with the test.

- [ ] **Step 4: View and template**

Append to `internal/ui/views.go`:

```go
// ClauseView is the cockpit's clause page (12-spec-refactoring-design-tree.md
// S20): one design clause at its current or a named version.
type ClauseView struct {
	Page     PageProps
	Clause   model.Clause
	BodyHTML template.HTML // Clause.Body rendered by internal/api, like DocView.BodyHTML
	// Arranged links each arranging document's section page.
	Arranged []ClauseArrangementRow
	Versions []model.ClauseVersion
	// CanonicalURL is /projects/<proj>/clause/<n>; VersionURL adds /<ver>.
	CanonicalURL string
}

// ClauseArrangementRow is one document arranging the clause, with the href
// of its section.
type ClauseArrangementRow struct {
	DocRef string
	Anchor string
	Depth  int
	Href   string // /docs/ref/<DocRef>#<Anchor>
}
```

Create `internal/ui/clause.templ`; read `internal/ui/docs.templ`'s `Doc` template first and copy its page skeleton (layout call, heading block, chip markup, table classes) so the page looks like the rest of the cockpit:

```templ
package ui

templ Clause(v ClauseView) {
	@layout(v.Page) {
		<article class="doc">
			<header>
				<h1>{ v.Clause.Ref }: { v.Clause.Heading }</h1>
				<p class="chips">
					<span class="chip">{ v.Clause.Status }</span>
					<span class="chip">v{ fmt.Sprint(v.Clause.Version) }</span>
				</p>
			</header>
			<section class="body">
				@templ.Raw(string(v.BodyHTML))
			</section>
			<section>
				<h2>Arranged in</h2>
				<ul>
					for _, a := range v.Arranged {
						<li><a href={ templ.SafeURL(a.Href) }>{ a.DocRef }#{ a.Anchor }</a> (depth { fmt.Sprint(a.Depth) })</li>
					}
				</ul>
			</section>
			<section>
				<h2>Governs</h2>
				<ul>
					for _, t := range v.Clause.GovernedTasks {
						<li><a href={ templ.SafeURL("/tasks/" + t.ID) }>{ t.ID }</a> { t.Title } ({ t.State }, { t.Source }, v{ fmt.Sprint(t.ClauseVersion) })</li>
					}
				</ul>
			</section>
			<section>
				<h2>Versions</h2>
				<ul>
					for _, ver := range v.Versions {
						<li><a href={ templ.SafeURL(fmt.Sprintf("%s/%d", v.CanonicalURL, ver.Version)) }>v{ fmt.Sprint(ver.Version) }</a> { ver.Heading }</li>
					}
				</ul>
			</section>
		</article>
	}
}
```

Use whatever the layout component and CSS class names in `docs.templ` actually are; the sketch names the sections and their content, the template copies the skeleton. Run `templ generate` from the repo root and commit the generated file.

- [ ] **Step 5: Handlers and routes**

Append to `internal/api/render.go`:

```go
func clauseView(md *mdrender.Cache, keys mdrender.ProjectKeys, c *model.Clause, versions []model.ClauseVersion, projectID string) ui.ClauseView {
	v := ui.ClauseView{
		Page:         ui.PageProps{Title: "worklode: " + c.Ref, ActiveGlobal: "knowledge"},
		Clause:       *c,
		BodyHTML:     md.Body(keys, c.Body),
		Versions:     versions,
		CanonicalURL: fmt.Sprintf("/projects/%s/clause/%d", projectID, c.Number),
	}
	for _, a := range c.ArrangedIn {
		v.Arranged = append(v.Arranged, ui.ClauseArrangementRow{DocRef: a.DocRef, Anchor: a.Anchor, Depth: a.Depth, Href: "/docs/ref/" + a.DocRef + "#" + a.Anchor})
	}
	return v
}
```

Create `internal/api/clausepage.go`:

```go
package api

// clausePage handles GET /projects/{proj}/clause/{n} and /{n}/{ver}: the
// canonical clause URL (S20). An uppercase {proj} is a project key typed by
// habit and redirects to the id form.
func (s *server) clausePage(w http.ResponseWriter, r *http.Request) {
	proj := r.PathValue("proj")
	p, redirected := s.projectFromPath(w, r, proj)
	if p == nil {
		return
	}
	if redirected {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n <= 0 {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	var c *model.Clause
	if ver := r.PathValue("ver"); ver != "" {
		v, err := strconv.Atoi(ver)
		if err != nil || v <= 0 {
			webErr(w, http.StatusNotFound, "not found")
			return
		}
		c, err = s.st.GetClauseVersion(r.Context(), p.Key, n, v)
	} else {
		c, err = s.st.GetClause(r.Context(), p.Key, n)
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	versions, err := s.st.ListClauseVersions(r.Context(), p.Key, n)
	if err != nil {
		s.log.Warn("rendering clause page without version history", "clause", c.Ref, "err", err)
	}
	view := clauseView(s.mdcache, s.projectKeys(r.Context(), p.ID), c, versions, p.ID)
	s.renderWeb(w, r, http.StatusOK, "clause page", ui.Clause(view))
}

// projectFromPath resolves {proj}: a project id as is; an uppercase project
// key redirects to the same path with the id in its place and reports true.
// A miss writes 404 and returns nil.
func (s *server) projectFromPath(w http.ResponseWriter, r *http.Request, proj string) (*model.Project, bool) {
	if proj != strings.ToLower(proj) {
		p, err := s.st.ProjectByKey(r.Context(), proj)
		if err != nil {
			webErr(w, http.StatusNotFound, "not found")
			return nil, false
		}
		http.Redirect(w, r, strings.Replace(r.URL.Path, "/projects/"+proj+"/", "/projects/"+p.ID+"/", 1), http.StatusFound)
		return p, true
	}
	p, err := s.st.GetProject(r.Context(), proj)
	if err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return nil, false
	}
	return p, false
}

// projectKindRedirect handles GET /projects/{proj}/{kind}/{n} for the
// document kinds: the /docs/<KEY>-<KIND>-<n> page stays canonical for
// documents in this increment, so the S20 form redirects there. Task kinds
// are deferred (07 §10.2 follow-up) and 404.
func (s *server) projectKindRedirect(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind != "spec" && kind != "adr" && kind != "plan" {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	p, redirected := s.projectFromPath(w, r, r.PathValue("proj"))
	if p == nil || redirected {
		return
	}
	if _, err := strconv.Atoi(r.PathValue("n")); err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/docs/%s-%s-%s", p.Key, strings.ToUpper(kind), r.PathValue("n")), http.StatusFound)
}

// clauseRefRedirect handles GET /clauses/{ref}: the resolving redirect the
// autolinker targets, the counterpart of /docs/ref/.
func (s *server) clauseRefRedirect(w http.ResponseWriter, r *http.Request) {
	ref, ok := designdoc.ParseClauseRef(r.PathValue("ref"))
	if !ok {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	p, err := s.st.ProjectByKey(r.Context(), ref.Key)
	if err != nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/clause/%d", p.ID, ref.Number), http.StatusFound)
}
```

`ProjectByKey` stands for whatever project-by-key reader the store has; find it before writing, and add one (`internal/store/projects.go`, `SELECT ... FROM projects WHERE key = $1`, `ErrNotFound` on no row) only if none exists. The `model.Project` field for the id is `ID`.

Router entries (web section):

```go
	"GET /projects/{proj}/clause/{n}":       guarded(permWebRead),
	"GET /projects/{proj}/clause/{n}/{ver}": guarded(permWebRead),
	"GET /projects/{proj}/{kind}/{n}":       guarded(permWebRead),
	"GET /clauses/{ref}":                    guarded(permWebRead),
```

Server registrations next to the `/docs/ref/{ref...}` line:

```go
	r.web("GET /projects/{proj}/clause/{n}", s.navWrap("knowledge", s.clausePage))
	r.web("GET /projects/{proj}/clause/{n}/{ver}", s.navWrap("knowledge", s.clausePage))
	r.web("GET /projects/{proj}/{kind}/{n}", s.projectKindRedirect)
	r.web("GET /clauses/{ref}", s.clauseRefRedirect)
```

Go's `ServeMux` ranks `/projects/{id}/progress/events` and `/projects/{id}/graph/data` above `/projects/{proj}/{kind}/{n}` because a literal segment is more specific than a wildcard, so the existing three-segment project routes keep winning. If `NewServer` panics with a pattern conflict, the conflicting route is one whose wildcard sits in the same position; report it rather than renaming existing routes.

- [ ] **Step 6: Run the tests**

Run: `templ generate && go test -trimpath ./internal/ui ./internal/mdrender && go test -trimpath ./internal/api -run 'TestClausePageAndRedirects|TestRouteGuards|TestNewServer|TestDocPage' -v`
Expected: PASS. Also `go test -trimpath ./internal/disttest` to confirm the guarded import boundary is untouched.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/clause.templ internal/ui/clause_templ.go internal/ui/views.go internal/api/clausepage.go internal/api/clausepage_test.go internal/api/render.go internal/api/router.go internal/api/server.go internal/mdrender/autolink.go internal/mdrender/autolink_test.go internal/store/projects.go
git commit -m "Give a clause a cockpit page, a canonical URL and an autolink"
```

---

### Task 6: Specs 05, 07, 09 and 10 describe what shipped

**Files:**
- Modify: `docs/specs2/05-documents.md` §4 (the clause paragraph)
- Modify: `docs/specs2/07-knowledge-graph-and-search.md` §10.2 (follow-up note, A3)
- Modify: `docs/specs2/09-cli-and-skills.md` (a `clause` row in the entity table; `show --version`)
- Modify: `docs/specs2/10-cockpit.md` (clause page, in the section that describes the document page)
- Modify: `docs/specs2/12-spec-refactoring-design-tree.md` ("Recorded from increment 1": mark A3 partly done under S20 with one sentence)

**Interfaces:** none.

- [ ] **Step 1: 05 §4**

Append to the end of the paragraph that begins `**Every anchored section is a design clause**`, replacing its last sentence (`The document body stays the authored source in this stage; clause-first edits, typed edges between clauses and the migration of the old specs are later work.`) with:

```markdown
A clause can also be edited directly: `PUT /api/v1/clauses/WL-CL-<n>` and `lode clause edit WL-CL-<n> --file <body> [--heading <text>]` take the new heading and body, regenerate the arranging document's body with that one section changed, and write it through the document's own path, so the body stays an exact reassembly of its clauses. On a draft document the clause's draft version is rewritten in place. On an accepted document the write goes to the candidate revision, opened if none is open, and the clause's next version appears when the revision lands with `lode doc revise --accept`. A clause arranged in no document, or in more than one, refuses the edit in this stage. `GET /api/v1/clauses/WL-CL-<n>/versions`, `/versions/<v>`, `lode clause versions` and `lode show WL-CL-<n> --version <v>` read the history. Typed edges between clauses, shared clauses across documents and the migration of the old specs are later work.
```

- [ ] **Step 2: 07 §10.2**

After the paragraph in §10.2 that gives documents the IRI `/docs/<KEY>-<TYPE>-<n>` (find it with `grep -n "/docs/<KEY>" docs/specs2/07-knowledge-graph-and-search.md`), add:

```markdown
**Follow-up (12-spec-refactoring-design-tree.md S20, A3).** The canonical URL of an entity is `/projects/<proj>/<kind>/<n>`, with `/<ver>` for a version, where `<proj>` is the project id and `<kind>` is unique across document kinds (`spec`, `adr`, `plan`, `clause`) and task kinds. The cockpit serves it for clauses (`/projects/worklode/clause/12`) and redirects the document kinds to the `/docs/<KEY>-<TYPE>-<n>` page, which stays canonical for documents for now. An uppercase `<proj>` is a project key typed by habit and redirects to the id form. `/clauses/<KEY>-CL-<n>` is the resolving redirect prose links use. Task kinds, versions on document URLs, and turning `/tasks/<id>` and `/docs/<ref>` into redirects are the remaining half of this follow-up.
```

- [ ] **Step 3: 09**

In the entity table, add a row after `| `clause`... ` does not exist yet, so insert alphabetically near `doc`:

```markdown
| `clause` | L1 | `edit`; view `versions`. Reading one is `lode show WL-CL-<n> [--version <v>]` |
```

If the L6 noun table in the same document is closed per entity, add `clause versions` to it as well (read the section; spec 09 §1 L6 names the table).

- [ ] **Step 4: 10**

In the section describing the document page (find it: `grep -n "document page\|Document page\|/docs/" docs/specs2/10-cockpit.md | head`), append a paragraph:

```markdown
**Clause page.** `/projects/<proj>/clause/<n>` shows one design clause: ref and heading, status and version chips, the rendered body, the documents arranging it with a link to each section, the tasks it governs, and its version list, each version at `/<n>/<ver>`. Every `WL-CL-<n>` in rendered prose links there through `/clauses/<ref>`. The page is read-only; the clause editor that saves through `PUT /api/v1/clauses/<ref>` is later work.
```

- [ ] **Step 5: 12**

In "Recorded from increment 1", add after S35:

```markdown
- S36 (S20 and A3 applied in increment 1b): the canonical URL ships for clauses first. `/projects/<proj>/clause/<n>` and `/<ver>` are served; the document kinds redirect to the existing `/docs/<KEY>-<TYPE>-<n>` page; task kinds and the root-route redirects are the remaining half, recorded in 07 §10.2. A clause edit writes through the arranging document (S13, S14): a draft is rewritten in place, an accepted document's edit lands with its revision (S35), and a clause arranged in zero or several documents refuses the edit until plans are arrangements.
```

and change `S31 to S35 recorded from the first increment` in the Frontier paragraph to `S31 to S36`.

- [ ] **Step 6: Style check and commit**

Run: `git diff -U0 docs/specs2 | grep '^+' | grep -nE '—|, not |rather than|instead of'`
Expected: no output.

```bash
git add docs/specs2/05-documents.md docs/specs2/07-knowledge-graph-and-search.md docs/specs2/09-cli-and-skills.md docs/specs2/10-cockpit.md docs/specs2/12-spec-refactoring-design-tree.md
git commit -m "Record clause editing, history and the clause URL in specs 05, 07, 09, 10 and 12"
```

---

## Self-review

Spec coverage: S13 (the frozen list is what review accepts) is why an accepted document's clause edit goes to the candidate revision (Task 2, R2). S14 (document-first, clause edits after) is the edit surface itself (Tasks 2 to 4). S20's URL and IRI half is Task 5, scoped by R5, with the remainder named in 07 §10.2 (Task 6, A3). S35 is exercised by Task 2's draft and accepted tests. #687's 1b list (S13, S14, S20 URL scheme, cockpit clause view and autolink) maps to Tasks 2 to 5.

Placeholder scan: every code step carries code; the places where an implementer must read before writing (actor id helper, project-by-key reader, templ layout names, namerule tables, revision test sequence) say exactly what to read and what to do in each outcome.

Type consistency: `designdoc.ClauseRef{Key, Number int64}` and `ParseClauseRef` (Task 1) are what Tasks 3 and 5 call; `EditClause(tx, now, key, number, in, actorID, eventID) (int64, error)` (Task 2) matches Task 3's handler; `ListClauseVersions(ctx, key, number) ([]model.ClauseVersion, error)` and `GetClauseVersion(ctx, key, number, version) (*model.Clause, error)` match Tasks 3, 4 and 5; `model.ClauseTask{ID, Title, State, Source, ClauseVersion}` is rendered identically in Task 4's `governs:` line and Task 5's template; `cli.EditClause`, `ListClauseVersions`, `GetClauseVersion` and `ClauseVersionsTable` match Task 4's commands; `ui.ClauseView` fields match `clauseView` and the template.
