ALTER TABLE tasks ADD COLUMN concern text
    CHECK (concern IN ('completeness','performance','usability','security'));
ALTER TABLE tasks ADD COLUMN needs_decomposition boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN focus jsonb NOT NULL DEFAULT '[]';
