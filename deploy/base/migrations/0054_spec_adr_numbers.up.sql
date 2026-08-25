-- Specs and ADRs join the per-project ordinal counters (029 §4), the wider
-- cutover 0053's comment named as separate work. A spec or ADR may still take
-- an explicit number -- reserving one, or a corpus import preserving the
-- number already in a filename -- but the default is now the same
-- project_entity_seq allocation plans and deliverables already use, so
-- `lode doc new --kind spec` no longer requires the caller to pick one.
ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR'));

-- Seed each project's SPEC/ADR counter past whatever number is already in
-- use, the same backfill 0053 ran for PLAN, so the first auto-assigned number
-- never collides with an existing document.
INSERT INTO project_entity_seq (project_id, kind, next)
SELECT project_id, 'SPEC', max(number) + 1
  FROM docs WHERE kind = 'spec'
 GROUP BY project_id;

INSERT INTO project_entity_seq (project_id, kind, next)
SELECT project_id, 'ADR', max(number) + 1
  FROM docs WHERE kind = 'adr'
 GROUP BY project_id;
