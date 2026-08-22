-- Task-scoped tokens (001 §2.1, WL-306): a wl_ token bound to one task and
-- expiring with its lease. NULL keeps today's actor-scoped shape unchanged.
-- The partial index serves the two lifecycle sweeps — extend-on-renew and
-- revoke-on-lease-end — which only ever touch a task's unrevoked tokens.
ALTER TABLE tokens ADD COLUMN task_id text REFERENCES tasks (id) ON DELETE CASCADE;
CREATE INDEX tokens_task_live ON tokens (task_id) WHERE task_id IS NOT NULL AND revoked_at IS NULL;
