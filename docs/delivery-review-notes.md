# States, facts and deliverables — session conclusions

Working notes from a design discussion on 2026-08-28. Nothing here is
accepted; every spec it touches (004, 025, 029, 045, 046) is still `draft`,
so each item lands as an amendment rather than a supersession.

The through-line: **a state is the furthest milestone the recorded facts
support; everything else is a query over those facts.** Most items below are
that rule applied to a place where it currently isn't.

## Conclusions

### C1 — Reintroduce `proposed` on documents, drop `in_review`

`draft → proposed → accepted → superseded`. 025 §7 removed `proposed` on the
grounds that an open review task already proves "under review". That argument
holds for review *progress* and not for authorial readiness: only the author
can state that the text is finished enough to read, and no other row proves
it. `in_review` (025 §7.3, spec'd, unimplemented) is the redundant one —
review progress is provable from the review task's own state.

Anchors freeze at `proposed`, not at `accepted`, so crit comments cannot be
renumbered out from under a reviewer. 025 §6 rule 4 currently exempts drafts
from the renumbering constraints, which allows exactly that.

Cost: `ns/concept.ttl` + `nsgen`, the CHECK in `0027_docs.up.sql`, the accept
guard at `internal/store/docs.go:377`, and an amendment to 025 §7 and §7.1.
The enum has one source, so nothing else hand-mirrors it.

### C2 — Rename the task state `in_review` to `submitted`

PR-open is a real milestone: the author submitted the work. It is not review.
Renaming makes the existing hook honest on every deliverable kind, and it
draws the same line as C1: the author's readiness is a state, review progress
is a query over `reviews`, `approvals` and `ci_runs`.

Cost is a value rename across the CHECK constraint, `ns/shapes.ttl`, UI, CLI
and e2e, plus edits to 045 and 046, which lean on the name throughout.

### C3 — `in_review` is the only hand-written transition on the ladder

Every delivery state is derived by `store.ResolveDelivery` from recorded
facts, forward-only and arrival-order independent. `internal/hooks/github.go:399`
says so in a comment while doing the opposite for this one edge.

### C4 — The PR-open edge belongs to 046's rule list

046 §1.1 grandfathers "the PR-opened hook enters `in_review`" as backbone
behavior, "not rule business". That edge is a GitHub-repo-shaped rule wearing
backbone clothes. 045 already makes the state optional per workflow and
guards the hook on whether the workflow declares it, which covers the
non-GitHub case (a dataset publication has no such state).

### C5 — CI stays

`pr-checks.yml` triggers on `pull_request`, which checks out
`refs/pull/N/merge`. The PR run is already the merge-result run; there is no
separate branch build to delete. The second run per landed change is
`deploy-dev.yml` on push to main, which is a deploy.

CI's value is not executing the tests, it is that something outside the
agent's control executed them. An agent is non-deterministic and carries a
ship-it bias, so it must not attest its own run. The duplicate worth cutting
is the agent's pre-push run, which can shrink to a fast subset. Concretely:
`lode install` stops installing a pre-push hook and removes one it finds, and
`lode doctor` warns while one is still installed.

Remaining gap: checks do not re-run when main moves under an open PR. Merge
queue is the fix, and it is the only thing that tests the merge that actually
happens. It is not the same as stacked PRs: a merge queue serializes queued
PRs and tests each against the main it would land on, while stacked PRs are a
chain of dependent branches solving a different problem.

### C6 — A deliverable never names a branch

Identity is the commit SHA. `task_commits`, `main_commits` and `LandedMainID`
already key delivery on it. A branch is a movable label, and the default
`LODE_BRANCH_TEMPLATE` (`{{ .id }}-{{ .slug }}`) makes branch names
predictable, so a deliverable naming one would be pre-claimable by a third
party.

029 §3.1 already settles the "address not known in advance" case: a
deliverable is verified **by address** when known ahead of time, or **by
label** when minted at build time, with worklode defining the label key and
value at creation. A worklode-minted label is not guessable, which is
strictly better than a branch-name placeholder.

Branch names are not a fallback identity either. A human can create a branch
that ignores the template, or push straight to main, and then the only carrier
is the commit message. Two forms, both plural: `KEY-N` references in the
subject, and `Worklode-Task:` at the start of a line in the body. A squash
carries several of each, and one subject can name keys from more than one
project, so the subject pattern is generated from the existing project keys
rather than fixed. C8 is the same plurality bug on the trailer side; C17 is
what the ids are for.

### C7 — One deploy event advances N tasks, with no fan-out

`main_commits` gives each main commit a monotonic id, `deploy_shas` maps a
deployed sha back to one, `env_deploys` holds the per-`(repo, environment)`
frontier, and `ResolveDelivery` advances every task whose landed id is at or
below it (`TasksBelowFrontier`). Four tasks in one docker image need no
per-task notification, and a replayed webhook lands the same result.

### C8 — Filed as WL-386: only the first task trailer is read

`store.TaskIDFromBody` (`internal/store/changes.go:71`) returns the first
`Worklode-Task:` trailer and stops. When N tasks land in one squash commit,
the others never get a `task_commits` row, `LandedMainID` returns nil, and
the frontier sweeps past them silently. Fix is a plural sibling used by
`hooks/push.go:183`, leaving the singular call at `changes.go:144` for PR
correlation, where one body means one task.

### C9 — Required environments are declared intent, never observed state

A deliverable may declare that prod is required before it counts as
delivered. That is intent, which 029 §3.2 keeps distinct from reported state.
A single checkbox that means both "prod is required" and "prod was reached"
collapses the two. Required is declared and editable; reached is read-only and
comes from `env_deploys`.

`ResolveDelivery` already tests `covered(dev)` and `covered(prod)`
independently, so "prod required, dev skipped" works without changing it.

### C10 — CSRF has an established pattern here

`internal/api/webform.go`: `SameSite=Lax` cookie, a same-origin header check
as the second lock, POST-redirect-GET, and `requireSession` on the
approval-decide route because 029 §7.3 makes deciding a session act. A review
link is a plain GET that renders; claiming is a POST through the API.

### C11 — The review surface is designed, not decomposed

`docs/review-design.md` (2026-08-16) settles review as a durable Postgres
object whose subject is a document or a task's change, explicitly not a PR,
with crit as the interim client and meat for diffs. Its "Spec decomposition"
section names three specs. That thread needs tasks, not design.

### C12 — Evidence advances the task (was Q1)

Task delivery is frontier-driven (`ResolveDelivery` over `main_commits` and
`env_deploys`); deliverable state is declaration-driven
(`artifact_declarations`, `artifact_evidence`), and 045 §7 says the two meet
"only in prose". They have to meet on the task's ladder instead: evidence
advances the task, and a deliverable that reaches `published` while its task
is still in review is a bug in the model rather than a valid state. The rest
of the deliverable work hangs off this.

### C13 — `artifacts.kind` is missing three kinds (was Q2)

`artifacts.kind` is `docker_image | pypi | git_tag | binary`. Missing: the
ordinary merged feature-branch deliverable, which today lives only on the
frontier path; a published dataset or datapackage in the data catalog; a CMS
post.

### C14 — One spec owns the evidence scheme, sources integrate against it (was Q3)

046 §0 puts frontier-driven delivery resolution out of scope and 029 §3.2
stops at "is this deliverable live", so nothing claims the seam today. One
spec owns the overall evidence scheme, and each source gets its own
integration spec against it. Sources in view: GitHub/GHCR, the data catalog,
the CMS.

### C15 — A deliverable copies `done_state` when it is defined (was Q4)

`project_repos.done_state` is one terminal state per repo, so it cannot
express a hotfix that is done at dev alongside a release that is not done
until prod. The deliverable's own declaration wins, and the repo default is
copied into the deliverable at definition time, so a later change to the
project default never bleeds into deliverables already declared. That also
gives `done_state` a retirement path instead of two owners.

### C16 — Environments mirror GitHub's names (was Q5)

Declaring "prod is required" needs the set of known environments per repo.
Mirror GitHub's names verbatim (`GET /repos/{owner}/{repo}/environments`): a
single-repo project takes that repo's set, a multi-repo project the union
across its repos. Still open: where they are stored, what refreshes them, how
`NormalizeEnvironment`'s existing dev/prod mapping relates to the imported
names, and the equivalent for a non-GitHub deliverable.

### C17 — Every commit is accounted for, by task or explicitly not (was Q8)

Work done from a task must carry its id. Nothing puts it in the subject today
(current main subjects carry only `(#357)`), but GitHub uses the PR title as
the squash subject, so setting the PR title from the task carries the id
through all three merge modes. Work with no task still needs per-repo
accounting, so that a gap is discoverable rather than invisible: a `NO-TASK`
marker mirroring the `NO-SPEC` convention plans already use, or a `<KEY>-0`
catch-all. Open only whether the id in the subject is a convention, a check,
or both.

### C18 — Deliverables need a `lode` verb, and worklode needs `lode api` (was Q9)

Store code and a cockpit creation form exist for deliverables; there is no CLI
surface, so anything agentic goes through the API directly. Beyond that one
gap, worklode needs a `lode api` command shaped like `gh api`, so any endpoint
that has not earned its own verb yet is still reachable from the CLI.

### C19 — Reviewers are crew roles, and a review is claimed atomically (part of Q6)

Crew gains three reviewer roles: `reviewer: code`, `reviewer: methodology`,
`reviewer: language`. A human review is assigned to the users holding the role
it needs, and the first to claim it gets it. The claim is atomic, so two
reviewers never do the same work.

## Open questions

Q1-Q5, Q8 and Q9 were answered in review and are now C12-C18, and half of Q6
is C19; the numbering is left as it was so the review thread still resolves.

### Q6 — Automation policy

C19 settles who reviews. Still open: when an agent may self-approve, and how it
escalates a complex or risky change to a human. The same policy decides whether
a planning task is created automatically when a plan is accepted, and whether
execution tasks are created automatically, which lets a crew set automation to
the level it is comfortable with; the high end depends on specs being good
enough to execute from. Belongs with 045/046 per-project config.

### Q7 — `done_state` as a per-repo workflow selector

045 §7 defers this explicitly. The AI and Democracy project (key `AID`), the
one being used to test worklode for completeness, needs it now, and C15 is
pressure on the same seam one level finer.
