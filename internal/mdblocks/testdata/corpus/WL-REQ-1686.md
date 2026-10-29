
The server parses the front matter on every body write with `mdrender.DocMeta` and stores the result:

```sql
ALTER TABLE tasks ADD COLUMN body_mode text NOT NULL DEFAULT 'doc'
    CHECK (body_mode IN ('doc', 'reveal'));
CREATE INDEX tasks_body_mode_idx ON tasks (project, body_mode) WHERE body_mode <> 'doc';
```

`lode task list --mode reveal` uses the index.

