---
status: draft
covers: docs/specs/025-documents-in-the-backbone.md
---
# Design-doc sync, part 2 — the backbone document store

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** The minimal document store spec 025 §5.1 defines: the
`docs`/`doc_sections`/`doc_edges` schema, an idempotent upsert on
`(project, kind, ordinal)` with sync provenance and event-log attribution, the
read methods behind `/api/v1/docs`, and the store-level upsert metric.

**Architecture:** Pure `internal/store` + `deploy/base/migrations` work — no
HTTP surface yet (part 3,
`2026-08-09-design-doc-sync-3-sync-api-and-cli.md`, adds it). The upsert is a
`Store` method taking a `*sql.Tx` so the API handler can run it inside a
`RecordEvent` apply callback (the events pattern of `internal/store/events.go`),
with `LogChange` rows per changed document and nil-safe metrics on the
`storeMetrics` struct. Sections and edges are derived wholly from the document
body, so change detection compares only `body` + `frontmatter` (+
status/title), and a changed document replaces its sections/edges via
delete-and-insert. The store carries `status` as data — no editorial
transitions, no accept-mints-tasks (025 §5.1).

**Tech Stack:** Go 1.26, pgx via database/sql, golang-migrate file migrations
(NOT embedded — compose/K8s apply them), Postgres 17 + pgvector for tests,
prometheus/client_golang.

## Global constraints

- Store tests need Postgres (`TEST_POSTGRES_DSN`, default
  `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`);
  they skip silently without it unless `CI` is set — run them against a real
  Postgres before claiming green.
- Migrations: next free pair in `deploy/base/migrations/` (expected
  `0011_docs.*`, but run `./scripts/check-migrations.sh --no-fix` and take the
  next number if another branch claimed 0011); every new pair must be listed
  in `deploy/base/kustomization.yaml`. Never edit a shipped migration.
- Doc id grammar (025 §16.3): `<KEY>-SPEC-<n>` / `<KEY>-ADR-<n>` /
  `<KEY>-PLAN-<spec>-<plan>`. The ordinal arrives file-derived from the
  client; the server composes the id from the project's key so a client can
  never write an id inconsistent with its project.
- `kind ∈ {spec, adr, plan}` (025 §5.1); plans take no sections (025 §9).
- Upsert idempotence: re-syncing unchanged content is reported `unchanged`
  and bumps no version; provenance (`source_branch`, `source_dirty`,
  `synced_at`) is stamped on every sync regardless, so a default-branch sync
  overwrites a forced one (025 §16.2).
- Metrics per spec 022: `worklode_` prefix, nil-safe struct in the owning
  package's `metrics.go`, bounded label values, tests.
- Run `go build ./...` and the named tests before every commit. Never put
  `Co-authored-by` or any agent advertisement in commit messages.

## Tasks

### Task 1 — Migration: docs, doc_sections, doc_edges

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - golang-migrate:migration
```

**Files:**
- Create: `deploy/base/migrations/0011_docs.up.sql`
- Create: `deploy/base/migrations/0011_docs.down.sql`
- Modify: `deploy/base/kustomization.yaml` (configMapGenerator files list)
- Test: `internal/store/store_test.go` (`wantTables`, L8)

- [ ] **Step 1: Write the failing test** — in
  `internal/store/store_test.go`, append to `wantTables`:

```go
	"docs",
	"doc_sections",
	"doc_edges",
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/store -run TestOpenTestStoreClonesFullSchema -v`
Expected: FAIL — `table "docs" missing from template-cloned database` (three
such lines). If it *skips*, Postgres is not reachable: start it
(`docker compose up -d postgres`) first.

- [ ] **Step 3: Write the migration** —
  `deploy/base/migrations/0011_docs.up.sql`:

```sql
-- Spec 025 §5.1: the minimal document store the git→backbone sync populates.
-- Identity is (project, kind, ordinal), file-derived per 025 §16.3; doc_id is the
-- rendered <KEY>-SPEC-<n> / <KEY>-ADR-<n> / <KEY>-PLAN-<s>-<p> form, composed
-- server-side from the project's key. status is carried as data — the store
-- runs no editorial transitions (025 §5.1).

CREATE TABLE docs (
    project       text NOT NULL REFERENCES projects (id),
    kind          text NOT NULL CHECK (kind IN ('spec', 'adr', 'plan')),
    ordinal       text NOT NULL,
    doc_id        text NOT NULL,
    status        text NOT NULL,
    title         text NOT NULL,
    body          text NOT NULL,
    frontmatter   jsonb NOT NULL,
    version       integer NOT NULL DEFAULT 1,
    -- Sync provenance (025 §16.2): which branch the projection came from, and
    -- whether the tree was dirty — how a consumer tells a forced projection
    -- from a reviewed one.
    source_branch text NOT NULL,
    source_dirty  boolean NOT NULL,
    synced_at     timestamptz NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (project, kind, ordinal)
);

CREATE UNIQUE INDEX docs_doc_id ON docs (doc_id);

-- Anchored sections; specs and ADRs only — plans take none (025 §9).
CREATE TABLE doc_sections (
    project  text NOT NULL,
    kind     text NOT NULL,
    ordinal  text NOT NULL,
    anchor   text NOT NULL,
    heading  text NOT NULL,
    depth    integer NOT NULL,
    position integer NOT NULL,
    PRIMARY KEY (project, kind, ordinal, anchor),
    FOREIGN KEY (project, kind, ordinal)
        REFERENCES docs (project, kind, ordinal) ON DELETE CASCADE
);

-- Frontmatter relations (025 §5.1), section-scoped where an end is a section.
-- target is the raw corpus reference (a filename, repo-relative path, or the
-- NO-SPEC sentinel) — resolution stays a read-time concern. rel 'blocks' is
-- admitted for plans' document-level ordering edges even though no
-- frontmatter key emits it yet.
CREATE TABLE doc_edges (
    project       text NOT NULL,
    kind          text NOT NULL,
    ordinal       text NOT NULL,
    src_anchor    text NOT NULL DEFAULT '',
    rel           text NOT NULL CHECK (rel IN
        ('implements', 'amends', 'amendedBy', 'replaces', 'isReplacedBy', 'blocks')),
    target        text NOT NULL,
    target_anchor text NOT NULL DEFAULT '',
    PRIMARY KEY (project, kind, ordinal, src_anchor, rel, target, target_anchor),
    FOREIGN KEY (project, kind, ordinal)
        REFERENCES docs (project, kind, ordinal) ON DELETE CASCADE
);
```

`deploy/base/migrations/0011_docs.down.sql`:

```sql
DROP TABLE doc_edges;
DROP TABLE doc_sections;
DROP TABLE docs;
```

- [ ] **Step 4: Register and check** — in `deploy/base/kustomization.yaml`,
  append to the `configMapGenerator` files list:

```yaml
      - migrations/0011_docs.up.sql
      - migrations/0011_docs.down.sql
```

Run: `./scripts/check-migrations.sh --no-fix`
Expected: exit 0 (if it renumbers, follow its instructions and update the
kustomization entries to match).

- [ ] **Step 5: Run the schema tests to verify they pass**

Run: `go test ./internal/store -run 'TestOpenTestStore|TestMigra' -v`
Expected: PASS — the template store now contains the three tables, and the
down migration reverts cleanly (the store test helper exercises up; verify
down with `go test ./internal/store -run TestOpenTestStoreClonesFullSchema`
plus, if in doubt, the `golang-migrate:test-roundtrip` skill).

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0011_docs.up.sql deploy/base/migrations/0011_docs.down.sql \
        deploy/base/kustomization.yaml internal/store/store_test.go
git commit -m "store: docs/doc_sections/doc_edges schema (spec 025 §5.1)"
```

### Task 2 — ApplyDocSync: the idempotent upsert

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `internal/store/docs.go`
- Test: `internal/store/docs_test.go`

**Interfaces produced (consumed by part 3's API handler):**

```go
type DocSection struct {
	Anchor, Heading string
	Depth, Position int
}

type DocEdge struct {
	SrcAnchor, Rel, Target, TargetAnchor string
}

// DocUpsert is one document as the sync client ships it (025 §5.1).
type DocUpsert struct {
	Kind, Ordinal, Status, Title, Body string
	Frontmatter                        json.RawMessage
	Sections                           []DocSection
	Edges                              []DocEdge
}

// DocSyncProvenance records where a sync came from (025 §16.2).
type DocSyncProvenance struct {
	SourceBranch string
	Dirty        bool
}

// DocSyncResult is one document's sync outcome: "added", "updated", or
// "unchanged".
type DocSyncResult struct {
	DocID, Kind, Outcome string
}

// ApplyDocSync upserts docs for projectID inside tx, idempotent on
// (project, kind, ordinal). Meant to be called from a RecordEvent apply
// callback; eventID attributes the state_log rows.
func (s *Store) ApplyDocSync(tx *sql.Tx, now time.Time, eventID int64,
	projectID string, prov DocSyncProvenance, docs []DocUpsert) ([]DocSyncResult, error)

// DocSyncOutcomes is ApplyDocSync's read-only twin: the per-doc outcomes a
// sync WOULD produce, writing nothing (--dry-run, 025 §16.2).
func (s *Store) DocSyncOutcomes(ctx context.Context, projectID string,
	docs []DocUpsert) ([]DocSyncResult, error)
```

Semantics (each is a test):

- validation (all → `ErrInvalidInput` wrapped with the offending value):
  `Kind` ∈ {spec, adr, plan}; `Ordinal` matches `^[1-9][0-9]*$` for spec/adr
  and `^(0|[1-9][0-9]*)-[1-9][0-9]*$` for plan; `Status` and `Title`
  non-empty; every `Edge.Rel` in the migration's CHECK list;
- unknown project → `ErrNotFound`;
- `doc_id` composed server-side: project key + `-` + `SPEC`/`ADR`/`PLAN` +
  `-` + ordinal;
- new row → `added`, `version` 1; content change (status, title, body, or
  frontmatter — jsonb-compared, `docs.frontmatter <> $n::jsonb`, so key order
  never causes a false diff) → `updated`, `version` +1, sections and edges
  replaced; identical content → `unchanged`, version and sections untouched;
- provenance + `synced_at`/`updated_at` stamped on **every** outcome
  including `unchanged` (a reviewed sync must overwrite a forced one's
  provenance, 025 §16.2);
- one `LogChange` row (`entity_kind: "doc"`, entity_id = doc_id) per `added`
  or `updated` doc, none for `unchanged`;
- a doc in the store but absent from the payload is left alone (034 defines
  sync as upsert; deletion is out of scope).

- [ ] **Step 1: Write the failing tests** — create
  `internal/store/docs_test.go`. Test skeleton (package `store`, same style
  as `tasks_test.go`; `OpenTestStore` + `CreateProject(ctx, "wl", "Worklode",
  "WL")`):

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// syncDocs drives ApplyDocSync the way the API will: through RecordEvent.
func syncDocs(t *testing.T, s *Store, project string, prov DocSyncProvenance,
	docs []DocUpsert) []DocSyncResult {
	t.Helper()
	res, err := syncDocsErr(s, project, prov, docs)
	if err != nil {
		t.Fatalf("ApplyDocSync: %v", err)
	}
	return res
}

func syncDocsErr(s *Store, project string, prov DocSyncProvenance,
	docs []DocUpsert) ([]DocSyncResult, error) {
	var res []DocSyncResult
	_, _, err := s.RecordEvent(context.Background(), "test", randomID(), "docs.synced", []byte(`{}`),
		func(tx *sql.Tx, eventID int64) error {
			var err error
			res, err = s.ApplyDocSync(tx, s.Now(), eventID, project, prov, docs)
			return err
		})
	return res, err
}

func specUpsert() DocUpsert {
	return DocUpsert{
		Kind: "spec", Ordinal: "34", Status: "accepted",
		Title: "Spec 034 — Design-doc sync",
		Body:  "---\nstatus: accepted\n---\n# Spec 034 — Design-doc sync\n\n## 1. Scope {#sec-1}\n\nBody.\n",
		Frontmatter: json.RawMessage(`{"status":"accepted"}`),
		Sections:    []DocSection{{Anchor: "sec-1", Heading: "Scope", Depth: 2, Position: 0}},
		Edges: []DocEdge{{SrcAnchor: "sec-1", Rel: "amends",
			Target: "025-documents-in-the-backbone.md", TargetAnchor: "sec-2"}},
	}
}

func planUpsert() DocUpsert {
	return DocUpsert{
		Kind: "plan", Ordinal: "34-1", Status: "draft", Title: "Part 1",
		Body:        "---\nstatus: draft\n---\n# Part 1\n",
		Frontmatter: json.RawMessage(`{"status":"draft","implements":"docs/specs/025-documents-in-the-backbone.md"}`),
		Edges:       []DocEdge{{Rel: "implements", Target: "docs/specs/025-documents-in-the-backbone.md"}},
	}
}

func TestApplyDocSyncAddUpdateUnchanged(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	prov := DocSyncProvenance{SourceBranch: "main"}

	res := syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert(), planUpsert()})
	if len(res) != 2 || res[0].Outcome != "added" || res[1].Outcome != "added" {
		t.Fatalf("first sync = %+v, want two added", res)
	}
	if res[0].DocID != "WL-SPEC-25" || res[1].DocID != "WL-PLAN-34-1" {
		t.Fatalf("doc ids = %q, %q", res[0].DocID, res[1].DocID)
	}

	// Same content, same key order or not: unchanged, version still 1,
	// provenance overwritten.
	forced := DocSyncProvenance{SourceBranch: "feature-x", Dirty: true}
	res = syncDocs(t, s, "wl", forced, []DocUpsert{specUpsert(), planUpsert()})
	for _, r := range res {
		if r.Outcome != "unchanged" {
			t.Errorf("%s outcome = %q, want unchanged", r.DocID, r.Outcome)
		}
	}
	d, _, _, err := s.GetDoc(ctx, "WL-SPEC-25")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || d.SourceBranch != "feature-x" || !d.SourceDirty {
		t.Errorf("after unchanged sync: version=%d branch=%q dirty=%v; want 1, feature-x, true",
			d.Version, d.SourceBranch, d.SourceDirty)
	}

	// Changed body: updated, version bumped, sections replaced.
	changed := specUpsert()
	changed.Body += "\n## 2. More {#sec-2}\n"
	changed.Sections = append(changed.Sections,
		DocSection{Anchor: "sec-2", Heading: "More", Depth: 2, Position: 1})
	res = syncDocs(t, s, "wl", prov, []DocUpsert{changed})
	if res[0].Outcome != "updated" {
		t.Fatalf("outcome = %q, want updated", res[0].Outcome)
	}
	d, secs, _, err := s.GetDoc(ctx, "WL-SPEC-25")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 2 || len(secs) != 2 {
		t.Errorf("after update: version=%d sections=%d; want 2, 2", d.Version, len(secs))
	}
}

func TestApplyDocSyncValidation(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	bad := func(mutate func(*DocUpsert)) error {
		d := specUpsert()
		mutate(&d)
		_, err := syncDocsErr(s, "wl", DocSyncProvenance{SourceBranch: "main"}, []DocUpsert{d})
		return err
	}
	for name, tc := range map[string]func(*DocUpsert){
		"bad kind":         func(d *DocUpsert) { d.Kind = "memo" },
		"bad spec ordinal": func(d *DocUpsert) { d.Ordinal = "034" },
		"plan ordinal on spec": func(d *DocUpsert) { d.Ordinal = "34-1" },
		"empty status":     func(d *DocUpsert) { d.Status = "" },
		"empty title":      func(d *DocUpsert) { d.Title = "" },
		"bad edge rel":     func(d *DocUpsert) { d.Edges[0].Rel = "mentions" },
	} {
		if err := bad(tc); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := syncDocsErr(s, "nope", DocSyncProvenance{}, []DocUpsert{specUpsert()}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: err = %v, want ErrNotFound", err)
	}
}

func TestApplyDocSyncWritesStateLog(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	prov := DocSyncProvenance{SourceBranch: "main"}
	syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert()})
	syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert()}) // unchanged: no new row

	entries, err := s.StateLogForEntity(ctx, "doc", "WL-SPEC-25")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("state_log rows = %d, want 1 (added only; unchanged logs nothing)", len(entries))
	}
}

func TestDocSyncOutcomesWritesNothing(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	res, err := s.DocSyncOutcomes(ctx, "wl", []DocUpsert{specUpsert()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Outcome != "added" {
		t.Fatalf("dry-run = %+v, want one added", res)
	}
	if _, _, _, err := s.GetDoc(ctx, "WL-SPEC-25"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dry run wrote a doc: GetDoc err = %v, want ErrNotFound", err)
	}
}
```

Also add the tiny `randomID` helper the driver needs (crypto/rand hex, or
reuse an existing one if `internal/store`'s tests already have an external-id
helper — grep first; if none exists, add
`func randomID() string { b := make([]byte, 8); rand.Read(b); return hex.EncodeToString(b) }`
with `crypto/rand`/`encoding/hex` imports to `docs_test.go`).

Note the tests use `s.GetDoc` (Task 3's read method). To keep this task
independently runnable, implement a minimal `GetDoc` here as part of this
task's minimal implementation (Task 3 finishes it with `ListDocs` and full
edge/section reads) — or land Tasks 2 and 3 in one review if the reviewer
prefers. The interface block in Task 3 is the authority on its signature.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/store -run 'TestApplyDocSync|TestDocSyncOutcomes' -v`
Expected: compile error — `ApplyDocSync`, `DocUpsert` undefined.

- [ ] **Step 3: Implement** — create `internal/store/docs.go` with the types
  above plus:

```go
var docKindTokens = map[string]string{"spec": "SPEC", "adr": "ADR", "plan": "PLAN"}

var (
	specOrdinalRe = regexp.MustCompile(`^[1-9][0-9]*$`)
	planOrdinalRe = regexp.MustCompile(`^(0|[1-9][0-9]*)-[1-9][0-9]*$`)
)

var validDocEdgeRels = map[string]bool{
	"implements": true, "amends": true, "amendedBy": true,
	"replaces": true, "isReplacedBy": true, "blocks": true,
}

// validateDocUpsert checks one upsert's shape (025 §5.1/§5).
func validateDocUpsert(d DocUpsert) error {
	token, ok := docKindTokens[d.Kind]
	if !ok {
		return fmt.Errorf("doc kind %q: %w", d.Kind, ErrInvalidInput)
	}
	re := specOrdinalRe
	if d.Kind == "plan" {
		re = planOrdinalRe
	}
	if !re.MatchString(d.Ordinal) {
		return fmt.Errorf("%s ordinal %q: %w", d.Kind, d.Ordinal, ErrInvalidInput)
	}
	if d.Status == "" || d.Title == "" {
		return fmt.Errorf("%s-%s: empty status or title: %w", token, d.Ordinal, ErrInvalidInput)
	}
	for _, e := range d.Edges {
		if !validDocEdgeRels[e.Rel] {
			return fmt.Errorf("edge rel %q: %w", e.Rel, ErrInvalidInput)
		}
	}
	return nil
}
```

`ApplyDocSync` per doc, inside the caller's tx:

1. validate every upsert first (no partial validation failures mid-write);
2. `SELECT key FROM projects WHERE id = $1` once (no row → `ErrNotFound`);
3. per doc: `docID := key + "-" + docKindTokens[d.Kind] + "-" + d.Ordinal`,
   then

```go
	var outcome string
	var version int
	err := tx.QueryRow(`
		SELECT CASE WHEN status = $4 AND title = $5 AND body = $6
		            AND frontmatter = $7::jsonb
		       THEN 'unchanged' ELSE 'updated' END, version
		  FROM docs WHERE project = $1 AND kind = $2 AND ordinal = $3
		  FOR UPDATE`,
		projectID, d.Kind, d.Ordinal, d.Status, d.Title, d.Body, string(d.Frontmatter),
	).Scan(&outcome, &version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		outcome = "added"
	case err != nil:
		return nil, fmt.Errorf("check doc %s: %w", docID, err)
	}
```

4. write the row per outcome:

```go
	ts := now.UTC().Truncate(time.Second)
	switch outcome {
	case "added":
		version = 1
		_, err = tx.Exec(`
			INSERT INTO docs (project, kind, ordinal, doc_id, status, title, body,
			                  frontmatter, version, source_branch, source_dirty,
			                  synced_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, 1, $9, $10, $11, $11, $11)`,
			projectID, d.Kind, d.Ordinal, docID, d.Status, d.Title, d.Body,
			string(d.Frontmatter), prov.SourceBranch, prov.Dirty, ts)
	case "updated":
		version++
		_, err = tx.Exec(`
			UPDATE docs SET status = $4, title = $5, body = $6, frontmatter = $7::jsonb,
			       version = version + 1, source_branch = $8, source_dirty = $9,
			       synced_at = $10, updated_at = $10
			 WHERE project = $1 AND kind = $2 AND ordinal = $3`,
			projectID, d.Kind, d.Ordinal, d.Status, d.Title, d.Body,
			string(d.Frontmatter), prov.SourceBranch, prov.Dirty, ts)
	case "unchanged": // provenance still overwritten (025 §16.2)
		_, err = tx.Exec(`
			UPDATE docs SET source_branch = $4, source_dirty = $5,
			       synced_at = $6, updated_at = $6
			 WHERE project = $1 AND kind = $2 AND ordinal = $3`,
			projectID, d.Kind, d.Ordinal, prov.SourceBranch, prov.Dirty, ts)
	}
	if err != nil {
		return nil, fmt.Errorf("write doc %s: %w", docID, err)
	}
```

5. for `added`/`updated`: replace the derived rows and log the change —

```go
	if outcome != "unchanged" {
		for _, q := range []string{
			`DELETE FROM doc_sections WHERE project = $1 AND kind = $2 AND ordinal = $3`,
			`DELETE FROM doc_edges WHERE project = $1 AND kind = $2 AND ordinal = $3`,
		} {
			if _, err := tx.Exec(q, projectID, d.Kind, d.Ordinal); err != nil {
				return nil, fmt.Errorf("clear derived rows for %s: %w", docID, err)
			}
		}
		for _, sec := range d.Sections {
			if _, err := tx.Exec(`
				INSERT INTO doc_sections (project, kind, ordinal, anchor, heading, depth, position)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				projectID, d.Kind, d.Ordinal, sec.Anchor, sec.Heading, sec.Depth, sec.Position); err != nil {
				return nil, fmt.Errorf("insert section %s#%s: %w", docID, sec.Anchor, err)
			}
		}
		for _, e := range d.Edges {
			if _, err := tx.Exec(`
				INSERT INTO doc_edges (project, kind, ordinal, src_anchor, rel, target, target_anchor)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				projectID, d.Kind, d.Ordinal, e.SrcAnchor, e.Rel, e.Target, e.TargetAnchor); err != nil {
				return nil, fmt.Errorf("insert edge %s %s %s: %w", docID, e.Rel, e.Target, err)
			}
		}
		if err := LogChange(tx, "doc", docID, eventID, map[string]any{
			"outcome": outcome, "version": version, "status": d.Status,
		}); err != nil {
			return nil, err
		}
	}
```

6. append `DocSyncResult{DocID: docID, Kind: d.Kind, Outcome: outcome}`.

(No metrics call here — Task 4 adds both the instrument and the call site.)

`DocSyncOutcomes` reuses the same comparison `SELECT` (without `FOR UPDATE`)
over `s.db` and never writes:

```go
func (s *Store) DocSyncOutcomes(ctx context.Context, projectID string, docs []DocUpsert) ([]DocSyncResult, error) {
	var key string
	if err := s.db.QueryRowContext(ctx, `SELECT key FROM projects WHERE id = $1`, projectID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", projectID, ErrNotFound)
		}
		return nil, fmt.Errorf("look up project: %w", err)
	}
	var out []DocSyncResult
	for _, d := range docs {
		if err := validateDocUpsert(d); err != nil {
			return nil, err
		}
		docID := key + "-" + docKindTokens[d.Kind] + "-" + d.Ordinal
		var outcome string
		err := s.db.QueryRowContext(ctx, `
			SELECT CASE WHEN status = $4 AND title = $5 AND body = $6
			            AND frontmatter = $7::jsonb
			       THEN 'unchanged' ELSE 'updated' END
			  FROM docs WHERE project = $1 AND kind = $2 AND ordinal = $3`,
			projectID, d.Kind, d.Ordinal, d.Status, d.Title, d.Body, string(d.Frontmatter),
		).Scan(&outcome)
		if errors.Is(err, sql.ErrNoRows) {
			outcome = "added"
		} else if err != nil {
			return nil, fmt.Errorf("check doc %s: %w", docID, err)
		}
		out = append(out, DocSyncResult{DocID: docID, Kind: d.Kind, Outcome: outcome})
	}
	return out, nil
}
```

Include the minimal `GetDoc` (doc row only, plus sections and edges ordered
by `position` / primary key) so this task's tests compile; Task 3's tests
harden it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store -run 'TestApplyDocSync|TestDocSyncOutcomes' -v`
Expected: PASS. Then the full package: `go test ./internal/store`.

- [ ] **Step 5: Commit**

```bash
git add internal/store/docs.go internal/store/docs_test.go
git commit -m "store: ApplyDocSync — idempotent doc upsert with provenance (spec 025 §5.1)"
```

### Task 3 — Store reads: GetDoc and ListDocs

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/store/docs.go`
- Test: `internal/store/docs_test.go`

**Interfaces produced (consumed by part 3's API handlers):**

```go
// Doc is one stored document. Body is "" in ListDocs rows (list is metadata;
// the full text comes from GetDoc).
type Doc struct {
	Project, Kind, Ordinal, DocID   string
	Status, Title, Body             string
	Frontmatter                     json.RawMessage
	Version                         int
	SourceBranch                    string
	SourceDirty                     bool
	SyncedAt, CreatedAt, UpdatedAt  time.Time
}

// GetDoc returns one document by its rendered id ("WL-SPEC-25"), with its
// sections (by position) and edges. ErrNotFound when no such doc.
func (s *Store) GetDoc(ctx context.Context, docID string) (*Doc, []DocSection, []DocEdge, error)

// DocFilter narrows ListDocs; zero fields do not filter.
type DocFilter struct {
	Project, Kind, Status string
}

// ListDocs returns matching documents ordered by project, kind, then ordinal
// numerically (spec 10 after spec 9; plan 34-2 after 34-1).
func (s *Store) ListDocs(ctx context.Context, f DocFilter) ([]Doc, error)
```

- [ ] **Step 1: Write the failing tests** — append to
  `internal/store/docs_test.go`:

```go
func TestGetDocDetail(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	syncDocs(t, s, "wl", DocSyncProvenance{SourceBranch: "main"},
		[]DocUpsert{specUpsert(), planUpsert()})

	d, secs, edges, err := s.GetDoc(ctx, "WL-SPEC-25")
	if err != nil {
		t.Fatal(err)
	}
	if d.DocID != "WL-SPEC-25" || d.Kind != "spec" || d.Ordinal != "34" ||
		d.Status != "accepted" || d.Body == "" || d.Version != 1 {
		t.Errorf("doc = %+v", d)
	}
	if len(secs) != 1 || secs[0].Anchor != "sec-1" || secs[0].Heading != "Scope" {
		t.Errorf("sections = %+v", secs)
	}
	if len(edges) != 1 || edges[0].Rel != "amends" || edges[0].TargetAnchor != "sec-2" {
		t.Errorf("edges = %+v", edges)
	}

	if _, _, _, err := s.GetDoc(ctx, "WL-SPEC-999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing doc: err = %v, want ErrNotFound", err)
	}
}

func TestListDocsFiltersAndOrder(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	nine := specUpsert()
	nine.Ordinal, nine.Status = "9", "draft"
	ten := specUpsert()
	ten.Ordinal = "10"
	p2 := planUpsert()
	p2.Ordinal = "34-2"
	syncDocs(t, s, "wl", DocSyncProvenance{SourceBranch: "main"},
		[]DocUpsert{ten, nine, specUpsert(), planUpsert(), p2})

	all, err := s.ListDocs(ctx, DocFilter{Project: "wl"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range all {
		ids = append(ids, d.DocID)
		if d.Body != "" {
			t.Errorf("%s: list row carries a body", d.DocID)
		}
	}
	want := []string{"WL-PLAN-34-1", "WL-PLAN-34-2", "WL-SPEC-6", "WL-SPEC-4", "WL-SPEC-25"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("order = %v, want %v (numeric ordinal order, 9 before 10)", ids, want)
	}

	drafts, err := s.ListDocs(ctx, DocFilter{Project: "wl", Kind: "spec", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].DocID != "WL-SPEC-6" {
		t.Errorf("filtered = %+v, want just WL-SPEC-6", drafts)
	}
}
```

(Add `"reflect"` to the test file's imports.)

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/store -run 'TestGetDocDetail|TestListDocs' -v`
Expected: compile error on `ListDocs`/`DocFilter` (and failures on any
`GetDoc` gap left by Task 2's minimal version).

- [ ] **Step 3: Implement** in `internal/store/docs.go`. `GetDoc`: one
  `SELECT ... FROM docs WHERE doc_id = $1`, then
  `SELECT anchor, heading, depth, position FROM doc_sections WHERE project=$1
  AND kind=$2 AND ordinal=$3 ORDER BY position` and
  `SELECT src_anchor, rel, target, target_anchor FROM doc_edges WHERE ...
  ORDER BY src_anchor, rel, target, target_anchor`. `ListDocs` with dynamic
  WHERE clauses (the `strings.Builder` + args-slice style `ListTasks` in
  `internal/store/tasks.go` uses) and:

```sql
ORDER BY project, kind, string_to_array(ordinal, '-')::int[]
```

(`ordinal` is always dash-separated integers — the validation in Task 2
guarantees the cast is safe.) Timestamps normalized `.UTC()` on scan, like
the rest of the store.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store -run 'TestGetDocDetail|TestListDocs|TestApplyDocSync' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/docs.go internal/store/docs_test.go
git commit -m "store: GetDoc/ListDocs reads for the doc store (spec 025 §5.1, §6)"
```

### Task 4 — Store metric: worklode_doc_upserts_total

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

Spec 025 §15.7's "store upsert outcomes" instrument, per spec 022: nil-safe on
`storeMetrics`, bounded labels (outcome ∈ added/updated/unchanged).

**Files:**
- Modify: `internal/store/metrics.go` (storeMetrics struct L29-34,
  `newStoreMetrics` L36-57)
- Modify: `internal/store/docs.go` (`ApplyDocSync` — add the call site)
- Test: `internal/store/metrics_test.go`

- [ ] **Step 1: Write the failing test** — append to
  `internal/store/metrics_test.go` (match the file's existing style — it
  already imports `prometheus` and `testutil`; follow the existing tests'
  helper usage):

```go
func TestDocUpsertMetric(t *testing.T) {
	s := OpenTestStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	prov := DocSyncProvenance{SourceBranch: "main"}
	syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert()}) // added
	syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert()}) // unchanged
	changed := specUpsert()
	changed.Body += "x"
	syncDocs(t, s, "wl", prov, []DocUpsert{changed}) // updated

	for outcome, want := range map[string]float64{"added": 1, "updated": 1, "unchanged": 1} {
		got := testutil.ToFloat64(s.metrics.docUpserts.WithLabelValues(outcome))
		if got != want {
			t.Errorf("worklode_doc_upserts_total{outcome=%q} = %v, want %v", outcome, got, want)
		}
	}
}

func TestDocUpsertMetricNilSafe(t *testing.T) {
	var m *storeMetrics
	m.docUpsert("added") // must not panic
}
```

(`metrics_test.go` is `package store` and currently imports only
`errors`/`strings`/`testing`/`time`; add `context`,
`github.com/prometheus/client_golang/prometheus`, and
`github.com/prometheus/client_golang/prometheus/testutil` for these tests.
The `syncDocs`/`specUpsert` helpers come from `docs_test.go`, same package.)

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/store -run TestDocUpsertMetric -v`
Expected: compile error — `docUpserts`/`docUpsert` undefined.

- [ ] **Step 3: Implement** — in `internal/store/metrics.go`, add the field
  and registration:

```go
	docUpserts *prometheus.CounterVec
```

in `newStoreMetrics`:

```go
		docUpserts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_doc_upserts_total",
			Help: "Doc-store upserts by outcome (added, updated, unchanged).",
		}, []string{"outcome"}),
```

(register it in the existing `reg.MustRegister(...)` call, and pre-initialise
the three outcome series next to the other pre-inits:
`for _, o := range []string{"added", "updated", "unchanged"} { m.docUpserts.WithLabelValues(o) }`),
plus the nil-safe method:

```go
func (m *storeMetrics) docUpsert(outcome string) {
	if m == nil {
		return
	}
	m.docUpserts.WithLabelValues(outcome).Inc()
}
```

In `ApplyDocSync` (docs.go), after each doc's outcome is decided:

```go
		s.metrics.docUpsert(outcome)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store -run 'TestDocUpsertMetric|TestApplyDocSync' -v`
then the full package: `go test ./internal/store`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/metrics.go internal/store/metrics_test.go internal/store/docs.go
git commit -m "store: worklode_doc_upserts_total metric (spec 025 §15.7)"
```
