-- Flux OCI revisions correlate by digest, which is otherwise an unindexed
-- scan of every artifact on every reconciliation event.
CREATE INDEX artifacts_digest_idx ON artifacts (digest) WHERE digest IS NOT NULL;
