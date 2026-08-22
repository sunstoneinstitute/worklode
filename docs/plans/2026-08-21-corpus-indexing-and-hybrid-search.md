---
status: draft
covers:
- spec: docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-0
  coverage: none
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-1
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-2
- spec: docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-2.1
  coverage: none
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-2.2
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-2.3
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-2.4
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-3
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-4
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-4.1
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-4.2
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-4.3
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-4.4
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-5
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-6
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-6.1
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-6.2
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-6.3
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-6.4
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-7
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-8
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-9
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-10
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-11
- spec: docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-12
  coverage: none
- docs/specs/040-corpus-indexing-and-hybrid-search.md#sec-13
---
# Corpus indexing and hybrid search — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

**Goal:** implement spec 040 — one `index_chunks` table over docs, tasks and
skills; a background convergence loop that keeps it agreeing with the corpus;
hybrid retrieval (dense cosine + `simple` full-text, fused by reciprocal rank)
behind `GET /api/v1/search` and `lode search`; and skill recommendation rewired
as a thin caller of the same path. Everything generalises 016's existing
machinery (`internal/embed`, `skillsync.embedMissing`,
`InvalidateOnProviderChange`, `store.RecommendSkills`) rather than building
beside it.

**Shape of the change.** One new migration (`0046_index_chunks` — the next
free number when this plan was written; re-check with
`./scripts/check-migrations.sh` at execution), one new
package `internal/corpusindex` (pure chunking + the convergence loop),
new store files `internal/store/index_chunks.go` and
`internal/store/search.go`, a `Role`-aware `embed.Provider`, one new API route,
one new CLI verb. `skill_embeddings` is dropped; `embedding_config` survives
unchanged. No cockpit work (spec 040 §9: out of scope). Spec 040 §0 and §2.1
are motivation and rationale with no work to undertake, and §12's open
questions are deliberately unresolved — each waits on evidence (§9's `mode`
parameter, Task 6, is what produces it) — so all three carry `coverage: none`.

**One reconciliation the executor must not "fix" back.** Spec 040 §5's DDL
shows `embedding vector(768) NOT NULL`, but §8 states invalidation *nulls* the
column ("This makes the `embedding` column nullable") and §11 requires
no-provider instances to write chunk rows with no vectors at all. §8/§11 win:
the migration declares `embedding vector(768)` **nullable**, and the dense arm
skips null rows by construction. Everything else in §5's DDL is copied
verbatim.

## Global constraints

Exact values, quoted from the spec — do not re-derive them:

- Embedding width is **768**, as a `vector(768)` typmod; `Provider.Dim()` must
  return 768 or `NewServer` refuses to boot (§2.2, §3).
- Chunk budget `ChunkRunes = 3600`, `ChunkOverlap = 600` (§4.1) — down from
  016's 6000/600; one regime for all kinds and all providers.
- Context header formats (§4.3):
  `WL-SPEC-025 "Documents in the backbone" — §15.2 The ordered log` for docs,
  `WL-142 [feature/in_progress] Fix the thing` for tasks,
  `skill: test-driven-development — <description>` for skills. The header is
  stored in its own column, prepended for embedding, weighted `A` in `tsv`,
  and never part of the returned excerpt.
- Text search configuration is **`simple`**, never `english` (§6.2). `tsv` is
  a `GENERATED ALWAYS ... STORED` column:
  `setweight(to_tsvector('simple', context_header), 'A') || setweight(to_tsvector('simple', chunk_text), 'B')`.
  The two-argument `to_tsvector(regconfig, text)` is required; the one-argument
  form is not IMMUTABLE and is illegal here.
- Dense arm: cosine (`<=>`), **max**-pooled per subject before ranking,
  candidate floor **0.35**, candidate limit **50** per arm — the floor filters
  candidates on the dense arm only, never the fused result (§6.1).
- Fusion: RRF with **k = 60**, weight **1.0** on both arms, `FULL OUTER JOIN`
  on the subject key, ordered by the summed reciprocal ranks (§6.3). The
  subject key for docs is `(doc_id, anchor)`; one spec may surface twice.
- Lexical arm parses queries with `websearch_to_tsquery('simple', $q)` (§6.2).
- Provider `ID()` incorporates the width:
  `openai:text-embedding-3-small@768@api.openai.com/v1/embeddings` (§3).
- EmbeddingGemma task prefixes: query `task: search result | query: `,
  document `title: none | text: ` (§3); a symmetric model configures both
  empty.
- Convergence interval default **5 minutes**, env `LODE_INDEX_INTERVAL`, runs
  on `lode serve` only, and still runs with no provider configured (§7, §11).
- Permission: `permSearchRead` ("search.read"), granted to
  `{RoleUser, RoleAdmin}`; carry §9's project-scoped-roles caveat as a comment
  next to the grant in `authz.go` (§9).
- Metric names and labels are fixed by §10:
  `worklode_index_chunks{subject_kind}`,
  `worklode_index_chunks_without_vector`,
  `worklode_index_subjects_stale{subject_kind}`,
  `worklode_index_reembed_total{subject_kind,outcome}`,
  `worklode_index_convergence_duration_seconds`,
  `worklode_search_requests_total{mode,outcome}`,
  `worklode_search_arm_duration_seconds{arm}`,
  `worklode_search_arm_empty_total{arm}`; `outcome` is `ok|error|empty`.
  Nil-safe struct in the owning package, `prometheus.Registerer` threaded from
  `serve.go`, bounded labels, never a project or task id (022).
- New migrations are a new numbered pair listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped one.
- Store tests need Postgres with pgvector; a skipped test proved nothing.
- `e2e/` drives public surfaces only.
- Wire shapes (`SearchHit`) are declared once in `internal/model` (ADR 036),
  stdlib imports only, wire-named fields.

## Tasks

### Task 1 — Role-aware provider interface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

`internal/embed`: the interface change of spec 040 §3, plus width in the ID.

- `Provider` becomes:

  ```go
  type Role int // RoleDocument, RoleQuery

  type Provider interface {
      Embed(ctx context.Context, role Role, texts []string) ([][]float32, error)
      ID() string
      Dim() int // must be 768; NewServer refuses a provider that disagrees
  }
  ```

- `OpenAI` gains `QueryPrefix`, `DocumentPrefix` (applied per `role` to every
  input text; both empty = today's behaviour, now explicit) and
  `Dimensions int` (marshalled into the request body when > 0, so a sidecar
  that rejects the parameter is configured with 0 and a natively-768 model).
  `Dim()` returns `Dimensions` when set, else 768.
- `ID()` becomes `openai:<model>@<dim>@<host+path>` — the width is part of the
  embedding space, so "the same model at a different truncation" invalidates
  (§3). Keep the existing userinfo/query/fragment exclusion.
- Update the two call sites (`internal/skillsync` embeds documents,
  `internal/api` recommend embeds a query) to pass the role; behaviour is
  unchanged for symmetric models.
- Tests first (`embed_test.go`): prefixes are applied to the right role and
  not the other; `dimensions` appears in the request JSON exactly when set;
  the new `ID()` format; a table case proving empty prefixes reproduce
  today's request bytes.

### Task 2 — Pure chunking and context headers

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New package `internal/corpusindex`, pure (no DB, no HTTP): spec 040 §4
end-to-end, table-tested.

- `chunk.go`: `const ChunkRunes = 3600`, `ChunkOverlap = 600` (§4.1). A
  `Chunk{Anchor string; Index int; Header, Text string}` value type and three
  builders:
  - `ChunkDoc(doc, sections)` — one chunk per section in `position` order; a
    section longer than `ChunkRunes` splits into overlapping sub-chunks that
    inherit the section's anchor; short sections are never merged (§4.2).
    Plans (no anchors) chunk on `##`/`###` headings with `anchor = ""`,
    falling back to fixed windows for an unstructured body.
  - `ChunkTask(task)` — `title + "\n\n" + body` as one chunk unless it
    overflows the budget; an empty body still indexes the title (§4.4).
  - `ChunkSkill(skill)` — description prepended to `SKILL.md`, windowed, as
    016 does today.
- `header.go`: the three §4.3 header formats, composed from columns the
  store already has. The header counts against the rune budget.
- `hash.go`: `ContentHash(kind, inputs...)` — one hash over the subject's
  indexed text, identical across all of one subject's chunks; the convergence
  loop's freshness comparand (§5, §7).
- Reuse `embed.Chunks` for windowing; do not move it. The 6000-rune constants
  in `internal/embed` are superseded by this package's — delete them and their
  last callers here if nothing else still reads them, or leave them to Task 3
  if skillsync still does.
- First test, written before the code: a doc fixture with a 9000-rune section
  yields sub-chunks all carrying the section's anchor, each ≤ 3600 runes
  including its header, overlapping by 600.

### Task 3 — Migration 0046 and the store moves onto index_chunks

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

`deploy/base/migrations/0046_index_chunks.{up,down}.sql` plus
`internal/store/index_chunks.go`, landing together with every existing
caller retargeted so the branch stays green.

- `up.sql`: §5's DDL verbatim — `index_chunks` with the three nullable FKs and
  their `CHECK`s, denormalised `project`, `anchor`/`chunk_index`,
  `context_header`/`chunk_text`/`content_hash`, the generated `tsv`, the three
  partial unique indexes, `(subject_kind, project)`, the HNSW
  (`vector_cosine_ops`) and GIN indexes — with the single §8/§11 correction:
  `embedding vector(768)` (nullable). Same migration drops `skill_embeddings`;
  rows are not carried over (016-width vectors from a possibly different
  model). `embedding_config` untouched. `down.sql` drops `index_chunks` and
  restores `skill_embeddings` as 0007 defined it. List both files in
  `deploy/base/kustomization.yaml`; run `./scripts/check-migrations.sh --no-fix`.
- `internal/store/index_chunks.go`:
  - `ReplaceSubjectChunks(ctx, subject, chunks, vectors)` — deletes and
    reinserts one subject's chunk set in one transaction; `vectors` may be nil
    (no provider), writing null embeddings.
  - `StaleSubjects(ctx, kind, limit)` — subjects whose live `content_hash`
    differs from their chunk rows', **including subjects with no chunk rows at
    all**; excludes soft-deleted skills (§1).
  - `ClearAllChunkVectors(ctx)` — `UPDATE ... SET embedding = NULL`, the §8
    invalidation primitive; returns the affected count.
  - `IndexCounts(ctx)` — per-kind chunk counts, no-vector count, for §10's
    gauges.
- `RecommendSkills` retargets from `skill_embeddings` to
  `index_chunks WHERE subject_kind = 'skill'`, keeping its exact semantics
  (max-pool per skill, floor, limit) and its tests; `ReplaceSkillEmbeddings`
  and `ClearAllSkillEmbeddings` are deleted, and `skillsync`'s
  `embedSkill`/`reembed` write through `ReplaceSubjectChunks` using Task 2's
  skill chunker and header (interim glue; Task 5 removes skillsync's embedding
  entirely).
- Store tests (real Postgres + pgvector): a 767- or 769-wide vector is refused
  by Postgres, not by Go (§13.5); cascade delete of a task removes its chunks;
  the one-subject `CHECK` fires; `StaleSubjects` returns a never-indexed
  subject and an edited one, and returns nothing on a second pass after
  replacement (§13.10).

### Task 4 — Hybrid search in the store

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`internal/store/search.go` + `model.SearchHit`: spec 040 §6, all three modes.

- `internal/model`: `SearchHit` with `Kind`, the subject id fields, `Anchor`,
  `Title`, `Excerpt` (from `chunk_text`, never the header), `Score` (fused),
  `DenseRank`, `LexicalRank` (0 = absent from that arm), per ADR 036.
- `Search(ctx, q SearchQuery) ([]model.SearchHit, error)` where `SearchQuery`
  carries the query text, optional pre-embedded query vector (nil = lexical
  only), kinds, project, limit, mode. One SQL statement per §6: two ranked CTE
  arms (dense: cosine, floor 0.35, max-pooled per subject, `LIMIT 50`;
  lexical: `websearch_to_tsquery('simple', $q)`, `ts_rank_cd`, max-pooled,
  `LIMIT 50`), fused by `FULL OUTER JOIN` on the subject key with
  `Σ 1.0/(60 + rank)`, coalescing a missing arm to zero contribution. Kind and
  project filters apply inside both arms; the `project IS NULL` disjunct keeps
  org-wide skills visible in a project-scoped search (§6.4). `mode=dense` and
  `mode=lexical` run one arm and return its ranking.
- Tests (real Postgres) written from the spec's own cases:
  - §6.3's worked example as a fixture: four subjects where the dense arm
    ranks the `child_of`-defining section third and the lexical arm first;
    assert fusion ranks it first, and that disabling the lexical arm makes
    the assertion fail (§13.2).
  - §13.3: a long doc with eight mediocre chunks does not outrank a short
    exact match — provable only because pooling precedes ranking.
  - §13.6: the query `child_of` does not match prose reading "the child task
    of a parent" and does match a chunk containing `child_of edge`. This test
    is what stops someone "fixing" the config to `english`.
  - A header match outranks a body match for the same term (weight `A` vs
    `B`).

### Task 5 — Convergence loop, invalidation, and server wiring

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`internal/corpusindex` grows the loop (spec 040 §7–§8); `serve.go`/`NewServer`
wire it; skillsync stops embedding.

- `indexer.go`: for each kind, page through `StaleSubjects`, build chunks
  (Task 2), embed with `RoleDocument` when a provider is configured, and
  `ReplaceSubjectChunks` — per subject, one transaction. No provider: chunk
  and write anyway, vectors nil (§11). A failed subject logs, counts
  `outcome="error"`, and is retried next pass; the loop never dies on one
  subject.
- `invalidate.go`: `InvalidateOnProviderChange` moves here from `skillsync`
  (it is no longer a skills concern, §8): compare `Provider.ID()` against
  `embedding_config`, and on mismatch `ClearAllChunkVectors` — **null the
  vectors, keep the rows** — then record the new id. During re-embed the
  instance degrades to lexical-only, never to nothing.
- `skillsync` drops `embedSkill`/`reembed`/`embedMissing` and its provider
  field — the loop owns all embedding now. Skill sync's job ends at upserting
  rows; the next pass indexes them.
- Wiring: `NewServer` refuses a configured provider whose `Dim() != 768`;
  the loop starts only when the caller passes a `BackgroundCtx` (the
  `doc-lifecycle` pattern), interval from `LODE_INDEX_INTERVAL`, default
  `5m`, parsed in `internal/cmd/serve.go`.
- `metrics.go` (owning package `corpusindex`, nil-safe, per 022): the five
  `worklode_index_*` metrics from Global Constraints, registered from
  `serve.go`.
- Tests: a pass over an unchanged corpus re-embeds nothing and leaves
  `worklode_index_subjects_stale` at zero (§13.10); provider-change nulls
  vectors but lexical rows survive and `Search` still returns them (§13.7);
  a no-provider loop writes rows with null embeddings (§13.8); a provider
  returning an error leaves the subject stale and the loop alive.

### Task 6 — GET /api/v1/search

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4, 5]
```

`internal/api`: the search route, its guard, and its metrics (spec 040 §9,
§10).

- `GET /api/v1/search?q=&kind=&project=&limit=&mode=` → `[]model.SearchHit`.
  `kind` repeatable, omitted = all three; `mode` is `hybrid` (default),
  `dense`, `lexical`. The handler embeds the query once with `RoleQuery` when
  a provider is configured and `mode != lexical`; with no provider it serves
  the lexical arm and the response reports `provider: "none"`,
  `mode: "lexical"` — real results, never an empty set for that reason (§11).
  The response envelope carries `provider` and effective `mode` alongside the
  hits; each hit carries both arm ranks (§9: what makes a bad result
  diagnosable).
- `routeGuards` entry + `permSearchRead` ("search.read") granted to
  `{RoleUser, RoleAdmin}` in `authz.go`, with §9's note — one permission over
  three subject kinds is only honest while all three reads are granted
  identically — as a comment next to the grant.
- Metrics: `worklode_search_requests_total{mode,outcome}` (`empty` when the
  fused result is empty), `worklode_search_arm_duration_seconds{arm}`,
  `worklode_search_arm_empty_total{arm}` — the lexical-arm-empty counter is
  the §10 alerting signature for a broken `tsv`.
- Handler tests: guard enforced (route boots, anonymous refused); mode
  fallback with no provider; kind/project filtering; arm ranks present in the
  JSON; metrics incremented per outcome.

### Task 7 — Recommendation becomes a caller of the retrieval path

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

`POST /api/v1/skills/recommend` keeps its route, request and response shape
(016 §2), and becomes a thin caller of Task 4's `Search` with `kind=skill`
(spec 040 §9), dropping its own embedding code path.

- The handler builds the query text as today, embeds with `RoleQuery` via the
  same helper Task 6 uses, calls `Search`, and maps hits back to the existing
  recommend response. It thereby gains the lexical arm: a brief naming a tool
  literally now matches the skill that names it back.
- No provider: pins plus lexical matches (§11) — strictly better than today's
  pins-only.
- `store.RecommendSkills` and its scan type are deleted once nothing calls
  them; pinned-skill handling is untouched.
- Existing recommend tests keep passing unchanged where they assert shape;
  add one asserting a lexical-only instance still recommends a
  literally-named skill.

### Task 8 — lode search

```yaml
kind: feature
priority: medium
skills:
  - worklode-lode-plugin
blockedBy: [6]
```

`internal/cmd` + `internal/cli`: the CLI verb (spec 040 §9).

- `lode search <query> [--kind doc|task|skill]... [--mode hybrid|dense|lexical]
  [--limit N] [--json]`, project-scoped like the rest of the CLI, calling
  `GET /api/v1/search`. Human rendering is the §9 line —
  `WL-SPEC-025 §15.2  0.032  The ordered log` — an address a reader can act
  on; `--json` emits the hits verbatim. A degraded instance's
  `provider: "none"` renders as a one-line notice on stderr, not a failure.
- New CLI surface: update `docs/agent-surfaces.md` per its checklist, and
  check both plugin marketplaces and skills for surfaces that should mention
  `lode search` (the `worklode-lode-plugin` skill owns the how).
- CLI tests against a stub server: rendering, filters, `--json`, exit codes.

### Task 9 — e2e journey, acceptance walk, docs

```yaml
kind: feature
priority: medium
skills:
  - worklode-ci
blockedBy: [5, 6, 7, 8]
```

Close spec 040 §13 through public surfaces only.

- `e2e/`: a search journey — create a doc with sections, a task, and a skill
  through existing public surfaces; run the server with a stub
  OpenAI-compatible `/v1/embeddings` endpoint (test-local HTTP server serving
  deterministic 768-wide vectors — environment, not a store write); wait for
  convergence; assert a semantic query hits all three kinds (§13.1), an
  identifier query ranks the literal container first (§13.2), a doc hit's
  anchor resolves through `lode doc show --section` (§13.4), and both arm
  ranks are present. A second scenario with no provider asserts real lexical
  results and `provider: "none"` (§13.8).
- Walk §13's ten acceptance bullets and name, in the PR description, the test
  that discharges each; §13.9 (CPU-only default deployment) is discharged by
  configuration — `LODE_EMBEDDING_URL` at a sidecar — and documented, not
  tested.
- Docs: extend CLAUDE.md's architecture paragraph mentioning
  `internal/skillsync`/`skillstore` to name `internal/corpusindex` and the
  search path; note `LODE_INDEX_INTERVAL` wherever `LODE_EMBEDDING_*` is
  documented (`internal/api/server.go` comments, compose files if they list
  envs).
