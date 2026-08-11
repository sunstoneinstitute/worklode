-- One-off merge of the GitHub login actor into the Keycloak actor
-- (spec 023 #sec-3.1): repoint every reference from github:169939 to
-- stig@sunstoneinstitute.ai, record the expected GitHub login, delete the
-- GitHub actor row. Idempotent — a re-run finds no github:169939 rows and
-- changes nothing. Requires migration 0014 (actors.expected_github_login).
-- Run wrapped in a transaction (psql --single-transaction, or an explicit
-- BEGIN/ROLLBACK for a dry run). Historical events/state_log payloads keep
-- github:169939 as append-only provenance and are not rewritten.

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM actors
                 WHERE id = 'stig@sunstoneinstitute.ai' AND kind = 'human') THEN
    RAISE EXCEPTION 'target actor stig@sunstoneinstitute.ai missing: log in via Keycloak once, then re-run';
  END IF;
END $$;

UPDATE tasks  SET created_by = 'stig@sunstoneinstitute.ai' WHERE created_by = 'github:169939';
UPDATE tasks  SET assignee   = 'stig@sunstoneinstitute.ai' WHERE assignee   = 'github:169939';
UPDATE leases SET actor_id   = 'stig@sunstoneinstitute.ai' WHERE actor_id   = 'github:169939';
UPDATE tokens SET actor_id   = 'stig@sunstoneinstitute.ai' WHERE actor_id   = 'github:169939';

-- github_user_tokens.actor_id is the primary key: repoint only when the
-- target has no row of its own, then drop whatever remains under the old id.
UPDATE github_user_tokens SET actor_id = 'stig@sunstoneinstitute.ai'
 WHERE actor_id = 'github:169939'
   AND NOT EXISTS (SELECT 1 FROM github_user_tokens
                   WHERE actor_id = 'stig@sunstoneinstitute.ai');
DELETE FROM github_user_tokens WHERE actor_id = 'github:169939';

UPDATE actors SET expected_github_login = 'stigsb'
 WHERE id = 'stig@sunstoneinstitute.ai';

DELETE FROM actors WHERE id = 'github:169939';
