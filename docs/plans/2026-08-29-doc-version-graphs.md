---
status: draft
covers:
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-4
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-4.1
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-4.2
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-4.3
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-24
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-decision-tasks.md
      - docs/plans/2026-08-29-doc-accept-gate-and-amendment.md
      - docs/plans/2026-08-29-escalation-and-grooming.md
---
# DCAT version graphs

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** project 025 §4's versioned document snapshots into the knowledge
graph. The store already holds everything §4 needs — `doc_versions`
(migration 0055, 025 §4.5) and `doc_sections.last_revised_in` — and
`internal/graphproj/doc.go` says in its own comment what is still missing:
"deliberately not yet projected here: … 025 §4's versioned snapshot graphs
(dcat:hasVersion and wl:lastRevisedIn)". This plan is purely that projection:
one immutable named graph per published version (§4.3), the
`dcat:hasVersion`/`dcat:hasCurrentVersion` pointers on the canonical node
(§4.1), `wl:lastRevisedIn` on every published section (§4.4, already covered
`full` by an earlier plan store-side; the graph half lands here), and the
ADR-0006 reconciliation §4.2 demands. No migration, no new store method, no
new `internal/model` type — `model.DocVersion`, `model.DocVersionSummary`
and `model.DocSection` already carry every projected fact (ADR 036 holds
with zero new shapes).

**Part 4 of the WL-SPEC-25 planning series.** Independent of parts 1–3
(`decision-tasks-plan`, `doc-accept-gate-and-amendment-plan`,
`escalation-and-grooming-plan`); the shared `#sec-24` claim is the only
overlap. §24's criteria 10 and 17 are the ones this part makes testable.
This part is deliberately smaller than the other three — five tasks, not
padded to eight: the storage, the IRIs (`iri.DocVersion` exists), and the
ontology terms (`wl:lastRevisedIn` and the DCAT reuse are already in
`ns/ontology.ttl` and `ns/shapes.ttl`) all pre-exist, so what remains is two
pure projection functions, one write-path change, one round-trip test, and
one cross-repo document edit.

**WL-150** ("025 §6 rule 2: the dct:description branch needs section-level
supersession in the graph") lands on top of the machinery this plan builds —
its `dct:description` on a vanished section needs the version-graph
projection to have somewhere to stand. Its scope is **not** folded in here;
once this plan exists, WL-150 should gain a `blockedBy` edge on it (the
series coordinator wires that edge).

**Read first:**
- `docs/specs/inlined/025-documents-in-the-backbone.md` §4–§4.5, §24
  criteria 10 and 17
- `internal/graphproj/doc.go` — `DocTriples` / `SectionTriples`, the two
  functions this plan extends, and the comment naming the gap
- `internal/projector/projector.go` — `projectOne`'s per-document loop: the
  whole-graph-PUT write model every task here must fit
- `internal/kg/iri/iri.go` — `Doc`, `Section`, `DocVersion` (already
  minted), `DeclaredGraph`, `GraphNS`
- `internal/store/docs.go` — `ListDocVersions` (union of `docs` +
  `doc_versions`, newest first), `GetDocVersion`, `snapshotDocVersion`
- `ns/ontology.ttl` lines ~278–290 and `ns/shapes.ttl` lines ~82–88 —
  `wl:lastRevisedIn` and its SHACL shape, already defined
- `internal/graphproj/oxigraph_test.go` + `graphtest/` — the SPARQL
  round-trip harness Task 4 uses
- `internal/projector/projector_test.go` — the fake graph-server harness
  (`newProjector`, `f.last(graph)`) Task 3's tests extend

## Global Constraints

- **Exact IRI shapes** (025 §4.1, §4.3; `iri.DocVersion` already emits the
  first):
  - snapshot node: `https://worklode.io/ns/id/doc/<slug>/v<n>`
  - version graph: `https://worklode.io/ns/graph/declared/<slug>/v<n>`
    (the existing `iri.DeclaredGraph(slug)` plus `/v<n>`)
- **Exact vocabulary**, reused not minted — new constants beside the
  existing ones in `internal/graphproj`:
  `dcat:hasVersion`, `dcat:hasCurrentVersion`, `dcat:previousVersion`
  (`http://www.w3.org/ns/dcat#…`), `prov:wasRevisionOf`
  (`http://www.w3.org/ns/prov#wasRevisionOf`), `dct:issued`
  (`http://purl.org/dc/terms/issued`, typed
  `http://www.w3.org/2001/XMLSchema#date`). `wl:lastRevisedIn` is
  `iri.Term("lastRevisedIn")`. A snapshot carries the document's own class
  (`docClass`), never a minted version class.
- **What gets version graphs:** every version (`store.ListDocVersions`) of a
  document whose `status != "draft"`. A draft has published nothing and its
  current body is mutable, so it keeps today's canonical-node-only
  projection — and therefore no `dcat:hasVersion`/`dcat:hasCurrentVersion`
  either, because a current-version pointer must never name a graph that
  was not written. Version numbers are dense from 1: every bump site goes
  through `snapshotDocVersion` and increments by exactly one
  (`internal/store/docs.go` plan-edit path, `internal/store/docrevisions.go`
  land path), so `v<n>`'s predecessor is always `v<n-1>`.
- **Immutability is by construction, not enforcement** (025 §4.3): a version
  graph renders only from rows that never change once written — a
  `doc_versions` row, or the `docs` row of a non-draft document, whose body
  moves only by minting the next version. Re-rendering is byte-identical
  (`graphproj.Document` sorts and dedupes), so the projector's
  reconciler-re-PUTs preserve §24 criterion 10's "the v3 graph is
  byte-identical afterwards" observably.
- **Atomicity maps onto ordered whole-graph PUTs** (§4.3): graph-server
  exposes no SPARQL Update, so "one SPARQL Update" becomes: PUT every
  version graph **before** the single PUT of the document's mutable declared
  graph, which carries `dcat:hasCurrentVersion`. The declared-graph PUT is
  the atomic pointer switch; no reader ever sees a current-version pointer
  whose target graph is absent. A test pins the order.
- **The graph carries structure, not markdown.** A version graph holds the
  snapshot node plus that version's section set (anchor, number, heading)
  re-parsed from the stored body with `designdoc.Parse`; the body literal
  itself is never embedded — no declared graph today carries markdown, and
  version bodies stay readable via `lode doc get --version` (§4.5, built).
  A version body that fails to parse (pre-anchor-era imports) projects a
  node-only graph rather than failing the document. Plans have no sections
  (025 §9), so a plan's version graphs are node-only by definition.
- **Metrics** (spec 022): the new write outcome gets
  `worklode_graph_projection_doc_version_graphs_total` (counter, no labels)
  in `internal/projector/metrics.go`, nil-safe like its siblings, with a
  test. Version-graph deletes reuse the existing
  `worklode_graph_projection_graphs_deleted_total`.
- **No store writes, no migration, no model change, no ns/ change.** This
  plan touches `internal/graphproj`, `internal/kg/iri`,
  `internal/projector`, and one file in the external rdf-registry repo.
- **Every task leaves `go test -trimpath ./...` green and ends in its own
  commit.** Never bare `go test`. graphproj/projector unit tests run without
  external services; Task 3's projector tests need Postgres with pgvector
  (`TEST_POSTGRES_DSN`, skips silently without it — a skipped run proved
  nothing); Task 4 needs Oxigraph (`TEST_SPARQL_URL`, same caveat).

## Tasks

### Task 1 — Snapshot-node triples and the version-graph IRI

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
```

Pure projection, no I/O. In `internal/kg/iri/iri.go`, beside
`DeclaredGraph`:

```go
// DeclaredVersionGraph returns the named graph holding one immutable
// document version (025 §4.3): graph/declared/<slug>/v<n>. Sibling of
// DeclaredGraph, which stays the document's mutable canonical graph.
func DeclaredVersionGraph(docSlug string, version int) string {
	return DeclaredGraph(docSlug) + "/v" + strconv.Itoa(version)
}
```

In `internal/graphproj/doc.go`, the new vocabulary constants (Global
Constraints spellings) and `DocVersionTriples(d model.Doc, v
model.DocVersion, sections []model.DocSection) []Triple`, projecting one
version's graph content:

- snapshot node `iri.DocVersion(d.Slug, v.Version)`: `rdf:type` via
  `docClass(d.Kind)`, `dct:title` from `v.Title`, `dcat:version`
  `Text(strconv.Itoa(v.Version))`, `dct:created` from `v.CreatedAt` as
  today's timestamps; when `v.Version > 1`, both `dcat:previousVersion` and
  `prov:wasRevisionOf` → `iri.DocVersion(d.Slug, v.Version-1)`; when
  `v.Issued != ""`, `dct:issued` typed `xsd:date`.
- one `wl:Section` node per entry in `sections` (version-free
  `iri.Section(d.Slug, sec.Anchor)` — the anchor is the identity, the graph
  provides the version context): `rdf:type`, `dct:title` from `sec.Heading`,
  `dct:isPartOf` → the **snapshot** IRI, not the canonical one. `sections`
  is what the caller parsed from that version's body; `model.DocSection` is
  reused as the carrier (`Anchor`, `Number`, `Heading` filled; no new type).

`prov:wasAttributedTo` from 025 §4.1's example is deliberately not emitted:
`doc_versions` stores no per-version author (§4.5's scope), and the
document-level `prov:wasGeneratedBy` on the canonical node already carries
authorship. Say so in the function comment.

First test in `internal/graphproj/doc_test.go`, same style as
`TestDocTriples`:

```go
func TestDocVersionTriples(t *testing.T) {
	d := model.Doc{Slug: "025-backbone", Kind: "spec", Status: "accepted"}
	v := model.DocVersion{Version: 3, Title: "Spec 025 — Backbone",
		Issued: "2026-07-26", CreatedAt: time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)}
	secs := []model.DocSection{{Anchor: "sec-1", Heading: "Why"}}
	doc := string(Document(DocVersionTriples(d, v, secs)))
	subj := "<" + iri.DocVersion("025-backbone", 3) + ">"
	for _, want := range []string{
		subj + " <" + RDFType + "> <" + iri.Term("Spec") + ">",
		subj + " <" + DCATVersion + "> \"3\"",
		subj + " <" + DCATPreviousVersion + "> <" + iri.DocVersion("025-backbone", 2) + ">",
		subj + " <" + ProvWasRevisionOf + "> <" + iri.DocVersion("025-backbone", 2) + ">",
		subj + " <" + DCTIssued + "> \"2026-07-26\"^^<" + XSDDate + ">",
		"<" + iri.Section("025-backbone", "sec-1") + "> <" + DCTIsPartOf + "> " + subj,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("version projection missing %q\n%s", want, doc)
		}
	}
}
```

Second case: `Version: 1` emits no `previousVersion`/`wasRevisionOf`;
`Issued: ""` emits no `dct:issued`; `sections == nil` yields a node-only
graph whose every subject is the snapshot IRI.

- [ ] `iri.DeclaredVersionGraph` + a case in the iri tests
- [ ] constants + `DocVersionTriples` + tests as above
- [ ] `go test -trimpath ./internal/graphproj ./internal/kg/... ` → `ok`
- [ ] commit: `graphproj: project 025 §4.1 version snapshots (DocVersionTriples)`

### Task 2 — Canonical version pointers and wl:lastRevisedIn

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Still pure. Two changes in `internal/graphproj/doc.go`:

1. `DocTriples` gains the version list:
   `DocTriples(d model.Doc, versions []model.DocVersionSummary)`. When
   `versions` is non-empty it appends `dcat:hasVersion` →
   `iri.DocVersion(d.Slug, v.Version)` per entry and
   `dcat:hasCurrentVersion` → `iri.DocVersion(d.Slug, d.Version)`. The
   caller passes nil for a draft (Global Constraints), so a draft's
   projection is byte-identical to today's. The plain `dcat:version`
   literal on the canonical node **stays** — DCAT permits it, 006 §11
   documents it as-built, and dropping it buys nothing; rewrite the
   "deliberately not yet projected" comment to point here instead.
2. `SectionTriples` emits, for every published section,
   `wl:lastRevisedIn` (`iri.Term("lastRevisedIn")`) →
   `iri.DocVersion(d.Slug, sec.LastRevisedIn)` — the store column
   `doc_sections.last_revised_in` is already on `model.DocSection`, this is
   its first reader graph-side. Published sections only exist on non-draft
   documents, so the target snapshot graph always exists.

Update the two existing callers (`internal/projector/projector.go` passes
`nil` for now — Task 3 feeds it real versions — and `doc_test.go`). Extend
`TestDocTriples` with a versions case asserting both pointer properties, and
`TestSectionTriples` with:

```go
want := "<" + iri.Section("025-backbone", "sec-1") + "> <" +
	iri.Term("lastRevisedIn") + "> <" + iri.DocVersion("025-backbone", 2) + ">"
```

for a section with `LastRevisedIn: 2`. Keep the subject-completeness
assertion on `DocTriples` green — the new triples all have the canonical
subject.

- [ ] `DocTriples` signature + pointers, callers updated
- [ ] `wl:lastRevisedIn` in `SectionTriples`
- [ ] `go test -trimpath ./internal/graphproj ./internal/projector` → `ok`
- [ ] commit: `graphproj: dcat:hasVersion/hasCurrentVersion and wl:lastRevisedIn (025 §4.1, §4.4)`

### Task 3 — Projector writes the version graphs

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

`internal/projector/projector.go`, `projectOne`'s document loop. For each
live document:

- `versions, err := p.st.ListDocVersions(ctx, d.ID)` when
  `d.Status != "draft"`; nil for drafts.
- For each version, `p.st.GetDocVersion(ctx, d.ID, v.Version)` for the
  body, parse sections with a small unexported helper
  `versionSections(kind, body string) []model.DocSection` (nil for plans;
  `designdoc.Parse` for specs/ADRs, mapping `Section.Anchor/Number/Title`;
  parse error → nil, node-only graph — log at Info, do not fail the doc),
  render `graphproj.DocVersionTriples`, and
  `PutGraph(ctx, Branch, iri.DeclaredVersionGraph(d.Slug, v.Version), …)`
  — **before** the declared-graph PUT, which now renders
  `graphproj.DocTriples(d, versions)`. Count each version-graph PUT on the
  new metric.
- Tombstoned documents: alongside the existing `DeleteGraph` of the
  declared graph, list the tombstone's versions (`ListDocVersions` still
  works — the `docs` row exists) and delete each version graph, tolerating
  `graphserver.ErrNotFound` exactly as the existing loop does, counting
  deletes on the existing metric.

`ponytail:` note to leave in the code: every pass re-fetches and re-parses
every version of every dirty project's documents. Idempotent (byte-identical
PUTs) and fine at the current corpus size; if version counts grow, skip
PUTting a version graph the server already has — version graphs are
immutable, so an existence probe is sufficient.

Metrics (`internal/projector/metrics.go`):
`worklode_graph_projection_doc_version_graphs_total` counter, nil-safe
`recordDocVersionGraph()`, registered in `NewMetrics`, asserted in
`metrics_test.go` beside its siblings.

Tests in `internal/projector/projector_test.go` (fake graph-server harness,
Postgres required):

```go
// TestRunOnceProjectsVersionGraphs: create a doc, accept it, land a
// revision (v1 → v2), RunOnce, then assert:
//  - f.last(iri.DeclaredVersionGraph(slug, 1)) holds the v1 snapshot node
//  - f.last(iri.DeclaredVersionGraph(slug, 2)) holds dcat:previousVersion → v1
//  - f.last(iri.DeclaredGraph(slug)) holds dcat:hasCurrentVersion → v2 and
//    both dcat:hasVersion triples
//  - the fake's recorded PUT order has both version graphs before the
//    declared graph (025 §4.3's pointer guarantee)
//  - a section revised in v2 carries wl:lastRevisedIn → v2; an untouched
//    one still points at v1 (§24 criterion 17's graph-side halves)
```

Plus: a draft document still writes only its declared graph with no
`hasCurrentVersion` (extend `TestRunOnceProjectsDocuments`); a tombstoned
two-version document gets all three graphs deleted (extend
`TestDeletedDocumentGraphIsRemoved`); re-running `RunOnce` after touching
the project leaves the v1 graph byte-identical (string-compare the fake's
two recordings — criterion 10).

- [ ] write path + ordering + tombstone deletes
- [ ] metric + nil-safety test
- [ ] `TEST_POSTGRES_DSN=… go test -trimpath ./internal/projector` → `ok` (not skipped)
- [ ] commit: `projector: write immutable per-version graphs before the canonical pointer (025 §4.3)`

### Task 4 — Oxigraph round-trip: the staleness query works

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

The point of the whole projection is 025 §4.4's query shape: "a claim pinned
at v3 against §4.2 is stale iff `§4.2 wl:lastRevisedIn` names a snapshot
whose `dcat:version` exceeds 3" — compared as numbers, never as IRI strings.
Prove it against a real SPARQL engine in
`internal/graphproj/oxigraph_test.go` (graphtest harness; skips without
Oxigraph, fatal in CI with `TEST_SPARQL_URL` set):

- Build a two-version document with `DocVersionTriples` (v1, v2) and
  `DocTriples`/`SectionTriples` where `sec-a` has `LastRevisedIn: 1` and
  `sec-b` has `LastRevisedIn: 2`; load each into its own run-unique named
  graph mirroring the DeclaredGraph/DeclaredVersionGraph split.
- The staleness query for a claim pinned at 1, over the graph union:

```sparql
SELECT ?sec WHERE {
  GRAPH ?g  { ?sec <…lastRevisedIn> ?snap }
  GRAPH ?vg { ?snap <http://www.w3.org/ns/dcat#version> ?n }
  FILTER (xsd:integer(?n) > 1)
}
```

  returns exactly `sec-b`. Add the §4.1 footgun case: versions 3 and 10,
  asserting the pin-at-9 query returns the v10-revised section — numeric
  comparison, where string comparison of `"10" < "3"` (and of the IRIs)
  would get it backwards.
- Round-trip `dcat:hasCurrentVersion` from the canonical graph to the
  snapshot's `dcat:version` — the join a pinned-claim reader (006's
  `wl:pinnedVersion`, WL-388's scope, not ours) will lean on.

No SHACL runner is added — `ns/shapes.ttl`'s `wl:lastRevisedIn` shape
(target must carry `dcat:version`) is exactly what the first query proves
operationally, and the SHACL CI gate is 006 §7/§16 territory owned by
WL-388's plan.

- [ ] tests as above
- [ ] `TEST_SPARQL_URL=http://localhost:7878 go test -trimpath ./internal/graphproj -run Oxigraph\|Version` → `ok` (not skipped)
- [ ] commit: `graphproj: prove the §4.4 staleness query numeric over Oxigraph`

### Task 5 — Amend rdf-registry ADR-0006 for the versioned sibling (cross-repo)

```yaml
kind: design
priority: medium
```

025 §4.2 is explicit: versioned instance IRIs are compatible with ADR-0006's
version-free-IRI rule only via "a small amendment to rdf-registry ADR-0006
permitting the versioned sibling under a named exception, rather than a
silent local deviation". That amendment does not exist yet:
`github.com/sunstoneinstitute/rdf-registry`,
`docs/adr/0006-iri-namespace-scheme.md`, today names exactly one exception
(GTIO's legacy version-in-path terms) and says nothing about worklode
document snapshots.

This task is a PR **in that repository**, not an edit on this branch:

- Add a named exception beside the GTIO one, quoting 025 §4.2's rule
  verbatim: *the canonical IRI remains version-free and is the only IRI
  anything links to by default; versioned IRIs
  (`https://worklode.io/ns/id/doc/<slug>/v<n>`) are additional siblings and
  appear exclusively in pinned claims (025 §11)*. Frame it as an instance-
  IRI exception: ADR-0006 §3's "no term is ever minted under a version
  path" is untouched — no *term* IRI changes.
- Note the worklode side is already normative: worklode spec 006 §10's
  instance grammar carries the same carve-out, and 025 §4/§17 own the
  rationale.
- Follow that repo's ADR conventions for recording an amendment (it has no
  worklode-style anchor machinery; a dated amendment note in the ADR body
  is its idiom).

Done when the rdf-registry PR is open and linked from this task; merging it
is the rdf-registry maintainers' call and does not block Tasks 1–4, which
implement what 025 §4 already normatively specifies.

- [ ] draft the amendment text, open the PR in rdf-registry
- [ ] link the PR on this task

## Not in this plan

- **Section-level supersession's `dct:description` branch** — WL-150, which
  should gain `blockedBy` → this plan (see above).
- **`wl:pinnedVersion` / RDF-1.2 pinned claims, SHACL CI gates, deliverable
  projection** — spec 006's own gaps, owned by WL-388's plan.
- **Backfilling versions for documents edited before migration 0055** —
  ruled out by 025 §4.5 itself: the data was never retained. Such documents
  project the versions the store has, which is correct by definition.
- **Graph-side diffing between versions** — no consumer; §4.5 scopes it out
  store-side and nothing graph-side changes that.
