# Clause Model, Increment 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every anchored section of a spec or ADR becomes a design clause with its own `WL-CL-<n>` identity and version history, documents store their arrangement of clauses, tasks carry direct `governedBy` links to clauses, and the chunk budget derives from the embedding model's declared context window.

**Architecture:** A clause is what `designdoc.Parse` already returns as a `Section`, so there is no new splitter: the store mints and versions clauses in the same transaction that rebuilds `doc_sections`, matching identity across writes by anchor and then by heading. Four new tables hold clauses, their immutable versions, each document's arrangement, and each task's governing clauses. Accepting a plan materialises `governedBy` links on the tasks it mints from the plan's `covers` edges; an architect adds or removes links by hand through `lode task govern`. Two read paths (`GET /api/v1/clauses/{id}` with `lode show WL-CL-<n>`, and `governed_by` on the task detail) make the result visible. Document bodies stay the authored source of truth in this increment.

**Tech Stack:** Go, Postgres via `database/sql`, golang-migrate SQL files, cobra CLI, the existing `internal/designdoc` parser.

**Spec:** `docs/specs2/12-spec-refactoring-design-tree.md` (S1 to S4, S7 to S11, S20, S21, S27) and `docs/specs2/07-knowledge-graph-and-search.md` §14.4. Executors read both.

## Why this increment and what it leaves out

The design tree settled thirty decisions. This plan builds the ones everything else depends on: the size ceiling comes from config (S7), clauses exist with stable refs and versions (S8, S9, S10, S20), status lives on the clause and acceptance sets it (S11), the reassembly guarantee holds (S21), and tasks carry their own `governedBy` links, minted from plan coverage and editable by the architect (S1, S2, S3, and the create-time optional half of S4).

Deferred to later plans: the migration of the 47 old exports into withdrawn clauses and the clause-to-clause supersession map (A2, S22, S27); the reconciler that fills a planless task's link from the gate trailer (the asynchronous half of S4, S18, S29, S30); typed edges `refines`, `constrains`, `conflictsWith`, `references` (S12, S26); plans as arrangements, coverage as membership and the token budget (S16, S19, S25); arrangements as frozen queries and clause-first editing (S13, S14); the cockpit page, autolinking and the `/projects/<proj>/<kind>/<n>` URL scheme (S20, A3); refusing an over-ceiling clause at post time; pinning a link to a clause version (the optional pin in S10).

## Global Constraints

- Build and test with `-trimpath` only: `make build`, `make test`, or `go test -trimpath ./internal/<pkg> -run <Test>`. Never bare `go test`.
- Store and API tests need Postgres with pgvector at `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (override `TEST_POSTGRES_DSN`). They skip silently without it, so confirm the suite actually ran (look for `ok` with a non-zero duration and no `SKIP`).
- Every shape that crosses HTTP is declared once in `internal/model` with wire field names (ADR 036). `internal/model/rule_test.go` enforces it.
- Every route appears in `internal/api/router.go`'s `routeGuards`; `NewServer` refuses to boot otherwise.
- `internal/cmd` decides, `internal/cli` renders: a human view is a `cli.*Render` function taking an `io.Writer`; no `tabwriter` or timestamp formatting in `internal/cmd`.
- A new CLI verb must be in `l3DomainActions` in `internal/cmd/namerule_test.go` and in spec 09 §1 L3's allowlist; `un-` inverses are free (L5).
- Feature-stem file naming: `model/clause.go`, `store/clauses.go`, `api/clauses.go`, `cli/clauses.go`; `model/governedby.go`, `store/governedby.go`, `api/governedby.go`, `cli/governedby.go`.
- Migrations live in `deploy/base/migrations/` as `NNNN_name.up.sql` and `.down.sql`. Highest today is `0080`. Run `./scripts/check-migrations.sh --no-fix` after adding one.
- Prose in `docs/specs2/` follows the `lode:anti-smartass` plain-language style: no em dashes, no "X, not Y" antithesis.
- Commit messages: imperative subject, no Co-authored-by or any self-reference.
- Refs follow the `<KEY>-<TYPE>-<n>` grammar of 025 §14.3; the clause type token is `CL`.

---

### Task 1: Clause reassembly acceptance test over docs/specs2 (S21)

**Files:**
- Modify: `internal/designdoc/designdoc_test.go` (append one test)

**Interfaces:**
- Consumes: `designdoc.Parse(src []byte) (*Document, error)`, `Document.Preamble`, `Document.Sections`, `Section.HeadingAndBody() string`, the nil-safe `Frontmatter.source() string`.
- Produces: nothing for later tasks. It is the guarantee Task 4 rests on: a document is exactly its preamble plus its sections' heading-and-body in order.

- [ ] **Step 1: Write the test**

Append to `internal/designdoc/designdoc_test.go`:

```go
// TestClausesReassembleSpecs2 is 12-spec-refactoring-design-tree.md S21's
// acceptance test. Every section is one clause, and Preamble plus each
// section's HeadingAndBody in order rebuilds the source byte for byte, so an
// arrangement of clauses loses nothing a document had.
func TestClausesReassembleSpecs2(t *testing.T) {
	files, err := filepath.Glob("../../docs/specs2/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files matched docs/specs2/*.md")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var b bytes.Buffer
		b.WriteString(d.Frontmatter.source())
		b.WriteString(d.Preamble)
		for _, sec := range d.Sections {
			b.WriteString(sec.HeadingAndBody())
		}
		if !bytes.Equal(b.Bytes(), src) {
			t.Errorf("%s: %d sections do not reassemble to the source", f, len(d.Sections))
		}
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test -trimpath ./internal/designdoc -run TestClausesReassembleSpecs2 -v`
Expected: PASS. If a file fails, the failure names it. Do not change the parser to make it pass; report the file and the diff of `b.Bytes()` against `src` instead, since a failure here is a real defect in the guarantee.

- [ ] **Step 3: Commit**

```bash
git add internal/designdoc/designdoc_test.go
git commit -m "Prove docs/specs2 reassembles from its sections (S21)"
```

---

### Task 2: Chunk budget from the embedding model's context window (S7)

**Files:**
- Modify: `internal/corpusindex/chunk.go:14-24` (replace the constants), `:52-60`, `:78-150` (thread the budget)
- Modify: `internal/corpusindex/chunk_test.go` (pass `DefaultBudget`, read its fields)
- Modify: `internal/indexer/indexer.go:196-232` (a `Budget` field used by `chunks`)
- Modify: `internal/api/server.go:135-145` (config field), `:1068-1076` (validation), `:1233-1235` (indexer wiring)
- Create: `internal/api/chunkbudget_test.go`
- Modify: `deploy/base/configmap.yaml:36`, `docker-compose.yml:67`, `README.md:586-588`

**Interfaces:**
- Produces: `corpusindex.Budget{Runes, Overlap int}`, `corpusindex.BudgetFor(contextTokens int) Budget`, `corpusindex.DefaultBudget`, `ChunkDoc(b Budget, doc model.Doc, sections []model.DocSection) []Chunk`, `ChunkTask(b Budget, task model.Task) []Chunk`, `ChunkSkill(b Budget, skill model.Skill, skillMD string) []Chunk`, `indexer.Indexer.Budget`, `Config.EmbeddingContextTokens string`, `embeddingBudget(cfg Config) (corpusindex.Budget, error)`.

- [ ] **Step 1: Write the failing config test**

Create `internal/api/chunkbudget_test.go`:

```go
package api

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
)

// TestEmbeddingBudget covers 07 §14.4: a model needs its context window
// declared, the budget derives from it, and no provider means the
// lexical-only default.
func TestEmbeddingBudget(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		want    corpusindex.Budget
		wantErr bool
	}{
		{"no provider", Config{}, corpusindex.DefaultBudget, false},
		{"model without window", Config{EmbeddingModel: "m"}, corpusindex.Budget{}, true},
		{"window without model", Config{EmbeddingContextTokens: "2048"}, corpusindex.Budget{}, true},
		{"not a number", Config{EmbeddingModel: "m", EmbeddingContextTokens: "big"}, corpusindex.Budget{}, true},
		{"zero", Config{EmbeddingModel: "m", EmbeddingContextTokens: "0"}, corpusindex.Budget{}, true},
		{"gemma", Config{EmbeddingModel: "m", EmbeddingContextTokens: "2048"}, corpusindex.Budget{Runes: 3584, Overlap: 597}, false},
		{"8k", Config{EmbeddingModel: "m", EmbeddingContextTokens: "8192"}, corpusindex.Budget{Runes: 14336, Overlap: 2389}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := embeddingBudget(c.cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("budget = %+v, want %+v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -trimpath ./internal/api -run TestEmbeddingBudget`
Expected: compile error, `undefined: embeddingBudget` and `cfg.EmbeddingContextTokens undefined`.

- [ ] **Step 3: Replace the constants in corpusindex with a Budget**

In `internal/corpusindex/chunk.go`, replace lines 14 to 24 (the comment and the `const` block) with:

```go
// Budget is the chunk sizing (040 §4.1). It derives from the embedding
// model's declared context window (07 §14.4); nothing here carries a model's
// window as a constant.
type Budget struct {
	// Runes is a chunk's ceiling, header included.
	Runes int
	// Overlap is how many runes of raw text consecutive sub-chunks share.
	Overlap int
}

// BudgetFor sizes chunks for a model whose context window is contextTokens.
// 1.75 runes per token is the ratio that kept 3600 runes inside
// EmbeddingGemma's 2048-token window; the overlap is a sixth of the budget.
func BudgetFor(contextTokens int) Budget {
	runes := contextTokens * 7 / 4
	return Budget{Runes: runes, Overlap: runes / 6}
}

// DefaultBudget sizes chunks when no embedding provider is configured and
// only the lexical arm indexes them. It equals BudgetFor(2048), the smallest
// window a default deployment has used, so a provider added later does not
// re-chunk the corpus unless its window differs.
var DefaultBudget = BudgetFor(2048)
```

Then thread the budget through every function that used the constants:

```go
func windowed(b Budget, anchor, header, text string, start int) []Chunk {
	budget := b.Runes - len([]rune(header))
	if budget < 1 {
		budget = 1 // pathological: a header alone at or past the budget
	}
	pieces := embed.Chunks(text, budget, b.Overlap)
```

```go
func ChunkDoc(b Budget, doc model.Doc, sections []model.DocSection) []Chunk {
	parsed, err := designdoc.Parse([]byte(doc.Body))
	if err != nil {
		return windowed(b, "", DocHeader(doc, "", ""), doc.Body, 0)
	}
	if len(parsed.Sections) == 0 {
		return windowed(b, "", DocHeader(doc, "", ""), parsed.Preamble, 0)
	}
	if len(sections) == 0 {
		return chunkPlan(b, doc, parsed)
	}
	return chunkSections(b, doc, sections, parsed)
}

func chunkSections(b Budget, doc model.Doc, sections []model.DocSection, parsed *designdoc.Document) []Chunk {
	// body unchanged except: chunks := windowed(b, sec.Anchor, header, parsed.Sections[i].Body, next[sec.Anchor])
}

func chunkPlan(b Budget, doc model.Doc, parsed *designdoc.Document) []Chunk {
	// body unchanged except every windowed(...) call takes b first
}

func ChunkTask(b Budget, task model.Task) []Chunk {
	// body unchanged except windowed(b, ...)
}

func ChunkSkill(b Budget, skill model.Skill, skillMD string) []Chunk {
	// body unchanged except windowed(b, ...)
}
```

Update the doc comments at the old `chunk.go:39` and `:46` that name `ChunkRunes` to say `Budget.Runes`.

- [ ] **Step 4: Update the corpusindex tests**

In `internal/corpusindex/chunk_test.go`, every `ChunkDoc(`, `ChunkTask(`, `ChunkSkill(` call gains `DefaultBudget` as its first argument, and every `ChunkRunes` becomes `DefaultBudget.Runes`, every `ChunkOverlap` becomes `DefaultBudget.Overlap` (lines 23, 24, 50, 51, 57 and any others `grep -n 'ChunkRunes\|ChunkOverlap' internal/corpusindex/*_test.go` finds). Add one test:

```go
func TestBudgetFor(t *testing.T) {
	got := BudgetFor(2048)
	if got.Runes != 3584 || got.Overlap != 597 {
		t.Errorf("BudgetFor(2048) = %+v, want {3584 597}", got)
	}
	if DefaultBudget != BudgetFor(2048) {
		t.Errorf("DefaultBudget = %+v, want BudgetFor(2048)", DefaultBudget)
	}
}
```

Run: `go test -trimpath ./internal/corpusindex`
Expected: PASS.

- [ ] **Step 5: Give the indexer a budget**

In `internal/indexer/indexer.go`, add a field to the `Indexer` struct next to `Embed`:

```go
	// Budget sizes the chunks (07 §14.4). The zero value means
	// corpusindex.DefaultBudget.
	Budget corpusindex.Budget
```

Add a method and use it in `chunks`:

```go
// budget is the configured chunk sizing, or the lexical-only default when the
// server set none.
func (ix *Indexer) budget() corpusindex.Budget {
	if ix.Budget.Runes == 0 {
		return corpusindex.DefaultBudget
	}
	return ix.Budget
}
```

In `chunks`, the three calls become `corpusindex.ChunkDoc(ix.budget(), *doc, sections)`, `corpusindex.ChunkTask(ix.budget(), *task)`, `corpusindex.ChunkSkill(ix.budget(), model.Skill{...}, skill.SkillMD)`.

Run: `go build -trimpath ./... && go test -trimpath ./internal/indexer`
Expected: builds, PASS (fix any other compile errors from the signature change that `go build` names; `internal/store/testhelpers.go:183` only constructs `corpusindex.Chunk` values and needs no change).

- [ ] **Step 6: Config field, validation and wiring in the server**

In `internal/api/server.go`, after the `EmbeddingModel` field (line 137) add:

```go
	// EmbeddingContextTokens is the model's context window in tokens, required
	// whenever EmbeddingModel is set (07 §14.4). The chunk budget derives from
	// it. A string because setEnvTaggedFields fills strings only; parsed in
	// embeddingBudget.
	EmbeddingContextTokens string `env:"LODE_EMBEDDING_CONTEXT_TOKENS"`
```

Add the function (near `NewServer`; `strconv` and `errors` are already imported):

```go
// embeddingBudget validates the embedding configuration and derives the chunk
// budget from it (07 §14.4): a model needs its context window declared, and
// no provider means the lexical-only default.
func embeddingBudget(cfg Config) (corpusindex.Budget, error) {
	if cfg.EmbeddingModel == "" {
		if cfg.EmbeddingContextTokens != "" {
			return corpusindex.Budget{}, errors.New("LODE_EMBEDDING_CONTEXT_TOKENS requires LODE_EMBEDDING_MODEL")
		}
		return corpusindex.DefaultBudget, nil
	}
	if cfg.EmbeddingContextTokens == "" {
		return corpusindex.Budget{}, errors.New("LODE_EMBEDDING_CONTEXT_TOKENS is required when LODE_EMBEDDING_MODEL is set")
	}
	n, err := strconv.Atoi(cfg.EmbeddingContextTokens)
	if err != nil || n <= 0 {
		return corpusindex.Budget{}, fmt.Errorf("LODE_EMBEDDING_CONTEXT_TOKENS: want a positive integer, got %q", cfg.EmbeddingContextTokens)
	}
	return corpusindex.BudgetFor(n), nil
}
```

Add `chunkBudget corpusindex.Budget` to the `server` struct. In `NewServer`, directly before the `if cfg.QueryEmbeddingURL != "" && cfg.EmbeddingURL == ""` check (line 1068), insert:

```go
	budget, err := embeddingBudget(cfg)
	if err != nil {
		return nil, nil, err
	}
	s.chunkBudget = budget
```

(If `err` is already declared in that scope, use `=` instead of `:=`.) At line 1233, the indexer literal gains `Budget: s.chunkBudget,` next to `Embed: s.embedder`. Add `"github.com/sunstoneinstitute/worklode/internal/corpusindex"` to the imports if not present.

- [ ] **Step 7: Run the tests**

Run: `go test -trimpath ./internal/api -run 'TestEmbeddingBudget|TestNewServer' && go vet ./...`
Expected: PASS. Any existing `NewServer` test that sets `EmbeddingModel` now needs `EmbeddingContextTokens: "2048"` in its `Config`; `grep -rn 'EmbeddingModel:' internal/api/*_test.go` lists them.

- [ ] **Step 8: Deployment and docs carry the new variable**

`deploy/base/configmap.yaml`, after the `LODE_EMBEDDING_MODEL: "/model"` line:

```yaml
  # The model's context window in tokens (07 §14.4). EmbeddingGemma is 2048;
  # the chunk budget derives from this number.
  LODE_EMBEDDING_CONTEXT_TOKENS: "2048"
```

`docker-compose.yml`, after the `LODE_EMBEDDING_MODEL` line:

```yaml
      LODE_EMBEDDING_CONTEXT_TOKENS: ${LODE_EMBEDDING_CONTEXT_TOKENS:-}
```

`README.md` around line 586: change "Its dense arm needs `LODE_EMBEDDING_URL` and `LODE_EMBEDDING_MODEL` on the server" to "Its dense arm needs `LODE_EMBEDDING_URL`, `LODE_EMBEDDING_MODEL` and `LODE_EMBEDDING_CONTEXT_TOKENS` (the model's context window, which sizes the chunks) on the server".

- [ ] **Step 9: Full test run and commit**

Run: `make test`
Expected: PASS.

```bash
git add internal/corpusindex internal/indexer internal/api/server.go internal/api/chunkbudget_test.go deploy/base/configmap.yaml docker-compose.yml README.md
git commit -m "Derive the chunk budget from LODE_EMBEDDING_CONTEXT_TOKENS (S7)"
```

---

### Task 3: Migration for clauses, clause versions, arrangements and governing links

**Files:**
- Create: `deploy/base/migrations/0081_clauses.up.sql`
- Create: `deploy/base/migrations/0081_clauses.down.sql`

**Interfaces:**
- Produces: tables `clauses`, `clause_versions`, `doc_clauses`, `task_governed_by` as below. Tasks 4 and 7 write them, Tasks 5 and 7 read them.

- [ ] **Step 1: Write the up migration**

```sql
-- Design clauses (docs/specs2/12-spec-refactoring-design-tree.md S8 to S11,
-- S20): every anchored section of a spec or ADR is a clause with a project
-- counter number (WL-CL-<n>, drawn from project_entity_seq kind 'CL'), a
-- status of its own and immutable versions. A document arranges clauses in
-- order; the arrangement is rebuilt on every body write.
CREATE TABLE clauses (
    id          bigserial PRIMARY KEY,
    project_id  text NOT NULL REFERENCES projects(id),
    number      bigint NOT NULL,
    status      text NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'accepted', 'superseded', 'withdrawn')),
    version     integer NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, number)
);

-- One row per version; a version's text never changes.
CREATE TABLE clause_versions (
    clause_id   bigint NOT NULL REFERENCES clauses(id) ON DELETE CASCADE,
    version     integer NOT NULL,
    heading     text NOT NULL,
    body        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (clause_id, version)
);

-- The document's current arrangement: which clause sits at which position and
-- heading depth, under which anchor. Rewritten with doc_sections.
CREATE TABLE doc_clauses (
    doc_id          bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    position        integer NOT NULL,
    clause_id       bigint NOT NULL REFERENCES clauses(id),
    clause_version  integer NOT NULL,
    depth           integer NOT NULL,
    anchor          text NOT NULL,
    PRIMARY KEY (doc_id, position),
    FOREIGN KEY (clause_id, clause_version) REFERENCES clause_versions(clause_id, version)
);
CREATE INDEX doc_clauses_clause ON doc_clauses (clause_id);

-- A task's governing clauses (S2, S3): direct links, materialised when a plan
-- mints the task from the plan's coverage, or added by hand. The clause
-- version current when the link was made is recorded; the link itself
-- resolves to the clause's newest version (S10).
CREATE TABLE task_governed_by (
    task_id         text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    clause_id       bigint NOT NULL REFERENCES clauses(id),
    clause_version  integer NOT NULL,
    source          text NOT NULL CHECK (source IN ('plan', 'manual')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, clause_id)
);
CREATE INDEX task_governed_by_clause ON task_governed_by (clause_id);
```

- [ ] **Step 2: Write the down migration**

```sql
DROP TABLE task_governed_by;
DROP TABLE doc_clauses;
DROP TABLE clause_versions;
DROP TABLE clauses;
```

- [ ] **Step 3: Check numbering and that the store tests still run**

Run: `./scripts/check-migrations.sh --no-fix && go test -trimpath ./internal/store -run TestCreateDoc`
Expected: the script reports no collision; the store tests apply the migration on their fresh database and PASS.

- [ ] **Step 4: Commit**

```bash
git add deploy/base/migrations/0081_clauses.up.sql deploy/base/migrations/0081_clauses.down.sql
git commit -m "Add clauses, clause_versions, doc_clauses and task_governed_by tables"
```

---

### Task 4: Mint and version clauses on every document write; accept them with the document

**Files:**
- Create: `internal/store/clauses.go`
- Create: `internal/store/clauses_test.go`
- Modify: `internal/store/docs.go:766-770` (call `syncClauses` in `rebuildSectionsFrom`), `:236-239` and `:434-437` (use `publishDocSections`)
- Modify: `internal/store/docpatch.go:360-363`, `internal/store/docrevisions.go:249-252` (use `publishDocSections`)

**Interfaces:**
- Consumes: `rebuildSectionsFrom(tx *sql.Tx, docID int64, kind string, doc *designdoc.Document, version int, prior map[string]priorSection)`, the four `UPDATE doc_sections SET published = true WHERE doc_id = $1` sites, `project_entity_seq (project_id, kind, next)`.
- Produces: `syncClauses(tx *sql.Tx, docID int64, doc *designdoc.Document) error`, `publishDocSections(tx *sql.Tx, docID int64) error`, `clauseRow` and `arrangedClauses(tx, docID) ([]clauseRow, error)` (Task 7 reuses them). Test helpers `arrangementOf(t, s, docID) []arranged`, `clauseDocV1`.

- [ ] **Step 1: Write the failing store test**

Create `internal/store/clauses_test.go`:

```go
package store

import (
	"context"
	"strings"
	"testing"
)

// arranged is one row of a document's arrangement as the tests read it back.
type arranged struct {
	Position, Depth int
	Number          int64
	ClauseVersion   int
	Anchor          string
	Status          string
}

func arrangementOf(t *testing.T, s *Store, docID int64) []arranged {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT dc.position, dc.depth, c.number, dc.clause_version, dc.anchor, c.status
		   FROM doc_clauses dc JOIN clauses c ON c.id = dc.clause_id
		  WHERE dc.doc_id = $1 ORDER BY dc.position`, docID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []arranged
	for rows.Next() {
		var a arranged
		if err := rows.Scan(&a.Position, &a.Depth, &a.Number, &a.ClauseVersion, &a.Anchor, &a.Status); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func assertArrangement(t *testing.T, got, want []arranged) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("arrangement has %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

const clauseDocV1 = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// TestSyncClausesMintsVersionsAndKeepsIdentity: a create mints one clause per
// anchored section in order; an edit versions only the changed clause, keeps
// the unchanged ones at their version, and mints a new number for a new
// section; an anchor change with the same heading keeps the clause (S8 to
// S10, S20).
func TestSyncClausesMintsVersionsAndKeepsIdentity(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
	})

	v2 := strings.Replace(clauseDocV1, "C.\n", "C changed.\n", 1) + "\n## 3. Three {#sec-3}\n\nD.\n"
	if _, err := updateDocBody(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 2, "sec-2", "draft"},
		{3, 2, 4, 1, "sec-3", "draft"},
	})

	v3 := strings.Replace(v2, "## 3. Three {#sec-3}", "## 2a. Three {#sec-2a}", 1)
	if _, err := updateDocBody(t, s, d.ID, v3); err != nil {
		t.Fatal(err)
	}
	got := arrangementOf(t, s, d.ID)
	if len(got) != 4 || got[3].Number != 4 || got[3].Anchor != "sec-2a" || got[3].ClauseVersion != 1 {
		t.Errorf("anchor change with the same heading should keep clause 4 at v1: %+v", got)
	}

	var versions int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM clause_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 5 {
		t.Errorf("clause_versions has %d rows, want 5 (four clauses, one bumped)", versions)
	}
}

// TestSyncClausesSkipsPlans: plans carry no anchors and arrange nothing in
// this increment.
func TestSyncClausesSkipsPlans(t *testing.T) {
	s := openDocStore(t)
	body := "---\nstatus: draft\ncovers: NO-SPEC\n---\n# P\n\n## Tasks\n\n### Task 1 — Do it\n\n```yaml\nkind: chore\n```\n\nText.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", Body: body, CreatedBy: "stig"})
	if got := arrangementOf(t, s, d.ID); len(got) != 0 {
		t.Errorf("plan arranged %d clauses, want 0", len(got))
	}
}

// TestAcceptDocAcceptsItsClauses: accepting a document accepts every clause it
// arranges and writes nothing else (S11).
func TestAcceptDocAcceptsItsClauses(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig", Owner: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	for _, a := range arrangementOf(t, s, d.ID) {
		if a.Status != "accepted" {
			t.Errorf("clause %d status = %s, want accepted", a.Number, a.Status)
		}
	}
}
```

If `acceptDoc` (docs_test.go:755) needs the document submitted first, call the same helper the existing accept tests call before it; read `TestAcceptDoc*` in `docs_test.go` and copy its preparation steps exactly.

- [ ] **Step 2: Run it to see it fail**

Run: `go test -trimpath ./internal/store -run 'TestSyncClauses|TestAcceptDocAcceptsItsClauses' -v`
Expected: FAIL. The arrangement is empty (`arrangement has 0 rows, want 3`) because nothing writes `doc_clauses` yet.

- [ ] **Step 3: Write the clause sync**

Create `internal/store/clauses.go`:

```go
package store

import (
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// clauseRow is one clause as a document's current arrangement holds it.
type clauseRow struct {
	id      int64
	version int
	heading string
	body    string
	anchor  string
	depth   int
}

// syncClauses makes a document's clauses agree with its parsed source
// (12-spec-refactoring-design-tree.md S8 to S11, S20). Every anchored section
// is a design clause. A section whose anchor, or failing that whose heading,
// names a clause the document already arranges keeps that clause; changed
// text is a new version of it; anything else is a new clause numbered from
// the project's CL counter. The arrangement (doc_clauses) is rewritten in
// section order. Plans never reach here: rebuildSectionsFrom returns before
// calling it.
func syncClauses(tx *sql.Tx, docID int64, doc *designdoc.Document) error {
	var project string
	if err := tx.QueryRow(`SELECT project_id FROM docs WHERE id = $1`, docID).Scan(&project); err != nil {
		return fmt.Errorf("project of doc %d: %w", docID, err)
	}
	prior, err := arrangedClauses(tx, docID)
	if err != nil {
		return err
	}
	byAnchor := map[string]*clauseRow{}
	byHeading := map[string]*clauseRow{}
	for i := range prior {
		byAnchor[prior[i].anchor] = &prior[i]
		if _, dup := byHeading[prior[i].heading]; !dup {
			byHeading[prior[i].heading] = &prior[i]
		}
	}
	used := map[int64]bool{}

	if _, err := tx.Exec(`DELETE FROM doc_clauses WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("clear arrangement of doc %d: %w", docID, err)
	}
	position := 0
	for _, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		match := byAnchor[sec.Anchor]
		if match == nil {
			match = byHeading[sec.Title]
		}
		if match != nil && used[match.id] {
			match = nil
		}
		var id int64
		var version int
		switch {
		case match == nil:
			id, version, err = insertClause(tx, project, sec.Title, sec.Body)
		case match.heading != sec.Title || match.body != sec.Body:
			id, version, err = bumpClause(tx, match.id, sec.Title, sec.Body)
		default:
			id, version = match.id, match.version
		}
		if err != nil {
			return err
		}
		used[id] = true
		if _, err := tx.Exec(
			`INSERT INTO doc_clauses (doc_id, position, clause_id, clause_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, position, id, version, sec.Level, sec.Anchor); err != nil {
			return fmt.Errorf("arrange clause %d in doc %d: %w", id, docID, err)
		}
		position++
	}
	return nil
}

// arrangedClauses reads a document's current arrangement with each clause's
// arranged version text, in position order.
func arrangedClauses(tx *sql.Tx, docID int64) ([]clauseRow, error) {
	rows, err := tx.Query(
		`SELECT dc.clause_id, dc.clause_version, cv.heading, cv.body, dc.anchor, dc.depth
		   FROM doc_clauses dc
		   JOIN clause_versions cv ON cv.clause_id = dc.clause_id AND cv.version = dc.clause_version
		  WHERE dc.doc_id = $1
		  ORDER BY dc.position`, docID)
	if err != nil {
		return nil, fmt.Errorf("read arrangement of doc %d: %w", docID, err)
	}
	defer rows.Close()
	var out []clauseRow
	for rows.Next() {
		var c clauseRow
		if err := rows.Scan(&c.id, &c.version, &c.heading, &c.body, &c.anchor, &c.depth); err != nil {
			return nil, fmt.Errorf("scan arrangement of doc %d: %w", docID, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// insertClause mints a clause at version 1 with the project's next CL number.
// The upsert is the same counter CreateDoc uses for document numbers, held
// under the row lock for the rest of the transaction.
func insertClause(tx *sql.Tx, project, heading, body string) (int64, int, error) {
	var number int64
	if err := tx.QueryRow(
		`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, 'CL', 2)
		 ON CONFLICT (project_id, kind) DO UPDATE SET next = project_entity_seq.next + 1
		 RETURNING next - 1`, project).Scan(&number); err != nil {
		return 0, 0, fmt.Errorf("allocate clause number in %s: %w", project, err)
	}
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO clauses (project_id, number) VALUES ($1, $2) RETURNING id`,
		project, number).Scan(&id); err != nil {
		return 0, 0, fmt.Errorf("insert clause %s CL %d: %w", project, number, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO clause_versions (clause_id, version, heading, body) VALUES ($1, 1, $2, $3)`,
		id, heading, body); err != nil {
		return 0, 0, fmt.Errorf("insert clause %d v1: %w", id, err)
	}
	return id, 1, nil
}

// bumpClause records changed text as the clause's next version (S10).
func bumpClause(tx *sql.Tx, id int64, heading, body string) (int64, int, error) {
	var version int
	if err := tx.QueryRow(
		`UPDATE clauses SET version = version + 1, updated_at = now() WHERE id = $1 RETURNING version`,
		id).Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("bump clause %d: %w", id, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO clause_versions (clause_id, version, heading, body) VALUES ($1, $2, $3, $4)`,
		id, version, heading, body); err != nil {
		return 0, 0, fmt.Errorf("insert clause %d v%d: %w", id, version, err)
	}
	return id, version, nil
}

// publishDocSections marks a document's sections published and its arranged
// clauses accepted. Accepting a document accepts every draft clause it
// arranges and writes nothing else (S11).
func publishDocSections(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(`UPDATE doc_sections SET published = true WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("publish sections of doc %d: %w", docID, err)
	}
	if _, err := tx.Exec(
		`UPDATE clauses SET status = 'accepted', updated_at = now()
		  WHERE status = 'draft'
		    AND id IN (SELECT clause_id FROM doc_clauses WHERE doc_id = $1)`, docID); err != nil {
		return fmt.Errorf("accept clauses of doc %d: %w", docID, err)
	}
	return nil
}
```

- [ ] **Step 4: Hook the sync into the section rebuild and the four publish sites**

In `internal/store/docs.go`, in `rebuildSectionsFrom`, directly after the `if kind == "plan" { return after, nil }` block (around line 769), add:

```go
	if err := syncClauses(tx, docID, doc); err != nil {
		return nil, err
	}
```

Replace each of the four `UPDATE doc_sections SET published = true WHERE doc_id = $1` statements and their error wrapping with a call to `publishDocSections`. At `docs.go:236-239`:

```go
	if acceptedAtCreate {
		if err := publishDocSections(tx, id); err != nil {
			return nil, err
		}
	}
```

At `docs.go:434-437`, `docpatch.go:360-363` and `docrevisions.go:249-252` the same shape: `if err := publishDocSections(tx, <the doc id variable in scope>); err != nil { return <the function's zero values>, err }`. Keep every other line of those functions as it was.

- [ ] **Step 5: Run the new tests and the whole store suite**

Run: `go test -trimpath ./internal/store -run 'TestSyncClauses|TestAcceptDocAcceptsItsClauses' -v`
Expected: PASS.

Run: `go test -trimpath -race -count=1 ./internal/store`
Expected: PASS. If a test that counts `project_entity_seq` rows or asserts on `doc_sections` publish messages fails, it is asserting on text this task changed; update the assertion, never the behaviour.

- [ ] **Step 6: Commit**

```bash
git add internal/store/clauses.go internal/store/clauses_test.go internal/store/docs.go internal/store/docpatch.go internal/store/docrevisions.go
git commit -m "Mint and version design clauses on every document write (S8-S11, S20)"
```

---

### Task 5: Read a clause: model, store, API

**Files:**
- Create: `internal/model/clause.go`
- Modify: `internal/store/clauses.go` (append the read)
- Create: `internal/api/clauses.go`
- Modify: `internal/api/router.go:286-300` (one `routeGuards` entry), `internal/api/server.go:847` region (one `r.api` registration)
- Test: `internal/store/clauses_test.go` (append), `internal/api/clauses_test.go` (create)

**Interfaces:**
- Consumes: tables from Task 3, `ErrNotFound`, `s.mapStoreErr`, `writeJSON`, `writeErr`, `guardedAny(permDocRead)`, `r.api(pattern, handler)`.
- Produces: `model.Clause`, `model.ClauseArrangement`, `(*Store).GetClause(ctx, projectKey string, number int64) (*model.Clause, error)`, `parseClauseRef(ref string) (key string, number int64, ok bool)` in `internal/api` (Task 8 reuses it), `GET /api/v1/clauses/{id}` where `{id}` is `WL-CL-12`.

- [ ] **Step 1: Declare the wire shape**

Create `internal/model/clause.go`:

```go
package model

import "time"

// Clause is a design clause (docs/specs2/12-spec-refactoring-design-tree.md
// S8 to S11, S20): the lowest heading unit of a spec or ADR, with its own
// identity, status and version history. Heading and Body are the current
// version's text.
type Clause struct {
	ID         int64  `json:"id"`
	Project    string `json:"project"`
	ProjectKey string `json:"project_key"`
	// Ref is the citable id, "WL-CL-12" (025 §14.3 grammar, type CL).
	Ref     string `json:"ref"`
	Number  int64  `json:"number"`
	Status  string `json:"status"` // draft | accepted | superseded | withdrawn
	Version int    `json:"version"`
	Heading string `json:"heading"`
	Body    string `json:"body"`
	// ArrangedIn lists the documents whose current arrangement holds this
	// clause, and at which version, position and depth.
	ArrangedIn []ClauseArrangement `json:"arranged_in"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

// ClauseArrangement is one document's placement of a clause.
type ClauseArrangement struct {
	Doc           int64  `json:"doc"`
	DocRef        string `json:"doc_ref"` // "WL-SPEC-4"
	Anchor        string `json:"anchor"`
	Position      int    `json:"position"`
	Depth         int    `json:"depth"`
	ClauseVersion int    `json:"clause_version"`
}
```

Run: `go test -trimpath ./internal/model`
Expected: PASS (the rule tests accept it: stdlib imports only, wire names).

- [ ] **Step 2: Write the failing store test**

Append to `internal/store/clauses_test.go`:

```go
// TestGetClause reads a clause by project key and number with its current
// text and the documents arranging it.
func TestGetClause(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	c, err := s.GetClause(context.Background(), "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ref != "P1-CL-2" || c.Heading != "Sub" || c.Body != "\nB.\n\n" || c.Status != "draft" || c.Version != 1 {
		t.Errorf("clause = %+v", c)
	}
	if len(c.ArrangedIn) != 1 || c.ArrangedIn[0].Doc != d.ID || c.ArrangedIn[0].DocRef != "P1-SPEC-1" ||
		c.ArrangedIn[0].Anchor != "sec-1.1" || c.ArrangedIn[0].Depth != 3 || c.ArrangedIn[0].Position != 1 {
		t.Errorf("arranged_in = %+v", c.ArrangedIn)
	}
	if _, err := s.GetClause(context.Background(), "P1", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing clause: err = %v, want ErrNotFound", err)
	}
}
```

Add `"errors"` to the test file's imports. The expected `Body` is the raw source between the heading line and the next heading, exactly as `designdoc.Section.Body` holds it; if the assertion fails only on whitespace, print `%q` and correct the expectation to what `Parse` yields rather than trimming in the store.

Run: `go test -trimpath ./internal/store -run TestGetClause`
Expected: compile error, `s.GetClause undefined`.

- [ ] **Step 3: Write the store read**

Append to `internal/store/clauses.go` (add `"context"`, `"errors"`, `"strings"`, `"github.com/sunstoneinstitute/worklode/internal/model"` to its imports):

```go
// GetClause reads one clause by its project key and number, with the current
// version's text and every document whose arrangement holds it.
func (s *Store) GetClause(ctx context.Context, projectKey string, number int64) (*model.Clause, error) {
	c := &model.Clause{}
	err := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.project_id, p.key, c.number, c.status, c.version,
		        cv.heading, cv.body, c.created_at, c.updated_at
		   FROM clauses c
		   JOIN projects p ON p.id = c.project_id
		   JOIN clause_versions cv ON cv.clause_id = c.id AND cv.version = c.version
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number,
	).Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Status, &c.Version,
		&c.Heading, &c.Body, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read clause %s-CL-%d: %w", projectKey, number, err)
	}
	c.Ref = fmt.Sprintf("%s-CL-%d", c.ProjectKey, c.Number)

	rows, err := s.db.QueryContext(ctx,
		`SELECT dc.doc_id, p.key, d.kind, d.number, dc.anchor, dc.position, dc.depth, dc.clause_version
		   FROM doc_clauses dc
		   JOIN docs d ON d.id = dc.doc_id
		   JOIN projects p ON p.id = d.project_id
		  WHERE dc.clause_id = $1
		  ORDER BY dc.doc_id, dc.position`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read arrangements of clause %d: %w", c.ID, err)
	}
	defer rows.Close()
	c.ArrangedIn = []model.ClauseArrangement{}
	for rows.Next() {
		var a model.ClauseArrangement
		var key, kind string
		var docNumber sql.NullInt64
		if err := rows.Scan(&a.Doc, &key, &kind, &docNumber, &a.Anchor, &a.Position, &a.Depth, &a.ClauseVersion); err != nil {
			return nil, fmt.Errorf("scan arrangement of clause %d: %w", c.ID, err)
		}
		a.DocRef = fmt.Sprintf("%s-%s-%d", key, strings.ToUpper(kind), docNumber.Int64)
		c.ArrangedIn = append(c.ArrangedIn, a)
	}
	return c, rows.Err()
}

// ClauseIDByRef resolves a clause's project key and number to its row id
// inside a transaction, or ErrNotFound.
func ClauseIDByRef(tx *sql.Tx, projectKey string, number int64) (int64, error) {
	var id int64
	err := tx.QueryRow(
		`SELECT c.id FROM clauses c JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("clause %s-CL-%d: %w", projectKey, number, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve clause %s-CL-%d: %w", projectKey, number, err)
	}
	return id, nil
}
```

Run: `go test -trimpath ./internal/store -run TestGetClause`
Expected: PASS.

- [ ] **Step 4: Write the failing API test**

Create `internal/api/clauses_test.go`. Build the server with `newTestServer(t)` (server_test.go:64, returns store, handler, admin token), seed a project with `seedProjectWithKey(t, st, "WL")` (probe_test.go:17, returns the project id), create a spec with `doReq(t, h, http.MethodPost, "/api/v1/docs", token, map[string]any{"project": <id>, "kind": "spec", "slug": "t", "body": <the clauseDocV1 text from Task 4>})` (check the request body field names against `model.CreateDocInput` or whatever `POST /api/v1/docs` decodes into, `grep -n 'func (s \*server) createDoc' -A 12 internal/api/docs.go`), then:

```go
	rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var c model.Clause
	decodeInto(t, rr, &c)
	if c.Ref != "WL-CL-1" || c.Heading != "One" {
		t.Errorf("clause = %+v", c)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/nonsense", token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed ref: status = %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-999", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("missing clause: status = %d", rr.Code)
	}
```

Run: `go test -trimpath ./internal/api -run TestGetClause`
Expected: FAIL with 404 or a route-guard boot error, because the route does not exist yet.

- [ ] **Step 5: Write the handler, guard and registration**

Create `internal/api/clauses.go`:

```go
package api

import (
	"net/http"
	"regexp"
	"strconv"
)

// clauseRef is the CL arm of 025 §14.3's <KEY>-<TYPE>-<n> grammar.
var clauseRef = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-CL-(\d+)$`)

// parseClauseRef splits "WL-CL-12" into its project key and number.
func parseClauseRef(ref string) (key string, number int64, ok bool) {
	m := clauseRef.FindStringSubmatch(ref)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// getClause handles GET /api/v1/clauses/{id}, where {id} is a clause ref
// such as WL-CL-12.
func (s *server) getClause(w http.ResponseWriter, r *http.Request) {
	key, number, ok := parseClauseRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause id must look like WL-CL-12")
		return
	}
	c, err := s.st.GetClause(r.Context(), key, number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
```

In `internal/api/router.go`, in the docs block of `routeGuards` (after line 294), add:

```go
	"GET /api/v1/clauses/{id}": guardedAny(permDocRead),
```

In `internal/api/server.go`, next to `r.api("GET /api/v1/docs/{id}/versions/{n}", s.getDocVersion)` (line 847), add:

```go
	r.api("GET /api/v1/clauses/{id}", s.getClause)
```

- [ ] **Step 6: Run the API tests**

Run: `go test -trimpath ./internal/api -run 'TestGetClause|TestRouteGuards|TestNewServer'`
Expected: PASS. A route-table test that lists every route (grep `routeGuards` in `internal/api/*_test.go`) may need the new entry added to its expected set.

- [ ] **Step 7: Commit**

```bash
git add internal/model/clause.go internal/store/clauses.go internal/store/clauses_test.go internal/api/clauses.go internal/api/clauses_test.go internal/api/router.go internal/api/server.go
git commit -m "Read a clause by ref over GET /api/v1/clauses/{id}"
```

---

### Task 6: `lode show WL-CL-<n>`

**Files:**
- Create: `internal/cli/clauses.go`
- Modify: `internal/cmd/show.go:29-44` (a `targetClause` kind), `:63-79` (`classify` case `"CL"`), `dispatchShowPositional` (dispatch), plus a `runClauseShow` function
- Test: `internal/cmd/show_test.go:20-40` (one `TestClassify` row), `internal/cli/clauses_test.go` (create)

**Interfaces:**
- Consumes: `model.Clause`, `doJSON[T]`, `newAPIClient()`, `jsonOut(cmd)`, `printRaw(cmd, raw)`, `cli.Markdown(w, body)`, `cli.LocalTime`.
- Produces: `(*cli.Client).GetClause(ctx, ref string) (model.Clause, []byte, error)`, `cli.ClauseRender(w io.Writer, c model.Clause)`.

- [ ] **Step 1: Write the failing render test**

Create `internal/cli/clauses_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestClauseRender(t *testing.T) {
	c := model.Clause{
		Ref: "WL-CL-12", Status: "accepted", Version: 3, Heading: "Lease lifecycle",
		Body: "\nA lease is renewed every minute.\n",
		ArrangedIn: []model.ClauseArrangement{{DocRef: "WL-SPEC-4", Anchor: "sec-2", Depth: 2, ClauseVersion: 3}},
		UpdatedAt:  time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
	}
	var b bytes.Buffer
	ClauseRender(&b, c)
	out := b.String()
	for _, want := range []string{"WL-CL-12", "Lease lifecycle", "accepted", "version:  3", "WL-SPEC-4#sec-2", "renewed every minute"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
}
```

Run: `go test -trimpath ./internal/cli -run TestClauseRender`
Expected: compile error, `undefined: ClauseRender`.

- [ ] **Step 2: Write the client and the render**

Create `internal/cli/clauses.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// GetClause calls GET /api/v1/clauses/{ref}: one design clause with its
// current text and the documents arranging it.
func (c *Client) GetClause(ctx context.Context, ref string) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodGet, "/api/v1/clauses/"+url.PathEscape(ref), nil, "clause")
}

// ClauseRender is the human view of one clause: its ref and heading, status
// and version, where it is arranged, then its text.
func ClauseRender(w io.Writer, c model.Clause) {
	fmt.Fprintf(w, "%s  %s\n", c.Ref, c.Heading)
	fmt.Fprintf(w, "  status:   %s\n", c.Status)
	fmt.Fprintf(w, "  version:  %d\n", c.Version)
	fmt.Fprintf(w, "  updated:  %s\n", LocalTime(c.UpdatedAt))
	for _, a := range c.ArrangedIn {
		fmt.Fprintf(w, "  arranged: %s#%s (depth %d, v%d)\n", a.DocRef, a.Anchor, a.Depth, a.ClauseVersion)
	}
	if c.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, c.Body)
	}
}
```

Run: `go test -trimpath ./internal/cli -run TestClauseRender`
Expected: PASS.

- [ ] **Step 3: Teach `lode show` the CL type**

In `internal/cmd/show.go`:

Add to the `targetKind` constants, after `targetDeliverable`:

```go
	// targetClause: a CL id — dispatch to runClauseShow.
	targetClause
```

In `classify`, add a case to the `switch typ`:

```go
		case "CL":
			return showTarget{Kind: targetClause}
```

Add the runner next to `runDeliverableShow`:

```go
// runClauseShow renders one design clause by its ref (WL-CL-12).
func runClauseShow(cmd *cobra.Command, ref string) error {
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	clause, raw, err := c.GetClause(cmd.Context(), ref)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	cli.ClauseRender(cmd.OutOrStdout(), clause)
	return nil
}
```

In `dispatchShowPositional`, the `switch t.Kind` has `case targetDeliverable: return runDeliverableShow(cmd, arg)`. Add the sibling case:

```go
	case targetClause:
		return runClauseShow(cmd, arg)
```

The `--section` and `--inline` flags stay doc-only; the existing check `if sectionSet && t.Kind != targetDoc` already refuses them for a clause.

In `internal/cmd/show_test.go` `TestClassify`, add the row `{"WL-CL-12", targetClause, ""},` after the `WL-DEL-3` row.

- [ ] **Step 4: Run the cmd tests and try the binary**

Run: `go test -trimpath ./internal/cmd -run 'TestClassify|TestShow' && make build && ./bin/lode show --help | head -5`
Expected: PASS, builds. If a `lode show` help text or `showKinds` test enumerates every accepted id shape, add `WL-CL-<n>` to it; do not add a `--clause` kind flag in this increment.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/clauses.go internal/cli/clauses_test.go internal/cmd/show.go internal/cmd/show_test.go
git commit -m "Show a design clause with lode show WL-CL-<n>"
```

---

### Task 7: `governedBy` in the store: minted from plan coverage, edited by hand, read back

**Files:**
- Create: `internal/model/governedby.go`
- Create: `internal/store/governedby.go`
- Create: `internal/store/governedby_test.go`
- Modify: `internal/store/docplanning.go:41-100` (`acceptPlanDoc` governs fresh tasks)

**Interfaces:**
- Consumes: `acceptPlanDoc(tx, now, id, d lockedDoc, actorID, eventID)` and its `fresh`/`taskID` maps, `CreateTask`, `syncClauses`, `arrangedClauses` and `clauseRow` from Task 4, `ClauseIDByRef` from Task 5, `pgViolation(err, code, constraint)` (errors.go:100), `doc_edges` rows of type `covers` (`from_doc` plan, `to_doc` spec, `to_anchor` nullable).
- Produces: `model.TaskGovernance`, `Govern(tx *sql.Tx, taskID string, clauseID int64, source string) error`, `Ungovern(tx *sql.Tx, taskID string, clauseID int64) error`, `(*Store).GovernedBy(ctx, taskID string) ([]model.TaskGovernance, error)`, `planClauses(tx *sql.Tx, planID int64) ([]int64, error)`, `ensureClauses(tx *sql.Tx, docID int64) error`.

- [ ] **Step 1: Declare the wire shape**

Create `internal/model/governedby.go`:

```go
package model

// TaskGovernance is one governing clause of a task
// (docs/specs2/12-spec-refactoring-design-tree.md S2, S10): the clause, the
// version the link was made against, and the clause's current version so a
// reader can see when the governing text has moved on.
type TaskGovernance struct {
	Clause        string `json:"clause"`         // "WL-CL-12"
	ClauseVersion int    `json:"clause_version"` // version current when the link was made
	Current       int    `json:"current"`        // the clause's current version
	Heading       string `json:"heading"`
	Status        string `json:"status"`
	Source        string `json:"source"` // plan | manual
}

// GovernInput names a clause to add to, or remove from, a task's governing
// set: the body of POST and DELETE /api/v1/tasks/{id}/governed-by.
type GovernInput struct {
	Clause string `json:"clause"` // "WL-CL-12"
}
```

Run: `go test -trimpath ./internal/model`
Expected: PASS.

- [ ] **Step 2: Write the failing store tests**

Create `internal/store/governedby_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// governingNumbers reads the CL numbers governing a task, in order.
func governingNumbers(t *testing.T, s *Store, taskID string) []int64 {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT c.number FROM task_governed_by g JOIN clauses c ON c.id = g.clause_id
		  WHERE g.task_id = $1 ORDER BY c.number`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A plan covering P1-SPEC-1 sec-1 only. The spec is clauseDocV1 (clauses.go
// tests): sec-1 is clause 1, its child sec-1.1 is clause 2, sec-2 is clause 3.
const governedPlanBody = "---\nstatus: draft\ncovers: [P1-SPEC-1#sec-1]\n---\n# Plan\n\n## Tasks\n\n### Task 1 — First\n\n```yaml\nkind: feature\n```\n\nDo it.\n\n### Task 2 — Second\n\n```yaml\nkind: chore\n```\n\nDo more.\n"

// TestAcceptPlanGovernsMintedTasks: accepting a plan gives every task it mints
// a governedBy link to each clause the plan's covers edges reach (S2). A
// section-scoped edge reaches the clause at that anchor and the clauses
// arranged under it, so covering sec-1 governs by clauses 1 and 2 and leaves
// clause 3 out.
func TestAcceptPlanGovernsMintedTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})

	var resolved int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND type = 'covers' AND to_doc IS NOT NULL`, plan.ID).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("plan's covers edge did not resolve to the spec (%d resolved rows); fix the covers ref in governedPlanBody", resolved)
	}

	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(minted) != 2 {
		t.Fatalf("minted %d tasks, want 2", len(minted))
	}
	for _, task := range minted {
		if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{1, 2}) {
			t.Errorf("%s governed by %v, want [1 2]", task.ID, got)
		}
	}

	var source string
	var linkVersion int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT source, clause_version FROM task_governed_by WHERE task_id = $1 ORDER BY clause_id LIMIT 1`,
		minted[0].ID).Scan(&source, &linkVersion); err != nil {
		t.Fatal(err)
	}
	if source != "plan" || linkVersion != 1 {
		t.Errorf("link source = %s v%d, want plan v1", source, linkVersion)
	}
}

// TestAcceptPlanSplitsCoveredDocOnFirstUse: a spec written before the clause
// tables existed has no arrangement yet; accepting a plan that covers it
// splits it first so the minted tasks still get their links.
func TestAcceptPlanSplitsCoveredDocOnFirstUse(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_clauses WHERE doc_id = $1`, spec.ID); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(arrangementOf(t, s, spec.ID)) != 3 {
		t.Errorf("covered spec was not split on first use")
	}
	if got := governingNumbers(t, s, minted[0].ID); len(got) != 2 {
		t.Errorf("%s governed by %v, want two clauses", minted[0].ID, got)
	}
}

// TestGovernAndUngovern: the architect adds and removes links by hand (S3);
// adding twice is a no-op, removing an absent link and naming an unknown
// task or clause are ErrNotFound.
func TestGovernAndUngovern(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})

	govern := func(number int64) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.governed", nil,
			func(tx *sql.Tx, _ int64) error {
				id, err := ClauseIDByRef(tx, "P1", number)
				if err != nil {
					return err
				}
				return Govern(tx, task.ID, id, "manual")
			})
		return err
	}
	if err := govern(3); err != nil {
		t.Fatal(err)
	}
	if err := govern(3); err != nil {
		t.Fatalf("second govern should be a no-op: %v", err)
	}
	if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{3}) {
		t.Errorf("governed by %v, want [3]", got)
	}
	if err := govern(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown clause: err = %v, want ErrNotFound", err)
	}

	list, err := s.GovernedBy(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Clause != "P1-CL-3" || list[0].Heading != "Two" || list[0].Source != "manual" ||
		list[0].ClauseVersion != 1 || list[0].Current != 1 {
		t.Errorf("GovernedBy = %+v", list)
	}

	ungovern := func(number int64) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.ungoverned", nil,
			func(tx *sql.Tx, _ int64) error {
				id, err := ClauseIDByRef(tx, "P1", number)
				if err != nil {
					return err
				}
				return Ungovern(tx, task.ID, id)
			})
		return err
	}
	if err := ungovern(3); err != nil {
		t.Fatal(err)
	}
	if err := ungovern(3); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing an absent link: err = %v, want ErrNotFound", err)
	}
	if got := governingNumbers(t, s, task.ID); len(got) != 0 {
		t.Errorf("governed by %v after ungovern, want none", got)
	}
}
```

`Store.RecordEvent(ctx, source, externalID, typ string, payload []byte, apply) (id int64, inserted bool, err error)` is at `events.go:59`. `createTask` and `nextExt` live in `tasks_test.go:61` and `:23`; `openDocStore` seeds project `p1` with key `P1` and actor `stig`, which `createTask` needs. If `createTask` requires a participant row (see `openTaskStore` at `tasks_test.go:30`), call `seedParticipant(t, s, "p1", "stig", "member", true)` first.

Run: `go test -trimpath ./internal/store -run 'TestAcceptPlanGoverns|TestAcceptPlanSplits|TestGovernAndUngovern' -v`
Expected: compile error, `undefined: Govern`, `Ungovern`, `s.GovernedBy`.

- [ ] **Step 3: Write the store side**

Create `internal/store/governedby.go`:

```go
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Govern links a task to a governing clause (12-spec-refactoring-design-tree.md
// S2, S3), recording the clause version current at link time (S10). source is
// "plan" when a plan's acceptance minted the link and "manual" when an
// architect added it. Linking an already governing clause changes nothing, so
// re-accepting a plan is safe.
func Govern(tx *sql.Tx, taskID string, clauseID int64, source string) error {
	_, err := tx.Exec(
		`INSERT INTO task_governed_by (task_id, clause_id, clause_version, source)
		 SELECT $1, c.id, c.version, $3 FROM clauses c WHERE c.id = $2
		 ON CONFLICT (task_id, clause_id) DO NOTHING`, taskID, clauseID, source)
	if pgViolation(err, "23503", "task_governed_by_task_id_fkey") {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("govern %s by clause %d: %w", taskID, clauseID, err)
	}
	return nil
}

// Ungovern removes one governing link, or reports ErrNotFound when the task
// is not governed by that clause.
func Ungovern(tx *sql.Tx, taskID string, clauseID int64) error {
	res, err := tx.Exec(`DELETE FROM task_governed_by WHERE task_id = $1 AND clause_id = $2`, taskID, clauseID)
	if err != nil {
		return fmt.Errorf("ungovern %s from clause %d: %w", taskID, clauseID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s is not governed by clause %d: %w", taskID, clauseID, ErrNotFound)
	}
	return nil
}

// GovernedBy lists a task's governing clauses with the version each link was
// made against and the clause's current version (S10).
func (s *Store) GovernedBy(ctx context.Context, taskID string) ([]model.TaskGovernance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.key, c.number, g.clause_version, c.version, cv.heading, c.status, g.source
		   FROM task_governed_by g
		   JOIN clauses c ON c.id = g.clause_id
		   JOIN projects p ON p.id = c.project_id
		   JOIN clause_versions cv ON cv.clause_id = c.id AND cv.version = c.version
		  WHERE g.task_id = $1
		  ORDER BY c.number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("read governing clauses of %s: %w", taskID, err)
	}
	defer rows.Close()
	out := []model.TaskGovernance{}
	for rows.Next() {
		var g model.TaskGovernance
		var key string
		var number int64
		if err := rows.Scan(&key, &number, &g.ClauseVersion, &g.Current, &g.Heading, &g.Status, &g.Source); err != nil {
			return nil, fmt.Errorf("scan governing clause of %s: %w", taskID, err)
		}
		g.Clause = fmt.Sprintf("%s-CL-%d", key, number)
		out = append(out, g)
	}
	return out, rows.Err()
}

// planClauses is every clause a plan's covers edges reach (S1, S2): a
// section-scoped edge reaches the clause arranged under that anchor and every
// clause arranged beneath it (a section is its whole subtree, 026 §3), a
// document-scoped edge reaches every clause the document arranges. A covered
// document that has no arrangement yet is split first.
func planClauses(tx *sql.Tx, planID int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT to_doc, coalesce(to_anchor, '') FROM doc_edges
		  WHERE from_doc = $1 AND type = 'covers' AND to_doc IS NOT NULL
		  ORDER BY to_doc, coalesce(to_anchor, '')`, planID)
	if err != nil {
		return nil, fmt.Errorf("read covers edges of plan %d: %w", planID, err)
	}
	type edge struct {
		doc    int64
		anchor string
	}
	var edges []edge
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.doc, &e.anchor); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan covers edge of plan %d: %w", planID, err)
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	arrangements := map[int64][]clauseRow{}
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, e := range edges {
		entries, ok := arrangements[e.doc]
		if !ok {
			if err := ensureClauses(tx, e.doc); err != nil {
				return nil, err
			}
			entries, err = arrangedClauses(tx, e.doc)
			if err != nil {
				return nil, err
			}
			arrangements[e.doc] = entries
		}
		if e.anchor == "" {
			for _, c := range entries {
				add(c.id)
			}
			continue
		}
		for i, c := range entries {
			if c.anchor != e.anchor {
				continue
			}
			add(c.id)
			for j := i + 1; j < len(entries) && entries[j].depth > c.depth; j++ {
				add(entries[j].id)
			}
			break
		}
	}
	return out, nil
}

// ensureClauses splits a spec or ADR that predates the clause tables, so a
// plan covering it can be governed by its clauses. A document written after
// the tables exist is split on every write and is left alone here.
func ensureClauses(tx *sql.Tx, docID int64) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM doc_clauses WHERE doc_id = $1`, docID).Scan(&n); err != nil {
		return fmt.Errorf("count arrangement of doc %d: %w", docID, err)
	}
	if n > 0 {
		return nil
	}
	var kind, body string
	if err := tx.QueryRow(`SELECT kind, body FROM docs WHERE id = $1`, docID).Scan(&kind, &body); err != nil {
		return fmt.Errorf("read doc %d: %w", docID, err)
	}
	if kind == "plan" {
		return nil
	}
	parsed, err := designdoc.Parse([]byte(body))
	if err != nil {
		return fmt.Errorf("parse doc %d: %w", docID, err)
	}
	return syncClauses(tx, docID, parsed)
}
```

- [ ] **Step 4: Govern the tasks a plan mints**

In `internal/store/docplanning.go` `acceptPlanDoc`, before the `for _, def := range defs` loop that mints (around line 66), add:

```go
	governing, err := planClauses(tx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve governing clauses of plan %d: %w", id, err)
	}
```

Inside that loop, directly after `fresh[def.Number] = true`, add:

```go
		for _, clauseID := range governing {
			if err := Govern(tx, task.ID, clauseID, "plan"); err != nil {
				return nil, nil, fmt.Errorf("govern task %d of plan %d: %w", def.Number, id, err)
			}
		}
```

If `err` is not yet declared at the insertion point, use `:=` on the first statement and `=` after. Only freshly minted tasks are governed, for the same reason only they get their `blocks` edges wired (the comment above the second loop).

- [ ] **Step 5: Run the tests**

Run: `go test -trimpath ./internal/store -run 'TestAcceptPlanGoverns|TestAcceptPlanSplits|TestGovernAndUngovern|TestDocAcceptPlan' -v`
Expected: PASS. If `TestAcceptPlanGovernsMintedTasks` fails at the `resolved != 1` guard, the `covers` ref did not resolve: try the slug form `covers: [t#sec-1]` and keep whichever form yields a resolved edge, noting which in the test's comment.

Run: `go test -trimpath -race -count=1 ./internal/store`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/model/governedby.go internal/store/governedby.go internal/store/governedby_test.go internal/store/docplanning.go
git commit -m "Govern minted tasks by the clauses their plan covers (S2, S3)"
```

---

### Task 8: `governedBy` over the API and the CLI

**Files:**
- Modify: `internal/model/taskdetail.go:57-70` (`GovernedBy` on `TaskDetail`), `internal/model/task.go:122-144` (`GovernedBy` on `CreateTaskInput`)
- Create: `internal/api/governedby.go`
- Modify: `internal/api/tasks.go:222` (fill `GovernedBy` in `getTask`), `:60-135` (`createTask` honours `GovernedBy`)
- Modify: `internal/api/router.go:221-222` (two `routeGuards` entries), `internal/api/server.go:805-806` (two registrations)
- Create: `internal/cli/governedby.go`
- Modify: `internal/cli/tasks.go:505-530` (`TaskDetailRender` prints governing clauses)
- Modify: `internal/cmd/task.go:50-58` (register two commands), `:298-361` (`--governed-by` on `task add`), plus `newTaskGovernCmd`, `newTaskUngovernCmd`, `newTaskClauseCmd`
- Modify: `internal/cmd/namerule_test.go:48-61` (`"govern": true`)
- Test: `internal/api/governedby_test.go` (create), `internal/cli/tasks_test.go` or `internal/cli/governedby_test.go` (render)

**Interfaces:**
- Consumes: `Govern`, `Ungovern`, `(*Store).GovernedBy`, `ClauseIDByRef` (Task 7 and 5), `parseClauseRef` (Task 5), `s.recordTaskEvent(ctx, source, eventType, taskID, v, apply)`, `readJSON`, `writeBodyErr`, `guardedBound(permTaskWrite)`, `c.do(ctx, method, path, body)`, `runTaskEdge`, `taskEdge`, `resolveTaskID`, `taskIDAt`.
- Produces: `TaskDetail.GovernedBy []TaskGovernance`, `CreateTaskInput.GovernedBy []string`, `POST /api/v1/tasks/{id}/governed-by`, `DELETE /api/v1/tasks/{id}/governed-by`, `(*cli.Client).Govern(ctx, id, clause string) ([]byte, error)`, `(*cli.Client).Ungovern(...)`, `lode task govern <id> --by <clause>`, `lode task ungovern <id> --by <clause>`, `lode task add --governed-by <clause>` (repeatable).

- [ ] **Step 1: Extend the wire shapes**

In `internal/model/taskdetail.go`, add to `TaskDetail` after `Decisions`:

```go
	// GovernedBy is the task's governing clauses (S2), empty when none.
	GovernedBy []TaskGovernance `json:"governed_by"`
```

In `internal/model/task.go`, add to `CreateTaskInput` after `FollowUpTo`:

```go
	// GovernedBy names clauses ("WL-CL-12") that govern the task from
	// creation. Optional: a planless task may acquire its clause later (S4).
	GovernedBy []string `json:"governed_by,omitempty"`
```

Run: `go test -trimpath ./internal/model`
Expected: PASS.

- [ ] **Step 2: Write the failing API test**

Create `internal/api/governedby_test.go`. Reuse the setup from Task 5's `clauses_test.go` (server, project `WL`, a spec created from `clauseDocV1`'s text so `WL-CL-1` to `WL-CL-3` exist), then create a task with `createTaskViaAPI(t, h, token, map[string]any{"project": <id>, "title": "t", "kind": "bug", "governed_by": []string{"WL-CL-3"}})` and assert:

```go
	id := created["id"].(string)
	detail := func() model.TaskDetail {
		rr := doReq(t, h, http.MethodGet, "/api/v1/tasks/"+id, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("get task: %d %s", rr.Code, rr.Body.String())
		}
		var d model.TaskDetail
		decodeInto(t, rr, &d)
		return d
	}
	if d := detail(); len(d.GovernedBy) != 1 || d.GovernedBy[0].Clause != "WL-CL-3" || d.GovernedBy[0].Source != "manual" {
		t.Fatalf("governed_by after create = %+v", d.GovernedBy)
	}

	rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-1"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("govern: %d %s", rr.Code, rr.Body.String())
	}
	if d := detail(); len(d.GovernedBy) != 2 || d.GovernedBy[0].Clause != "WL-CL-1" {
		t.Errorf("governed_by after govern = %+v", d.GovernedBy)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-999"}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown clause: %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "junk"}); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed clause: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodDelete, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-3"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ungovern: %d %s", rr.Code, rr.Body.String())
	}
	if d := detail(); len(d.GovernedBy) != 1 || d.GovernedBy[0].Clause != "WL-CL-1" {
		t.Errorf("governed_by after ungovern = %+v", d.GovernedBy)
	}
	if rr := doReq(t, h, http.MethodDelete, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-3"}); rr.Code != http.StatusNotFound {
		t.Errorf("ungovern absent link: %d", rr.Code)
	}
```

Run: `go test -trimpath ./internal/api -run TestGovernedBy`
Expected: FAIL (404 on the new route, or the unknown `governed_by` field ignored so the first assertion fails).

- [ ] **Step 3: Write the handlers**

Create `internal/api/governedby.go`:

```go
package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// govern handles POST /api/v1/tasks/{id}/governed-by: the architect adds a
// governing clause to a task by hand (12-spec-refactoring-design-tree.md S3).
// Every change is an event on the task.
func (s *server) govern(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GovernInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	key, number, ok := parseClauseRef(req.Clause)
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause must look like WL-CL-12")
		return
	}
	err := s.recordTaskEvent(r.Context(), "cli", "task.governed", id, req,
		func(tx *sql.Tx, eventID int64) error {
			clauseID, err := store.ClauseIDByRef(tx, key, number)
			if err != nil {
				return err
			}
			return store.Govern(tx, id, clauseID, "manual")
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// ungovern handles DELETE /api/v1/tasks/{id}/governed-by.
func (s *server) ungovern(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GovernInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	key, number, ok := parseClauseRef(req.Clause)
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause must look like WL-CL-12")
		return
	}
	err := s.recordTaskEvent(r.Context(), "cli", "task.ungoverned", id, req,
		func(tx *sql.Tx, eventID int64) error {
			clauseID, err := store.ClauseIDByRef(tx, key, number)
			if err != nil {
				return err
			}
			return store.Ungovern(tx, id, clauseID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

In `internal/api/tasks.go` `getTask`, after `decisions` is read and before `resp := model.TaskDetail{...}`, add:

```go
	governed, err := s.st.GovernedBy(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
```

and add `GovernedBy: governed` to the `model.TaskDetail{...}` literal.

In `createTask`, before the transaction (next to the `req.Parent` / `req.FollowUpTo` checks), validate the refs:

```go
	type clauseKey struct {
		key    string
		number int64
	}
	governing := make([]clauseKey, 0, len(req.GovernedBy))
	for _, ref := range req.GovernedBy {
		key, number, ok := parseClauseRef(ref)
		if !ok {
			writeErr(w, http.StatusBadRequest, "governed_by entries must look like WL-CL-12, got "+ref)
			return
		}
		governing = append(governing, clauseKey{key, number})
	}
```

and inside the `recordEvent` apply function, after `store.CreateTask` succeeds and before the existing `Parent`/`FollowUpTo` edge wiring:

```go
			for _, g := range governing {
				clauseID, err := store.ClauseIDByRef(tx, g.key, g.number)
				if err != nil {
					return err
				}
				if err := store.Govern(tx, t.ID, clauseID, "manual"); err != nil {
					return err
				}
			}
```

(`t` is whatever the handler names the task `store.CreateTask` returned.)

In `internal/api/router.go`, after the two `/edges` entries (lines 221-222):

```go
	"POST /api/v1/tasks/{id}/governed-by":    guardedBound(permTaskWrite),
	"DELETE /api/v1/tasks/{id}/governed-by":  guardedBound(permTaskWrite),
```

In `internal/api/server.go`, after the two `/edges` registrations (lines 805-806):

```go
	r.api("POST /api/v1/tasks/{id}/governed-by", s.govern)
	r.api("DELETE /api/v1/tasks/{id}/governed-by", s.ungovern)
```

Run: `go test -trimpath ./internal/api -run 'TestGovernedBy|TestRouteGuards|TestNewServer|TestCreateTask'`
Expected: PASS.

- [ ] **Step 4: Write the failing render test**

Create `internal/cli/governedby_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTaskDetailRenderGovernedBy(t *testing.T) {
	d := model.TaskDetail{Task: model.Task{ID: "WL-12", Title: "T", Project: "worklode", Priority: "medium", Kind: "bug", State: "ready"}}
	d.GovernedBy = []model.TaskGovernance{
		{Clause: "WL-CL-3", Heading: "Two", ClauseVersion: 1, Current: 1, Source: "plan"},
		{Clause: "WL-CL-7", Heading: "Seven", ClauseVersion: 2, Current: 4, Source: "manual"},
	}
	var b bytes.Buffer
	TaskDetailRender(&b, d, "")
	out := b.String()
	if !strings.Contains(out, "governed by: WL-CL-3  Two (v1)") {
		t.Errorf("missing current link line:\n%s", out)
	}
	if !strings.Contains(out, "governed by: WL-CL-7  Seven (v2, clause now v4)") {
		t.Errorf("missing moved-on link line:\n%s", out)
	}
}
```

Run: `go test -trimpath ./internal/cli -run TestTaskDetailRenderGovernedBy`
Expected: FAIL, the lines are absent.

- [ ] **Step 5: Client methods and the render line**

Create `internal/cli/governedby.go`:

```go
package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Govern calls POST /api/v1/tasks/{id}/governed-by: clause governs id.
func (c *Client) Govern(ctx context.Context, id, clause string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/governed-by", model.GovernInput{Clause: clause})
}

// Ungovern calls DELETE /api/v1/tasks/{id}/governed-by: clause no longer
// governs id.
func (c *Client) Ungovern(ctx context.Context, id, clause string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/governed-by", model.GovernInput{Clause: clause})
}
```

In `internal/cli/tasks.go` `TaskDetailRender`, after the `milestone` line (line 523-525), add:

```go
	for _, g := range t.GovernedBy {
		if g.Current != g.ClauseVersion {
			fmt.Fprintf(w, "  governed by: %s  %s (v%d, clause now v%d)\n", g.Clause, g.Heading, g.ClauseVersion, g.Current)
			continue
		}
		fmt.Fprintf(w, "  governed by: %s  %s (v%d)\n", g.Clause, g.Heading, g.ClauseVersion)
	}
```

Run: `go test -trimpath ./internal/cli`
Expected: PASS.

- [ ] **Step 6: The commands**

In `internal/cmd/task.go`, add next to `newTaskUnblockCmd` (line 1310):

```go
// newTaskClauseCmd builds a `lode task <verb> <id> --by <clause-ref>`
// command: the subject resolves as a task id, the clause ref is passed
// through as written (WL-CL-12) and the server validates it.
func newTaskClauseCmd(use, short, msg string, call taskEdge) *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:               use,
		Short:             short,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			return runTaskEdge(cmd, c, id, by, msg, call)
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "clause ref, e.g. WL-CL-12 (required)")
	cmd.MarkFlagRequired("by")
	return cmd
}

func newTaskGovernCmd() *cobra.Command {
	return newTaskClauseCmd("govern <id>",
		"Record a design clause that governs a task",
		"%s is now governed by %s", (*cli.Client).Govern)
}

func newTaskUngovernCmd() *cobra.Command {
	return newTaskClauseCmd("ungovern <id>",
		"Remove a governing clause from a task",
		"%s is no longer governed by %s", (*cli.Client).Ungovern)
}
```

Register both in `newTaskCmd`'s `AddCommand` list after `newTaskUnblockCmd(),` (line 54):

```go
		newTaskGovernCmd(),
		newTaskUngovernCmd(),
```

In `newTaskAddCmd` (line 298), declare `var governedBy []string`, add the flag next to `--follow-up-to`:

```go
	cmd.Flags().StringArrayVar(&governedBy, "governed-by", nil, "clause that governs the task, e.g. WL-CL-12 (repeatable)")
```

and set `GovernedBy: governedBy` in the `model.CreateTaskInput{...}` literal the command builds (line 331).

In `internal/cmd/namerule_test.go` `l3DomainActions`, add `"govern": true,` after `"block": true,`.

- [ ] **Step 7: Run the cmd tests and build**

Run: `go test -trimpath ./internal/cmd -run 'TestNameRule|TestClassify|TestTask' && make build && ./bin/lode task govern --help`
Expected: PASS, and the help shows `govern <id>` with the `--by` flag. If a test enumerates `lode task`'s subcommands or the `add` flags (grep `follow-up-to` in `internal/cmd/*_test.go`), add the new entries there.

- [ ] **Step 8: Commit**

```bash
git add internal/model/taskdetail.go internal/model/task.go internal/api/governedby.go internal/api/governedby_test.go internal/api/tasks.go internal/api/router.go internal/api/server.go internal/cli/governedby.go internal/cli/governedby_test.go internal/cli/tasks.go internal/cmd/task.go internal/cmd/namerule_test.go
git commit -m "Add and read governedBy on tasks over the API and lode task govern"
```

---

### Task 9: Specs 03, 05 and 09 record what was built

**Files:**
- Modify: `docs/specs2/03-tasks-and-execution.md:131-171` (§4 Edges: governing clauses)
- Modify: `docs/specs2/05-documents.md:38-56` (§3 table), `:57-83` (§4, append a paragraph)
- Modify: `docs/specs2/09-cli-and-skills.md:13` (L3 allowlist), `:49` (`task` row)

**Interfaces:** none. The specs catch up with Tasks 3 to 8 so they stop describing a store without clauses and a task without governing links.

- [ ] **Step 1: Governing clauses in 03 §4**

Append to the end of §4 (before `## 5. Hierarchy and decomposition`):

```markdown
**Governing clauses.** A task carries direct links to the design clauses that govern it (05-documents.md §4, 12-spec-refactoring-design-tree.md S2 and S3), stored in `task_governed_by (task_id, clause_id, clause_version, source)`. Accepting a plan links every task it mints to each clause the plan's `covers` edges reach: a section-scoped edge reaches the clause at that anchor and the clauses arranged under it, a document-scoped edge reaches every clause the document arranges. The plan is never the source of truth for the link afterwards; a stale or withdrawn plan leaves its tasks governed. `clause_version` records the clause version current when the link was made, and the link resolves to the clause's newest version. After minting, only an architect changes the links, by hand, and every change is an event on the task (`task.governed`, `task.ungoverned`). A task created without a plan may name its clauses at creation and may acquire them later.

| Surface | Effect |
|---|---|
| `POST /api/v1/tasks` with `governed_by: [WL-CL-12]` | create with links |
| `POST` / `DELETE /api/v1/tasks/{id}/governed-by` with `{"clause": "WL-CL-12"}` | add or remove one link |
| `GET /api/v1/tasks/{id}` field `governed_by` | the links with each clause's current version |
| `lode task add --governed-by WL-CL-12` | create with a link, repeatable |
| `lode task govern <id> --by WL-CL-12` / `ungovern` | add or remove one link |
| `lode show <id>` | one `governed by` line per link, noting when the clause has a newer version |
```

- [ ] **Step 2: Add the tables to 05 §3**

In the §3 table, after the `doc_sections` row, add four rows:

```markdown
| `clauses` | one design clause per anchored section of a spec or ADR: project, `number` (the `WL-CL-<n>` ref, from `project_entity_seq` kind `CL`), status, current version |
| `clause_versions` | `(clause_id, version)` primary key, `heading`, `body`. Append-only; a version's text never changes |
| `doc_clauses` | the document's current arrangement: `(doc_id, position)`, `clause_id`, `clause_version`, `depth`, `anchor`. Rewritten with `doc_sections` on every body write |
| `task_governed_by` | `(task_id, clause_id)`, `clause_version`, `source` (`plan` or `manual`): the clauses governing a task (03-tasks-and-execution.md §4) |
```

- [ ] **Step 3: Add the clause paragraph to 05 §4**

Append to the end of §4 (before `## 5. Versioning`):

```markdown
**Every anchored section is a design clause** (12-spec-refactoring-design-tree.md S8 to S11, S20). The store mints a clause the first time a section appears, numbered from the project's `CL` counter as `WL-CL-<n>`, and records the section's heading and body as version 1. On each later write, a section keeps its clause when its anchor matches, or failing that when its heading matches a clause the document already arranged; changed text becomes the clause's next version, and a section matching nothing becomes a new clause. Accepting a document accepts every draft clause it arranges and writes nothing else. `lode show WL-CL-<n>` and `GET /api/v1/clauses/WL-CL-<n>` read a clause with its current text and the documents arranging it. Tasks link to clauses through `governedBy` (03-tasks-and-execution.md §4). The document body stays the authored source in this stage; clause-first edits, typed edges between clauses and the migration of the old specs are later work.
```

- [ ] **Step 4: The verb in 09**

Line 13 (L3): add `govern` to the allowlist after `block`. Line 49 (the `task` row): add `govern`/`ungovern` after `block`/`unblock`, and `add --governed-by` is covered by `add`.

- [ ] **Step 5: Check the style and commit**

Run: `grep -n '—' docs/specs2/03-tasks-and-execution.md docs/specs2/05-documents.md docs/specs2/09-cli-and-skills.md | grep -v 'Task 1 —'`
Expected: no new hits from this task (the `### Task 1 — Short imperative title` example is the plan-task grammar and stays).

```bash
git add docs/specs2/03-tasks-and-execution.md docs/specs2/05-documents.md docs/specs2/09-cli-and-skills.md
git commit -m "Record clauses and governing links in specs 03, 05 and 09"
```

---

## Self-review

Spec coverage against the settled decisions this increment claims: S7 is Task 2; S8, S9, S10 and S20 are Tasks 3 and 4 (lowest heading unit, immutable versions, stable `WL-CL-<n>` from the project counter); S11 is `publishDocSections` in Task 4; S21 is Task 1; S1 and S2 are `planClauses` and the minting hook in Task 7 (a whole-document `covers` reaches all clauses, and the link is materialised on the task); S3 is `Govern`/`Ungovern` with `task.governed`/`task.ungoverned` events in Tasks 7 and 8; S4's create-time half is `CreateTaskInput.GovernedBy` in Task 8, its gate-and-reconciler half is deferred; S27 needs nothing yet because old clauses are not minted in this increment and the same counter will serve them. Everything else the design tree settled is listed as deferred at the top.

Type consistency: `syncClauses(tx, docID, doc)`, `publishDocSections(tx, docID)`, `arrangedClauses(tx, docID) ([]clauseRow, error)` with `clauseRow{id, version, heading, body, anchor, depth}` are the same in Tasks 4 and 7; `ClauseIDByRef(tx, key, number)` from Task 5 is what Tasks 7 and 8 call; `Govern(tx, taskID, clauseID, source)` and `Ungovern(tx, taskID, clauseID)` match between Task 7's tests, Task 7's code and Task 8's handlers; `parseClauseRef(ref) (key, number, ok)` from Task 5 is what Task 8 uses; `GetClause(ctx, projectKey string, number int64)` in Task 5's store matches the handler's call; `cli.GetClause(ctx, ref string)`, `cli.ClauseRender(w, c)` match `runClauseShow`; `cli.Govern`/`cli.Ungovern` have the `taskEdge` shape `func(*cli.Client, context.Context, string, string) ([]byte, error)` that `runTaskEdge` takes; `corpusindex.Budget`, `BudgetFor`, `DefaultBudget` and the `b Budget` first parameter are used consistently across Task 2's corpusindex, indexer and server steps.
