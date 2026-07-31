# Spec 009 — Data-platform requirements for the Worklode KG

**Status:** spec · **Owner hand-off:** data-platform team · **Umbrella:** `000-umbrella-architecture.md`

**Amended by:** 014

Worklode's **knowledge graph** (the declared architecture graph + the projected work graph)
lives in the data-platform `graph-server` (Postgres RDF quad store). The **execution backbone**
(tasks, leases, events) stays in Worklode's own Postgres — so the data-platform only has to host
the *knowledge* half. This spec is the minimum the data-platform must ship for that.

## Context (verified against data-platform `graph-server`, 2026-07)

Built and deployed in **dev**: named-graph writes with a genuine single-writer serialization point
(`SELECT … FOR UPDATE` per-branch + one ACID Postgres txn), O(1) copy-on-write branch create,
child-wins overlay reads, Keycloak-authenticated HTTP (GSP), the outbox table, and **Oxigraph plus
the outbox→Oxigraph materializer** behind a real `/sparql` endpoint. The full projector path —
client-credentials token → `PUT` named graph to `main` → GSP read-back → drift query over SPARQL —
is proven end-to-end in dev by data-platform's runbook
`docs/runbooks/2026-07-22-worklode-projector-acceptance.md`.

Still open: **prod** (no graph-server manifests under `deploy/overlays/prod/`; the prod-deploy plan
is deferred pending the Hetzner prod cluster) and the rdf-registry base-URL override.

## Must-have (v1 blockers)

1. **Prod deployment of `graph-server`** — **open**. Dev-only today; no graph-server manifests in
   `deploy/overlays/prod/`, and data-platform's prod-deploy plan is deferred until the Hetzner prod
   cluster exists. The KG cannot be authoritative for the platform on a dev service. Items 2, 4 and
   5 are proven in dev and ride on this one to reach prod.
2. **A working query/read path** — **done in dev**. Oxigraph and the outbox→Oxigraph materializer
   are deployed (`deploy/overlays/dev/`), giving a real SPARQL endpoint; the drift query in the
   acceptance runbook returns over it. The GSP-`GET`-per-graph-and-query-in-Worklode fallback is
   dropped. Remaining work is the prod copy of these manifests (item 1).
3. **A stable, documented IRI scheme** for Worklode entities — **scheme agreed, override open**;
   aligned with rdf-registry ADR-0006
   (branch-free term IRIs; `/id/…` for instances). Worklode mints IRIs for `Component`,
   `DesignDoc`, `Task`; the host/namespace grammar must be fixed and agreed. (Canonical scheme is
   authored in Worklode **spec 006**; this item is the data-platform-side commitment to host it.)
   **Base = `https://worklode.io/ns/`** (decided): the `ls:` ontology stays in rdf-registry but its
   pipeline **publishes under the `worklode.io/ns/` base**, not `sunstone.institute/rdf/`. This needs
   a **base-URL override** for the `ls` ontology in rdf-registry (ADR-0006's implicit "repo path =
   host path" mapping doesn't hold for a foreign domain) — a required rdf-registry change.

   > **Amended by 014 §1.** The sources move to `rdf/wl/`, but the published base stays
   > `https://worklode.io/ns/` with no `wl/` segment; the base-URL override applies to the `wl`
   > ontology. The override is not yet implemented in rdf-registry.

4. **External-service write auth confirmed** — **done in dev**. Worklode's projector is a Go service
   authenticating via Keycloak client-credentials (`dataplatform-svc`) and `PUT`-ing named graphs.
   The acceptance runbook proves the client-credentials path end-to-end for an external caller; no
   graph-server-side config was needed, because the `dataplatform-dev:readwrite` client role travels
   under its owning client regardless of `azp`.
5. **A writable, fixed branch** for the work graph — **confirmed**. Project = property, not branch
   (sibling branches are invisible to each other, which would hide cross-project edges). The runbook
   commits to the fixed `main` branch and reads it back.

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
a drift question (e.g. "components with no governing DesignDoc"). This passes against **dev**
today (data-platform runbook `docs/runbooks/2026-07-22-worklode-projector-acceptance.md`); prod
remains blocked on item 1.
