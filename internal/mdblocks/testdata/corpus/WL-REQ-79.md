
```sql
CREATE TABLE tasks (
    id         text PRIMARY KEY,                    -- <KEY>-<n>
    project_id text NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    title      text NOT NULL,
    body       text,
    priority   text NOT NULL CHECK (priority IN ('critical','high','medium','low')),
    kind       text NOT NULL CHECK (kind IN
                 ('feature','bug','chore','design','review','spike','decision','rally')),
    state      text NOT NULL CHECK (state IN
                  ('draft','ready','in_progress','in_review','merged',
                   'deployed_dev','deployed_prod','released','abandoned')),
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
```

Further columns: `concern` (nullable, §8.1), the `needs-decomposition` label (§8.6), `milestone_id` (nullable, §13.2), `about_doc` (nullable, the document a review or design task is about, §9.6), `assignee` (one, nullable, §13.5), and the three tombstone columns of §12. `plan_doc` says which plan defines a task (WL-SPEC-77 §11) and is a different fact from `about_doc`. Conventions everywhere: `timestamptz`, `bigint GENERATED ALWAYS AS IDENTITY` keys, `boolean` flags, `jsonb` payloads, partial unique indexes and `CHECK` constraints.

