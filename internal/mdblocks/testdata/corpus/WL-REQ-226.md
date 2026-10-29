
Bytes live in Hetzner Object Storage (S3-compatible, path-style). Postgres holds the index and reference graph. The object key is a pure function of the hash, sharded two hex characters deep: `blobs/<hash[0:2]>/<hash>`.

```sql
CREATE TABLE blobs (
    hash       text PRIMARY KEY,   -- sha256, lowercase hex
    media_type text NOT NULL,      -- server-sniffed
    size       bigint NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE TABLE task_blobs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hash       text NOT NULL REFERENCES blobs(hash) ON DELETE RESTRICT,
    filename   text NOT NULL,
    embedded   boolean NOT NULL DEFAULT false,  -- derived: the body cites /blob/<hash>
    attached   boolean NOT NULL DEFAULT false,  -- declared: lode task attach
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, hash),
    CONSTRAINT task_blobs_referenced CHECK (embedded OR attached)
);
CREATE INDEX task_blobs_hash_idx ON task_blobs (hash);
```

`embedded` is recomputed on every task create and update from the body's `/blob/<hash>` references, in the same transaction. `attached` is set by `lode task attach` and cleared by `lode task detach`, and survives body edits. A row with neither flag is deleted by reconciliation. `ON DELETE RESTRICT` makes a GC bug an error instead of a broken image.

