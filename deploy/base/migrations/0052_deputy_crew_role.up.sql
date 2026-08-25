-- Deputy Crew designation (spec 029 §6.1): one Crew member per project may
-- act with full lead authority when the lead does not act, without becoming
-- lead — the accountable human stays the lead. Mirrors is_lead's shape: a
-- column on the participant row, at most one true per project, and mutually
-- exclusive with is_lead on the same row (a member cannot hold both).
ALTER TABLE project_participants
    ADD COLUMN is_deputy boolean NOT NULL DEFAULT false;

ALTER TABLE project_participants
    ADD CONSTRAINT project_participants_lead_deputy_exclusive
    CHECK (NOT (is_lead AND is_deputy));

CREATE UNIQUE INDEX project_participants_one_deputy
    ON project_participants (project_id) WHERE is_deputy;

CREATE UNIQUE INDEX project_participants_one_flag_per_actor
    ON project_participants (project_id, actor_id) WHERE is_lead OR is_deputy;
