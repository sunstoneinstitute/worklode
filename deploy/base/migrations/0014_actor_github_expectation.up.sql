-- The GitHub login Keycloak asserts for this actor (spec 001 §9.2), recorded
-- at login so the future link flow can strict-match long after login. NULL
-- means the Keycloak account carries no github_username attribute.
ALTER TABLE actors ADD COLUMN expected_github_login text;
