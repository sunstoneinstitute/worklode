-- Add the 'decision' task kind (025 §10) and its side table (§10.1):
-- everything a decision needs beyond its own title/body lives in
-- task_decisions, so the core tasks table stays generic and no kind of task
-- carries columns another kind leaves null. Ships with the concept.ttl edit
-- and regenerated code in the same commit, which
-- TestTaskKindsAgreeAcrossSources holds together.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'));

CREATE TABLE task_decisions (
    task_id          text PRIMARY KEY REFERENCES tasks(id),
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select', 'multi_select', 'single_select_notes',
                         'pick_or_freetext', 'yes_no', 'freetext')),
    options          jsonb,      -- [{label, description}], null for yes_no/freetext
    min_picks        int,        -- multi_select only
    max_picks        int,        -- multi_select only
    answer           jsonb,      -- {picked: [...], notes, freetext}; null until recorded
    decided_at       timestamptz
);
