-- When the actor's groups were last synced from Keycloak (WL-REQ-1608).
-- NULL (never synced, or a service actor) counts as stale.
ALTER TABLE actors ADD COLUMN groups_synced_at timestamptz;
