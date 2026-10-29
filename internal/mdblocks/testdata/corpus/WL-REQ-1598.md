
```sql
CREATE TABLE decisions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id          text NOT NULL REFERENCES tasks(id),
    key              text NOT NULL,              -- stable within the task
    position         int  NOT NULL,              -- authored order
    "group"          text NOT NULL DEFAULT '',
    question         text NOT NULL,
    context          text NOT NULL DEFAULT '',   -- markdown
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select','multi_select','single_select_notes',
                         'pick_or_freetext','yes_no','freetext')),
    options          jsonb,                      -- [{label, description}]; null for yes_no/freetext
    min_picks        int, max_picks int,         -- multi_select only
    answer           jsonb,                      -- {picked: [...], notes, freetext}; null until recorded
    decided_by       text REFERENCES actors(id),
    decided_at       timestamptz,
    UNIQUE (task_id, key)
);
```

A row is addressed as `<task>/<key>` (`WL-643/x-distribution`). Any task may carry rows; on a non-decision kind they are attached context and do not gate closing. `response_type` is fixed when the question is posed. A `yes_no` answer is `{"value": "yes" | "no" | "unsure"}`, so a decider with no view records that rather than leaving the row open. Writing `answer`, `decided_by` and `decided_at` is one transaction. An unanswered row may be reworded, re-ordered or moved to another task; an answered row is immutable, and deciding again is a new row or a new task. A rally carries no decision rows (`AddDecision` rejects it).

