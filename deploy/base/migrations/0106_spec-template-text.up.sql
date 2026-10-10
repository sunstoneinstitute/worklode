-- Specs become templates (WL-SPEC-77 §19.7, §19.1). Two steps:
--
-- 1. Every arranged rule whose text was always empty becomes a spec heading
--    at the same position, anchor and depth. Its covers edges and governedBy
--    links move to the requirements and catalogues grouped under the heading
--    (the resolution a <doc>#sec-N covers entry gets); a covers edge with no
--    such rule becomes an unresolved <spec>#<anchor> ref. The rule and its
--    versions are deleted. rule_heading_conversions keeps one row per
--    converted arrangement for `lode rule lint`.
-- 2. Rule text leaves docs.body: each anchored section a rule fills keeps its
--    heading line as the placeholder and loses its body. Text before the
--    first heading and every unanchored section stay as template text.
--    spec_template mirrors designdoc.Parse's heading scan; store.specTemplate
--    is the Go side of the same cut.
BEGIN;

CREATE TABLE rule_heading_conversions (
    project_id     text        NOT NULL,
    rule_ref       text        NOT NULL,
    doc_id         bigint      NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    anchor         text        NOT NULL,
    heading        text        NOT NULL,
    rule_status    text        NOT NULL,
    covering_plans integer     NOT NULL,
    governed_tasks integer     NOT NULL,
    grouped_rules  integer     NOT NULL,
    converted_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rule_heading_conversions_project ON rule_heading_conversions (project_id);

CREATE TEMP TABLE shells ON COMMIT DROP AS
SELECT r.id, r.project_id, r.status,
       p.key || CASE WHEN r.kind IN ('requirement', 'catalogue') THEN '-REQ-' ELSE '-RULE-' END || r.number AS ref
  FROM rules r JOIN projects p ON p.id = r.project_id
 WHERE NOT EXISTS (SELECT 1 FROM rule_versions v WHERE v.rule_id = r.id AND btrim(v.body, E' \t\r\n') <> '')
   AND EXISTS (SELECT 1 FROM doc_rules a JOIN docs d ON d.id = a.doc_id WHERE a.rule_id = r.id AND d.kind = 'spec');

-- The covered rules each shell groups in any spec arranging it: later
-- entries until the next one at the shell's depth or shallower.
CREATE TEMP TABLE shell_rules ON COMMIT DROP AS
SELECT DISTINCT s.id AS shell, g.rule_id AS rule
  FROM shells s
  JOIN doc_rules a ON a.rule_id = s.id
  JOIN doc_rules g ON g.doc_id = a.doc_id AND g.position > a.position AND g.rule_id IS NOT NULL
  JOIN rules r ON r.id = g.rule_id AND r.kind IN ('requirement', 'catalogue')
 WHERE g.rule_id NOT IN (SELECT id FROM shells)
   AND NOT EXISTS (SELECT 1 FROM doc_rules n
                    WHERE n.doc_id = a.doc_id AND n.depth <= a.depth
                      AND n.position > a.position AND n.position <= g.position);

-- Where a covers edge to a shell with nothing under it points instead.
CREATE TEMP TABLE shell_home ON COMMIT DROP AS
SELECT DISTINCT ON (s.id) s.id AS shell, p.key || '-SPEC-' || d.number || '#' || a.anchor AS ref
  FROM shells s
  JOIN doc_rules a ON a.rule_id = s.id
  JOIN docs d ON d.id = a.doc_id AND d.kind = 'spec'
  JOIN projects p ON p.id = d.project_id
 ORDER BY s.id, d.deleted_at IS NOT NULL, d.id;

INSERT INTO rule_heading_conversions
    (project_id, rule_ref, doc_id, anchor, heading, rule_status, covering_plans, governed_tasks, grouped_rules)
SELECT s.project_id, s.ref, a.doc_id, a.anchor, v.heading, s.status,
       (SELECT count(DISTINCT e.from_doc) FROM doc_edges e WHERE e.type = 'covers' AND e.to_rule = s.id),
       (SELECT count(*) FROM task_governed_by t WHERE t.rule_id = s.id),
       (SELECT count(*) FROM shell_rules x WHERE x.shell = s.id)
  FROM shells s
  JOIN doc_rules a ON a.rule_id = s.id
  JOIN rule_versions v ON v.rule_id = a.rule_id AND v.version = a.rule_version;

-- Covers edges: live, candidate revision and versioned.
INSERT INTO doc_edges (from_doc, from_anchor, type, to_rule)
SELECT DISTINCT e.from_doc, e.from_anchor, 'covers', x.rule
  FROM doc_edges e JOIN shell_rules x ON x.shell = e.to_rule
 WHERE e.type = 'covers'
ON CONFLICT DO NOTHING;
INSERT INTO doc_edges (from_doc, from_anchor, type, to_external)
SELECT DISTINCT e.from_doc, e.from_anchor, 'covers', h.ref
  FROM doc_edges e JOIN shell_home h ON h.shell = e.to_rule
 WHERE e.type = 'covers' AND NOT EXISTS (SELECT 1 FROM shell_rules x WHERE x.shell = e.to_rule)
ON CONFLICT DO NOTHING;
DELETE FROM doc_edges WHERE to_rule IN (SELECT id FROM shells);

INSERT INTO doc_revision_edges (doc_id, from_anchor, type, to_rule)
SELECT DISTINCT e.doc_id, e.from_anchor, 'covers', x.rule
  FROM doc_revision_edges e JOIN shell_rules x ON x.shell = e.to_rule
 WHERE e.type = 'covers'
ON CONFLICT DO NOTHING;
INSERT INTO doc_revision_edges (doc_id, from_anchor, type, to_external)
SELECT DISTINCT e.doc_id, e.from_anchor, 'covers', h.ref
  FROM doc_revision_edges e JOIN shell_home h ON h.shell = e.to_rule
 WHERE e.type = 'covers' AND NOT EXISTS (SELECT 1 FROM shell_rules x WHERE x.shell = e.to_rule)
ON CONFLICT DO NOTHING;
DELETE FROM doc_revision_edges WHERE to_rule IN (SELECT id FROM shells);

INSERT INTO doc_edge_versions (doc_id, version, from_anchor, type, to_rule)
SELECT DISTINCT e.doc_id, e.version, e.from_anchor, 'covers', x.rule
  FROM doc_edge_versions e JOIN shell_rules x ON x.shell = e.to_rule
 WHERE e.type = 'covers'
   AND NOT EXISTS (SELECT 1 FROM doc_edge_versions o
                    WHERE o.doc_id = e.doc_id AND o.version = e.version AND o.type = 'covers'
                      AND o.from_anchor IS NOT DISTINCT FROM e.from_anchor AND o.to_rule = x.rule);
INSERT INTO doc_edge_versions (doc_id, version, from_anchor, type, to_external)
SELECT DISTINCT e.doc_id, e.version, e.from_anchor, 'covers', h.ref
  FROM doc_edge_versions e JOIN shell_home h ON h.shell = e.to_rule
 WHERE e.type = 'covers' AND NOT EXISTS (SELECT 1 FROM shell_rules x WHERE x.shell = e.to_rule);
DELETE FROM doc_edge_versions WHERE to_rule IN (SELECT id FROM shells);

-- governedBy links move to the grouped rules at their current versions.
INSERT INTO task_governed_by (task_id, rule_id, rule_version, source, pinned_version, created_at)
SELECT DISTINCT ON (t.task_id, x.rule) t.task_id, x.rule, r.version, t.source,
       CASE WHEN t.pinned_version IS NOT NULL THEN r.version END, t.created_at
  FROM task_governed_by t
  JOIN shell_rules x ON x.shell = t.rule_id
  JOIN rules r ON r.id = x.rule
 ORDER BY t.task_id, x.rule, t.created_at
ON CONFLICT (task_id, rule_id) DO NOTHING;
DELETE FROM task_governed_by WHERE rule_id IN (SELECT id FROM shells);

-- Arrangements, live, candidate and versioned, hold a heading instead.
UPDATE doc_rules a SET heading = v.heading, rule_id = NULL, rule_version = NULL
  FROM rule_versions v
 WHERE a.rule_id IN (SELECT id FROM shells) AND v.rule_id = a.rule_id AND v.version = a.rule_version;
UPDATE doc_revision_rules a SET heading = v.heading, rule_id = NULL, rule_version = NULL
  FROM rule_versions v
 WHERE a.rule_id IN (SELECT id FROM shells) AND v.rule_id = a.rule_id AND v.version = a.rule_version;
UPDATE doc_rule_versions a SET heading = v.heading, rule_id = NULL, rule_version = NULL
  FROM rule_versions v
 WHERE a.rule_id IN (SELECT id FROM shells) AND v.rule_id = a.rule_id AND v.version = a.rule_version;

DELETE FROM rules WHERE id IN (SELECT id FROM shells);

-- spec_template is body with the text of each section anchored at one of
-- rule_anchors removed, its heading line kept. A heading is an ATX line of
-- two to six hashes and a space or tab outside a fenced block; a section's
-- text runs to the next heading of any level.
CREATE FUNCTION spec_template(body text, rule_anchors text[]) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    lines    text[] := string_to_array(body, E'\n');
    n        integer := array_length(lines, 1);
    out      text := '';
    piece    text;
    line     text;
    stripped text;
    fence    text := '';
    current  text;
    is_head  boolean;
BEGIN
    IF n IS NULL THEN
        RETURN body;
    END IF;
    FOR i IN 1..n LOOP
        piece := lines[i] || CASE WHEN i < n THEN E'\n' ELSE '' END;
        line := rtrim(lines[i], E'\r');
        stripped := ltrim(line, E' \t');
        is_head := false;
        IF fence <> '' THEN
            IF starts_with(stripped, fence) THEN
                fence := '';
            END IF;
        ELSIF starts_with(stripped, '```') OR starts_with(stripped, '~~~') THEN
            fence := left(stripped, 3);
        ELSIF line ~ '^#{2,6}[ \t]' THEN
            is_head := true;
            current := coalesce(substring(line FROM '\{#([0-9A-Za-z_.-]+)\}[ \t]*$'), '');
        END IF;
        IF is_head OR current IS NULL OR NOT (current = ANY (rule_anchors)) THEN
            out := out || piece;
        END IF;
    END LOOP;
    RETURN out;
END
$$;

UPDATE docs d SET body = spec_template(d.body, a.anchors)
  FROM (SELECT doc_id, array_agg(anchor) AS anchors FROM doc_rules WHERE rule_id IS NOT NULL GROUP BY doc_id) a
 WHERE a.doc_id = d.id AND d.kind = 'spec' AND spec_template(d.body, a.anchors) <> d.body;

DROP FUNCTION spec_template(text, text[]);

COMMIT;
