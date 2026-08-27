---
status: draft
covers:
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-11.5
    coverage: partial
---
# The repo-implements deriver — implementation plan

Builds the deriver WL-275 decided the shape of: `observed/repo-implements` is
**repo-local** (007 §1.1 — `lode derive`, one writer and one graph per repo,
`observed/repo-implements/<host>/<owner>/<repo>`), and the pinned version
rides the asserted edge as an RDF-1.2 triple-term annotation
(`<< <component> wl:implements <section> >> wl:pinnedVersion
<id/doc/<slug>/v<n>>`, `wl:pinnedVersion` minted in 006 §3 and mirrored in
`ns/ontology.ttl`). Everything below builds against those settled decisions;
neither is reopened here.

What already exists: `internal/kg/implements` (WL-44) parses
`.worklode/implements.yaml` (`Load`/`Parse`), derives the claiming component
through the components manifest (`Resolve(f, m, repoCoords) ([]Claim, error)`
— the pin travels on `Claim`), and renders the edges
(`Triples(claims) []graphproj.Triple`). `runDeriveLocal`
(`internal/cmd/overview.go`) already loads the components manifest, holds the
resolved `host/owner/name` (real host since WL-269, foreign-manifest check
since WL-270), and runs each source through `derive.Run` into
`iri.RepoObservedGraph(source, host, owner, name)`. `internal/overview`
(`queries.go`) is where 007's standing queries live, each a small SPARQL
constant with a typed wrapper.

## Tasks

### Task 1 — Emit the pin annotation from implements.Triples

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

`implements.Triples` emits only the bare `<component> wl:implements
<section>` edges; the pin sits unused on `Claim`. Extend the rendering so
each claim also yields its annotation line in RDF-1.2 N-Triples syntax:

```
<< <component> <…ontology#implements> <section> >> <…ontology#pinnedVersion> <id/doc/<slug>/v<n>> .
```

- Decide the mechanics against `graphproj.Triple`'s shape: either a triple
  whose subject is the serialized triple term, or (if `graphproj`'s renderer
  refuses non-IRI subjects) a second rendering path in
  `internal/kg/implements` that emits finished N-Triples lines. Prefer the
  smallest change that keeps `derive.Run`'s document contract (bytes in, PUT
  whole graph).
- Prove graph-server ingests it: an e2e-adjacent or integration test PUTs a
  document containing an annotation line to Oxigraph (the test harness the
  other derivers use) and reads it back with a
  `<< ?c ?p ?s >> wl:pinnedVersion ?v` query. If the deployed graph-server
  rejects RDF-1.2 N-Triples, stop and report — that is a deployment gap to
  fix, not something to code around with reification.

- [ ] annotation emission with table tests (multi-claim, multi-component split)
- [ ] round-trip test against the graph-server test harness
- [ ] Commit

### Task 2 — Wire repo-implements as runDeriveLocal's third source

```yaml
kind: feature
priority: high
blockedBy: [1]
```

In `runDeriveLocal` (`internal/cmd/overview.go`), after the existing two
sources: read `.worklode/implements.yaml` from the checkout. Absent file →
the source is skipped with an inline note (most repos claim nothing; absence
is normal, not an error — mirror the go-imports skip reporting). Present →
`implements.Load`, `implements.Resolve` against the already-loaded components
manifest and the already-resolved `host/owner/name`, render (Task 1), and run
through `derive.Run` into
`iri.RepoObservedGraph("repo-implements", host, owner, name)`.

Resolution errors (path matching no component, pin/section slug mismatch) are
**errors, not skips** — 025 §11.3 makes them publication errors naming the
offender, and a claim silently dropped is worse than a failed derive.

- [ ] third source wired, absent-manifest skip note, resolution errors fatal
- [ ] `TestDeriveDryRunPrintsTriples`-style test showing edge + annotation
- [ ] dry-run output covered for a repo with no implements.yaml (note, no error)
- [ ] Commit

### Task 3 — Standing queries: coverage, stale claim, orphaned claim

```yaml
kind: feature
priority: medium
blockedBy: [2]
```

In `internal/overview/queries.go`, following the existing
query-constant-plus-typed-wrapper pattern, add the reads 025 §11.5 tabulates
(the delivered-coverage row stays out — it needs the Deliverable→Deployment
join whose v1 sources are not projected yet; say so in a comment rather than
shipping a query that always returns nothing):

- **Unimplemented intent**: accepted sections with no inbound `wl:implements`
  edge from any graph.
- **Coverage of a document**: implemented sections ÷ non-superseded sections,
  per document.
- **Stale claim**: `<< ?c wl:implements ?s >> wl:pinnedVersion ?pv` where
  `?pv dcat:version` < `?s wl:lastRevisedIn/dcat:version` — numbers, never
  IRI strings (025 §4.1).
- **Orphaned claim**: a claim whose section IRI matches no section of the
  document's current version.

Surface them in `lode overview` beside the drift queries, same rendering
conventions (`internal/cli` renderer, `--json` passthrough). Note that against
today's production graph **all four** queries return nothing: WL-289's
document projection deliberately defers sections
(`internal/graphproj/doc.go`), so no `wl:Section` nodes exist for the
unimplemented-intent and coverage joins until 025's fuller projection lands
them, and the stale and orphan queries additionally wait on versioned
snapshots (`wl:lastRevisedIn`). Assert every query's text against the harness
with hand-loaded fixture triples anyway so the shapes cannot rot silently.

- [ ] four queries with wrappers and harness tests
- [ ] `lode overview` renders the new sections; command catalog regenerated
- [ ] Commit
