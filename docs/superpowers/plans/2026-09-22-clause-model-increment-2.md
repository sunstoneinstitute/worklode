# Clause Model, Increment 2 (the clause graph) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clauses relate to each other through a small set of typed edges, `references` edges are derived from clause text on every version, a task's governing link can pin a clause version, and a clause carries an owner and tags.

**Architecture:** One new table, `clause_edges`, holds every edge between two clauses with its type and whether a person or the store wrote it. Hand-made edges arrive over `POST/DELETE /api/v1/clauses/{id}/edges` and `lode clause link/unlink`. Derived `references` edges are rewritten by `syncClauses` whenever a clause's text is inserted or revised, resolved through the shared ref grammar in `internal/designdoc`. Pinning is one nullable column on `task_governed_by`, set by the same `Govern` call that creates the link. Owner and tags are two columns on `clauses`, set over `PATCH /api/v1/clauses/{id}`. Every reader of a clause (API detail, `lode show`, cockpit page) gains the edges, owner and tags because they all read `model.Clause`.

**Tech Stack:** Go, Postgres via `database/sql`, cobra CLI, templ for the cockpit, Turtle for `ns/`, `riot` for validating it.

**Spec:** `docs/specs2/12-spec-refactoring-design-tree.md` (S10, S12, S15, S17, S26) and GitHub issue #687's "Increment 2" section. Executors read both.

**Stacked on:** branch `clause-increment-1b` at `5c858633`, itself stacked on `spec-refactor` (PR #686). This branch is `clause-increment-2`; its PR targets `clause-increment-1b` until that lands, then `spec-refactor`, then `main`.

## Rulings made while planning

The user was not available for brainstorming, so these are decided here. Each names what it costs if wrong.

- **R1 Spec 07 §2 owns the vocabulary; `ns/clause.ttl` already holds the terms.** 07 §2 is the table of every `wl:` term and where it comes from, and `ns/clause.ttl`'s own header says the clause terms move into `ontology.ttl` once a spec carries them. So Task 1 adds the rows to 07 §2, then moves `wl:Clause`, `wl:refines`, `wl:constrains`, `wl:conflictsWith` and `wl:references` from `clause.ttl` into `ontology.ttl` without the `vs:term_status "unstable"` marker. The arrangement terms (`wl:Arrangement`, `wl:arranges`, `wl:clauseList`, `wl:position`, `wl:OpenQuestion`) stay in `clause.ttl` for increment 3. Costs a second move later; nothing else.
- **R2 One table for every clause edge, hand-made or derived.** `clause_edges (from_clause, to_clause, type, source)` with the primary key on all of the first three. `source` is `manual` or `derived`. A derived edge is only ever `references`; a manual edge may be any of the four types. A manual `references` edge is allowed (an author can point at a clause the text does not name) and survives re-derivation because derivation deletes only `source = 'derived'` rows and inserts with `ON CONFLICT DO NOTHING`. Costs nothing S12 or S26 promised.
- **R3 No separate `GET .../edges` route.** `model.Clause` gains `Edges []ClauseEdge` holding both directions, filled by `GetClause`, so the clause detail is the listing. A route that returns a subset of the detail would be a second shape for the same rows. Costs one route later if a client wants edges without the body.
- **R4 Derivation resolves what it can and drops the rest, silently.** `syncClauses` scans the heading and body of every inserted or revised clause for `<KEY>-CL-<n>` refs and `<KEY>-(SPEC|ADR|PLAN)-<n>#<anchor>` refs. A clause ref resolves to the clause row; a section ref resolves through `resolveDocRef` to the document and then to the clause arranged at that anchor. A ref to a whole document (no anchor), to a section with no clause, to a clause in no project, or to the clause itself yields no edge. Costs one derived edge per unresolvable ref that a later increment may want as a dangling pointer; spec 026's dangling-ref lint covers documents, and clauses can join it then.
- **R5 Pinning rides on `Govern`.** `Govern(tx, taskID, clauseID, source, pin bool)`: with `pin` the link records `pinned_version = clauses.version`; without it `pinned_version` is NULL. Re-governing an already governed clause updates the pin either way (`ON CONFLICT DO UPDATE`), so a second `lode task govern --by X --pin` pins and a plain `lode task govern --by X` unpins. There is no separate unpin verb. `POST /tasks/{id}/governed-by` accepts `{"clause": "WL-CL-3", "pin": true}`; a version number is not accepted, because S10 says a link pins the version current when the link is made, and `lode show WL-CL-3 --version N` already reads old text. Costs one verb later if pinning an older version turns out wanted.
- **R6 A pinned link reports its versioned URL.** `model.TaskGovernance` gains `Pinned int` (0 when unpinned) and `URL string`, the canonical page from 1b: `/projects/<proj>/clause/<n>` and `/<ver>` when pinned. S10 asks for the versioned IRI form; that is the versioned URL scheme S20 chose. Costs nothing.
- **R7 Owner is free text, like `docs.owner`.** `clauses.owner text NULL` with no FK, matching `docs.owner` (migration 0058), which is also an actor id stored without a constraint. `clauses.tags text[] NOT NULL DEFAULT '{}'`. Both set over `PATCH /api/v1/clauses/{id}` with `model.ClauseMetaInput{Owner *string, Tags *[]string}`, pointer fields so an omitted key leaves the column alone and a present key replaces it (an empty string clears the owner, an empty list clears the tags). 1b's `PUT` stays the text write; PATCH is the metadata write, the same split `docs` has between body and owner. Costs a backfill migration if owner gains an FK later.
- **R8 The CLI verbs are `lode clause link`, `lode clause unlink`, `lode clause set owner`, `lode clause set tags`, `lode task govern --pin`.** `link` is already in `l3DomainActions` (used by `lode doc link`), `un` prefixes are stripped by the naming test, `set` is L3 canonical. No new verb, so `namerule_test.go` and spec 09's allowlist need no addition beyond the `clause` row. The edge type is a flag, one of `--refines`, `--constrains`, `--conflicts-with`, `--references`, exactly one required. Costs nothing.
- **R9 `conflictsWith` is stored as written.** The ontology marks it symmetric; the store keeps one row in the direction the author gave, and `GetClause` lists incoming and outgoing edges, so both clauses show it. No mirror row. Costs nothing.
- **R10 S17 is a check.** Task 9 reads the clause code for anything keyed on project size and writes one sentence in spec 12 saying none exists. If the check finds one, the implementer reports it instead of fixing it.
- **R11 Events.** Edge writes are `clause.linked` and `clause.unlinked`; the metadata write is `clause.updated`; all through `s.recordEvent` (the write is about a clause, not a document or task). A derived edge write raises no event of its own: it rides in the document write that caused it.

Deferred to later increments, stated so no reviewer reports them missing: named clusters (S12; a clause has tags now, which covers the grouping until a cluster needs its own edges), the sprawl metrics R1 to R3 as a computation, plans as arrangements (S16, S25), the reconciler and gate trailer (S4, S18, S29, S30), split and merge lineage (S22), the migration of the old specs (A2), the BlockNote editor.

## Global Constraints

- Build and test with `-trimpath` only: `make build`, `make test`, or `go test -trimpath ./internal/<pkg> -run <Test>`. Never bare `go test`.
- Store and API tests need Postgres with pgvector at `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (override `TEST_POSTGRES_DSN`). They skip silently without it, so confirm the suite ran (`-v`, look for `--- PASS`, no `SKIP`).
- Every shape that crosses HTTP is declared once in `internal/model` with wire field names (ADR 036). `internal/model/rule_test.go` and `deps_test.go` enforce it.
- Every route appears in `internal/api/router.go`'s `routeGuards`; `NewServer` refuses to boot otherwise. API reads `guardedAny(permDocRead)`; API writes on clauses `guardedAny(permDocWrite)`; task writes `guardedBound(permTaskWrite)` as the existing `governed-by` routes do.
- `internal/store/AGENTS.md`: a write a caller can make collide returns a sentinel from `errors.go`, mapped in `mapStoreErr`; never a raw pgx error for a caller-triggerable condition.
- `internal/cmd` decides, `internal/cli` renders. `internal/cli` never imports cobra.
- `internal/ui` depends only on stdlib, `internal/model` and templ. After editing a `.templ` file run `templ generate` from the repo root and commit the generated `_templ.go`.
- Migrations: the next number is whatever `./scripts/check-migrations.sh` accepts (0083 at planning time). Add the pair to `deploy/base/kustomization.yaml` in the same commit. The down file fully reverses the up. Read `deploy/base/AGENTS.md` first.
- `ns/*.ttl` must parse: `riot --validate ns/*.ttl` (riot is installed at `/opt/homebrew/bin/riot`). The spec is amended before the ontology mirrors it (025 §17).
- Prose in `docs/specs2/` follows the `lode:anti-smartass` plain-language style: no em dashes, no "X, not Y" antithesis.
- Commit messages: imperative subject, no Co-authored-by or any self-reference.
- Feature-stem file naming: the edge code lives in `model/clauseedge.go`, `store/clauseedges.go`, `api/clauseedges.go`, `cli/clauseedges.go`, `cmd/clause.go` (existing group). Owner and tags extend the existing clause files. Pinning extends the existing governedby files.

---

### Task 1: Spec 07 §2 names the clause terms; `ns/ontology.ttl` carries them (R1)

**Files:**
- Modify: `docs/specs2/07-knowledge-graph-and-search.md` (the §2 table, after the "Document section" row)
- Modify: `ns/ontology.ttl` (append a "Clauses" block after the coverage block that ends with `wl:coverageLevel`)
- Modify: `ns/clause.ttl` (delete the five moved terms; update the header)

**Interfaces:** none in Go.

- [ ] **Step 1: Add the rows to 07 §2**

After the row `| Document section | \`wl:Section\`, \`wl:lastRevisedIn\` | mint |` insert:

```markdown
| Design clause | `wl:Clause`, subclass of `wl:Section`: the lowest heading unit of a spec or ADR, with its own identity, status and versions (12-spec-refactoring-design-tree.md S8 to S11) | mint |
| Clause to clause | `wl:refines`, `wl:constrains`, `wl:conflictsWith` (symmetric), written by an architect; `wl:references`, derived from the clause text on every version (S12, S26) | mint |
```

- [ ] **Step 2: Move the terms into `ns/ontology.ttl`**

Append after the `wl:coverageLevel` block:

```turtle
# --- Clauses (docs/specs2/12-spec-refactoring-design-tree.md S8 to S12, S26; 07 §2) ---

wl:Clause a owl:Class ;
    wl:layer wlc:intent ;
    rdfs:subClassOf wl:Section ;
    rdfs:comment "The lowest heading unit of a spec or ADR: the thing that carries an identity (WL-CL-<n>), a status, versions and edges (S8 to S11)." .

wl:refines a owl:ObjectProperty ;
    wl:layer wlc:intent ;
    rdfs:domain wl:Clause ;
    rdfs:range wl:Clause ;
    rdfs:comment "Subject narrows or details the object (S12)." .

wl:constrains a owl:ObjectProperty ;
    wl:layer wlc:intent ;
    rdfs:domain wl:Clause ;
    rdfs:range wl:Clause ;
    rdfs:comment "Subject must hold whenever the object does; a task implementing the object must also satisfy the subject (S12)." .

wl:conflictsWith a owl:ObjectProperty, owl:SymmetricProperty ;
    wl:layer wlc:intent ;
    rdfs:domain wl:Clause ;
    rdfs:range wl:Clause ;
    rdfs:comment "A recorded tension an architect chose to leave in place (S12)." .

wl:references a owl:ObjectProperty ;
    wl:layer wlc:intent ;
    rdfs:domain wl:Clause ;
    rdfs:range wl:Clause ;
    rdfs:comment "A textual pointer from the clause to another clause, derived from the clause text on every version and replaced with it (S26)." .
```

Check that `wlc:intent` is a `wl:layer` value already used in the file (`grep -n "wl:layer wlc:" ns/ontology.ttl | sort | uniq -c`). If the layer concepts are named differently, use the one `wl:Section` carries.

- [ ] **Step 3: Remove the moved terms from `ns/clause.ttl`**

Delete the `wl:Clause`, `wl:refines`, `wl:constrains`, `wl:conflictsWith` and `wl:references` blocks. Change `wl:OpenQuestion`'s `rdfs:subClassOf wl:Clause` to stay as is (the class now lives in `ontology.ttl`, same namespace). Replace the header's last two sentences with: "The clause class and its four edge properties moved to ontology.ttl with 07 §2 (clause model increment 2); the arrangement terms below wait for increment 3."

- [ ] **Step 4: Validate and commit**

Run: `riot --validate ns/*.ttl`
Expected: no output, exit 0.

Run: `grep -n '—' docs/specs2/07-knowledge-graph-and-search.md | wc -l` before and after the edit; the count must not grow.

```bash
git add docs/specs2/07-knowledge-graph-and-search.md ns/ontology.ttl ns/clause.ttl
git commit -m "Name the clause class and its edges in spec 07 and the ontology (S12, S26)"
```

---

### Task 2: Migration for clause edges, pinning, owner and tags

**Files:**
- Create: `deploy/base/migrations/0083_clause_graph.up.sql`, `deploy/base/migrations/0083_clause_graph.down.sql` (number per `check-migrations.sh`)
- Modify: `deploy/base/kustomization.yaml` (add both files after the 0082 pair)

**Interfaces:**
- Produces: table `clause_edges (from_clause bigint, to_clause bigint, type text, source text, created_at)`; columns `clauses.owner text`, `clauses.tags text[]`, `task_governed_by.pinned_version integer`.

- [ ] **Step 1: Write the up migration**

```sql
-- Clause graph (docs/specs2/12-spec-refactoring-design-tree.md S10, S12,
-- S15, S26): typed edges between clauses, a pinned version on a governing
-- link, and an owner and tags on a clause.

-- One row per edge. type is the wl: property; source says whether an
-- architect wrote it (manual) or the store derived it from the clause text
-- (derived, only ever type 'references'). The key includes type so two
-- clauses may relate in more than one way.
CREATE TABLE clause_edges (
    from_clause bigint NOT NULL REFERENCES clauses(id) ON DELETE CASCADE,
    to_clause   bigint NOT NULL REFERENCES clauses(id) ON DELETE CASCADE,
    type        text NOT NULL CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references')),
    source      text NOT NULL CHECK (source IN ('manual', 'derived')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (from_clause, to_clause, type),
    CHECK (from_clause <> to_clause)
);
CREATE INDEX clause_edges_to ON clause_edges (to_clause);

ALTER TABLE clauses ADD COLUMN owner text;
ALTER TABLE clauses ADD COLUMN tags text[] NOT NULL DEFAULT '{}';

-- A pinned link resolves to this version instead of the clause's newest (S10).
ALTER TABLE task_governed_by ADD COLUMN pinned_version integer;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_pinned_fkey
    FOREIGN KEY (clause_id, pinned_version) REFERENCES clause_versions(clause_id, version);
```

- [ ] **Step 2: Write the down migration**

```sql
ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_pinned_fkey;
ALTER TABLE task_governed_by DROP COLUMN pinned_version;
ALTER TABLE clauses DROP COLUMN tags;
ALTER TABLE clauses DROP COLUMN owner;
DROP TABLE clause_edges;
```

- [ ] **Step 3: List the pair and check**

Add to `deploy/base/kustomization.yaml` directly after the `0082_clauses.down.sql` line:

```yaml
      - migrations/0083_clause_graph.up.sql
      - migrations/0083_clause_graph.down.sql
```

Run: `./scripts/check-migrations.sh --no-fix`
Expected: exit 0. If it renumbers, use the number it chose everywhere in this task.

Run: `go test -trimpath ./internal/store -run TestGetClause -v`
Expected: PASS (the store tests apply every migration to a fresh database, so a broken migration fails here).

- [ ] **Step 4: Commit**

```bash
git add deploy/base/migrations/0083_clause_graph.up.sql deploy/base/migrations/0083_clause_graph.down.sql deploy/base/kustomization.yaml
git commit -m "Add clause_edges, a pinned version on governing links, and owner and tags on clauses"
```

---

### Task 3: Store edges (R2, R3, R9)

**Files:**
- Create: `internal/model/clauseedge.go`
- Modify: `internal/model/clause.go` (add `Edges []ClauseEdge` to `Clause`)
- Create: `internal/store/clauseedges.go`
- Modify: `internal/store/clauses.go` (`GetClause` and `GetClauseVersion` fill `Edges`)
- Modify: `internal/store/errors.go` (no new sentinel; `ErrEdgeExists` and `ErrNotFound` cover it)
- Create: `internal/store/clauseedges_test.go`

**Interfaces:**
- Produces: `model.ClauseEdge{Type, From, To, ToHeading, FromHeading, Source string; CreatedAt time.Time}`; `model.ClauseEdgeInput{Type, To string}`; `store.LinkClauses(tx *sql.Tx, fromID, toID int64, typ string) error`; `store.UnlinkClauses(tx *sql.Tx, fromID, toID int64, typ string) error`; `clauseEdges(q queryer, clauseID int64) ([]model.ClauseEdge, error)` (package-local; use `*sql.DB` or `*sql.Tx` through whatever small interface `internal/store` already has for that, or two thin wrappers).
- Consumes: `ClauseIDByRef`, `ErrEdgeExists`, `ErrNotFound`, `ErrInvalidInput`, `isUniqueViolationOn`, `pgViolation`.

- [ ] **Step 1: The model**

`internal/model/clauseedge.go`:

```go
package model

import "time"

// ClauseEdge is one typed edge between two clauses
// (docs/specs2/12-spec-refactoring-design-tree.md S12, S26). From and To are
// clause refs ("WL-CL-12"). Source is "manual" for an edge an architect
// wrote and "derived" for a references edge the store read out of the
// clause text.
type ClauseEdge struct {
	Type        string    `json:"type"` // refines | constrains | conflictsWith | references
	From        string    `json:"from"`
	FromHeading string    `json:"from_heading"`
	To          string    `json:"to"`
	ToHeading   string    `json:"to_heading"`
	Source      string    `json:"source"` // manual | derived
	CreatedAt   time.Time `json:"created_at"`
}

// ClauseEdgeInput is the body of POST and DELETE /api/v1/clauses/{id}/edges:
// the edge type and the clause at the other end.
type ClauseEdgeInput struct {
	Type string `json:"type"`
	To   string `json:"to"` // "WL-CL-12"
}
```

In `internal/model/clause.go`, after `GovernedTasks`, add:

```go
	// Edges are every typed edge in or out of this clause (S12, S26).
	Edges []ClauseEdge `json:"edges"`
	// Owner is an actor id, empty when unowned; Tags are free labels (S15).
	Owner string   `json:"owner"`
	Tags  []string `json:"tags"`
```

(Owner and Tags are declared here so Task 6 does not touch the model again; they read as empty until Task 6 fills them.)

- [ ] **Step 2: The failing store test**

`internal/store/clauseedges_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func clauseID(t *testing.T, s *Store, key string, number int64) int64 {
	t.Helper()
	var id int64
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, err = ClauseIDByRef(tx, key, number)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestLinkClauses: a manual edge appears on both clauses' details, a repeat
// is ErrEdgeExists, a self edge and an unknown type are ErrInvalidInput,
// unlinking an absent edge is ErrNotFound (S12).
func TestLinkClauses(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	_ = d
	a, b := clauseID(t, s, "P1", 1), clauseID(t, s, "P1", 3)
	ctx := context.Background()
	link := func(from, to int64, typ string) error {
		return s.Tx(ctx, func(tx *sql.Tx) error { return LinkClauses(tx, from, to, typ) })
	}
	if err := link(a, b, "refines"); err != nil {
		t.Fatal(err)
	}
	if err := link(a, b, "refines"); !errors.Is(err, ErrEdgeExists) {
		t.Errorf("repeat link: got %v, want ErrEdgeExists", err)
	}
	if err := link(a, a, "refines"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("self link: got %v, want ErrInvalidInput", err)
	}
	if err := link(a, b, "amends"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown type: got %v, want ErrInvalidInput", err)
	}
	from, err := s.GetClause(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	to, err := s.GetClause(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(from.Edges) != 1 || from.Edges[0].Type != "refines" || from.Edges[0].To != "P1-CL-3" || from.Edges[0].Source != "manual" || from.Edges[0].ToHeading != "Two" {
		t.Errorf("from side: %+v", from.Edges)
	}
	if len(to.Edges) != 1 || to.Edges[0].From != "P1-CL-1" || to.Edges[0].FromHeading != "One" {
		t.Errorf("to side: %+v", to.Edges)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkClauses(tx, a, b, "refines") }); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkClauses(tx, a, b, "refines") }); !errors.Is(err, ErrNotFound) {
		t.Errorf("unlink absent: got %v, want ErrNotFound", err)
	}
}
```

Run: `go test -trimpath ./internal/store -run TestLinkClauses -v`
Expected: FAIL to compile, `undefined: LinkClauses`.

- [ ] **Step 3: The store**

`internal/store/clauseedges.go`:

```go
package store

import (
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// clauseEdgeTypes are the wl: properties a clause edge may carry (S12, S26).
var clauseEdgeTypes = map[string]bool{"refines": true, "constrains": true, "conflictsWith": true, "references": true}

// LinkClauses writes a manual edge (12-spec-refactoring-design-tree.md S12).
// A second identical edge is ErrEdgeExists; a self edge or an unknown type is
// ErrInvalidInput; an unknown clause id is ErrNotFound.
func LinkClauses(tx *sql.Tx, fromID, toID int64, typ string) error {
	if !clauseEdgeTypes[typ] {
		return fmt.Errorf("edge type %q is not one of refines, constrains, conflictsWith, references: %w", typ, ErrInvalidInput)
	}
	if fromID == toID {
		return fmt.Errorf("a clause cannot relate to itself: %w", ErrInvalidInput)
	}
	_, err := tx.Exec(
		`INSERT INTO clause_edges (from_clause, to_clause, type, source) VALUES ($1, $2, $3, 'manual')`,
		fromID, toID, typ)
	switch {
	case isUniqueViolationOn(err, "clause_edges_pkey"):
		return fmt.Errorf("clause %d already %s clause %d: %w", fromID, typ, toID, ErrEdgeExists)
	case pgViolation(err, "23503", "clause_edges_from_clause_fkey"), pgViolation(err, "23503", "clause_edges_to_clause_fkey"):
		return fmt.Errorf("clause: %w", ErrNotFound)
	case err != nil:
		return fmt.Errorf("link clause %d %s %d: %w", fromID, typ, toID, err)
	}
	return nil
}

// UnlinkClauses removes a manual edge, or reports ErrNotFound. A derived
// edge cannot be removed by hand: it comes back on the next version anyway.
func UnlinkClauses(tx *sql.Tx, fromID, toID int64, typ string) error {
	var source string
	err := tx.QueryRow(`SELECT source FROM clause_edges WHERE from_clause = $1 AND to_clause = $2 AND type = $3`,
		fromID, toID, typ).Scan(&source)
	if err == sql.ErrNoRows {
		return fmt.Errorf("clause %d does not %s clause %d: %w", fromID, typ, toID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read edge %d %s %d: %w", fromID, typ, toID, err)
	}
	if source == "derived" {
		return fmt.Errorf("the %s edge from clause %d to %d is derived from its text; edit the clause instead: %w", typ, fromID, toID, ErrInvalidInput)
	}
	if _, err := tx.Exec(`DELETE FROM clause_edges WHERE from_clause = $1 AND to_clause = $2 AND type = $3`,
		fromID, toID, typ); err != nil {
		return fmt.Errorf("unlink clause %d %s %d: %w", fromID, typ, toID, err)
	}
	return nil
}

// clauseEdgesSQL lists every edge in or out of one clause with both refs and
// headings, outgoing first, then by type and the other clause's number.
const clauseEdgesSQL = `
SELECT e.type, e.source, e.created_at,
       pf.key, cf.number, vf.heading,
       pt.key, ct.number, vt.heading
  FROM clause_edges e
  JOIN clauses cf ON cf.id = e.from_clause
  JOIN projects pf ON pf.id = cf.project_id
  JOIN clause_versions vf ON vf.clause_id = cf.id AND vf.version = cf.version
  JOIN clauses ct ON ct.id = e.to_clause
  JOIN projects pt ON pt.id = ct.project_id
  JOIN clause_versions vt ON vt.clause_id = ct.id AND vt.version = ct.version
 WHERE e.from_clause = $1 OR e.to_clause = $1
 ORDER BY (e.from_clause = $1) DESC, e.type, ct.number, cf.number`

// scanClauseEdges turns the rows of clauseEdgesSQL into model edges.
func scanClauseEdges(rows *sql.Rows) ([]model.ClauseEdge, error) {
	out := []model.ClauseEdge{}
	for rows.Next() {
		var e model.ClauseEdge
		var fk, tk string
		var fn, tn int64
		if err := rows.Scan(&e.Type, &e.Source, &e.CreatedAt, &fk, &fn, &e.FromHeading, &tk, &tn, &e.ToHeading); err != nil {
			return nil, fmt.Errorf("scan clause edge: %w", err)
		}
		e.From = fmt.Sprintf("%s-CL-%d", fk, fn)
		e.To = fmt.Sprintf("%s-CL-%d", tk, tn)
		out = append(out, e)
	}
	return out, rows.Err()
}
```

In `internal/store/clauses.go`, in `GetClause` after the governed-tasks block (before `return c, trows.Err()`), and in `GetClauseVersion` wherever it builds its `*model.Clause`, add the edges read. Restructure `GetClause`'s tail so `trows.Err()` is checked before the next query (a still-open `Rows` on the same connection is fine on `*sql.DB` but keep the pattern clean):

```go
	if err := trows.Err(); err != nil {
		return nil, err
	}
	erows, err := s.db.QueryContext(ctx, clauseEdgesSQL, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read edges of clause %d: %w", c.ID, err)
	}
	defer erows.Close()
	if c.Edges, err = scanClauseEdges(erows); err != nil {
		return nil, err
	}
	return c, nil
```

Read `GetClauseVersion` first: if it calls `GetClause` and swaps the text, it already inherits the edges and needs nothing.

- [ ] **Step 4: Run, then the whole store package**

Run: `go test -trimpath ./internal/store -run 'TestLinkClauses|TestGetClause|TestClauseVersions' -v`
Expected: PASS.

Run: `go test -trimpath ./internal/model ./internal/store`
Expected: ok for both (`rule_test.go` accepts the new model types because Task 5's API uses them; if it rejects an unused input type at this point, note it and let Task 5 close it).

- [ ] **Step 5: Commit**

```bash
git add internal/model/clauseedge.go internal/model/clause.go internal/store/clauseedges.go internal/store/clauseedges_test.go internal/store/clauses.go
git commit -m "Store typed edges between clauses and read them on the clause detail (S12)"
```

---

### Task 4: Derived `references` edges (R4, S26)

**Files:**
- Modify: `internal/designdoc/resolve.go` (append `SectionRef`, `FindClauseRefs`, `FindSectionRefs`)
- Modify: `internal/designdoc/designdoc_test.go` (append `TestFindRefs`)
- Modify: `internal/store/clauseedges.go` (append `deriveReferences`)
- Modify: `internal/store/clauses.go` (`syncClauses` calls `deriveReferences` for inserted and revised clauses)
- Modify: `internal/store/clauseedges_test.go` (append `TestDerivedReferences`)

**Interfaces:**
- Produces: `designdoc.SectionRef{Shorthand Shorthand; Anchor string}`; `designdoc.FindClauseRefs(text string) []ClauseRef`; `designdoc.FindSectionRefs(text string) []SectionRef` (each deduplicated, in order of first appearance); `deriveReferences(tx *sql.Tx, project string, clauseID int64, text string) error`.
- Consumes: `clauseRefPattern`, `shorthandPattern`'s grammar, `ParseShorthand`, `resolveDocRef(tx, project, base) (int64, bool, error)`.

- [ ] **Step 1: The failing designdoc test**

Append to `internal/designdoc/designdoc_test.go`:

```go
func TestFindRefs(t *testing.T) {
	text := "See WL-CL-12 and WL-CL-12 again, then P1-SPEC-4#sec-2.1 and WL-ADR-7 (no anchor) and wl-cl-3."
	cs := FindClauseRefs(text)
	if len(cs) != 1 || cs[0].Key != "WL" || cs[0].Number != 12 {
		t.Errorf("clause refs: %+v", cs)
	}
	ss := FindSectionRefs(text)
	if len(ss) != 1 || ss[0].Shorthand.Key != "P1" || ss[0].Shorthand.Number != 4 || ss[0].Anchor != "sec-2.1" {
		t.Errorf("section refs: %+v", ss)
	}
}
```

Read `Shorthand`'s fields first (`sed -n 39,68p internal/designdoc/resolve.go`) and use its real field names for key, kind and number in the test and in the code below.

Run: `go test -trimpath ./internal/designdoc -run TestFindRefs`
Expected: FAIL to compile.

- [ ] **Step 2: The scanners**

Append to `internal/designdoc/resolve.go`:

```go
var (
	// clauseRefInText and sectionRefInText are the ref grammars of
	// ParseClauseRef and ParseShorthand loosened to find refs inside prose
	// (S26). A section ref must carry an anchor: a bare document ref names
	// an arrangement, and edges run between clauses.
	clauseRefInText  = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-CL-\d+\b`)
	sectionRefInText = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9}-(?:SPEC|ADR|PLAN)-\d+)#(sec-[0-9A-Za-z._-]+)\b`)
)

// SectionRef is a document shorthand plus a section anchor found in prose.
type SectionRef struct {
	Shorthand Shorthand
	Anchor    string
}

// FindClauseRefs returns every distinct clause ref in text, in order of
// first appearance.
func FindClauseRefs(text string) []ClauseRef {
	var out []ClauseRef
	seen := map[string]bool{}
	for _, m := range clauseRefInText.FindAllString(text, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		if r, ok := ParseClauseRef(m); ok {
			out = append(out, r)
		}
	}
	return out
}

// FindSectionRefs returns every distinct anchored document ref in text, in
// order of first appearance.
func FindSectionRefs(text string) []SectionRef {
	var out []SectionRef
	seen := map[string]bool{}
	for _, m := range sectionRefInText.FindAllStringSubmatch(text, -1) {
		if seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		if sh, ok := ParseShorthand(m[1]); ok {
			out = append(out, SectionRef{Shorthand: sh, Anchor: strings.TrimSuffix(m[2], ".")})
		}
	}
	return out
}
```

Add `strings` to the imports if absent. The `TrimSuffix` handles a ref that ends a sentence (`P1-SPEC-4#sec-2.` is `sec-2` plus a full stop); a real anchor never ends in a dot.

Run: `go test -trimpath ./internal/designdoc`
Expected: ok.

- [ ] **Step 3: The failing store test**

Append to `internal/store/clauseedges_test.go`:

```go
// TestDerivedReferences: a clause naming another clause by ref or by
// document section gets a derived references edge; the set is replaced on
// the next version; a manual references edge survives the rewrite; refs to
// itself, to a whole document, or to nothing yield no edge (S26).
func TestDerivedReferences(t *testing.T) {
	s := openDocStore(t)
	// P1-CL-1..3 from clauseDocV1 (sec-1, sec-1.1, sec-2), doc P1-SPEC-1.
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	body := "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nSee P1-CL-1, P1-SPEC-1#sec-2, P1-SPEC-1 (whole), P1-CL-999, and P1-CL-4 (itself).\n"
	u := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: body, CreatedBy: "stig"})
	ctx := context.Background()
	got, err := s.GetClause(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"P1-CL-1": true, "P1-CL-3": true}
	if len(got.Edges) != 2 {
		t.Fatalf("edges: %+v", got.Edges)
	}
	for _, e := range got.Edges {
		if e.Type != "references" || e.Source != "derived" || e.From != "P1-CL-4" || !want[e.To] {
			t.Errorf("unexpected edge %+v", e)
		}
	}

	// A manual references edge to P1-CL-2, then a rewrite that drops P1-CL-1.
	self, two := clauseID(t, s, "P1", 4), clauseID(t, s, "P1", 2)
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return LinkClauses(tx, self, two, "references") }); err != nil {
		t.Fatal(err)
	}
	if _, err := updateDocBody(t, s, u.ID, "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nSee P1-SPEC-1#sec-2 only.\n"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetClause(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, e := range got.Edges {
		sources[e.To] = e.Source
	}
	if len(sources) != 2 || sources["P1-CL-3"] != "derived" || sources["P1-CL-2"] != "manual" {
		t.Errorf("after rewrite: %+v", got.Edges)
	}
}
```

Run: `go test -trimpath ./internal/store -run TestDerivedReferences -v`
Expected: FAIL (`edges` is empty: nothing derives yet).

- [ ] **Step 4: Derivation in the store**

Append to `internal/store/clauseedges.go`:

```go
// deriveReferences replaces a clause's derived references edges with the
// clauses its text names (S26): every WL-CL-<n> ref, and every
// WL-SPEC-<n>#sec-<a> ref resolved to the clause arranged at that anchor. A
// ref to the clause itself, to a whole document, or to nothing contributes
// no edge. Manual edges are untouched; a derived edge that would duplicate a
// manual one is skipped by the ON CONFLICT.
func deriveReferences(tx *sql.Tx, project string, clauseID int64, text string) error {
	if _, err := tx.Exec(
		`DELETE FROM clause_edges WHERE from_clause = $1 AND type = 'references' AND source = 'derived'`,
		clauseID); err != nil {
		return fmt.Errorf("clear derived references of clause %d: %w", clauseID, err)
	}
	var targets []int64
	for _, r := range designdoc.FindClauseRefs(text) {
		var id int64
		err := tx.QueryRow(
			`SELECT c.id FROM clauses c JOIN projects p ON p.id = c.project_id WHERE p.key = $1 AND c.number = $2`,
			r.Key, r.Number).Scan(&id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s-CL-%d: %w", r.Key, r.Number, err)
		}
		targets = append(targets, id)
	}
	for _, r := range designdoc.FindSectionRefs(text) {
		docID, ok, err := resolveDocRef(tx, project, r.Shorthand.String())
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var id int64
		err = tx.QueryRow(`SELECT clause_id FROM doc_clauses WHERE doc_id = $1 AND anchor = $2`, docID, r.Anchor).Scan(&id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s#%s: %w", r.Shorthand.String(), r.Anchor, err)
		}
		targets = append(targets, id)
	}
	for _, to := range targets {
		if to == clauseID {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO clause_edges (from_clause, to_clause, type, source) VALUES ($1, $2, 'references', 'derived')
			 ON CONFLICT (from_clause, to_clause, type) DO NOTHING`, clauseID, to); err != nil {
			return fmt.Errorf("derive reference %d -> %d: %w", clauseID, to, err)
		}
	}
	return nil
}
```

`r.Shorthand.String()` must yield the `P1-SPEC-1` form `resolveDocRef` parses. Read `Shorthand` for a method that does that; if none exists, build the string with `fmt.Sprintf("%s-%s-%d", key, strings.ToUpper(kind), number)` from its fields and say so in the report. Add the `designdoc` import.

In `syncClauses` (`internal/store/clauses.go`), inside the arrangement walk's `switch`, the `insertClause` and `reviseClause` arms produce a changed clause. After the `if err != nil { return err }` that follows the switch, add:

```go
		if m == nil || m.heading != sec.Title || m.body != sec.Body {
			if err := deriveReferences(tx, project, id, sec.Title+"\n"+sec.Body); err != nil {
				return err
			}
		}
```

(`project` is the variable `syncClauses` already reads from `docs.project_id` at its top.)

Run: `go test -trimpath ./internal/store -run 'TestDerivedReferences|TestSyncClauses|TestLinkClauses|TestEditClause' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/designdoc/resolve.go internal/designdoc/designdoc_test.go internal/store/clauseedges.go internal/store/clauseedges_test.go internal/store/clauses.go
git commit -m "Derive references edges from clause text on every version (S26)"
```

---

### Task 5: Edge API and CLI (R3, R8, R11)

**Files:**
- Create: `internal/api/clauseedges.go` (`linkClause`, `unlinkClause`)
- Modify: `internal/api/router.go` (two `routeGuards` entries), `internal/api/server.go` (two `r.api` lines next to the clause routes)
- Create: `internal/api/clauseedges_test.go`
- Create: `internal/cli/clauseedges.go` (`LinkClauses`, `UnlinkClauses`; extend `ClauseRender` in `cli/clauses.go` with edge lines)
- Modify: `internal/cli/clauses_test.go` (render test)
- Modify: `internal/cmd/clause.go` (`link`, `unlink` subcommands)
- Modify: `plugins/claude/lode/skills/worklode/references/commands.md` (regenerate; `TestCommandReference` says how)

**Interfaces:**
- Produces: `POST /api/v1/clauses/{id}/edges` body `model.ClauseEdgeInput`, 201 with the input echoed; `DELETE /api/v1/clauses/{id}/edges` body `model.ClauseEdgeInput`, 204; `(c *Client) LinkClauses(ctx, ref string, in model.ClauseEdgeInput) ([]byte, error)`; `(c *Client) UnlinkClauses(ctx, ref string, in model.ClauseEdgeInput) ([]byte, error)`.
- Consumes: `clauseRef(w, r)`, `store.ClauseIDByRef`, `store.LinkClauses`, `store.UnlinkClauses`, `s.recordEvent(ctx, source, eventType, v, apply)`, `s.mapStoreErr`, `readJSON`, `writeBodyErr`, `writeJSON`, `designdoc.ParseClauseRef`.

- [ ] **Step 1: The failing API test**

`internal/api/clauseedges_test.go`:

```go
package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestClauseEdgesAPI: link, read on both details, repeat is 409, bad type is
// 422, unknown target is 404, unlink is 204 and absent unlink is 404.
func TestClauseEdgesAPI(t *testing.T) {
	st, h, token := newTestServer(t)
	project := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, project, "t", clauseDocV1) // WL-CL-1..3

	body := `{"type":"constrains","to":"WL-CL-3"}`
	rr := doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(body))
	if rr.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(body))
	if rr.Code != http.StatusConflict {
		t.Errorf("repeat link: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(`{"type":"amends","to":"WL-CL-3"}`))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad type: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(`{"type":"refines","to":"WL-CL-999"}`))
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown target: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(`{"type":"refines","to":"nope"}`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed target: %d", rr.Code)
	}

	var c model.Clause
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-3", token, nil)
	decodeInto(t, rr, &c)
	if len(c.Edges) != 1 || c.Edges[0].From != "WL-CL-1" || c.Edges[0].Type != "constrains" {
		t.Errorf("edges on target: %+v", c.Edges)
	}

	rr = doReq(t, h, http.MethodDelete, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(body))
	if rr.Code != http.StatusNoContent {
		t.Errorf("unlink: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodDelete, "/api/v1/clauses/WL-CL-1/edges", token, strings.NewReader(body))
	if rr.Code != http.StatusNotFound {
		t.Errorf("unlink absent: %d", rr.Code)
	}
}
```

Read `internal/api/clauses_test.go` for how it creates the document (a helper like `createDocViaAPI` or a direct store call) and use that; the name above is a placeholder for whichever exists. `clauseDocV1` is already declared in that test file.

Run: `go test -trimpath ./internal/api -run TestClauseEdgesAPI -v`
Expected: FAIL with 404 or a router panic (route missing).

- [ ] **Step 2: Handlers and routes**

`internal/api/clauseedges.go`:

```go
package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// clauseEdgeReq reads the edge body and both clause refs, or answers 400.
func (s *server) clauseEdgeReq(w http.ResponseWriter, r *http.Request) (from designdoc.ClauseRef, to designdoc.ClauseRef, req model.ClauseEdgeInput, ok bool) {
	from, ok = clauseRef(w, r)
	if !ok {
		return
	}
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return from, to, req, false
	}
	to, ok = designdoc.ParseClauseRef(req.To)
	if !ok {
		writeErr(w, http.StatusBadRequest, "to must look like WL-CL-12")
		return from, to, req, false
	}
	return from, to, req, true
}

// linkClause handles POST /api/v1/clauses/{id}/edges: an architect relates
// two clauses (12-spec-refactoring-design-tree.md S12).
func (s *server) linkClause(w http.ResponseWriter, r *http.Request) {
	from, to, req, ok := s.clauseEdgeReq(w, r)
	if !ok {
		return
	}
	err := s.recordEvent(r.Context(), "cli", "clause.linked", req, func(tx *sql.Tx, _ int64) error {
		fromID, err := store.ClauseIDByRef(tx, from.Key, from.Number)
		if err != nil {
			return err
		}
		toID, err := store.ClauseIDByRef(tx, to.Key, to.Number)
		if err != nil {
			return err
		}
		return store.LinkClauses(tx, fromID, toID, req.Type)
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// unlinkClause handles DELETE /api/v1/clauses/{id}/edges.
func (s *server) unlinkClause(w http.ResponseWriter, r *http.Request) {
	from, to, req, ok := s.clauseEdgeReq(w, r)
	if !ok {
		return
	}
	err := s.recordEvent(r.Context(), "cli", "clause.unlinked", req, func(tx *sql.Tx, _ int64) error {
		fromID, err := store.ClauseIDByRef(tx, from.Key, from.Number)
		if err != nil {
			return err
		}
		toID, err := store.ClauseIDByRef(tx, to.Key, to.Number)
		if err != nil {
			return err
		}
		return store.UnlinkClauses(tx, fromID, toID, req.Type)
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Read `s.recordEvent`'s signature (`internal/api/server.go` around line 1589) and match it exactly. In `router.go` add next to the clause entries:

```go
	"POST /api/v1/clauses/{id}/edges":   guardedAny(permDocWrite),
	"DELETE /api/v1/clauses/{id}/edges": guardedAny(permDocWrite),
```

In `server.go` next to the clause `r.api` lines:

```go
	r.api("POST /api/v1/clauses/{id}/edges", s.linkClause)
	r.api("DELETE /api/v1/clauses/{id}/edges", s.unlinkClause)
```

Run: `go test -trimpath ./internal/api -run 'TestClauseEdgesAPI|TestNewServer' -v`
Expected: PASS.

- [ ] **Step 3: CLI client and render**

`internal/cli/clauseedges.go`:

```go
package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// LinkClauses calls POST /api/v1/clauses/{ref}/edges.
func (c *Client) LinkClauses(ctx context.Context, ref string, in model.ClauseEdgeInput) ([]byte, error) {
	_, raw, err := doJSON[model.ClauseEdgeInput](ctx, c, http.MethodPost, "/api/v1/clauses/"+url.PathEscape(ref)+"/edges", in, "clause edge")
	return raw, err
}

// UnlinkClauses calls DELETE /api/v1/clauses/{ref}/edges.
func (c *Client) UnlinkClauses(ctx context.Context, ref string, in model.ClauseEdgeInput) ([]byte, error) {
	return c.doNoContent(ctx, http.MethodDelete, "/api/v1/clauses/"+url.PathEscape(ref)+"/edges", in)
}
```

Read `internal/cli/governedby.go` for how `Ungovern` issues a DELETE with a body and returns (the name `doNoContent` above is a placeholder for the helper it uses); mirror it exactly.

In `internal/cli/clauses.go`'s `ClauseRender`, after the `governs:` loop, add:

```go
	for _, e := range c.Edges {
		if e.From == c.Ref {
			fmt.Fprintf(w, "  %-9s %s %s (%s)\n", e.Type+":", e.To, e.ToHeading, e.Source)
		} else {
			fmt.Fprintf(w, "  %-9s %s %s (%s, incoming)\n", e.Type+":", e.From, e.FromHeading, e.Source)
		}
	}
```

Add a render test in `internal/cli/clauses_test.go` with one outgoing manual `constrains` edge and one incoming derived `references` edge, asserting both lines appear with `(manual)` and `(derived, incoming)`.

- [ ] **Step 4: `lode clause link` and `unlink`**

In `internal/cmd/clause.go`, register two more subcommands in `newClauseCmd` and add:

```go
// edgeTypeFlags binds the four edge-type flags and returns a resolver that
// yields the one type set, or an error when none or several are.
func edgeTypeFlags(cmd *cobra.Command) func() (string, error) {
	var refines, constrains, conflicts, references string
	cmd.Flags().StringVar(&refines, "refines", "", "clause this one narrows or details")
	cmd.Flags().StringVar(&constrains, "constrains", "", "clause this one must hold alongside")
	cmd.Flags().StringVar(&conflicts, "conflicts-with", "", "clause this one is in recorded tension with")
	cmd.Flags().StringVar(&references, "references", "", "clause this one points at (a derived edge is written for you when the text names it)")
	cmd.MarkFlagsOneRequired("refines", "constrains", "conflicts-with", "references")
	cmd.MarkFlagsMutuallyExclusive("refines", "constrains", "conflicts-with", "references")
	return func() (string, error) {
		switch {
		case refines != "":
			return "refines", nil
		case constrains != "":
			return "constrains", nil
		case conflicts != "":
			return "conflictsWith", nil
		default:
			return "references", nil
		}
	}
}

func edgeTarget(cmd *cobra.Command) string {
	for _, f := range []string{"refines", "constrains", "conflicts-with", "references"} {
		if v, _ := cmd.Flags().GetString(f); v != "" {
			return v
		}
	}
	return ""
}

func newClauseLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <ref>",
		Short: "Relate a clause to another: --refines, --constrains, --conflicts-with or --references <ref>",
		Args:  cobra.ExactArgs(1),
	}
	typ := edgeTypeFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		t, err := typ()
		if err != nil {
			return err
		}
		c, err := newAPIClient()
		if err != nil {
			return err
		}
		to := edgeTarget(cmd)
		raw, err := c.LinkClauses(cmd.Context(), args[0], model.ClauseEdgeInput{Type: t, To: to})
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			printRaw(cmd, raw)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", args[0], t, to)
		return nil
	}
	return cmd
}

func newClauseUnlinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlink <ref>",
		Short: "Remove a relation written with clause link",
		Args:  cobra.ExactArgs(1),
	}
	typ := edgeTypeFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		t, err := typ()
		if err != nil {
			return err
		}
		c, err := newAPIClient()
		if err != nil {
			return err
		}
		to := edgeTarget(cmd)
		raw, err := c.UnlinkClauses(cmd.Context(), args[0], model.ClauseEdgeInput{Type: t, To: to})
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			printRaw(cmd, raw)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s no longer %s %s\n", args[0], t, to)
		return nil
	}
	return cmd
}
```

Add `fmt` to the imports. The confirmation lines carry no timestamp or table, so they may live in `internal/cmd` (CLAUDE.md, "what legitimately renders in internal/cmd").

Run: `go test -trimpath ./internal/cmd -run 'TestNameRule|TestCommandReference|TestRenderRule' -v`
Expected: `TestCommandReference` fails until `commands.md` is regenerated; follow its message, regenerate, rerun. All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/clauseedges.go internal/api/clauseedges_test.go internal/api/router.go internal/api/server.go internal/cli/clauseedges.go internal/cli/clauses.go internal/cli/clauses_test.go internal/cmd/clause.go plugins/claude/lode/skills/worklode/references/commands.md
git commit -m "Relate clauses over the API and lode clause link (S12)"
```

---

### Task 6: Owner and tags (R7, R8, R11)

**Files:**
- Modify: `internal/model/clause.go` (append `ClauseMetaInput`)
- Modify: `internal/store/clauses.go` (`GetClause` selects `owner`, `tags`; append `SetClauseMeta`)
- Modify: `internal/store/clauses_test.go` (append `TestSetClauseMeta`)
- Modify: `internal/api/clauses.go` (append `patchClause`), `internal/api/router.go`, `internal/api/server.go` (`PATCH /api/v1/clauses/{id}`)
- Modify: `internal/api/clauses_test.go` (append `TestClauseMetaAPI`)
- Modify: `internal/cli/clauses.go` (`SetClauseMeta`; `ClauseRender` prints owner and tags), `internal/cli/clauses_test.go`
- Modify: `internal/cmd/clause.go` (`set owner`, `set tags`)
- Modify: `plugins/claude/lode/skills/worklode/references/commands.md` (regenerate)

**Interfaces:**
- Produces: `model.ClauseMetaInput{Owner *string; Tags *[]string}`; `store.SetClauseMeta(tx *sql.Tx, clauseID int64, in model.ClauseMetaInput) error`; `PATCH /api/v1/clauses/{id}` 200 with `model.Clause`; `(c *Client) SetClauseMeta(ctx, ref string, in model.ClauseMetaInput) (model.Clause, []byte, error)`.
- Consumes: `pq.Array` or `pgtype` for `text[]` (read how `internal/store` scans an existing `text[]` column, e.g. `grep -rn "pq.Array\|pgtype.FlatArray\|\[\]string" internal/store/tasks.go | head`, and use the same mechanism).

- [ ] **Step 1: The model**

Append to `internal/model/clause.go`:

```go
// ClauseMetaInput is the body of PATCH /api/v1/clauses/{id} (S15). A nil
// field leaves the column alone; a present field replaces it, so an empty
// Owner clears the owner and an empty Tags clears the tags.
type ClauseMetaInput struct {
	Owner *string   `json:"owner,omitempty"`
	Tags  *[]string `json:"tags,omitempty"`
}
```

- [ ] **Step 2: Failing store test**

Append to `internal/store/clauses_test.go`:

```go
func TestSetClauseMeta(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	ctx := context.Background()
	id := clauseID(t, s, "P1", 1)
	owner, tags := "stig", []string{"storage", "search"}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetClauseMeta(tx, id, model.ClauseMetaInput{Owner: &owner, Tags: &tags})
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetClause(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner != "stig" || len(c.Tags) != 2 || c.Tags[0] != "storage" {
		t.Errorf("after set: owner %q tags %v", c.Owner, c.Tags)
	}
	none := ""
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetClauseMeta(tx, id, model.ClauseMetaInput{Owner: &none})
	}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetClause(ctx, "P1", 1)
	if c.Owner != "" || len(c.Tags) != 2 {
		t.Errorf("owner cleared, tags kept: owner %q tags %v", c.Owner, c.Tags)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetClauseMeta(tx, 999999, model.ClauseMetaInput{Owner: &owner})
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown clause: %v", err)
	}
}
```

Add the `errors` and `model` imports if the file lacks them. `clauseID` is Task 3's helper.

Run: `go test -trimpath ./internal/store -run TestSetClauseMeta`
Expected: FAIL to compile.

- [ ] **Step 3: Store**

In `GetClause`'s first SELECT add `c.owner, c.tags` and scan into `sql.NullString` and the array type the package uses, then set `c.Owner = owner.String` and `c.Tags = tags` (never nil: `if c.Tags == nil { c.Tags = []string{} }`). Do the same in `GetClauseVersion` if it has its own SELECT. Append:

```go
// SetClauseMeta sets a clause's owner and tags (S15). A nil field is left
// alone. Dates never live on a clause; they reach it through its tasks.
func SetClauseMeta(tx *sql.Tx, clauseID int64, in model.ClauseMetaInput) error {
	if in.Owner == nil && in.Tags == nil {
		return fmt.Errorf("nothing to set: %w", ErrInvalidInput)
	}
	res, err := tx.Exec(
		`UPDATE clauses
		    SET owner = CASE WHEN $2::boolean THEN nullif($3, '') ELSE owner END,
		        tags  = CASE WHEN $4::boolean THEN $5 ELSE tags END,
		        updated_at = now()
		  WHERE id = $1`,
		clauseID, in.Owner != nil, deref(in.Owner), in.Tags != nil, arrayParam(derefTags(in.Tags)))
	if err != nil {
		return fmt.Errorf("set meta of clause %d: %w", clauseID, err)
	}
	return requireOneAffected(res, fmt.Sprintf("clause %d", clauseID), ErrNotFound)
}
```

`deref`, `derefTags` and `arrayParam` are placeholders: use whatever the package already has for a nullable string and a `text[]` parameter (search before writing; if nothing exists, write two three-line helpers next to `SetClauseMeta`). `requireOneAffected` exists in `errors.go`; read its signature.

Run: `go test -trimpath ./internal/store -run 'TestSetClauseMeta|TestGetClause' -v`
Expected: PASS.

- [ ] **Step 4: API**

Append to `internal/api/clauses.go`:

```go
// patchClause handles PATCH /api/v1/clauses/{id}: owner and tags (S15). The
// text is PUT; this is the metadata write, the way docs split body from owner.
func (s *server) patchClause(w http.ResponseWriter, r *http.Request) {
	ref, ok := clauseRef(w, r)
	if !ok {
		return
	}
	var req model.ClauseMetaInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	err := s.recordEvent(r.Context(), "cli", "clause.updated", req, func(tx *sql.Tx, _ int64) error {
		id, err := store.ClauseIDByRef(tx, ref.Key, ref.Number)
		if err != nil {
			return err
		}
		return store.SetClauseMeta(tx, id, req)
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	c, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
```

Route: `"PATCH /api/v1/clauses/{id}": guardedAny(permDocWrite)` and `r.api("PATCH /api/v1/clauses/{id}", s.patchClause)`.

Test `TestClauseMetaAPI` in `internal/api/clauses_test.go`: PATCH `{"owner":"stig","tags":["a","b"]}` is 200 with those fields; PATCH `{}` is 422; PATCH on `WL-CL-999` is 404.

- [ ] **Step 5: CLI**

`internal/cli/clauses.go`: `SetClauseMeta` calls PATCH through `doJSON[model.Clause]`. `ClauseRender` prints, after `version:`:

```go
	if c.Owner != "" {
		fmt.Fprintf(w, "  owner:    %s\n", c.Owner)
	}
	if len(c.Tags) > 0 {
		fmt.Fprintf(w, "  tags:     %s\n", strings.Join(c.Tags, ", "))
	}
```

`internal/cmd/clause.go`: a `set` group with `owner <ref> <actor>` (`""` or `-` clears; pass `Owner: &v` with `-` mapped to `""`) and `tags <ref> [tag ...]` (no tags clears; pass `Tags: &args[1:]`, which is `[]string{}` when empty). Both print `cli.ClauseRender` or `--json`. Read `newTaskSetCmd` (`internal/cmd/task.go:988`) for the group shape. Regenerate `commands.md`.

Run: `go test -trimpath ./internal/api -run 'TestClauseMetaAPI|TestNewServer' -v && go test -trimpath ./internal/cli ./internal/cmd`
Expected: PASS / ok.

- [ ] **Step 6: Commit**

```bash
git add internal/model/clause.go internal/store/clauses.go internal/store/clauses_test.go internal/api/clauses.go internal/api/clauses_test.go internal/api/router.go internal/api/server.go internal/cli/clauses.go internal/cli/clauses_test.go internal/cmd/clause.go plugins/claude/lode/skills/worklode/references/commands.md
git commit -m "Give a clause an owner and tags over PATCH and lode clause set (S15)"
```

---

### Task 7: Pinning a governing link (R5, R6, S10)

**Files:**
- Modify: `internal/model/governedby.go` (`GovernInput.Pin`, `TaskGovernance.Pinned`, `TaskGovernance.URL`)
- Modify: `internal/store/governedby.go` (`Govern` gains `pin bool`; `GovernedBy` reads `pinned_version`, builds `URL`)
- Modify: `internal/store/docplanning.go` (`Govern(..., "plan", false)`)
- Modify: `internal/store/governedby_test.go` (append `TestGovernPin`)
- Modify: `internal/api/governedby.go` (`store.Govern(tx, id, clauseID, "manual", req.Pin)`), `internal/api/tasks.go` (create path passes `false`)
- Modify: `internal/api/governedby_test.go` (pin case)
- Modify: `internal/cli/governedby.go` (`Govern(ctx, id, clause string, pin bool)`), `internal/cli/tasks.go` (`TaskDetailRender` prints `pinned vN`), `internal/cli/governedby_test.go`
- Modify: `internal/cmd/task.go` (`govern` gains `--pin`; it no longer goes through `newTaskClauseCmd`, `ungovern` still does)
- Modify: `plugins/claude/lode/skills/worklode/references/commands.md` (regenerate)

**Interfaces:**
- Produces: `store.Govern(tx *sql.Tx, taskID string, clauseID int64, source string, pin bool) error`; `model.GovernInput{Clause string; Pin bool}`; `model.TaskGovernance{..., Pinned int, URL string}`; `(c *Client) Govern(ctx, id, clause string, pin bool) ([]byte, error)`.
- Consumes: the existing callers listed above (`grep -rn "store.Govern(\|Govern(tx" internal/ | grep -v _test`).

- [ ] **Step 1: Failing store test**

Append to `internal/store/governedby_test.go`:

```go
// TestGovernPin: a pinned link resolves to the version current at link time
// after the clause moves on; re-governing without the pin unpins (S10).
func TestGovernPin(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	now := s.Now()
	task := createTask(t, s, now, TaskInput{ProjectID: "p1", Title: "t", Kind: "feature", Priority: "medium", CreatedBy: "stig"})
	ctx := context.Background()
	id := clauseID(t, s, "P1", 3)
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return Govern(tx, task.ID, id, "manual", true) }); err != nil {
		t.Fatal(err)
	}
	// Land a revision so the clause is at v2.
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := updateRevision(t, s, d.ID, strings.Replace(clauseDocV1, "C.\n", "C changed.\n", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	g, err := s.GovernedBy(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || g[0].Pinned != 1 || g[0].Current != 2 || g[0].URL != "/projects/p1/clause/3/1" {
		t.Errorf("pinned: %+v", g)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return Govern(tx, task.ID, id, "manual", false) }); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GovernedBy(ctx, task.ID)
	if len(g) != 1 || g[0].Pinned != 0 || g[0].URL != "/projects/p1/clause/3" {
		t.Errorf("unpinned: %+v", g)
	}
}
```

Read `createTask` and `TaskInput` on disk for the field names (1b's ledger says the test helpers follow disk). `acceptDoc`, `reviseDoc`, `updateRevision`, `acceptRevision` are in `docs_test.go`.

Run: `go test -trimpath ./internal/store -run TestGovernPin`
Expected: FAIL to compile (`Govern` takes four arguments).

- [ ] **Step 2: Store**

`Govern`:

```go
func Govern(tx *sql.Tx, taskID string, clauseID int64, source string, pin bool) error {
	_, err := tx.Exec(
		`INSERT INTO task_governed_by (task_id, clause_id, clause_version, source, pinned_version)
		 SELECT $1, c.id, c.version, $3, CASE WHEN $4 THEN c.version END FROM clauses c WHERE c.id = $2
		 ON CONFLICT (task_id, clause_id) DO UPDATE SET pinned_version = EXCLUDED.pinned_version`,
		taskID, clauseID, source, pin)
```

Keep the FK mapping that follows. Update the doc comment: re-governing updates the pin and nothing else. `GovernedBy`'s SELECT adds `g.pinned_version, c.project_id` and scans into `sql.NullInt64` and a string; then:

```go
		g.Pinned = int(pinned.Int64)
		g.URL = fmt.Sprintf("/projects/%s/clause/%d", projectID, number)
		if pinned.Valid {
			g.URL += fmt.Sprintf("/%d", pinned.Int64)
		}
```

Model:

```go
	Pinned int    `json:"pinned,omitempty"` // the version the link is pinned to, 0 when it follows the newest
	URL    string `json:"url"`              // canonical clause page, with /<ver> when pinned (S10, S20)
```

and `GovernInput` gains `Pin bool \`json:"pin,omitempty"\``.

Fix the two non-test callers: `docplanning.go` passes `false`; `api/tasks.go`'s create loop passes `false`; `api/governedby.go` passes `req.Pin`.

Run: `go build -trimpath ./... && go test -trimpath ./internal/store -run 'TestGovern|TestAcceptPlan' -v`
Expected: PASS.

- [ ] **Step 3: API test, CLI, cmd**

In `internal/api/governedby_test.go` add a case: POST `{"clause":"WL-CL-1","pin":true}` then GET the task, assert `governed_by[0].pinned == 1` and `url` ends in `/1`.

`internal/cli/governedby.go`: `Govern(ctx, id, clause string, pin bool)` sends `model.GovernInput{Clause: clause, Pin: pin}`. `TaskDetailRender`'s governed-by line gains `, pinned vN` when `Pinned > 0`; extend the render test in `internal/cli/governedby_test.go` with a pinned row.

`internal/cmd/task.go`: replace `newTaskGovernCmd` with its own command carrying `--by` (required) and `--pin`, resolving the task id as `newTaskClauseCmd` does and calling `c.Govern(cmd.Context(), id, by, pin)`, then printing `"%s is now governed by %s"` (add ` (pinned)` when pinned) or `--json`. `ungovern` keeps `newTaskClauseCmd`. Regenerate `commands.md`.

Run: `make test`
Expected: all ok.

- [ ] **Step 4: Commit**

```bash
git add internal/model/governedby.go internal/store/governedby.go internal/store/governedby_test.go internal/store/docplanning.go internal/api/governedby.go internal/api/governedby_test.go internal/api/tasks.go internal/cli/governedby.go internal/cli/governedby_test.go internal/cli/tasks.go internal/cmd/task.go plugins/claude/lode/skills/worklode/references/commands.md
git commit -m "Pin a governing link to the clause version current when it was made (S10)"
```

---

### Task 8: Cockpit clause page shows owner, tags and edges

**Files:**
- Modify: `internal/ui/clause.templ` (owner and tags chips in the head card; an "Edges" card between "Governs" and "Versions") and regenerate `clause_templ.go`
- Modify: `internal/api/clausepage_test.go` (assert the edge line and owner render)

**Interfaces:** none new; `ui.ClauseView.Clause` already carries `Edges`, `Owner`, `Tags`.

- [ ] **Step 1: Template**

In the head card's `<p>` after the version chip:

```templ
					if v.Clause.Owner != "" {
						<span class="chip plain">owner { v.Clause.Owner }</span>
					}
					for _, tag := range v.Clause.Tags {
						<span class="chip plain">{ tag }</span>
					}
```

New card after "Governs":

```templ
			<section class="card">
				<div class="hd"><h3>Edges</h3></div>
				<div class="bd">
					if len(v.Clause.Edges) == 0 {
						<p class="muted">No edges.</p>
					} else {
						<ul>
							for _, e := range v.Clause.Edges {
								<li>
									if e.From == v.Clause.Ref {
										<span class="mono">{ e.Type }</span>{ " " }
										<a href={ templ.SafeURL("/clauses/" + e.To) }>{ e.To }</a>{ " " }{ e.ToHeading }
									} else {
										<a href={ templ.SafeURL("/clauses/" + e.From) }>{ e.From }</a>{ " " }{ e.FromHeading }{ " " }
										<span class="mono">{ e.Type }</span>{ " this" }
									}
									<span class="muted small">{ " " }{ e.Source }</span>
								</li>
							}
						</ul>
					}
				</div>
			</section>
```

`/clauses/<ref>` is 1b's resolving redirect. Run `templ generate` from the repo root.

- [ ] **Step 2: Test and commit**

In `internal/api/clausepage_test.go`, in the existing page test (or a sibling), link `WL-CL-1 constrains WL-CL-3` through the store, set an owner, fetch the page for `WL-CL-3`, and assert the body contains `constrains`, `WL-CL-1` and `owner stig`.

Run: `go test -trimpath ./internal/api -run 'TestClausePage' -v && go test -trimpath ./internal/ui`
Expected: PASS / ok.

```bash
git add internal/ui/clause.templ internal/ui/clause_templ.go internal/api/clausepage_test.go
git commit -m "Show a clause's owner, tags and edges on its cockpit page"
```

---

### Task 9: Specs 03, 05, 09 and 12 describe what shipped (R10)

**Files:**
- Modify: `docs/specs2/03-tasks-and-execution.md` §4 ("Governing clauses" paragraph: pinning)
- Modify: `docs/specs2/05-documents.md` §3 (table rows) and §4 (the clause paragraph: edges, derivation, owner, tags)
- Modify: `docs/specs2/09-cli-and-skills.md` (the `clause` row in §2's entity table; the `task` row's `govern` gains `--pin`; the `lode clause` views row if `set` counts as a view there)
- Modify: `docs/specs2/12-spec-refactoring-design-tree.md` ("Recorded from increment 1" gains the increment 2 rulings that a reader of the code needs)

**Interfaces:** none.

- [ ] **Step 1: 03 §4**

After the sentence ending "and the link resolves to the clause's newest version." insert: "A link may be pinned when it is made (`--pin`); a pinned link resolves to that version and reports the versioned page `/projects/<proj>/clause/<n>/<ver>`. Governing the same clause again without the pin unpins it."

- [ ] **Step 2: 05 §3 and §4**

Table: after the `task_governed_by` row change its description to end with ", `pinned_version` when the link is pinned (S10)"; add after the `clauses` row's description ", `owner`, `tags`"; add a new row:

```markdown
| `clause_edges` | `(from_clause, to_clause, type)` primary key, `source` (`manual` or `derived`): the typed edges between clauses, `refines`, `constrains`, `conflictsWith` and `references` (12-spec-refactoring-design-tree.md S12, S26) |
```

§4, append to the clause paragraph: "A clause relates to other clauses through typed edges written by an architect (`refines`, `constrains`, `conflictsWith`, `references`; 07-knowledge-graph-and-search.md §2) and through `references` edges the store derives from the clause text on every version: each `WL-CL-<n>` ref and each `WL-SPEC-<n>#sec-<a>` ref that resolves to a clause becomes an edge, the derived set replaces the previous one, and a ref to the clause itself, to a whole document or to nothing contributes no edge. A clause carries an owner (an actor id) and tags; dates stay on tasks and milestones (S15)."

- [ ] **Step 3: 09**

`clause` row: `edit`, `link`/`unlink`, `set owner`, `set tags`; view `versions`. `task` row: `govern [--pin]`/`ungovern`.

- [ ] **Step 4: 12**

Append to "Recorded from increment 1" (rename the heading to "Recorded from increments 1 and 2"):

```markdown
- S37 (S12, S26 applied): one table holds every clause edge with its type and its source, `manual` or `derived`. A derived edge is always `references` and is rewritten from the clause text on every version; a manual `references` edge survives that rewrite. `conflictsWith` is stored in the direction written and read from both ends. Named clusters are tags for now.
- S38 (S26 applied): derivation resolves `WL-CL-<n>` refs and anchored document refs to clauses and drops everything else without a record: a whole-document ref, a section with no clause, a clause in no project, the clause itself.
- S39 (S10 applied): a link is pinned at the version current when it is made or not at all; governing the same clause again sets or clears the pin. A pinned link reports the versioned page URL.
- S40 (S15 applied): owner is an actor id stored as text with no constraint, as `docs.owner` is; tags are free text. Set over `PATCH /api/v1/clauses/{id}`.
- S41 (S17 checked): the clause code keys nothing on project size. One counter per project, one table per fact, no per-project thresholds.
```

For S41, first run `grep -rn "size\|threshold\|large\|small" internal/store/clauses.go internal/store/clauseedges.go internal/store/governedby.go` and read the hits; if any is a project-size assumption, report it instead of writing S41.

- [ ] **Step 5: Style check and commit**

Run: `git diff | grep '^+' | grep -nE '—|, not |rather than|instead of'`
Expected: no output.

```bash
git add docs/specs2/03-tasks-and-execution.md docs/specs2/05-documents.md docs/specs2/09-cli-and-skills.md docs/specs2/12-spec-refactoring-design-tree.md
git commit -m "Record clause edges, derived references, pinning and clause metadata in specs 03, 05, 09 and 12"
```

---

## Self-review

Spec coverage: S12 is Tasks 1 to 5 (vocabulary, table, store, API, CLI) with named clusters deferred to tags (R2, S37); S26 is Task 4 (R4, S38); S10's pinning is Task 7 (R5, R6, S39); S15 is Tasks 6 and 8 (R7, S40); S17 is Task 9's check (R10, S41). The cockpit reads everything through `model.Clause` (Task 8). The sprawl metrics stay deferred.

Type consistency: `model.ClauseEdge{Type, From, FromHeading, To, ToHeading, Source, CreatedAt}` in Task 3 is what `ClauseRender` (Task 5) and `clause.templ` (Task 8) read; `model.ClauseEdgeInput{Type, To}` is the body Tasks 5's API and CLI share; `LinkClauses(tx, fromID, toID, typ)` and `UnlinkClauses(tx, fromID, toID, typ)` are called with the same arity in Task 5's handlers; `deriveReferences(tx, project, clauseID, text)` in Task 4 is called from `syncClauses` with its existing `project` variable; `Govern(tx, taskID, clauseID, source, pin)` in Task 7 has three non-test callers, each named; `model.ClauseMetaInput{Owner *string, Tags *[]string}` is what `SetClauseMeta` and `patchClause` take; `model.TaskGovernance.Pinned/URL` are read by `TaskDetailRender`. `clauseID(t, s, key, number)` from Task 3's test file is reused by Tasks 4, 6 and 7's tests in the same package.

Placeholders: the named ones (`createDocViaAPI`, `doNoContent`, `deref`, `derefTags`, `arrayParam`, `Shorthand.String()`) each carry an instruction to find the real helper on disk and a fallback. Nothing else is left open.
