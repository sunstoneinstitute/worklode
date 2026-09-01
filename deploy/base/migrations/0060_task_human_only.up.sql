-- human_only marks a task no unattended worker may pick up: it is ready to
-- work, but only by a person. Distinct from needs_decomposition ("too big to
-- work as one task") and from draft ("not ready to work yet"); the ready-set
-- query excludes all three, while an explicit claim by id still succeeds.
ALTER TABLE tasks ADD COLUMN human_only boolean NOT NULL DEFAULT false;
