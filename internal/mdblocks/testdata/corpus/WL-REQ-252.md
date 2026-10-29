
The knowledge graph lives in the data-platform `graph-server` (Postgres RDF quad store with Oxigraph and an outbox materializer behind `/sparql`). The execution backbone stays in Worklode's Postgres.

Worklode needs from `graph-server`:

| Requirement | Need |
|---|---|
| `graph-server` in every environment Worklode runs in | must |
| SPARQL read path | must |
| External write auth via Keycloak client credentials | must |
| Fixed writable `main` branch; project is a property, never a branch | must |
| `If-Match` / ETag CAS on GSP writes | before a second writer exists |
| Per-branch write ACLs | should |

Not required from the data platform: the lease and claim primitive, branch merge and diff, and Markdown as an asset. Only RDF descriptors live in `graph-server`.

