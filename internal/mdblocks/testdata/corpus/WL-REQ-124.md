
One `approvals` table serves every approval (deliverables, documents, dossiers, PRs, tasks), keyed to `(entity_kind, entity_id, subject_revision)` and carrying the required role (a Keycloak group name) or named actor, the resolving actor, timestamps and state:

```
awaiting → changes_requested → awaiting (on re-request)
awaiting → approved | rejected      -- final by convention
```

A requirement is materialized as an `awaiting` row when the entity is created, so a missing approval is a visible row and "what is waiting on whom" is a query. `subject_revision` is the immutable revision the approver saw: a document revision, an analysis commit, a PR head, a deliverable evidence revision. A material change reopens the changed target only; dependent objects get an explicit impact review (downstream owner supplies an impact note, a qualified prior approver confirms or reopens). Rule-created rows are owned by the system `worklode` actor.

