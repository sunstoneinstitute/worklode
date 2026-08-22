ALTER TABLE projects DROP CONSTRAINT projects_key_format;
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$');
