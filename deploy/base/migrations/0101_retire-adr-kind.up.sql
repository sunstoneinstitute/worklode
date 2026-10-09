-- Retire the adr document kind (WL-966, decision WL-1005, WL-SPEC-77 §2).
--
-- Every live ADR becomes a spec with the next free spec number in its
-- project. The row keeps its id, so its sections, rules, versions, notes,
-- reviewers, edges and tasks follow it unchanged. former_adr keeps the old
-- number, so a <KEY>-ADR-<n> reference still resolves to the successor
-- (WL-SPEC-77 §7a), and the body's first line names the former ref so search
-- finds it.
--
-- Tombstoned ADRs are hard-deleted so the kind CHECK can drop 'adr'. Their
-- cascading dependents go with them. A reference into one from another
-- document (doc_edges, doc_revision_edges, doc_edge_versions) becomes an
-- unresolved to_external/owner_external ref, and a task pointing at one loses
-- the pointer.

ALTER TABLE docs ADD COLUMN former_adr integer;
CREATE UNIQUE INDEX docs_project_former_adr
    ON docs (project_id, former_adr) WHERE former_adr IS NOT NULL;

CREATE TEMP TABLE gone_adrs AS
SELECT d.id, p.key || '-ADR-' || d.number AS ref
  FROM docs d JOIN projects p ON p.id = d.project_id
 WHERE d.kind = 'adr' AND d.deleted_at IS NOT NULL;

UPDATE doc_edges e
   SET to_external = g.ref || coalesce('#' || e.to_anchor, ''), to_doc = NULL, to_anchor = NULL
  FROM gone_adrs g WHERE e.to_doc = g.id;
UPDATE doc_edges e SET owner_external = g.ref, owner_doc = NULL
  FROM gone_adrs g WHERE e.owner_doc = g.id;
UPDATE doc_revision_edges e
   SET to_external = g.ref || coalesce('#' || e.to_anchor, ''), to_doc = NULL, to_anchor = NULL
  FROM gone_adrs g WHERE e.to_doc = g.id;
UPDATE doc_revision_edges e SET owner_external = g.ref, owner_doc = NULL
  FROM gone_adrs g WHERE e.owner_doc = g.id;
UPDATE doc_edge_versions e
   SET to_external = g.ref || coalesce('#' || e.to_anchor, ''), to_doc = NULL, to_anchor = NULL
  FROM gone_adrs g WHERE e.to_doc = g.id;
UPDATE doc_edge_versions e SET owner_external = g.ref, owner_doc = NULL
  FROM gone_adrs g WHERE e.owner_doc = g.id;
UPDATE tasks SET plan_doc = NULL WHERE plan_doc IN (SELECT id FROM gone_adrs);
UPDATE tasks SET about_doc = NULL WHERE about_doc IN (SELECT id FROM gone_adrs);
DELETE FROM docs WHERE id IN (SELECT id FROM gone_adrs);
DROP TABLE gone_adrs;

-- New numbers start past both the SPEC counter and every spec number ever
-- used, tombstones included: a retired number is never given out again.
WITH first AS (
    SELECT a.project_id,
           greatest(coalesce(max(s.next), 1),
                    coalesce((SELECT max(x.number) + 1 FROM docs x
                               WHERE x.project_id = a.project_id AND x.kind = 'spec'), 1)) AS n
      FROM (SELECT DISTINCT project_id FROM docs WHERE kind = 'adr') a
      LEFT JOIN project_entity_seq s ON s.project_id = a.project_id AND s.kind = 'SPEC'
     GROUP BY a.project_id
), numbered AS (
    SELECT d.id, f.n + row_number() OVER (PARTITION BY d.project_id ORDER BY d.number) - 1 AS n
      FROM docs d JOIN first f ON f.project_id = d.project_id
     WHERE d.kind = 'adr'
)
UPDATE docs d
   SET kind = 'spec', former_adr = d.number, number = numbered.n,
       body = 'Was ' || p.key || '-ADR-' || d.number || E'.\n\n' || d.body
  FROM numbered, projects p
 WHERE numbered.id = d.id AND p.id = d.project_id;

INSERT INTO project_entity_seq (project_id, kind, next)
SELECT project_id, 'SPEC', max(number) + 1
  FROM docs WHERE former_adr IS NOT NULL
 GROUP BY project_id
ON CONFLICT (project_id, kind) DO UPDATE SET next = greatest(project_entity_seq.next, EXCLUDED.next);

DELETE FROM project_entity_seq WHERE kind = 'ADR';

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'MILE', 'RULE'));

ALTER TABLE docs DROP CONSTRAINT docs_kind_check;
ALTER TABLE docs ADD CONSTRAINT docs_kind_check CHECK (kind IN ('spec', 'plan'));
