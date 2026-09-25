-- The Keycloak githubUsername user attribute, carried in the ID token as the
-- githubUsername claim and re-synced at every login (WL-RULE-53). GitHub
-- facts attach to the person through it, so it is an identity. The unique
-- index fails loudly on duplicate claims, which are a Keycloak
-- misconfiguration to fix, not data to keep.
ALTER TABLE actors RENAME COLUMN expected_github_login TO github_username;
CREATE UNIQUE INDEX actors_github_username_unique
    ON actors (lower(github_username)) WHERE github_username IS NOT NULL;
