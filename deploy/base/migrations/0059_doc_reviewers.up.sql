-- WL-359: spec 025 §7.3's durable reviewer set had no storage. The
-- per-revision decision state (awaiting/approved/...) already lives in
-- `approvals` (0057); this is the assignment itself, independent of any one
-- revision — the fact §8.2's in-place amendment needs, since a `review`
-- task minted for a patched section names "the original approvers".
CREATE TABLE doc_reviewers (
    doc_id      bigint NOT NULL REFERENCES docs (id) ON DELETE CASCADE,
    actor_id    text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    assigned_at timestamptz NOT NULL,
    PRIMARY KEY (doc_id, actor_id)
);
