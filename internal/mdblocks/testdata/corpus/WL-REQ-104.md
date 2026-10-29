
```sql
CREATE TABLE events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source      text NOT NULL,           -- open set, see below
    external_id text NOT NULL,
    type        text NOT NULL,
    payload     jsonb,
    received_at timestamptz NOT NULL,
    txid        xid8 NOT NULL DEFAULT pg_current_xact_id(),
    UNIQUE (source, external_id)
);
CREATE INDEX events_txid_id ON events (txid, id);

CREATE TABLE state_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind text NOT NULL,
    entity_id   text NOT NULL,
    change      jsonb NOT NULL,
    event_id    bigint NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    at          timestamptz NOT NULL,
    txid        xid8 NOT NULL DEFAULT pg_current_xact_id()
);

CREATE TABLE event_subscribers (
    name              text PRIMARY KEY,
    last_read_offset  bigint NOT NULL DEFAULT 0,
    last_acked_offset bigint NOT NULL DEFAULT 0,
    updated_at        timestamptz NOT NULL,
    CHECK (last_acked_offset <= last_read_offset)
);
```

Events are never deleted or compacted. `events.source` is an open set with no CHECK: `github`, `flux`, `watcher`, `cli`, `web`, `system`, and one value per ingest system (CMS, CI publish, pipeline). `events.type` has no CHECK because webhook deliveries keep their vendor dotted types (`push`, `issues.opened`) in the same table as domain events; the domain-event set is enforced in Go at emit time.

