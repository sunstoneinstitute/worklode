# Clause Model, Increment 4b (lineage, the refactor primitive, the old-spec migration) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clauses carry split and merge lineage (`wasDerivedFrom`, many-to-many `supersededBy`). A refactor is one transactional act over a clause-to-clause map: `lode clause supersede --map <file>` withdraws the old clauses, writes the `supersededBy` edges, marks the affected plans stale and records an event on every task the refactor touches. A task governed by a withdrawn clause reports the live clauses it resolves to through the chain. A generator turns `docs/specs2/section-map.tsv` into the map that migrates the 47 old specs (A2).

**Architecture:** Lineage edges are two more types in the `clause_edges` table increment 2 built, with a third `source` value, `refactor`. `wasDerivedFrom` is written by hand through the existing `lode clause link`. `supersededBy` has one writer, `SupersedeClauses` in `internal/store/clausesupersede.go`, which resolves every ref in one transaction (section refs through `ensureClauses`, so an old spec that predates the clause tables gets its clauses minted on the way), writes the edges, and calls increment 3's `SetClauseStatus(..., "withdrawn", ...)`, which already marks arranging plans stale (S23). Resolution through the chain is one recursive query added to `GovernedBy`. The migration generator is a Python script in `scripts/` with a test, run by hand against the backbone once the new specs are imported.

**Tech Stack:** Go, Postgres via `database/sql`, cobra CLI, Python 3 for the generator.

**Spec:** `docs/specs2/12-spec-refactoring-design-tree.md` (S3, S22, S24, A2) and GitHub issue #687's "Increment 4" section. Executors read both.

**Stacked on:** branch `clause-increment-3` (increment 3, rebased onto `origin/main`). This branch is `clause-increment-4b`; its PR is stacked on increment 3's PR. The sibling `clause-increment-4a` holds migration 0085 and spec 12 S50 to S54; this branch takes migration 0086 and S55 onwards.

## Rulings made while planning

The user was not available for brainstorming, so these are decided here. Each names what it costs if wrong.

- **R1 Stored edge names are S22's: `supersededBy` and `wasDerivedFrom`.** The ontology already reuses `dct:isReplacedBy` and `prov:wasDerivedFrom` (ns/ontology.ttl's reuse list) and forbids a new `wl:supersededSection`. The two edge types map to those reused terms, noted in the reuse list comment; no new `wl:` term is minted. Costs a comment edit if a projection needs its own predicate.
- **R2 Every clause on a map's left side becomes `withdrawn`, with or without successors.** A2 and S27 say `withdrawn`; S22's merge makes B `supersededBy` A, and B has no live continuation of its own. The `superseded` clause status stays unused by this increment. The S23 trigger fires on `withdrawn` only, so plans arranging a refactored clause go stale with no new code. Costs a status migration if `superseded` is later wanted for merged clauses.
- **R3 A split is document-first; the refactor primitive does not edit text.** Splitting A into A' and B is an ordinary document edit (A keeps its identity as a new version, B is minted by the arranging document's write, as today) followed by `lode clause link WL-CL-B --derived-from WL-CL-A`. A merge is an edit of A plus `lode clause supersede` with `WL-CL-B -> WL-CL-A`. No `split` or `merge` verb. Costs one verb if the two-step form proves error-prone.
- **R4 `supersededBy` has one writer.** `LinkClauses` refuses `supersededBy` with a message naming `lode clause supersede`, and `UnlinkClauses` refuses to remove a `refactor` edge. A refactor is undone by a later refactor, never by deleting its record. `wasDerivedFrom` is an ordinary manual edge. Costs nothing.
- **R5 S3's refactor half writes events, not link rows.** S22 keeps every link on the clause it was minted against, so the refactor never moves a `task_governed_by` row. What S3 requires of the refactor, that every change to a task's governance is an event on the task, is met by one `task.governance_superseded` event per task governed by a clause the refactor withdraws, naming the old clause and its successors. Increment 3's plan-closing step (S43) is the other writer. Costs one event type.
- **R6 The map is resolved and applied in one transaction; `--dry-run` resolves and rolls back.** Re-running a map that already applied is a no-op per entry: an old clause already `withdrawn` keeps its status, and an existing edge is skipped (`ON CONFLICT DO NOTHING`). A successor that is itself withdrawn, or that also appears on the left side, is `ErrInvalidInput` naming the line. Costs nothing.
- **R7 A map line names clauses by `WL-CL-<n>` or by `<KEY>-<TYPE>-<n>#<anchor>`.** A section ref resolves to the clause arranged at that anchor in that document's arrangement, after `ensureClauses` mints clauses for a document that predates the clause tables. The sibling 4a ships `store.ClauseAtSection` for the same lookup; this branch cannot see it, so Task 2 writes a package-local `clauseAtAnchor` and the second of the two branches to land deletes one of them. Costs one small duplicate for the life of one PR.
- **R8 Resolution through the chain is on the read side only.** `model.TaskGovernance` gains `ResolvesTo []string`: the refs of the live clauses reached from the governing clause by following `supersededBy` edges transitively, empty when the governing clause is live. `lode task show` prints it after the clause; the cockpit task detail shows it where it lists governing clauses. Costs one recursive query per `GovernedBy` call.
- **R9 The embedding-derived candidate map (S24's "next step") is deferred.** This increment ships the bookkeeping primitive S24 settles on. The candidate map is the step after it and gets its own plan. Costs nothing shipped.
- **R10 A2 ships as a generator, its hand rulings and a runbook; running it against the backbone is the architect's act.** The eleven new specs in `docs/specs2/` are not in the backbone yet (docs/specs2/README.md), and they carry no `{#sec-N}` anchors, so the map cannot be applied until they are imported. The generator reads `section-map.tsv` and a JSON file mapping each new spec's file stem to its backbone ref, and writes the map. Every `dropped-*` row becomes a withdraw-only line. Costs one manual run.
- **R11 The 23 residue rows are decided here** (Task 5 encodes them in `docs/specs2/ttl/residue.tsv`):
  - Twelve rows name `Open questions` as the target: the successor is the target document's `Open questions` clause. Under S8 a clause is the lowest heading unit, so the bullet list is one clause and the reason these rows did not resolve (clauses.py split the list per rule) does not apply. Rows: WL-SPEC-1 sec-13, WL-SPEC-6 sec-15, WL-SPEC-8 sec-20, WL-SPEC-13 sec-5, WL-SPEC-16 sec-8, WL-SPEC-17 sec-9, WL-SPEC-21 sec-14, WL-SPEC-21 sec-15.1, WL-SPEC-37 sec-12, WL-SPEC-38 sec-8, WL-SPEC-40 sec-12, WL-SPEC-70 sec-8.
  - Eight `merged` rows with target `-`: the successor is the target document's preamble clause, the text under its title that says what the document covers. Rows: WL-SPEC-4 sec-0, WL-SPEC-4 sec-12, WL-SPEC-5 sec-0, WL-SPEC-5 sec-8, WL-SPEC-25 sec-23, WL-SPEC-29 sec-9, WL-SPEC-32 sec-0, WL-SPEC-56 sec-0.
  - WL-SPEC-8 sec-1 (two `pointer` rows, targets 09 and 01, `-`): successors are both documents' preamble clauses.
  - WL-SPEC-29 sec-6.2 (`pointer` to 02 §13): 02 has no §13; the successor is 02 §10 "Task-declared secrets", the section on secrets that 029 §6.2's neighbourhood describes. Flagged in the PR body for a check.
  If the store's split gives a document no preamble clause, the preamble successor is the document's first arranged clause.
  Costs a map edit per row that is wrong; every row is listed in the PR body.

Deferred, stated so no reviewer reports them missing: the candidate map (R9), running A2 against the backbone (R10), retiring `coverage:` levels (increment 3 R3), the per-project trailer switch (4a).

## Global Constraints

- Build and test with `-trimpath` only: `make build`, `make vet`, `make test`, or `go test -trimpath ./internal/<pkg> -run <Test>`. Never bare `go test`.
- **At most one `-race` suite runs on the machine at a time.** `make test` is one. Run it once per task, in the foreground, before committing. Never background a test run, never use `sleep` or polling loops.
- Store and API tests need Postgres with pgvector at `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`. They skip silently without it, so confirm with `-v` that they ran.
- ADR 036: every HTTP shape once in `internal/model` with wire names; `rule_test.go` rejects untyped maps (use `json.RawMessage` where a map is needed).
- Every route is in `internal/api/router.go`'s `routeGuards`. The supersede route uses the guard `POST /api/v1/clauses/{id}/edges` uses (`guardedAny(permDocWrite)`).
- `internal/store/AGENTS.md`: caller-triggerable conditions return sentinels from `errors.go` mapped in `mapStoreErr`.
- `internal/cmd` decides, `internal/cli` renders; `internal/cli` never imports cobra. New verbs satisfy `internal/cmd/namerule_test.go` (`internal/cmd/CLAUDE.md` "Naming", WL-SPEC-61).
- Metrics (WL-SPEC-22): a new store operation with outcomes adds a nil-safe `worklode_*` counter with bounded labels and a test.
- Migrations: read `deploy/base/AGENTS.md`; the pair is listed in `deploy/base/kustomization.yaml`; the down reverses the up; `./scripts/check-migrations.sh --no-fix` passes.
- Plugin skill and command reference files are agent surfaces: after a CLI change run `go test -trimpath ./internal/cmd -run 'TestAgentSurfaces|TestCommandReference'` and `./scripts/sync-codex-marketplace.py`, commit what they regenerate.
- Prose in `docs/specs2/`: plain language, no em dashes, no "X, not Y" antithesis.
- Commits are signed; if signing fails, stop and report. Imperative subject, no Co-authored-by or any attribution.
- Feature-stem naming: `store/clausesupersede.go` (+ test), `api/clausesupersede.go` (+ test), `cli/clausesupersede.go`, `model/clause.go` extended. No file over 2000 lines.

---

### Task 1: Lineage edge types (S22)

**Files:**
- Create: `deploy/base/migrations/0086_clause_lineage.up.sql`, `.down.sql`; list both in `deploy/base/kustomization.yaml`
- Modify: `internal/store/clauseedges.go` (`clauseEdgeTypes`, `LinkClauses`, `UnlinkClauses`), `internal/store/clauseedges_test.go`
- Modify: `internal/cmd/clause.go` (`edgeTypeFlagTypes`, `edgeFlagUsage`: `--derived-from` for `wasDerivedFrom`)
- Modify: `ns/ontology.ttl` (reuse-list comment only)

- [ ] **Step 1: Migration.** Up: drop and re-add the two column CHECKs on `clause_edges` (read their real names from `\d clause_edges` against a migrated test DB, or from Postgres's default `clause_edges_type_check` / `clause_edges_source_check`, and confirm by running the store test) so `type` also allows `supersededBy`, `wasDerivedFrom` and `source` also allows `refactor`. Down: `DELETE FROM clause_edges WHERE type IN ('supersededBy','wasDerivedFrom') OR source = 'refactor'`, then restore the increment 2 CHECKs, with a one-line comment that the delete discards lineage.
- [ ] **Step 2: Test first.** In `clauseedges_test.go`: `LinkClauses(tx, b, a, "wasDerivedFrom")` succeeds and the clause detail's `Edges` lists it; `LinkClauses(..., "supersededBy")` is `ErrInvalidInput` whose message contains `lode clause supersede`; a row inserted with `source = 'refactor'` cannot be removed by `UnlinkClauses` (`ErrInvalidInput`). Run, see them fail.
- [ ] **Step 3: Code.** Add both types to `clauseEdgeTypes`; `LinkClauses` refuses `supersededBy` (R4); `UnlinkClauses` refuses `source = 'refactor'` beside the existing `derived` refusal. Update the two error strings that list the allowed types. Add the `--derived-from` flag in the existing table style.
- [ ] **Step 4: ns.** In ontology.ttl's reuse list, extend the `dct:replaces / dct:isReplacedBy` and `prov:wasDerivedFrom` comment lines to say clause edges store them as `supersededBy` and `wasDerivedFrom` (12 S22). `riot --validate ns/*.ttl` passes.
- [ ] **Step 5:** focused tests, `./scripts/check-migrations.sh --no-fix`, agent-surface tests and codex sync, `make test`, commit "Add wasDerivedFrom and supersededBy clause edges (S22)".

### Task 2: `SupersedeClauses`, the refactor primitive (S24, S3, R2 to R7)

**Files:**
- Create: `internal/store/clausesupersede.go`, `internal/store/clausesupersede_test.go`
- Modify: `internal/model/clause.go` (`SupersedeEntry`, `SupersedeInput`, `SupersedeResult`), `internal/store/metrics.go`

**Interfaces:**
- `model.SupersedeEntry{Old string; New []string}` (`json:"old"`, `json:"new"`), `model.SupersedeInput{Entries []SupersedeEntry; DryRun bool}` (`json:"entries"`, `json:"dry_run,omitempty"`), `model.SupersedeResult{Entries []SupersedeResolved; Withdrawn int; Edges int; Tasks int; StalePlans int; DryRun bool}` with `SupersedeResolved{Old string; New []string}` carrying resolved `WL-CL-<n>` refs.
- `(*Store).SupersedeClauses(ctx, project, actor string, in model.SupersedeInput) (model.SupersedeResult, error)`.

- [ ] **Step 1: Test first.** Fixtures from `clauses_test.go` (`openDocStore`, `mustCreateDoc`, `acceptDoc`, `clauseID`, `governedPlanBody`). Cases, each its own test:
  1. merge: `WL-CL-B -> WL-CL-A` withdraws B, writes one `supersededBy` edge B→A with `source = 'refactor'`, leaves A live.
  2. many-to-many: one old clause to two successors writes two edges; two old clauses to one successor writes two edges.
  3. withdraw-only line (empty `New`) withdraws with no edge.
  4. section refs on both sides resolve (`P1-SPEC-<n>#sec-1.1`), including an old document created before its clauses exist (delete its `doc_clauses`/`clauses` rows in the fixture, as `ensureClauses`'s own test does, and check they are minted and then withdrawn).
  5. an accepted plan arranging the old clause goes `stale` (S23 through `SetClauseStatus`).
  6. a task governed by the old clause keeps its `task_governed_by` row unchanged and gets one `task.governance_superseded` event whose payload names the old ref and the successor refs (R5).
  7. dry run returns the resolved entries and counts and writes nothing (status, edges, events unchanged).
  8. re-running the same map changes nothing: counts are zero and no edge or `task.governance_superseded` event is duplicated (the one `clause.superseded` event per run is expected).
  9. refusals, each `ErrInvalidInput` naming the offending ref: a successor that is withdrawn, a successor that is also on the left side, an old clause listed twice, a ref that parses as neither form. An unknown clause or anchor is `ErrNotFound`.
  10. metrics: `worklode_clause_supersede_total{outcome}` counts `applied`, `dry_run`, `invalid`, `not_found`, `error`.
- [ ] **Step 2: Code.** One `s.RecordEvent(ctx, "cli", <randomExternalID()>, "clause.superseded", payload, apply)` whose apply func does everything below, so the refactor itself is one event (payload: actor and the resolved entries) and its id is the `eventID` passed to `SetClauseStatus`. `ClauseIDByRef(tx, key, number)` (clauses.go) resolves `WL-CL-<n>`; `resolveDocRef(tx, project, base)` (docedges.go) resolves the document of a section ref; `designdoc.ParseClauseRef` and `designdoc.ParseShorthand` parse. Parse each ref with the existing `designdoc` parsers (`ParseClauseRef`, and the section-ref grammar `lode show` uses). Resolve `WL-CL-<n>` with the existing clause-number lookup scoped by project key (S32 allows another project's clause), and `#anchor` refs with a package-local `clauseAtAnchor(tx, docID, anchor)` that calls `ensureClauses(tx, docID)` and then reads `doc_clauses` for that anchor (R7; reuse `resolveDocRef` for the document). Validate everything before the first write. Then per entry: `SetClauseStatus(tx, now, old, "withdrawn", eventID)` unless already withdrawn; `INSERT ... ON CONFLICT DO NOTHING` each edge with `source = 'refactor'`; for each task in `task_governed_by` on `old`, insert a `task.governance_superseded` event in the same transaction (hand-written `INSERT INTO events (source, external_id, type, payload, received_at)` as `mintReplanTask` in `replan.go` does, source `cli`, random external id). Count what actually changed. The dry run runs only the resolve-and-validate half inside a plain `s.Tx` that it rolls back by returning a sentinel, translated to a nil error, and never calls `RecordEvent`. Lock clause rows in id order (`SELECT ... FOR UPDATE` ordered by id) before writing, so two refactors over overlapping maps cannot deadlock.
- [ ] **Step 3:** focused tests, `make test`, commit "Supersede clauses from a clause-to-clause map in one transaction (S24)".

### Task 3: API and `lode clause supersede --map` (S24)

**Files:**
- Create: `internal/api/clausesupersede.go`, `internal/api/clausesupersede_test.go`, `internal/cli/clausesupersede.go`
- Modify: `internal/api/router.go`, `internal/cmd/clause.go`, `internal/cmd/namerule_test.go` (`supersede` in `l3DomainActions`), `plugins/claude/lode/skills/worklode/references/commands.md` (regenerated)

- [ ] **Step 1:** `POST /api/v1/projects/{id}/clauses/supersede`, body `model.SupersedeInput`, 200 `model.SupersedeResult`, guard `guardedAny(permDocWrite)`, actor from the request subject. Errors through `mapStoreErr`. HTTP tests: 200 applied, 200 dry run, 422 invalid, 404 unknown project.
- [ ] **Step 2:** `lode clause supersede --map <file> [--dry-run] [--json]`. The map file format, parsed in `internal/cmd` (it decides): one entry per line, `<old> -> <new> [<new> ...]`, `<old> ->` for withdraw-only, `#` starts a comment, blank lines ignored; a line without `->` is an error naming its line number. `-` as the file reads stdin. Rendering is `cli.SupersedeRender(w, res)`: one line per entry, `WL-CL-12 -> WL-CL-40, WL-CL-41`, then the counts, prefixed "dry run:" when `DryRun`. A unit test covers the parser (comment, blank, withdraw-only, malformed line).
- [ ] **Step 3:** `supersede` joins `l3DomainActions` with a one-line comment citing S24. Agent-surface tests and codex sync; `make test`; commit "Add lode clause supersede --map (S24)".

### Task 4: Resolve a governing clause through the chain (S22, R8)

**Files:**
- Modify: `internal/model/governedby.go` (`ResolvesTo []string` with `json:"resolves_to,omitempty"`), `internal/store/governedby.go` (`GovernedBy`), `internal/store/governedby_test.go`, `internal/cli/tasks.go` (the `GovernedBy` loop near line 526), the cockpit task-detail template that lists governing clauses (find it with `grep -rn GovernedBy internal/ui`), `internal/cli/governedby_test.go`

- [ ] **Step 1: Test first.** A task governed by B; B superseded by A1 and A2; A1 superseded by C. `GovernedBy` reports B with `ResolvesTo` `[A2, C]` in clause-number order (withdrawn intermediates are skipped, only live ends are listed). A live governing clause has empty `ResolvesTo`. A cycle in the fixture (inserted by hand) terminates.
- [ ] **Step 2: Code.** One recursive CTE over `clause_edges` of type `supersededBy` from the governing clause, `UNION` (not `UNION ALL`) so cycles stop, keeping reached clauses whose status is not `withdrawn`, rendered as `<KEY>-CL-<n>`. Run it only for withdrawn governing clauses. CLI prints `-> WL-CL-40, WL-CL-41` after a withdrawn clause's line; the cockpit shows the same refs as links.
- [ ] **Step 3:** `make test`; commit "Resolve a withdrawn governing clause to its live successors (S22)".

### Task 5: A2 map generator and the residue rulings (A2, R10, R11)

**Files:**
- Create: `scripts/supersession-map.py`, `scripts/supersession-map_test.py`, `docs/specs2/ttl/residue.tsv`
- Modify: `docs/specs2/ttl/README.md` (replace the residue section's "need a hand decision" with a pointer to `residue.tsv`; add a "Migrate the old specs" runbook section)

- [ ] **Step 1: `residue.tsv`.** Columns `old_ref old_anchor new_file new_anchor why`, one row per successor for the 23 rows in R11 (WL-SPEC-8 sec-1 gets two rows). `new_anchor` is the literal anchor when the target section is numbered, `preamble` for the preamble clause, `open-questions` for the Open questions clause. `why` is a few words.
- [ ] **Step 2: Test first** (`supersession-map_test.py`, plain `assert`s run by `make test-scripts`): a four-row section map fixture and a refs JSON produce the expected lines: a `moved` row `WL-SPEC-1#sec-10 -> WL-SPEC-80#sec-9.4`; two `merged` rows from one old section to two targets collapse to one line with two successors; a `dropped-history` row becomes `WL-SPEC-1#sec-1 ->`; a residue row takes its target from `residue.tsv`; a new file missing from the refs JSON is an error naming it.
- [ ] **Step 3: Script.** `scripts/supersession-map.py --section-map docs/specs2/section-map.tsv --residue docs/specs2/ttl/residue.tsv --refs refs.json [--anchors anchors.json]`. `refs.json` maps a file stem (`02-identity-actors-and-secrets`) to its backbone ref (`WL-SPEC-80`). A numbered `new_section` `9.4` becomes `sec-9.4`; `preamble` and `open-questions` are looked up in `anchors.json` (stem to `{"preamble": "sec-0", "open-questions": "sec-11"}`), which the runbook produces from `lode doc show <ref> --json` after import; with no `--anchors` those rows are an error naming the file. Output is the map format of Task 3 on stdout, sorted by old ref, with a header comment naming the inputs.
- [ ] **Step 4: Runbook** in `docs/specs2/ttl/README.md`: import the eleven specs with anchors (`lode doc add`, anchors assigned by the repo's renumber tooling), write `refs.json` and `anchors.json` from `lode doc list --kind spec --json` and `lode doc show --json`, generate the map, `lode clause supersede --map <file> --dry-run`, check the counts (every old section appears once; 524 edges less the rows the residue rulings changed), then run it without `--dry-run`. State that the run is the architect's act and has not been done.
- [ ] **Step 5:** `make test-scripts`; commit "Generate the old-spec supersession map with the residue rulings (A2)".

### Task 6: Specs 03, 05, 09 and 12 describe what shipped

**Files:** `docs/specs2/03-tasks-and-execution.md`, `05-documents.md`, `09-cli-and-skills.md`, `12-spec-refactoring-design-tree.md`

- [ ] **03 §4 "Governing clauses":** a task governed by a withdrawn clause keeps the link and reads the live clauses it resolves to through `supersededBy` (S22); a refactor records `task.governance_superseded` on each task it touches and moves no link (S3).
- [ ] **05 §4 clause paragraph:** clause lineage (`wasDerivedFrom` written by hand, `supersededBy` written only by a refactor), split and merge as R3 describes, and the refactor as one transaction over a map that withdraws the left side and marks arranging plans stale (S22, S24).
- [ ] **09:** the `clause` row gains `supersede --map` and `link --derived-from`.
- [ ] **12 "Recorded from implementation":** append S55 onwards, one per ruling that is a decision a code reader needs: R2 (S55), R3 (S56), R4 (S57), R5 (S58), R6 (S59), R8 (S60), R9 (S61), R10 and R11 (S62). S50 to S54 are reserved for the sibling 4a, which lands on its own branch, so this branch starts at S55 even though S49 is the highest number it can see. Update the Frontier paragraph's range to end at the highest number this task adds.
- [ ] Style check `git diff | grep '^+' | grep -nE '—|, not |rather than|instead of'` returns nothing; commit "Record clause lineage, the refactor primitive and the A2 map in specs 03, 05, 09 and 12".

## Self-review

S22 is Tasks 1 and 4, S24 Tasks 2 and 3 (candidates deferred by R9), S3's refactor half Task 2 (R5), A2 Task 5 (run deferred by R10). Types: `SupersedeInput`/`SupersedeResult` are produced in Task 2 and consumed in Task 3; `ResolvesTo` is produced and consumed in Task 4; the map format is defined in Task 3 and produced by Task 5.
