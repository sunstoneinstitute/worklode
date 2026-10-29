
The label follows whether the repository's default branch carries a
merge-queue rule (WL-REQ-1340). Queue for merge is disabled
when the PR is already queued. Merge is disabled until the latest CI run for
the PR's head concluded `success`. Whether the PR is a draft or mergeable is
not stored; GitHub refuses those and its reason comes back as a 409. Queue for
merge enqueues through the GitHub App's GraphQL `enqueuePullRequest`; Merge
merges through the REST endpoint with the method the repository allows,
read from the repository before each merge: squash, else merge commit, else
rebase. The
server acts with the App's installation token. Before calling GitHub it
checks that the PR carries the task, is open and is not queued (409
otherwise), and records `pr.merge_requested` with actor, PR and operation;
after the call it records `pr.merge_result` with the outcome and GitHub's
message. GitHub's refusal is a 409, an unreachable GitHub a 502, and an
instance with no GitHub App a 503; with no App both buttons are disabled
with the reason "no GitHub App is configured".
The position line changes on the following stream event; the reply itself
changes nothing on the page.

