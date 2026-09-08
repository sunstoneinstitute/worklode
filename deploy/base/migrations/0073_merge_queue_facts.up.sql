-- WL-SPEC-66 §6.1, §6.3: merge queue facts for a PR and its repo/branch.
--
-- queued_at is set when a PR enters the merge queue (GitHub's
-- enqueued_for_merge / merge_group.checks_requested event) and cleared when
-- it leaves it, whichever way. repo_branch_rules records whether a
-- repo/branch pair has a merge queue enabled, as last observed from the
-- GitHub API, so the merge button knows whether to enqueue or merge
-- directly.
ALTER TABLE pull_requests ADD COLUMN queued_at timestamptz;

CREATE TABLE repo_branch_rules (
    repo        text NOT NULL,
    branch      text NOT NULL,
    merge_queue boolean NOT NULL,
    checked_at  timestamptz NOT NULL,
    PRIMARY KEY (repo, branch)
);
