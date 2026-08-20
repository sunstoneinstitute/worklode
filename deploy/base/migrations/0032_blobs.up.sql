-- Content-addressed blobs for task bodies and attachments (spec 021).
--
-- Bytes live in S3-compatible object storage at blobs/<hash[0:2]>/<hash>;
-- this table is the index, not the payload. There is deliberately no key
-- column: the key is a pure function of the hash, and storing it would
-- create a second source of truth that can disagree with the content
-- address.
CREATE TABLE blobs (
    hash       text PRIMARY KEY,
    media_type text NOT NULL,
    size       bigint NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT blobs_hash_format CHECK (hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT blobs_size_positive CHECK (size >= 0)
);

-- The reference graph. A blobs row with no row here is garbage; GC is
-- exactly that query (spec 021 section 11). When spec 014 adds
-- section_blobs, the GC predicate grows a second NOT EXISTS clause -- that
-- is the one place a new reference table has to touch.
--
-- embedded is DERIVED: reconciled from the parsed task body on every write,
-- so removing an image from the body stops keeping its bytes alive.
-- attached is DECLARED: set by `lode task attach`, and survives body edits
-- because it was never in the body.
--
-- ON DELETE RESTRICT on hash is the interlock that makes GC safe: the
-- database refuses to drop a blob anything still references, so a GC bug
-- errors instead of breaking an image.
CREATE TABLE task_blobs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hash       text NOT NULL REFERENCES blobs(hash) ON DELETE RESTRICT,
    filename   text NOT NULL,
    embedded   boolean NOT NULL DEFAULT false,
    attached   boolean NOT NULL DEFAULT false,
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, hash),
    CONSTRAINT task_blobs_referenced CHECK (embedded OR attached)
);

-- Supports the GC predicate, which probes by hash across all tasks.
CREATE INDEX task_blobs_hash_idx ON task_blobs (hash);
