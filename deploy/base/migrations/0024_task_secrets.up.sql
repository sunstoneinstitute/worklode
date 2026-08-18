-- Spec 017: tasks declare which org-catalog secrets they need, by symbolic
-- name. Names only — values and op:// refs never enter the backbone.
ALTER TABLE tasks ADD COLUMN secrets jsonb NOT NULL DEFAULT '[]'::jsonb;
