-- Tables fed from GitHub webhooks take a github_ prefix, which also frees
-- `reviews` for the review surface's own table (WL-REQ-1322, WL-REQ-127).
BEGIN;

ALTER TABLE issues RENAME TO github_issues;
ALTER TABLE github_issues RENAME CONSTRAINT issues_pkey TO github_issues_pkey;
ALTER TABLE github_issues RENAME CONSTRAINT issues_task_id_fkey TO github_issues_task_id_fkey;
ALTER TABLE github_issues RENAME CONSTRAINT issues_triage_state_check TO github_issues_triage_state_check;

ALTER TABLE pull_requests RENAME TO github_pull_requests;
ALTER TABLE github_pull_requests RENAME CONSTRAINT pull_requests_pkey TO github_pull_requests_pkey;
ALTER TABLE github_pull_requests RENAME CONSTRAINT pull_requests_state_check TO github_pull_requests_state_check;
ALTER TABLE github_pull_requests RENAME CONSTRAINT pull_requests_task_id_fkey TO github_pull_requests_task_id_fkey;

ALTER TABLE ci_runs RENAME TO github_ci_runs;
ALTER TABLE github_ci_runs RENAME CONSTRAINT ci_runs_pkey TO github_ci_runs_pkey;
ALTER TABLE github_ci_runs RENAME CONSTRAINT ci_runs_repo_head_sha_workflow_started_at_key TO github_ci_runs_repo_head_sha_workflow_started_at_key;
ALTER SEQUENCE ci_runs_id_seq RENAME TO github_ci_runs_id_seq;

ALTER TABLE reviews RENAME TO github_reviews;
ALTER TABLE github_reviews RENAME CONSTRAINT reviews_pkey TO github_reviews_pkey;
ALTER TABLE github_reviews RENAME CONSTRAINT reviews_repo_pr_number_reviewer_submitted_at_key TO github_reviews_repo_pr_number_reviewer_submitted_at_key;
ALTER TABLE github_reviews RENAME CONSTRAINT reviews_state_check TO github_reviews_state_check;
ALTER SEQUENCE reviews_id_seq RENAME TO github_reviews_id_seq;

COMMIT;
