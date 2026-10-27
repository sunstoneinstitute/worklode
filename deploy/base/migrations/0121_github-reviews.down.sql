BEGIN;

ALTER SEQUENCE github_reviews_id_seq RENAME TO reviews_id_seq;
ALTER TABLE github_reviews RENAME CONSTRAINT github_reviews_repo_pr_number_reviewer_submitted_at_key
    TO reviews_repo_pr_number_reviewer_submitted_at_key;
ALTER TABLE github_reviews RENAME CONSTRAINT github_reviews_pkey TO reviews_pkey;
ALTER TABLE github_reviews RENAME TO reviews;

COMMIT;
