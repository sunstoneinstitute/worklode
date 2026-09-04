-- 0066 shipped task_decisions with task_id as the primary key and none of
-- 025 §10.1's per-question columns (key, position, group, question, context,
-- decided_by), so it could hold one decision per task and could not address a
-- row as <task>/<key>. Nothing writes it yet, so the fix is to replace it
-- with the table the spec names.

DROP TABLE task_decisions;

CREATE TABLE decisions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id          text NOT NULL REFERENCES tasks(id),
    key              text NOT NULL,     -- stable within the task: "x-distribution"
    position         int  NOT NULL,     -- authored order
    "group"          text NOT NULL DEFAULT '',  -- optional sub-grouping
    question         text NOT NULL,     -- phrased as a question; the row's title
    context          text NOT NULL DEFAULT '',  -- markdown: what the decider needs to know
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select', 'multi_select', 'single_select_notes',
                         'pick_or_freetext', 'yes_no', 'freetext')),
    options          jsonb,      -- [{label, description}], null for yes_no/freetext
    min_picks        int,        -- multi_select only
    max_picks        int,        -- multi_select only
    answer           jsonb,      -- {picked: [...], notes, freetext, value}; null until recorded
    decided_by       text REFERENCES actors(id),
    decided_at       timestamptz,
    UNIQUE (task_id, key)
);

CREATE INDEX decisions_task_position_idx ON decisions (task_id, position);
