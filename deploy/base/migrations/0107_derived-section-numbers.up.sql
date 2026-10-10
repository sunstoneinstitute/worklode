-- Section numbers and anchors are derived from an arrangement's positions
-- and depths (WL-REQ-165). An entry stores a slug only when it is unnumbered
-- (sec-open-questions); doc_rules.anchor and doc_rule_versions.anchor are no
-- longer written and are dropped one release later. doc_sections, the current
-- version's section index, carries each entry's derived anchor at the same
-- position, and doc_entries reads the two together.
--
-- Every existing spec and ADR is renumbered here: doc_sections and the
-- locators that name its anchors (notes, edges, candidate edges, task
-- escalations) move to the derived anchor of the same entry.
-- derived_anchors mirrors designdoc.DeriveNumbers.
BEGIN;

ALTER TABLE doc_rules ADD COLUMN slug text;
ALTER TABLE doc_rule_versions ADD COLUMN slug text;
ALTER TABLE doc_revision_rules ADD COLUMN slug text;
UPDATE doc_rules SET slug = anchor WHERE anchor !~ '^sec-[0-9]+(\.[0-9]+)*[a-z]?$';
UPDATE doc_rule_versions SET slug = anchor WHERE anchor !~ '^sec-[0-9]+(\.[0-9]+)*[a-z]?$';
UPDATE doc_revision_rules SET slug = anchor WHERE anchor !~ '^sec-[0-9]+(\.[0-9]+)*[a-z]?$';
ALTER TABLE doc_rules ALTER COLUMN anchor DROP NOT NULL;
ALTER TABLE doc_rule_versions ALTER COLUMN anchor DROP NOT NULL;
ALTER TABLE doc_revision_rules ALTER COLUMN anchor DROP NOT NULL;

CREATE FUNCTION derived_anchors(depths integer[], slugs text[])
RETURNS TABLE (ord integer, num text, anchor text)
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    sd    integer[] := '{}';
    sn    text[]    := '{}';
    sa    text[]    := '{}';
    sc    integer[] := '{}';
    roots integer   := 0;
    top   integer;
BEGIN
    FOR i IN 1..coalesce(array_length(depths, 1), 0) LOOP
        top := coalesce(array_length(sd, 1), 0);
        WHILE top > 0 AND sd[top] >= depths[i] LOOP
            sd := sd[1:top-1]; sn := sn[1:top-1]; sa := sa[1:top-1]; sc := sc[1:top-1];
            top := top - 1;
        END LOOP;
        ord := i;
        IF slugs[i] IS NOT NULL THEN
            num := ''; anchor := slugs[i];
        ELSIF top = 0 THEN
            roots := roots + 1; num := roots::text; anchor := 'sec-' || num;
        ELSE
            sc[top] := sc[top] + 1;
            IF sn[top] <> '' THEN
                num := sn[top] || '.' || sc[top]; anchor := 'sec-' || num;
            ELSE
                num := ''; anchor := sa[top] || '.' || sc[top];
            END IF;
        END IF;
        sd := sd || depths[i]; sn := sn || num; sa := sa || anchor; sc := sc || 0;
        RETURN NEXT;
    END LOOP;
END
$$;

CREATE TEMP TABLE renumbered ON COMMIT DROP AS
SELECT g.doc_id, g.positions[x.ord] AS position, g.anchors[x.ord] AS old_anchor,
       x.num, x.anchor AS new_anchor
  FROM (SELECT doc_id,
               array_agg(depth ORDER BY position) AS depths,
               array_agg(slug ORDER BY position) AS slugs,
               array_agg(position ORDER BY position) AS positions,
               array_agg(anchor ORDER BY position) AS anchors
          FROM doc_rules GROUP BY doc_id) g
 CROSS JOIN LATERAL derived_anchors(g.depths, g.slugs) x;

DROP FUNCTION derived_anchors(integer[], text[]);

-- A section row no entry holds is not part of the arrangement.
DELETE FROM doc_sections s
 WHERE s.doc_id IN (SELECT doc_id FROM renumbered)
   AND NOT EXISTS (SELECT 1 FROM renumbered r WHERE r.doc_id = s.doc_id AND r.old_anchor = s.anchor);

-- Each move goes through a prefixed value first so a permutation of anchors
-- never collides with a unique index halfway through.
UPDATE doc_sections s SET anchor = 'renumber:' || r.new_anchor, number = nullif(r.num, ''), position = r.position
  FROM renumbered r WHERE r.doc_id = s.doc_id AND r.old_anchor = s.anchor;
UPDATE doc_sections SET anchor = substr(anchor, 10) WHERE anchor LIKE 'renumber:%';

UPDATE doc_notes n SET anchor = 'renumber:' || r.new_anchor
  FROM renumbered r WHERE r.doc_id = n.doc_id AND r.old_anchor = n.anchor AND r.old_anchor <> r.new_anchor;
UPDATE doc_notes SET anchor = substr(anchor, 10) WHERE anchor LIKE 'renumber:%';

UPDATE doc_edges e SET to_anchor = 'renumber:' || r.new_anchor
  FROM renumbered r WHERE r.doc_id = e.to_doc AND r.old_anchor = e.to_anchor AND r.old_anchor <> r.new_anchor;
UPDATE doc_edges SET to_anchor = substr(to_anchor, 10) WHERE to_anchor LIKE 'renumber:%';
UPDATE doc_edges e SET from_anchor = 'renumber:' || r.new_anchor
  FROM renumbered r WHERE r.doc_id = e.from_doc AND r.old_anchor = e.from_anchor AND r.old_anchor <> r.new_anchor;
UPDATE doc_edges SET from_anchor = substr(from_anchor, 10) WHERE from_anchor LIKE 'renumber:%';

UPDATE doc_revision_edges e SET to_anchor = 'renumber:' || r.new_anchor
  FROM renumbered r WHERE r.doc_id = e.to_doc AND r.old_anchor = e.to_anchor AND r.old_anchor <> r.new_anchor;
UPDATE doc_revision_edges SET to_anchor = substr(to_anchor, 10) WHERE to_anchor LIKE 'renumber:%';

UPDATE tasks t SET about_anchor = r.new_anchor
  FROM renumbered r WHERE r.doc_id = t.about_doc AND r.old_anchor = t.about_anchor AND r.old_anchor <> r.new_anchor;

CREATE VIEW doc_entries AS
SELECT dr.doc_id, dr.position, dr.rule_id, dr.rule_version, dr.heading, dr.depth, dr.slug, s.anchor
  FROM doc_rules dr
  LEFT JOIN doc_sections s ON s.doc_id = dr.doc_id AND s.position = dr.position;

CREATE OR REPLACE VIEW covered_sections AS
SELECT c.edge_id, c.plan_id, c.rule_id, dr.doc_id, dr.anchor
  FROM covered_rules c
  JOIN doc_entries dr ON dr.rule_id = c.rule_id;

COMMIT;
