
No fact has two owners. The backbone (Worklode's Postgres) owns execution facts: task state, leases, `blocks`, `child_of`, events, the authored design documents (WL-SPEC-77), artifacts, deployments and commits. The graph owns declared architecture facts: Component, `governs`, `reviewer`, `requires`, `replaces`, Deliverable and its acceptance criteria. Components are declared per repository in `.worklode/components.yaml`; nothing in Worklode writes `governs` or `reviewer`. Task is the bridge: backbone-authoritative, mirrored into the graph. Design documents are authored in the backbone and projected into per-document `declared/` graphs.

The corpus index moves no ownership. It is derived state, rebuildable from backbone rows at any time. Dropping every chunk costs compute and no information. The queryable graph view of documents belongs to the data platform. The index is a retrieval aid over the backbone's own copy.

