DROP INDEX actors_github_username_unique;
ALTER TABLE actors RENAME COLUMN github_username TO expected_github_login;
