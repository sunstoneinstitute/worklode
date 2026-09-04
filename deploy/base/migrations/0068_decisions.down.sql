-- Reverse 0068: destructive by definition — the posed questions go with it.
-- Restores task_decisions as 0066 created it.

DROP TABLE decisions;

CREATE TABLE task_decisions (
    task_id          text PRIMARY KEY REFERENCES tasks(id),
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select', 'multi_select', 'single_select_notes',
                         'pick_or_freetext', 'yes_no', 'freetext')),
    options          jsonb,
    min_picks        int,
    max_picks        int,
    answer           jsonb,
    decided_at       timestamptz
);
