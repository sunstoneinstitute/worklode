-- Reverse the schema half of the spec template migration: the conversion
-- report goes. The data half is one-way: converted headings stay headings,
-- moved covers and governedBy links stay moved, and rule text stays out of
-- docs.body (the renderer puts it back on read).
DROP TABLE rule_heading_conversions;
