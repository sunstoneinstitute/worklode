-- No-op: 0070_label_declarations.down.sql already drops this column, and
-- dropping it here too would make that migration's down fail on databases
-- where both applied (WL-847).
SELECT 1;
