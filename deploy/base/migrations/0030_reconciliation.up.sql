-- Reconciliation (docs/specs/013-reconciliation.md): events.applied_at marks a
-- completed apply; project_repos.mapped_at lets project doctor spot deliveries
-- that predate the mapping.

ALTER TABLE events ADD COLUMN applied_at timestamptz;

-- Pre-existing non-ignored events were applied live by the webhook path.
-- .ignored events stay NULL: they are exactly the replay candidates.
UPDATE events SET applied_at = received_at WHERE type NOT LIKE '%.ignored';

CREATE INDEX events_unapplied ON events (id) WHERE applied_at IS NULL;

ALTER TABLE project_repos ADD COLUMN mapped_at timestamptz NOT NULL DEFAULT now();

-- Existing mappings predate the column; epoch keeps them from retroactively
-- flagging every old delivery as pre-mapping.
UPDATE project_repos SET mapped_at = to_timestamp(0);
