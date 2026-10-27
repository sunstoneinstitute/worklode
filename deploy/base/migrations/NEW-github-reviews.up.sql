-- GitHub pull-request reviews move to github_reviews, freeing `reviews` for
-- the review surface's own table (WL-REQ-1322, WL-REQ-127).
BEGIN;

ALTER TABLE reviews RENAME TO github_reviews;
ALTER TABLE github_reviews RENAME CONSTRAINT reviews_pkey TO github_reviews_pkey;
ALTER TABLE github_reviews RENAME CONSTRAINT reviews_repo_pr_number_reviewer_submitted_at_key
    TO github_reviews_repo_pr_number_reviewer_submitted_at_key;
ALTER SEQUENCE reviews_id_seq RENAME TO github_reviews_id_seq;

COMMIT;
