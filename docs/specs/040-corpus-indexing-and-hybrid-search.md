---
status: draft
requires:
- docs/specs/016-org-wide-skills.md
- docs/specs/025-documents-in-the-backbone.md
- docs/specs/019-project-scoping.md
- docs/specs/022-prometheus-metrics.md
---
# Spec 040 — Corpus indexing and hybrid search

## 0. Why {#sec-0}

Spec 016 §2 built an embedding pipeline for one entity — skills — and listed
"embedding of tasks/docs" as later work. Later has arrived: the backbone now
holds the design corpus (025 §5.1: `docs`, `doc_sections`) and a growing task
log, and nothing can find anything in them by meaning. `lode doc list` filters
by frontmatter, `lode task list` filters by state, and an agent asked "has
anyone specified how leases interact with worktree pruning?" opens twenty files.

That question is the whole motivation. The corpus is small in bytes and large in
surface: 20-odd specs with frozen section anchors, 80-odd plans, hundreds of
tasks, dozens of skills. It is exactly the size where exhaustive reading is too
slow for an agent and keyword search is too literal for a human — a spec says
"worktree-bound lease", the asker says "can two agents grab the same branch",
and `grep` returns nothing.

**But the mirror-image failure is just as common here, and embeddings cause it.**
The other half of what gets asked of this corpus is exact tokens: `child_of`,
`LODE_BRANCH_TEMPLATE`, `WL-142`, `hnsw`, `wlc:TaskKind`. Dense retrieval is
weakest exactly there. An identifier is a short, low-context string whose
meaning is its spelling, and a 768-dimensional summary of "what this text is
about" is the wrong tool for finding it. Measured against a four-row fixture
standing in for the corpus (§6.3), the query `child_of` ranked the section that
actually defines `child_of` **third** on vector similarity alone, behind two
sections that were merely about task hierarchy. Lexical search ranked it first.
Neither retriever is adequate on its own. Which one fails depends on the query,
not the corpus — so the system cannot just pick one.

This spec therefore specifies **hybrid retrieval fused by reciprocal rank**
(§6) as the search mechanism, not as a later enhancement. A dense-only index
over this corpus would be a demo: impressive on "how do leases work", useless on
the identifier lookups that make up much of the real traffic.

Everything else here generalises 016's machinery from one entity to three. The
provider interface, the chunk table shape, the provider-change invalidation rule
and the "no provider configured is still fully functional" contract already
exist and are re-used. What is new is the second and third subject kind, a fixed
embedding width that makes the vectors indexable, section-anchored chunking, a
lexical arm, and a convergence loop that keeps the index honest without hooking
every write site.

**What this spec does not do.** It does not move ownership of any fact. The
index is derived state over rows the backbone already owns, rebuildable from
them at any time, and never a source of truth — dropping every chunk costs
compute, not information. The queryable *graph* view of documents remains the
data platform's (006/025); this is a retrieval aid over the backbone's own
copy, not a second knowledge graph.

## 1. What gets indexed {#sec-1}

Three subject kinds, no more:

| Kind | Source | Unit of retrieval |
|---|---|---|
| `doc` | `docs.body` (025 §5.1), split on `doc_sections` | a section (`WL-SPEC-25 §15.2`) |
| `task` | `tasks.title` + `tasks.body` | the task |
| `skill` | `skill_versions.skill_md` of the latest version | the skill |

Skills index **`SKILL.md` only** — the sibling files a skill bundle ships are
out of scope, as they already are in 016 §2. A skill's `description` is
prepended to its body before chunking, unchanged from today.

Deliberately not indexed: events, agent-session transcripts, inbox items, blob
contents, and task comments. Each is either high-volume and low-value per token
(events, transcripts) or has no stable identity to point a hit at yet. Adding a
kind later is a migration and an indexer case, not a redesign — the schema in §5
is polymorphic on purpose.

Soft-deleted skills (`skills.deleted_at`) and their chunks stay out of results;
the chunk rows are deleted rather than filtered, so the index carries no
tombstones.

## 2. The embedding space {#sec-2}

### 2.1 Anthropic has no embeddings API {#sec-2.1}

Stating this because the question was asked: Anthropic does not serve text
embeddings. Its published recommendation is Voyage AI, a separate vendor with a
separate key and a separate bill. Voyage's models are strong, but adopting one
buys a third-party dependency for the *entire* corpus with no self-hosted path,
which is the one thing §2.3 rules out. Anthropic is therefore not a candidate
here; that is a fact about the API surface, not a judgement about the models.

That leaves OpenAI, Google, and open-weights models we host ourselves.

### 2.2 The width is the contract, not the model {#sec-2.2}

The load-bearing decision on the dense side is not *which model* but **that the
embedding width is fixed at 768 dimensions**, by contract, for every provider.

Three things fall out of it, and only the first is obvious:

1. **The vectors become indexable.** pgvector stores up to 16,000 dimensions but
   HNSW and IVFFlat index only up to 2,000. Today's `skill_embeddings.embedding`
   is a bare `vector` with no typmod and no index — an exact scan, fine for
   dozens of skills and not fine for the thousands of chunks §1 implies. A fixed
   768 admits `vector(768)` and an HNSW index (§5). Note what this rules out:
   `gemini-embedding-001` at its native 3072 dimensions is **not indexable in
   pgvector at all**, so "just use Gemini's default" is not an available option.
2. **Changing model never means changing schema.** All three candidates can emit
   768: EmbeddingGemma natively, `text-embedding-3-*` via the `dimensions`
   request parameter, `gemini-embedding-001` via `output_dimensionality`. All
   three are Matryoshka-trained (trained so that a shorter prefix of the vector
   is itself a valid, smaller embedding), so a truncated-and-renormalised vector
   is a real embedding rather than a lossy crop. A model swap re-embeds the
   corpus (§8) and touches no migration.
3. **The typmod becomes a guard rather than a hazard.** 016's comment on
   `skill_embeddings` warns that mixed dimensions make cosine queries error at
   query time. With a typmod the mismatch is refused at `INSERT`, at the moment
   the wrong-shaped vector is produced, by the row that produced it.

768 rather than 1536, for three reasons: storage and index-build cost scale
linearly with the width, recall differences at this corpus size are in the
noise, and 768 is the largest width every candidate reaches natively or by
truncation.

### 2.3 Default model: EmbeddingGemma-300M, self-hosted on CPU {#sec-2.3}

The requirement that the model run on CPU and the option of a hosted API pull
in different directions, so it's worth naming the tension directly. A hosted
API needs no GPU *of ours*, but it still isn't a model we can run ourselves.
Only open weights satisfy the requirement in that stronger sense — actually
running the model, not just avoiding the GPU cost — and this corpus argues for
the stronger sense. It is the org's own specs, ADRs, plans, and task bodies,
i.e. the written record of unreleased work. Indexing it means embedding all of
it, and embedding it through a hosted API means sending all of it somewhere.
That is a data-egress decision, and it is cheaper to simply not make it.

The default is therefore **`google/embeddinggemma-300m`**, served locally:

| Property | Value | Consequence here |
|---|---|---|
| Parameters | 300M | ~200 MB resident quantised; a sidecar, not a node pool |
| Native width | 768, MRL to 512/256/128 | §2.2's contract met natively |
| Context | 2048 tokens | sets the chunk budget in §4 |
| Weights | open (Gemma terms) | self-hostable; no per-token cost |
| Input convention | asymmetric task prefixes | forces the interface change in §3 |

It is served by a `text-embeddings-inference` (or equivalent llama.cpp/Ollama)
sidecar exposing an OpenAI-compatible `/v1/embeddings`. That is the same wire
protocol `internal/embed.OpenAI` already speaks, so **the default deployment
needs no new provider code** — it is `LODE_EMBEDDING_URL` pointed at a sidecar.
On a corpus of this size, a CPU sidecar re-embeds the world in minutes; the
indexer is a background convergence loop (§7), never on a request path.

### 2.4 Supported alternatives {#sec-2.4}

An instance that would rather buy than run configures one of these instead. Both
are first-class — the store records which space the vectors belong to and
invalidates on change (§8), so this is a config decision, not a fork.

| Provider | Model | Width knob |
|---|---|---|
| OpenAI | `text-embedding-3-small` | `dimensions: 768` |
| Google | `gemini-embedding-001` | `output_dimensionality: 768`, renormalise |

`text-embedding-3-small` is the pragmatic hosted default: symmetric (no task
prefixes), an 8192-token window that makes §4's chunk budget non-binding, and a
price where the entire corpus costs well under a dollar to embed. The tradeoff:
it is unavailable to an air-gapped instance and cannot run anywhere else.

`gemini-embedding-001` earns its place on multilingual coverage. It **must** be
truncated to 768 and renormalised; its native 3072 is unindexable (§2.2).

Rule for adding a fourth: it must reach exactly 768 dimensions and be reachable
over an OpenAI-compatible endpoint or a new `embed.Provider` implementation. No
other property is negotiable, because §2.2's contract is what keeps the schema
still.

## 3. Provider interface: queries and documents are not the same input {#sec-3}

`embed.Provider.Embed(ctx, texts)` today has one input mode, and `internal/api`
uses it for both stored skill bodies and the recommend-time query. That is
correct for OpenAI's symmetric models and wrong for both of the others:
EmbeddingGemma expects `task: search result | query: ` on a query and
`title: none | text: ` on a document, and Gemini expects a `task_type` of
`RETRIEVAL_QUERY` or `RETRIEVAL_DOCUMENT`. Embedding a query with the document
convention is not an error anyone sees — it silently costs retrieval quality,
which is the worst failure shape available.

The interface therefore names the mode:

```go
type Role int // Document | Query

type Provider interface {
    Embed(ctx context.Context, role Role, texts []string) ([][]float32, error)
    ID() string
    Dim() int // must be indexDim (768); NewServer refuses a provider that disagrees
}
```

`OpenAI` gains `QueryPrefix`/`DocumentPrefix` and a `Dimensions` field on the
request body. A symmetric model configures both prefixes empty, which makes
today's behaviour the explicit default rather than an implicit one.

`ID()` keeps its present meaning and its present job (§8). It must incorporate
the width, because "the same model at a different truncation" is a different
space and must invalidate: `openai:text-embedding-3-small@768@api.openai.com/v1/embeddings`.

## 4. Chunking {#sec-4}

### 4.1 Budget {#sec-4.1}

Sized to the smallest supported window (EmbeddingGemma's 2048 tokens), because a
chunk that overflows the default model is a chunk that silently loses its tail:

```go
const (
    ChunkRunes   = 3600 // ~900–1200 tokens depending on prose vs. markdown
    ChunkOverlap = 600  // ~17%, so a boundary-spanning statement survives
)
```

Down from 016's 6000/600, which was sized to an 8k window. Instances on
`text-embedding-3-small` could run larger chunks; they do not, because one
chunking regime keeps the corpus comparable and the code single-path.

### 4.2 Documents chunk on their own section boundaries {#sec-4.2}

Fixed-width windowing over a spec is the wrong tool when the spec already
carries structure the store has already parsed. `doc_sections` holds every
`{#sec-N}` anchor with its heading, depth and position (025 §5.1), and those
anchors are frozen identity (025 §3.2). So:

- One chunk per section, in `position` order.
- A section longer than `ChunkRunes` splits into overlapping sub-chunks that
  **inherit the section's anchor**. Sub-chunk index disambiguates within the
  row; the anchor is what a result reports.
- Consecutive short sections are **not** merged. A merged chunk would have to
  report one of two anchors, and reporting the wrong one is worse than a slightly
  under-full vector.
- Plans carry no anchors (025 §9). They chunk on `##`/`###` headings by the same
  rule with an empty anchor, falling back to fixed windows for an unstructured
  body.

The payoff is in the result, not the ingest: a hit points at
`WL-SPEC-025 §15.2`, which is a citable address a human can open and an agent
can pass to `lode doc show --section`. A hit that points at "chunk 7 of 31" is
not.

### 4.3 Every chunk carries its own context header {#sec-4.3}

A chunk lifted from the middle of a section is full of pronouns whose referents
live three paragraphs up. Embedding it bare embeds the ambiguity. Each chunk
therefore gets a header composed from metadata the store already has:

```
WL-SPEC-025 "Documents in the backbone" — §15.2 The ordered log
```

Tasks get `WL-142 [feature/in_progress] Fix the thing`; skills get
`skill: test-driven-development — <description>`. This is contextual retrieval
done structurally: no LLM call, no extra cost, no new source of truth — every
field is already a column.

The header is **stored**, in its own column, and does three jobs for it:

1. Prepended to the chunk text as the dense arm's embed input, so the vector is
   conditioned on where the text lives.
2. Indexed at lexical weight `A` against the body's `B` (§5), so a query naming
   a document or a heading outranks one merely mentioned in a body.
3. Kept reproducible — the exact bytes that were embedded are recoverable,
   which is what lets a re-embed be verified rather than trusted.

It counts against §4.1's budget, which the 3600-rune figure already allows for.
It is not part of the excerpt returned to callers (§9).

### 4.4 Tasks are one chunk until they are not {#sec-4.4}

A task is `title + "\n\n" + body`, embedded whole. Task bodies are typically a
few hundred tokens, and splitting one across chunks fragments an already-atomic
unit of meaning. Bodies past `ChunkRunes` split by §4.1's windowing; in practice
almost nothing does. A task with an empty body is still indexed on its title —
titles are the highest-signal text in the tracker.

## 5. Storage {#sec-5}

One table holds all three kinds, rather than three parallel tables. Three
tables were considered and rejected: that would mean three near-identical
store methods, three index pairs, and a cross-kind search built as a
`UNION ALL` of ranked subqueries that then has to be re-ranked. One table
means one query. A naive polymorphic key would lose referential integrity;
instead each kind gets its own nullable FK column, with a `CHECK` constraint
that exactly one is set.

```sql
CREATE TABLE index_chunks (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_kind text NOT NULL CHECK (subject_kind IN ('doc', 'task', 'skill')),

    -- Exactly one is set; each cascades, so deleting a subject drops its chunks
    -- and the index needs no tombstones.
    doc_id       text   REFERENCES docs   (doc_id) ON DELETE CASCADE,
    task_id      text   REFERENCES tasks  (id)     ON DELETE CASCADE,
    skill_id     bigint REFERENCES skills (id)     ON DELETE CASCADE,

    -- Denormalised so a project-scoped search (019) is one predicate rather
    -- than three joins. Null for skills: the registry is org-wide (016).
    project      text REFERENCES projects (id) ON DELETE RESTRICT,

    -- Frozen section anchor for docs (025 §3.2); '' for everything else.
    anchor       text NOT NULL DEFAULT '',
    chunk_index  int  NOT NULL,

    -- The indexed text itself. Stored because the lexical arm (§6.2) matches
    -- against it, the API excerpts from it, and a re-embed that cannot be
    -- reproduced cannot be verified.
    context_header text NOT NULL DEFAULT '',   -- §4.3
    chunk_text     text NOT NULL,

    -- Hash of the subject's indexed text, identical across all of one
    -- subject's chunks. The convergence loop (§7) compares this against the
    -- live subject; it is the whole freshness mechanism.
    content_hash text NOT NULL,

    -- Fixed width (§2.2): a wrong-shaped vector is refused at INSERT, and the
    -- typmod is what makes the HNSW index below legal.
    embedding    vector(768) NOT NULL,

    -- The lexical arm. 'simple', not 'english' — see §6.2; the choice is
    -- load-bearing, not stylistic. Generated rather than trigger-maintained so
    -- it cannot drift from chunk_text. Both to_tsvector(regconfig, text) and
    -- setweight are IMMUTABLE, which is what makes a generated column legal;
    -- the one-argument to_tsvector(text) is not, and must not be used here.
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', context_header), 'A') ||
        setweight(to_tsvector('simple', chunk_text), 'B')
    ) STORED,

    indexed_at   timestamptz NOT NULL,

    CONSTRAINT index_chunks_one_subject
        CHECK (num_nonnulls(doc_id, task_id, skill_id) = 1),
    CONSTRAINT index_chunks_kind_matches_subject CHECK (
        (subject_kind = 'doc'   AND doc_id   IS NOT NULL) OR
        (subject_kind = 'task'  AND task_id  IS NOT NULL) OR
        (subject_kind = 'skill' AND skill_id IS NOT NULL))
);

CREATE UNIQUE INDEX index_chunks_doc   ON index_chunks (doc_id, anchor, chunk_index)
    WHERE doc_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_task  ON index_chunks (task_id, chunk_index)
    WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_skill ON index_chunks (skill_id, chunk_index)
    WHERE skill_id IS NOT NULL;

CREATE INDEX index_chunks_kind_project ON index_chunks (subject_kind, project);

CREATE INDEX index_chunks_embedding ON index_chunks
    USING hnsw (embedding vector_cosine_ops);
CREATE INDEX index_chunks_tsv ON index_chunks USING gin (tsv);
```

`skill_embeddings` is dropped by the same migration, and its rows are not
carried over. They are 016-width vectors from a possibly different model, so
they are not comparable with anything this spec produces, and they carry none
of the text the lexical arm needs. The corpus re-embeds on first convergence,
which costs minutes on the sidecar. `embedding_config` survives unchanged; it
is already exactly the right table for §8.

## 6. Retrieval: two arms, fused by rank {#sec-6}

A search runs two independent retrievers over `index_chunks` and fuses their
**rankings**. Neither arm is primary and neither is a fallback.

### 6.1 Dense arm {#sec-6.1}

The query is embedded once (`Role = Query`, §3) and scored by cosine similarity,
max-pooled per subject:

```sql
SELECT subject_kind, doc_id, task_id, skill_id, anchor,
       max(1 - (embedding <=> $1::vector)) AS score,
       row_number() OVER (ORDER BY max(1 - (embedding <=> $1::vector)) DESC) AS rank
FROM index_chunks
WHERE <kind/project filters> AND (1 - (embedding <=> $1::vector)) >= $floor
GROUP BY subject_kind, doc_id, task_id, skill_id, anchor
ORDER BY score DESC
LIMIT $candidates          -- default 50, not the caller's limit
```

**Max, not mean**, identical to 016's `RecommendSkills` rule and for the same
reason: one strongly matching section should surface its document, and averaging
punishes long documents for being long.

**Pooling happens before ranking, and that is not cosmetic.** RRF gives every
row in a ranked list a share of the score. So fusing *chunk* rankings directly
would let a long document accumulate mass by placing eight mediocre chunks in
the top 50, letting it outrank a short document that answered the question
exactly once. Ranking by subject instead — for docs, by `(doc_id, anchor)`, so
one spec may still return two sections as two results — is what keeps fusion
honest.

The floor (default 0.35, from 016) is a **candidate filter on this arm only**,
not a threshold on the final result. It has to be: after fusion the score is a
rank-reciprocal sum, not a similarity, and comparing it to 0.35 would be a
category error.

### 6.2 Lexical arm: `simple`, and why that matters {#sec-6.2}

```sql
SELECT ..., max(ts_rank_cd(tsv, websearch_to_tsquery('simple', $q))) AS score,
       row_number() OVER (ORDER BY ... DESC) AS rank
FROM index_chunks
WHERE <kind/project filters> AND tsv @@ websearch_to_tsquery('simple', $q)
GROUP BY subject_kind, doc_id, task_id, skill_id, anchor
ORDER BY score DESC
LIMIT $candidates
```

`websearch_to_tsquery` rather than `plainto_tsquery` so a user can quote a
phrase and negate a term without the parser erroring on punctuation.

**The text search configuration is `simple`, not `english`, and that decision is
load-bearing.** The `english` configuration stems and drops stopwords, and this
corpus is full of snake_case identifiers built from stopwords. Measured on
Postgres 16:

| Input | `english` | `simple` |
|---|---|---|
| `child_of` | `'child'` | `'child'`, `'of'` |
| `is_replaced_by` | `'replac'` | `'is'`, `'replaced'`, `'by'` |
| `LODE_BRANCH_TEMPLATE` | `lode`, `branch`, `templat` | `lode`, `branch`, `template` |

Under `english`, `child_of` loses its `of` entirely, so the query `child_of`
matches the prose *"the child task of a parent"* — verified: it returns true.
Under `simple` that same query does **not** match the prose and **does** match
`child_of edge`. An identifier lookup that silently returns every paragraph
about children is worse than no lexical arm at all, because it pollutes the
fused ranking with confident noise.

The recall that stemming would have bought is not lost. It is **relocated**: it
becomes the dense arm's entire job. That is the division of labour a hybrid
system buys, and it is why the two arms should not be configured to do the same
thing. The lexical arm exists to be *exact*.

Weights follow `setweight` in §5 — a header match (`A`) outranks a body match
(`B`) under `ts_rank_cd`'s default weighting.

### 6.3 Fusion: reciprocal rank, k = 60 {#sec-6.3}

```
score(s) = Σ_arms  w_arm / (k + rank_arm(s))          k = 60, w = 1.0 both arms
```

Implemented as a `FULL OUTER JOIN` between the two candidate sets, joined on
the subject key. A missing rank's contribution is `coalesce`d to zero. Rows are
ordered by the summed score, then cut to the caller's `LIMIT`.

**Rank, not score — that is the point.** Cosine similarity lives on roughly
[0.2, 0.9], and the spread depends on the corpus. `ts_rank_cd` is unbounded,
and depends on term frequency and document length. There is no principled way
to put the two on one scale. Min-max normalising per query is the usual
attempt, and it is unstable: a query where every dense score is around 0.4
stretches that noise across the whole range and lets the dense arm dominate on
nothing. RRF sidesteps this by discarding the magnitudes and keeping only the
ordering each arm is actually reliable about. `k = 60` is the constant from the
original formulation. Its job is to flatten the head of the ranking, so a
rank-1 hit does not automatically beat the sum of two rank-2 hits.

Worked example, on the fixture that motivated §0 — query `child_of`:

| Subject | dense rank | lexical rank | RRF | final |
|---|---|---|---|---|
| `WL-SPEC-004 §6.1` (defines `child_of`) | 3 | 1 | 0.03227 | **1** |
| `WL-SPEC-025 §9.2` (plan acceptance) | 1 | — | 0.01639 | 2 |
| `WL-142` (task about child ordering) | 2 | — | 0.01613 | 3 |

The dense arm put the correct answer third. Fusion puts it first, without
needing to know that this query was "an identifier query" — no classifier, no
routing heuristic, no per-query mode switch. That property is why RRF is the
mechanism and not a hand-tuned blend.

### 6.4 Filters, and an honest note on HNSW {#sec-6.4}

Kind and project filters apply inside both arms. The `project IS NULL` clause
— the "or it's org-wide" half of the filter — is what keeps org-wide skills
visible from inside a project-scoped search (019).

pgvector applies the `WHERE` clause after walking the index, so a highly
selective filter can return fewer than `$candidates` rows from the dense arm
even when matches exist. At this corpus size the query planner will often
choose an exact scan anyway, so the problem rarely shows up; the lexical arm,
being a GIN index over a `@@` predicate, does not have the problem at all. If
it does start showing up, the fix is `hnsw.iterative_scan`, not a redesign —
noted here so the first person who sees a short candidate set does not go
looking for a bug in the ranking.

## 7. Freshness: convergence, not hooks {#sec-7}

The indexer is a background loop that makes the index agree with the corpus. It
does not hook write sites, and it does not subscribe to the event log.

For each kind it selects subjects whose live `content_hash` differs from the
hash on their chunk rows — including subjects with no chunk rows at all — then
re-embeds each and replaces its chunk set in one transaction. Chunks whose
subject no longer exists are already gone by FK cascade (§5).

Convergence rather than events, for three reasons. It is **self-healing**: a
crashed run, a failed provider call, or a row written by a path nobody
remembered to instrument all resolve on the next pass, whereas a missed event is
missed forever. It is **already the pattern** — `skillsync.embedMissing` does
exactly this today and is the code being generalised. And the event vocabulary
does not currently reach far enough: `eventbus` knows about
`wl:DocumentSubmitted` and `wl:DocumentAccepted`, but nothing about tasks
(025 §15.2). So an event-driven indexer would need new event types before it
could index a task at all.

The cost is latency — a task is searchable on the next pass, not the next
instant. That is the right trade for a retrieval aid, and the event log remains
available later as a *latency optimisation layered on top of* convergence, never
as a replacement for it. Anything that makes convergence skippable makes the
index unfalsifiable.

The loop runs on an interval (default 5 minutes, `LODE_INDEX_INTERVAL`), on
`lode serve` only. It still runs with no embedding provider configured, because
the lexical arm needs the rows (§11); it simply writes no vectors.

## 8. Provider change invalidates the vectors {#sec-8}

This section is unchanged in principle from 016 §2, just widened in scope.
`embedding_config.provider_id` records the space the stored vectors belong to.
At startup, before the indexer embeds anything, the server compares the
configured provider's `ID()` against the stored one; on a mismatch it clears
**every** vector and records the new id. The next convergence pass rebuilds them.

Because the chunk row now carries the lexical arm's text as well as the vector,
invalidation nulls the `embedding` column instead of deleting the row. The text
and its `tsv` are provider-independent, so there is no reason to throw away a
working lexical index just because the embedding model changed. This is also
why the `embedding` column is nullable: the dense arm already filters on
similarity, so a null-vector row simply does not appear in its candidate set.
**During a re-embed the instance therefore degrades to lexical-only, not to
nothing.**

`skillsync.InvalidateOnProviderChange` generalises to all kinds and moves out of
`skillsync` — it is no longer a skills concern.

Clearing rather than filtering is deliberate. Vectors from two different
models in one table are not merely stale — they are meaningless to compare —
and a `WHERE provider_id = ...` predicate would make it possible to serve them
anyway.

## 9. Surfaces {#sec-9}

**API.** `GET /api/v1/search?q=&kind=&project=&limit=&mode=`, returning ranked
`model.SearchHit` values (declared once in `internal/model`, per ADR 036), each
carrying `Kind`, the subject's id, `Anchor`, `Title`, an excerpt from
`chunk_text`, the fused score, and **the per-arm ranks that produced it**.
Returning the arm ranks is what makes a bad result diagnosable rather than
merely disappointing. `kind` is repeatable; omitted means all three.

`mode` is `hybrid` (default), `dense`, or `lexical`, and exists so the arms can
be compared on a real query rather than argued about. It is a debugging feature
with a stable contract, not a tuning knob callers are expected to set.

The route needs a `routeGuards` entry or `NewServer` refuses to boot
(`internal/api/router.go`). It takes a new `permSearchRead` ("search.read"),
granted to `{RoleUser, RoleAdmin}` — the same grant `permDocRead`,
`permTaskRead` and `permSkillRead` already carry, so this adds a permission
without changing who can see what. **When project-scoped roles arrive, this is
the endpoint that needs revisiting**: one permission over three subject kinds is
only honest while all three reads are granted identically. That note belongs in
`authz.go` next to the grant, not only here.

**CLI.** `lode search <query> [--kind doc|task|skill] [--mode] [--limit]
[--json]`, rendering `WL-SPEC-025 §15.2  0.032  The ordered log` — an address a
reader can act on directly.

**Cockpit.** Out of scope for this spec. The API is the surface that unblocks
agents, which is the motivating case (§0); a search box is a cockpit spec's
business.

**Recommendation stays.** `POST /api/v1/skills/recommend` (016 §2) keeps its
behaviour and its response shape. It becomes a thin caller of the same retrieval
path with `kind=skill`, dropping its own embedding code rather than growing a
second one. It gains the lexical arm for free, which is a real improvement: a
task brief naming a tool by name now matches the skill that names it back.

## 10. Metrics {#sec-10}

Per 022: nil-safe struct in the owning package, `prometheus.Registerer` threaded
from `serve.go`, bounded labels. `internal/embed`'s existing provider-call
metrics already cover the outbound call and are not duplicated.

| Metric | Type | Labels |
|---|---|---|
| `worklode_index_chunks` | gauge | `subject_kind` |
| `worklode_index_chunks_without_vector` | gauge | — |
| `worklode_index_subjects_stale` | gauge | `subject_kind` |
| `worklode_index_reembed_total` | counter | `subject_kind`, `outcome` |
| `worklode_index_convergence_duration_seconds` | histogram | — |
| `worklode_search_requests_total` | counter | `mode`, `outcome` |
| `worklode_search_arm_duration_seconds` | histogram | `arm` |
| `worklode_search_arm_empty_total` | counter | `arm` |

`subject_kind` is bounded by the `CHECK` in §5, `mode` and `arm` by their
enumerations; `outcome` is `ok|error|empty`, so "found nothing" is visible
without log-diving.

Two are worth alerting on. `worklode_index_subjects_stale` should return to zero
every pass; a floor above zero means a subject fails to embed repeatedly.
`worklode_search_arm_empty_total{arm="lexical"}` rising while the dense arm
stays busy is the signature of a broken `tsv`. This is the failure mode this
spec is most exposed to: a lexical arm that quietly returns nothing degrades
the whole system into exactly the dense-only setup §0 rejects, and nothing
else would notice.

## 11. Degraded operation {#sec-11}

016's contract, kept and strengthened. **An instance with no embedding provider
configured still has working search** — this is the clearest dividend of the
two-arm design. Convergence still runs, still chunks, still writes `chunk_text`
and `tsv`; only the vectors are absent. Search runs the lexical arm alone, and
fusion over one arm is just that arm's ranking. The response reports
`provider: "none"` and `mode: "lexical"` so a caller can tell a degraded
instance from a well-configured one, but it returns real results, not an empty
set. Recommendation likewise falls back to pins plus lexical matches.

A provider that is configured but failing degrades the same way: convergence
logs and retries next pass, existing vectors keep serving, and newly indexed
chunks are lexical-only until it recovers.

The reverse — a corrupt or missing `tsv` — degrades to dense-only, which is the
0.x behaviour and still useful. Neither arm's failure takes search down, and
that is the property to preserve when this code is changed.

## 12. Open questions {#sec-12}

Recorded rather than resolved, because each wants evidence this spec cannot
manufacture:

1. **Arm weights.** RRF ships with `w = 1.0` on both arms because an untuned
   equal weighting is the honest default and the literature's. Whether this
   corpus wants the lexical arm weighted up is an empirical question needing a
   judged query set, which §9's `mode` parameter exists to produce.
2. **Trigram fallback for misspellings.** `pg_trgm` would catch `wroktree` and
   partial identifiers the way neither arm does. It is a third arm, fuses the
   same way, and should wait until the two-arm system has been used enough to
   show the gap is real.
3. **Chunk-level access control.** Moot today (§9), load-bearing the moment
   project-scoped roles exist.
4. **Query rewriting** for the agent path — expanding a terse query before
   embedding. Cheap to add, needs measurement to justify.
5. **Skill bundle files** beyond `SKILL.md`, out of scope by request and by
   016 §2.

## 13. Acceptance {#sec-13}

1. Docs, tasks and skills are all indexed, and a semantic query ("how do leases
   interact with worktree pruning") returns hits across all three.
2. An identifier query (`child_of`, `LODE_BRANCH_TEMPLATE`) ranks the subject
   that literally contains the identifier first, and the test that proves it
   fails when the lexical arm is disabled. §0's ranking inversion is the
   regression test, not an anecdote.
3. Fusion is over max-pooled **subject** rankings, and a long document with many
   weak chunks does not outrank a short exact match. A test constructs that case.
4. A document hit reports a frozen section anchor that resolves through
   `lode doc show --section`, and the response carries both arm ranks.
5. Every stored vector is 768-wide, both indexes exist, and an attempt to store
   another width is refused by Postgres rather than by convention.
6. `to_tsvector` uses `simple`: a test asserts that the query `child_of` does not
   match prose reading "the child task of a parent". This is the assertion that
   stops someone "fixing" the config to `english` later.
7. Changing `LODE_EMBEDDING_MODEL` clears the vectors and the next convergence
   pass rebuilds them, with no schema migration, no mixed-space results at any
   point, and **lexical search working throughout**.
8. An instance with no provider configured serves real lexical results with
   `provider: "none"`, not an empty set.
9. The default deployment embeds the whole corpus on CPU, with no GPU and no
   third-party embedding call.
10. Convergence is idempotent: a second pass over an unchanged corpus re-embeds
    nothing and leaves `worklode_index_subjects_stale` at zero.
