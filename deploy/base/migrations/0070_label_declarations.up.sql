-- Spec 029 §3.1: a deliverable is identified by address OR by label, for
-- artifacts whose address is minted at build time (a docker tag, an Iceberg
-- snapshot). A label declaration stores the full selector string
-- ('worklode.deliverable=COW/datasets') in artifact_uri — the routing key and
-- the evidence key stay one string — and this column says which form the row
-- is, so an address lookup can never match a label declaration by accident.
ALTER TABLE artifact_declarations ADD COLUMN selector text NOT NULL DEFAULT 'address'
    CHECK (selector IN ('address', 'label'));
