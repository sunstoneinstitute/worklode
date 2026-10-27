-- The remaining tables fed only from GitHub (webhooks and App reads) take the
-- github_ prefix, like 0121's (WL-REQ-127).
BEGIN;

ALTER TABLE repo_branch_rules RENAME TO github_branch_rules;
ALTER TABLE github_branch_rules RENAME CONSTRAINT repo_branch_rules_pkey TO github_branch_rules_pkey;

ALTER TABLE deploy_shas RENAME TO github_deploy_shas;
ALTER TABLE github_deploy_shas RENAME CONSTRAINT deploy_shas_pkey TO github_deploy_shas_pkey;
ALTER TABLE github_deploy_shas RENAME CONSTRAINT deploy_shas_main_id_fkey TO github_deploy_shas_main_id_fkey;

ALTER TABLE release_frontiers RENAME TO github_release_frontiers;
ALTER TABLE github_release_frontiers RENAME CONSTRAINT release_frontiers_pkey TO github_release_frontiers_pkey;
ALTER TABLE github_release_frontiers RENAME CONSTRAINT release_frontiers_main_id_fkey TO github_release_frontiers_main_id_fkey;

COMMIT;
