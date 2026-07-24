# Spec 06 — Data-platform requirements for the Worklode KG

**Status:** spec · **Owner hand-off:** data-platform team · **Umbrella:** `00-umbrella-architecture.md`

Worklode's **knowledge graph** (the asserted architecture graph + the projected work graph)
lives in the data-platform `graph-server` (Postgres RDF quad store). The **execution backbone**
(tasks, leases, events) stays in Worklode's own Postgres — so the data-platform only has to host
the *knowledge* half. This spec is the minimum the data-platform must ship for that.

## Context (verified against data-platform `graph-server`)

Already built: named-graph writes with a genuine single-writer serialization point (`SELECT …
FOR UPDATE` per-branch + one ACID Postgres txn), O(1) copy-on-write branch create, child-wins
overlay reads, Keycloak-authenticated HTTP (GSP), an outbox table. Dev-only deployment.

## Must-have (v1 blockers)

1. **Prod deployment of `graph-server`.** Dev-only today (no prod overlay under
   `deploy/overlays/prod/`). The KG cannot be authoritative for the platform on a dev service.
2. **A working query/read path.** The SoR is the quad store, but SPARQL reads route through
   **Oxigraph, which is not deployed**, and there is no outbox→Oxigraph materializer. Overview
   and every drift query need graph-pattern querying.
   → **Recommended: deploy Oxigraph + the outbox materializer** (a real SPARQL endpoint). The
   GSP-`GET`-per-graph-and-query-in-Worklode fallback cannot do graph patterns at scale.
3. **A stable, documented IRI scheme** for Worklode entities, aligned with rdf-registry ADR-0006
   (branch-free term IRIs; `/id/…` for instances). Worklode mints IRIs for `Component`,
   `DesignDoc`, `Task`; the host/namespace grammar must be fixed and agreed. (Canonical scheme is
   authored in Worklode **spec 03**; this item is the data-platform-side commitment to host it.)
   **Base = `https://worklode.io/ns/`** (decided): the `ls:` ontology stays in rdf-registry but its
   pipeline **publishes under the `worklode.io/ns/` base**, not `sunstone.institute/rdf/`. This needs
   a **base-URL override** for the `ls` ontology in rdf-registry (ADR-0006's implicit "repo path =
   host path" mapping doesn't hold for a foreign domain) — a required rdf-registry change.
4. **External-service write auth confirmed.** Worklode's projector is a Go service authenticating
   via Keycloak client-credentials (`dataplatform-svc`) and `PUT`-ing named graphs. The atomic
   per-branch write exists; verify the client-credentials path works end-to-end for an external caller.
5. **A writable, fixed branch** for the work graph (project = property, not branch — sibling
   branches are invisible to each other, which would hide cross-project edges). Branch-create +
   overlay-read are built; confirm committing to a fixed `main`-equivalent branch.

## Skills embedding store (from spec 07; blocks 07's suggestion path, not specs 01–05)

- **Self-hosted embedding service + Lance vector store.** Spec 07's skill suggestions need
  two calls from the Worklode backbone: `embed(text) → vector` (at skill ingest and once per
  suggestion query) and a top-k cosine search over the skill index. Vectors are stored as
  **Lance files** on the data-platform; the Worklode catalog stays the source rows, so the
  index is rebuildable at any time by re-embedding. Scale is small (10²–10³ vectors, a
  handful of embeds per day) — a modest open embedding model behind an internal
  Keycloak-authenticated endpoint suffices. No external embedding vendor.

## Should-have (soon; not v1 blockers)

6. **`If-Match` / ETag CAS on GSP writes** (their spec's v1.1). With a *single* Worklode projector
   plus the per-branch lock, lost-update risk is already contained, so this is non-blocking — but
   wanted before any second writer touches the work graph.
7. **Per-branch / per-namespace write ACLs** (their first future-enforcement candidate). Lets
   Worklode's writes be access-scoped from other data-platform writers. Fine to defer for v1.

## Explicitly NOT required from the data-platform (Worklode owns these)

- The **lease/claim/job primitive** (`graph.job` + `SKIP LOCKED`) — stays on the Worklode backbone.
- **Branch merge/diff** — design-review branches can defer merge to CI-side conflict detection.
- **Markdown-as-asset** — design content stays as files in the designs repo; only RDF *descriptors*
  (IRI, status, `governs`/`requires` edges) live in `graph-server`.

## Acceptance

Worklode's projector can, against prod `graph-server`: authenticate, `PUT` a Worklode named graph
to the fixed branch under the agreed IRI scheme, and read it back via a SPARQL query that answers
a drift question (e.g. "components with no governing DesignDoc").
