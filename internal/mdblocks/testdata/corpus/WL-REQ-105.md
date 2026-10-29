
`RecordEvent(source, externalID, type, payload, apply)` is the sole write entry point. It inserts the event with `ON CONFLICT (source, external_id) DO NOTHING`; on first sight it calls `apply(tx, eventID)` in the same transaction; on a repeat it returns the existing id with `inserted=false` and skips `apply`. Every typed-table mutation (task transition, lease insert or close, edge change, fact-table write) is an `apply` callback, so the event and the change commit or roll back together, and every change carries provenance through `state_log.event_id`. An event that could commit without its change, or a change without its event, does not exist.

`external_id` is deterministic wherever possible: domain events use `<type>:<subject>:<version>`, sweeps use `lease-expired-<leaseID>`, subscriber actions use `<subscriber>:<rule>:<event-id>`. Events with no natural key mint a random hex id. A retried client request therefore gets the existing event rather than a duplicate.

The event's `@id` is `wlid:event/<id>`, so the id is reserved before the insert (`nextval` on the identity sequence, `OVERRIDING SYSTEM VALUE`). A minted task id is the exception: `task.created` and `issue.promoted` allocate it from the project counter inside `apply` and set `task` on the just-inserted row in the same transaction, which no reader can observe half-done because readers stay below the commit horizon.

