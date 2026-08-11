-- Curated v0 backing for the cockpit's "Pinned focus" and "Next decision"
-- cards: a lead sets these by hand until spec-029 supersedes them with derived
-- data. All columns nullable, default NULL (NULL = unset).
ALTER TABLE projects
    ADD COLUMN focus_note           text,
    ADD COLUMN focus_pinned_by      text,
    ADD COLUMN focus_pinned_at      timestamptz,
    ADD COLUMN decision_title       text,
    ADD COLUMN decision_accountable text,
    ADD COLUMN decision_readiness   text;
