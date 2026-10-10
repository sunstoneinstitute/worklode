-- Coverage levels are retired (WL-REQ-165, WL-REQ-187, WL-REQ-203): a
-- covers edge means the plan builds the whole rule. The level data is read
-- here before its columns go.
--
--   * A defers owner moves from doc_coverage_completed_with to owner_doc /
--     owner_external on the edge row, in all three edge tables.
--   * The standalone `none` edges WL-928 classified
--     (docs/research/coverage-none-classification.tsv) as N1 or N2 are
--     deleted, as is every classified entry naming no rule (a to_external
--     edge into a DP, EA or HDB spec, WL-928 F3). Unsure entries stay.
--   * N3, a `none` parent whose subtree the same plan covers at another
--     level, deletes the parent's and its `none` descendants' edges.
--   * Every other edge, `partial` and unsure included, becomes a plain
--     covers edge.
BEGIN;

-- Defers owners.
ALTER TABLE doc_edges ADD COLUMN owner_doc bigint REFERENCES docs(id), ADD COLUMN owner_external text;
UPDATE doc_edges e SET owner_doc = w.to_doc, owner_external = w.to_external
  FROM doc_coverage_completed_with w
 WHERE w.edge_id = e.id AND w.position = 0 AND e.type = 'defers';
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_owner_on_defers
    CHECK (num_nonnulls(owner_doc, owner_external) <= CASE WHEN type = 'defers' THEN 1 ELSE 0 END);

ALTER TABLE doc_revision_edges ADD COLUMN owner_doc bigint REFERENCES docs(id), ADD COLUMN owner_external text;
UPDATE doc_revision_edges
   SET owner_doc = (completed_with->0->>'to_doc')::bigint, owner_external = completed_with->0->>'to_external'
 WHERE type = 'defers' AND jsonb_array_length(coalesce(completed_with, '[]')) > 0;
ALTER TABLE doc_revision_edges ADD CONSTRAINT doc_revision_edges_owner_on_defers
    CHECK (num_nonnulls(owner_doc, owner_external) <= CASE WHEN type = 'defers' THEN 1 ELSE 0 END);

ALTER TABLE doc_edge_versions ADD COLUMN owner_doc bigint, ADD COLUMN owner_external text;
UPDATE doc_edge_versions
   SET owner_doc = (completed_with->0->>'to_doc')::bigint, owner_external = completed_with->0->>'to_external'
 WHERE type = 'defers' AND jsonb_array_length(coalesce(completed_with, '[]')) > 0;

-- The WL-928 classification, one row per rule; rule_no is NULL for an entry
-- naming no rule. The last row is not in the TSV: WL-PLAN-140's
-- WL-REQ-337 entry was listed as N3 there and is N2 (WL-928 F6).
CREATE TEMP TABLE coverage_class (plan_ref text, target text, rule_no bigint, class text) ON COMMIT DROP;
INSERT INTO coverage_class VALUES
    ('DP-PLAN-1', 'DP-SPEC-1#sec-0', NULL, 'N1'),
    ('DP-PLAN-1', 'DP-SPEC-1#sec-1', NULL, 'N1'),
    ('DP-PLAN-1', 'DP-SPEC-1#sec-14', NULL, 'unsure'),
    ('DP-PLAN-1', 'DP-SPEC-1#sec-2', NULL, 'N2'),
    ('DP-PLAN-2', 'DP-SPEC-1#sec-0', NULL, 'N1'),
    ('DP-PLAN-2', 'DP-SPEC-1#sec-1', NULL, 'N1'),
    ('DP-PLAN-2', 'DP-SPEC-1#sec-14', NULL, 'unsure'),
    ('DP-PLAN-2', 'DP-SPEC-1#sec-2', NULL, 'N2'),
    ('DP-PLAN-3', 'DP-SPEC-1#sec-0', NULL, 'N1'),
    ('DP-PLAN-3', 'DP-SPEC-1#sec-1', NULL, 'N1'),
    ('DP-PLAN-3', 'DP-SPEC-1#sec-14', NULL, 'unsure'),
    ('DP-PLAN-3', 'DP-SPEC-1#sec-2', NULL, 'N2'),
    ('EA-PLAN-6', 'EA-SPEC-1#sec-11', NULL, 'N2'),
    ('EA-PLAN-7', 'EA-SPEC-1#sec-11', NULL, 'N2'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-0', NULL, 'N1'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-1', NULL, 'N1'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-11', NULL, 'N2'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-13', NULL, 'N2'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-14', NULL, 'N1'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-15', NULL, 'N1'),
    ('EA-PLAN-8', 'EA-SPEC-2#sec-2', NULL, 'N2'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-0', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-1', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-10', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-13', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-14', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-2', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-3', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-4', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-5', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-5.2', NULL, 'unsure'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-6', NULL, 'N1'),
    ('EA-PLAN-9', 'EA-SPEC-1#sec-6.1', NULL, 'N1'),
    ('EA-PLAN-13', 'EA-SPEC-3#sec-0', NULL, 'N1'),
    ('EA-PLAN-13', 'EA-SPEC-3#sec-1', NULL, 'N2'),
    ('EA-PLAN-13', 'EA-SPEC-3#sec-11', NULL, 'N1'),
    ('EA-PLAN-13', 'EA-SPEC-3#sec-12', NULL, 'N1'),
    ('EA-PLAN-13', 'EA-SPEC-3#sec-13', NULL, 'N1'),
    ('EA-PLAN-13', 'EA-SPEC-3#sec-2', NULL, 'N2'),
    ('HDB-PLAN-4', 'HDB-SPEC-2#sec-1', NULL, 'N1'),
    ('HDB-PLAN-4', 'HDB-SPEC-2#sec-2', NULL, 'N1'),
    ('HDB-PLAN-4', 'HDB-SPEC-2#sec-5', NULL, 'N2'),
    ('HDB-PLAN-4', 'HDB-SPEC-2#sec-7', NULL, 'N2'),
    ('HDB-PLAN-63', 'HDB-SPEC-25#sec-1', NULL, 'N1'),
    ('HDB-PLAN-63', 'HDB-SPEC-25#sec-2', NULL, 'N2'),
    ('HDB-PLAN-63', 'HDB-SPEC-25#sec-3', NULL, 'N1'),
    ('HDB-PLAN-63', 'HDB-SPEC-25#sec-4', NULL, 'N2'),
    ('HDB-PLAN-63', 'HDB-SPEC-25#sec-6', NULL, 'N2'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-0', 825, 'N1'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-1', 826, 'N1'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-2.1', 828, 'N1'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-2.2', 829, 'N2'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-4.1', 835, 'N2'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-4.4', 838, 'N1'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-5', 840, 'N2'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-5.1', 841, 'N1'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-7', 843, 'N1'),
    ('WL-PLAN-1', 'WL-SPEC-38#sec-8', 844, 'N1'),
    ('WL-PLAN-2', 'WL-SPEC-40#sec-0', 854, 'N1'),
    ('WL-PLAN-2', 'WL-SPEC-40#sec-12', 879, 'N1'),
    ('WL-PLAN-2', 'WL-SPEC-40#sec-2.1', 857, 'N1'),
    ('WL-PLAN-4', 'WL-SPEC-39#sec-0', 846, 'N1'),
    ('WL-PLAN-4', 'WL-SPEC-39#sec-1', 847, 'N1'),
    ('WL-PLAN-4', 'WL-SPEC-39#sec-5', 852, 'N1'),
    ('WL-PLAN-4', 'WL-SPEC-39#sec-6', 853, 'N1'),
    ('WL-PLAN-6', 'WL-SPEC-55#sec-0', 987, 'N1'),
    ('WL-PLAN-6', 'WL-SPEC-55#sec-1', 988, 'N1'),
    ('WL-PLAN-6', 'WL-SPEC-55#sec-6', 994, 'N1'),
    ('WL-PLAN-7', 'WL-SPEC-53#sec-0', 966, 'N1'),
    ('WL-PLAN-7', 'WL-SPEC-53#sec-7', 973, 'N1'),
    ('WL-PLAN-81', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-83', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-85', 'WL-SPEC-32#sec-1', 777, 'N2'),
    ('WL-PLAN-85', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-87', 'WL-SPEC-29#sec-8.2', 772, 'N2'),
    ('WL-PLAN-87', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-87', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-93', 'WL-SPEC-26#sec-5', 737, 'N2'),
    ('WL-PLAN-93', 'WL-SPEC-26#sec-5', 738, 'N2'),
    ('WL-PLAN-93', 'WL-SPEC-26#sec-5', 739, 'N2'),
    ('WL-PLAN-93', 'WL-SPEC-26#sec-5', 740, 'N2'),
    ('WL-PLAN-93', 'WL-SPEC-26#sec-5', 741, 'N2'),
    ('WL-PLAN-97', 'WL-SPEC-29#sec-0', 754, 'N1'),
    ('WL-PLAN-97', 'WL-SPEC-29#sec-9', 775, 'N1'),
    ('WL-PLAN-97', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-97', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-98', 'WL-SPEC-29#sec-1', 755, 'N2'),
    ('WL-PLAN-99', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-99', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-100', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-100', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-101', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-101', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-103', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-103', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-104', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-104', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-105', 'WL-SPEC-29#sec-8.2', 772, 'N2'),
    ('WL-PLAN-106', 'WL-SPEC-1#sec-0', 373, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-1#sec-1', 374, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-1#sec-12', 398, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-1#sec-13', 399, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-12#sec-0', 545, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-13#sec-0', 553, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-13#sec-4', 562, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-13#sec-5', 563, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-16#sec-0', 566, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-16#sec-7', 573, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-16#sec-8', 574, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-17#sec-0', 576, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-17#sec-7', 583, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-17#sec-8', 584, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-17#sec-9', 585, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-19#sec-0', 587, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-19#sec-6', 601, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-20#sec-0', 602, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-20#sec-1', 603, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-20#sec-5', 615, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-20#sec-6', 616, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-21#sec-0', 617, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-21#sec-14', 635, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-21#sec-15.1', 637, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-22#sec-0', 638, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-0', 648, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-1', 649, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16', 708, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16', 709, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16', 710, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16', 711, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16', 712, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16', 713, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16.1', 709, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16.2', 710, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16.3', 711, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16.4', 712, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-16.5', 713, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-21', 718, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-22', 719, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-23', 720, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-25#sec-5.1', 664, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-26#sec-11', 752, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-29#sec-0', 754, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-29#sec-9', 775, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-32#sec-13', 790, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-0', 791, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-1', 792, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-10', 821, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-11', 822, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-12', 823, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-3.3', 801, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-5.1', 807, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-37#sec-9', 820, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-0', 825, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-2.1', 828, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-2.2', 829, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-4.5', 839, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-5', 840, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-5.1', 841, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-7', 843, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-38#sec-8', 844, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-39#sec-0', 846, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-39#sec-1', 847, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-39#sec-5', 852, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-39#sec-6', 853, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-4#sec-0', 407, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-4#sec-10', 443, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-4#sec-11', 444, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-4#sec-12', 445, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-4#sec-6.1', 430, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-40#sec-0', 854, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-40#sec-12', 879, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-40#sec-2.1', 857, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-41#sec-0', 881, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-41#sec-5', 886, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-41#sec-7', 888, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-42#sec-0', 889, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-44#sec-0', 897, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-0', 904, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-1', 905, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-1', 906, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-1', 907, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-1', 908, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-1', 909, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-10', 929, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-6', 922, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-6', 923, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-6', 924, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-45#sec-6', 925, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-46#sec-0', 931, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-46#sec-1.1', 933, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-46#sec-4.4', 949, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-46#sec-7', 956, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-46#sec-8', 957, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-46#sec-9', 958, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-5#sec-0', 447, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-5#sec-7', 455, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-5#sec-8', 456, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-52#sec-0', 959, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-52#sec-6', 965, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-53#sec-0', 966, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-53#sec-7', 973, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-0', 974, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-1', 975, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2', 976, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2', 977, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2', 978, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2', 979, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2.1', 977, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2.2', 978, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-2.3', 979, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-5', 985, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-54#sec-6', 986, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-55#sec-0', 987, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-55#sec-5', 993, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-55#sec-6', 994, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-56#sec-0', 996, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-56#sec-3.4', 1003, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-56#sec-5', 1005, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-57#sec-0', 1006, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-57#sec-1', 1007, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-57#sec-6', 1017, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-57#sec-8', 1019, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-57#sec-9', 1020, 'unsure'),
    ('WL-PLAN-106', 'WL-SPEC-6#sec-0', 458, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-6#sec-13.4', 482, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-6#sec-14', 483, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-6#sec-15', 484, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-7#sec-0', 486, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-7#sec-6', 501, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-7#sec-7', 502, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-0', 504, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16', 526, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16', 527, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16', 528, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16', 529, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16', 530, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16', 531, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16.1', 527, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16.2', 528, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16.3', 529, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16.4', 530, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-16.5', 531, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-19', 541, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-20', 542, 'N1'),
    ('WL-PLAN-106', 'WL-SPEC-8#sec-22', 544, 'N1'),
    ('WL-PLAN-112', 'WL-SPEC-56#sec-0', 996, 'N1'),
    ('WL-PLAN-112', 'WL-SPEC-56#sec-3.4', 1003, 'N1'),
    ('WL-PLAN-112', 'WL-SPEC-56#sec-5', 1005, 'N1'),
    ('WL-PLAN-113', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-113', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-113', 'WL-SPEC-32#sec-13', 790, 'N1'),
    ('WL-PLAN-121', 'WL-SPEC-57#sec-0', 1006, 'N1'),
    ('WL-PLAN-121', 'WL-SPEC-57#sec-6', 1017, 'N1'),
    ('WL-PLAN-125', 'WL-SPEC-41#sec-0', 881, 'N1'),
    ('WL-PLAN-125', 'WL-SPEC-41#sec-7', 888, 'N1'),
    ('WL-PLAN-127', 'WL-SPEC-61#sec-0', 1077, 'N2'),
    ('WL-PLAN-127', 'WL-SPEC-61#sec-1.1', 1079, 'N2'),
    ('WL-PLAN-127', 'WL-SPEC-61#sec-6', 1089, 'N2'),
    ('WL-PLAN-127', 'WL-SPEC-61#sec-7', 1090, 'N1'),
    ('WL-PLAN-128', 'WL-SPEC-61#sec-0', 1077, 'N2'),
    ('WL-PLAN-128', 'WL-SPEC-61#sec-1', 1078, 'N2'),
    ('WL-PLAN-128', 'WL-SPEC-61#sec-1', 1079, 'N2'),
    ('WL-PLAN-128', 'WL-SPEC-61#sec-6', 1089, 'N2'),
    ('WL-PLAN-128', 'WL-SPEC-61#sec-7', 1090, 'N1'),
    ('WL-PLAN-129', 'WL-SPEC-61#sec-0', 1077, 'N2'),
    ('WL-PLAN-129', 'WL-SPEC-61#sec-1', 1078, 'N2'),
    ('WL-PLAN-129', 'WL-SPEC-61#sec-1', 1079, 'N2'),
    ('WL-PLAN-129', 'WL-SPEC-61#sec-6', 1089, 'N2'),
    ('WL-PLAN-129', 'WL-SPEC-61#sec-7', 1090, 'N1'),
    ('WL-PLAN-130', 'WL-SPEC-61#sec-0', 1077, 'N2'),
    ('WL-PLAN-130', 'WL-SPEC-61#sec-1.1', 1079, 'N2'),
    ('WL-PLAN-130', 'WL-SPEC-61#sec-2.5', 1085, 'N2'),
    ('WL-PLAN-130', 'WL-SPEC-61#sec-6', 1089, 'N2'),
    ('WL-PLAN-130', 'WL-SPEC-61#sec-7', 1090, 'N1'),
    ('WL-PLAN-131', 'WL-SPEC-63#sec-0', 1114, 'N1'),
    ('WL-PLAN-132', 'WL-SPEC-59#sec-0', 1036, 'N1'),
    ('WL-PLAN-132', 'WL-SPEC-59#sec-14', 1058, 'N1'),
    ('WL-PLAN-132', 'WL-SPEC-59#sec-16', 1060, 'N1'),
    ('WL-PLAN-133', 'WL-SPEC-59#sec-14', 1058, 'N1'),
    ('WL-PLAN-134', 'WL-SPEC-59#sec-1', 1037, 'N2'),
    ('WL-PLAN-134', 'WL-SPEC-59#sec-1', 1038, 'N2'),
    ('WL-PLAN-134', 'WL-SPEC-59#sec-1', 1039, 'N2'),
    ('WL-PLAN-134', 'WL-SPEC-59#sec-14', 1058, 'N1'),
    ('WL-PLAN-135', 'WL-SPEC-59#sec-14', 1058, 'N1'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-0', 1146, 'N1'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-4', 1165, 'N2'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-4', 1167, 'N2'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-4', 1168, 'N2'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-4', 1169, 'N2'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-4', 1170, 'N2'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-4.1', 1166, 'N1'),
    ('WL-PLAN-136', 'WL-SPEC-66#sec-9', 1182, 'N1'),
    ('WL-PLAN-137', 'WL-SPEC-66#sec-0', 1146, 'N1'),
    ('WL-PLAN-137', 'WL-SPEC-66#sec-2.5', 1157, 'N2'),
    ('WL-PLAN-137', 'WL-SPEC-66#sec-4.1', 1166, 'N1'),
    ('WL-PLAN-137', 'WL-SPEC-66#sec-9', 1182, 'N1'),
    ('WL-PLAN-138', 'WL-SPEC-66#sec-0', 1146, 'N1'),
    ('WL-PLAN-138', 'WL-SPEC-66#sec-2.5', 1157, 'N2'),
    ('WL-PLAN-138', 'WL-SPEC-66#sec-9', 1182, 'N1'),
    ('WL-PLAN-139', 'WL-SPEC-66#sec-0', 1146, 'N1'),
    ('WL-PLAN-139', 'WL-SPEC-66#sec-2.5', 1157, 'N2'),
    ('WL-PLAN-139', 'WL-SPEC-66#sec-9', 1182, 'N1'),
    ('WL-PLAN-140', 'WL-SPEC-32#sec-10', 787, 'N2'),
    ('WL-PLAN-140', 'WL-SPEC-32#sec-11', 788, 'N2'),
    ('WL-PLAN-141', 'WL-SPEC-67#sec-0', 1183, 'N1'),
    ('WL-PLAN-141', 'WL-SPEC-67#sec-1', 1184, 'N2'),
    ('WL-PLAN-141', 'WL-SPEC-67#sec-8', 1197, 'N2'),
    ('WL-PLAN-142', 'WL-SPEC-67#sec-8', 1197, 'N2')
,
    ('WL-PLAN-140', 'WL-SPEC-32#sec-3', 779, 'N2');

CREATE TEMP TABLE coverage_class_r ON COMMIT DROP AS
SELECT c.target, c.rule_no, c.class, pd.id AS plan_id, r.id AS rule_id
  FROM coverage_class c
  LEFT JOIN projects pp ON pp.key = split_part(c.plan_ref, '-', 1)
  LEFT JOIN docs pd ON pd.project_id = pp.id AND pd.kind = 'plan' AND pd.deleted_at IS NULL
                   AND pd.number = split_part(c.plan_ref, '-', 3)::int
  LEFT JOIN projects rp ON rp.key = 'WL'
  LEFT JOIN rules r ON r.project_id = rp.id AND r.number = c.rule_no;

-- N1, N2 and every entry naming no rule (F3).
DELETE FROM doc_edges e
 USING coverage_class_r c
 WHERE (c.class IN ('N1', 'N2') OR c.rule_no IS NULL) AND e.from_doc = c.plan_id
   AND e.type = 'covers' AND e.coverage = 'none'
   AND (e.to_rule = c.rule_id OR (c.rule_no IS NULL AND e.to_external = c.target));

-- N3. A rule's subtree is the later rules of an arranging document up to the
-- next heading at its depth or shallower (as in migration 0092).
CREATE TEMP TABLE covers_n3 ON COMMIT DROP AS
SELECT DISTINCT e.from_doc, a.doc_id, a.position, a.depth
  FROM doc_edges e
  JOIN doc_rules a ON a.rule_id = e.to_rule
 WHERE e.type = 'covers' AND e.coverage = 'none'
   AND EXISTS (SELECT 1 FROM doc_rules s
                 JOIN doc_edges c ON c.from_doc = e.from_doc AND c.type = 'covers'
                                 AND c.to_rule = s.rule_id AND c.coverage <> 'none'
                WHERE s.doc_id = a.doc_id AND s.position > a.position
                  AND NOT EXISTS (SELECT 1 FROM doc_rules n
                                   WHERE n.doc_id = a.doc_id AND n.depth <= a.depth
                                     AND n.position > a.position AND n.position <= s.position));

DELETE FROM doc_edges e
 USING covers_n3 p, doc_rules s
 WHERE e.from_doc = p.from_doc AND e.type = 'covers' AND e.coverage = 'none'
   AND s.doc_id = p.doc_id AND s.rule_id = e.to_rule AND s.position >= p.position
   AND NOT EXISTS (SELECT 1 FROM doc_rules n
                    WHERE n.doc_id = p.doc_id AND n.depth <= p.depth
                      AND n.position > p.position AND n.position <= s.position);

-- The level and the completion table go.
DROP VIEW covered_sections;
DROP VIEW covered_rules;
DROP TABLE doc_coverage_completed_with;

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_coverage_level;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_coverage_on_covers;
ALTER TABLE doc_edges DROP COLUMN coverage;
ALTER TABLE doc_revision_edges DROP CONSTRAINT doc_revision_edges_coverage_level;
ALTER TABLE doc_revision_edges DROP CONSTRAINT doc_revision_edges_coverage_on_covers;
ALTER TABLE doc_revision_edges DROP COLUMN coverage, DROP COLUMN completed_with;
ALTER TABLE doc_edge_versions DROP COLUMN coverage, DROP COLUMN completed_with;

-- covered_rules is every rule a covers edge reaches: its to_rule and,
-- transitively, each rule that supersedes a reached rule, so a successor
-- counts as covered when a predecessor was (WL-REQ-165). supersedes runs
-- new -> old (from_rule is the successor).
CREATE VIEW covered_rules AS
WITH RECURSIVE walk (edge_id, plan_id, rule_id) AS (
    SELECT id, from_doc, to_rule
      FROM doc_edges
     WHERE type = 'covers' AND to_rule IS NOT NULL
  UNION
    SELECT w.edge_id, w.plan_id, s.from_rule
      FROM walk w
      JOIN rule_edges s ON s.to_rule = w.rule_id AND s.type = 'supersedes'
)
SELECT edge_id, plan_id, rule_id FROM walk;

-- covered_sections places each covered rule at every section arranging it.
CREATE VIEW covered_sections AS
SELECT c.edge_id, c.plan_id, c.rule_id, dr.doc_id, dr.anchor
  FROM covered_rules c
  JOIN doc_rules dr ON dr.rule_id = c.rule_id;

COMMIT;
