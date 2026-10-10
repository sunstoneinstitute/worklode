-- WL-REQ-6: whether a repo's default branch rulesets require pull
-- requests, so `lode doctor` can warn about a gated repo that does not.
-- NULL means not yet observed; the branch-rules refresh fills it.
ALTER TABLE repo_branch_rules ADD COLUMN pull_request boolean;
