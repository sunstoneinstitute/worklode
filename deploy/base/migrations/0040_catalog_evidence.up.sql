-- Data-catalog ingest (spec 029 §3.1, §3.2): a deliverable declares how it is
-- verified — by address — and its state is reported by emitters, never
-- asserted by a human closing a task. The catalog is the first such emitter,
-- so it becomes a fourth signed ingest source beside github, flux and watcher.
ALTER TABLE events DROP CONSTRAINT events_source_check;
ALTER TABLE events ADD CONSTRAINT events_source_check
    CHECK (source IN ('github','flux','watcher','cli','system','web','catalog'));

-- Who declared which artifact address. This is the routing table for the
-- ingest: a delivery names an artifact, and the evidence lands against every
-- open entity that declared it. Polymorphic over the three kinds that can
-- declare one; entity_id carries no FK for the same reason approvals.entity_id
-- (0038) carries none — one column cannot reference three tables.
CREATE TABLE artifact_declarations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind  text NOT NULL CHECK (entity_kind IN ('deliverable','task','doc')),
    entity_id    text NOT NULL,
    artifact_uri text NOT NULL,
    created_at   timestamptz NOT NULL,
    UNIQUE (entity_kind, entity_id, artifact_uri)
);

-- Covers the ingest's hot read: every declaration of one artifact address.
CREATE INDEX artifact_declarations_uri_idx ON artifact_declarations (artifact_uri);

-- One reported fact about a declared artifact. Not artifacts (docker images
-- and release assets, correlated by digest and source sha) and not
-- task_commits: this is "an external system asserted a state at a time, with
-- this event as provenance". event_id is ON DELETE RESTRICT because the event
-- is that provenance — evidence must not outlive it. The UNIQUE key makes a
-- replayed delivery a no-op per entity and artifact: artifact_uri is in the key
-- because one delivery reports about one address today, but a future batched
-- payload naming several must file a row per address rather than silently
-- dropping all but the first.
CREATE TABLE artifact_evidence (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind  text NOT NULL CHECK (entity_kind IN ('deliverable','task','doc')),
    entity_id    text NOT NULL,
    artifact_uri text NOT NULL,
    source       text NOT NULL,
    state        text NOT NULL CHECK
        (state IN ('published','updated','deprecated','removed','failed')),
    provenance   text NOT NULL CHECK (provenance IN ('observed','user_reported')),
    version      text NOT NULL DEFAULT '',
    url          text NOT NULL DEFAULT '',
    detail       jsonb,
    event_id     bigint NOT NULL REFERENCES events (id) ON DELETE RESTRICT,
    occurred_at  timestamptz NOT NULL,
    UNIQUE (entity_kind, entity_id, artifact_uri, event_id)
);

-- Covers the read every projection wants: the latest evidence for one entity.
CREATE INDEX artifact_evidence_entity_idx
    ON artifact_evidence (entity_kind, entity_id, occurred_at DESC, id DESC);
