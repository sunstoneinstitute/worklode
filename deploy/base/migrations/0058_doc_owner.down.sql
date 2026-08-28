DROP INDEX docs_owner;
ALTER TABLE docs RENAME COLUMN owner TO assignee;
