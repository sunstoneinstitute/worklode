---
name: reseed-project
description: Reseed a worklode backbone project's tasks from the docs corpus — after a task wipe or dev-database reset, when seeded tasks have drifted from docs/specs and docs/plans reality, or when spec 025's document store lands and the interim task seeding is redone as documents. Manual invocation only.
disable-model-invocation: true
---

# Reseed a worklode project

Seed **genuine pending work only**. Spec 025 §1: rows are things someone made;
groupings are queries. Never seed epics, per-spec umbrella tasks, sprint
containers, or anything whose state duplicates a coverage query.

**Prerequisites:** `lode` authenticated against the target server (`lode
login` or a minted token); for a reset, direct Postgres access to that
server's database (e.g. `kubectl port-forward` to the CNPG primary) — this is
the live DSN, not `TEST_POSTGRES_DSN`.

Two paths — pick by server capability: if `lode doc --help` works, use
**Full seeding** (3b); otherwise **Interim seeding** (3a).

## 1. Audit the corpus first

Audit against **the commit the server runs**, not your checkout's HEAD: the
deployed image tag is a git SHA: `kubectl -n <namespace> get deploy -o
jsonpath='{..image}'` with kubectl pointed at the target cluster (the repo's
overlay file shows a stale `newTag: latest` — always query the live cluster);
check out that SHA before auditing. `lode --version`
reports only the local CLI.

**Unexecuted plans.** Evidence is implementation presence, never plan
checkboxes (they were never maintained — verified 2026-08-02): a plan is
executed iff the package, CLI verb, or migration it promises exists.

```bash
ls internal/ internal/cmd/ deploy/base/migrations/
```

Classification rules for plans the binary test doesn't fit:

- **Document-producing plans** (`*-design.md`, requirements gathering): the
  document existing in `docs/specs/` — or the plan file itself carrying
  spec-style frontmatter with `status: accepted` (an in-place design record,
  e.g. `provider-neutral-cli-login-design.md`) — is the evidence. Executed,
  no task.
- **Cross-repo plans** (work landing in another repo/cluster): seed a task
  only if this project should track the dependency; say so in the body.
- **Acceptance/QA companion plans** fold into their primary plan's task.
- **Infra/CI plans**: evidence is the workflow/overlay existing, not a package.

**Unplanned specs.** Only **accepted** specs get planning tasks — a
draft/proposed spec's pending work is review and acceptance, not planning:

```bash
grep -ho "docs/specs/[0-9a-z-]*\.md" docs/plans/*.md | sort -u   # specs claimed by a plan
grep -l "^status: accepted" docs/specs/*.md                      # accepted specs
```

Accepted-and-unclaimed → one planning task each.

## 2. Reset (only when wiping)

Back up first, to somewhere durable (session scratchpads are not):
`pg_dump <live-dsn> > worklode-dev-backup-$(date +%F).sql`.

FK order verified against migrations 0001/0004/0005/0008 (`\d+ tasks` will
not show the second-order `agent_sessions → leases` chain). Keep issue/PR
rows — they are GitHub history; only detach them:

```sql
BEGIN;
UPDATE issues        SET task_id = NULL WHERE task_id IN (SELECT id FROM tasks WHERE project_id = 'worklode');
UPDATE pull_requests SET task_id = NULL WHERE task_id IN (SELECT id FROM tasks WHERE project_id = 'worklode');
DELETE FROM agent_sessions WHERE lease_id IN
  (SELECT id FROM leases WHERE task_id IN (SELECT id FROM tasks WHERE project_id = 'worklode'));  -- usage rows cascade
DELETE FROM leases     WHERE task_id IN (SELECT id FROM tasks WHERE project_id = 'worklode');
DELETE FROM task_edges WHERE from_task IN (SELECT id FROM tasks WHERE project_id = 'worklode')
                          OR to_task   IN (SELECT id FROM tasks WHERE project_id = 'worklode');
DELETE FROM tasks WHERE project_id = 'worklode';   -- 0005's task-commit rows cascade
UPDATE projects SET next_task_num = 1 WHERE id = 'worklode';
COMMIT;
```

## 3a. Interim seeding (pre-025 server: no `lode doc`)

One task per genuine pending item, nothing structural:

```bash
# per accepted-but-unplanned spec (kind renames to 'design' after the 025 migration):
lode task add --kind spec --title "Write implementation plan(s) for spec NNN — <title>" \
  --body "Spec NNN is accepted but has no plan in docs/plans/. Produce plans per docs/authoring-design-docs.md."

# per unexecuted plan:
lode task add --kind feature --title "Execute plan: <short title>" \
  --body "Execute docs/plans/<file> (implements spec NNN). Transitional stand-in for the plan's execution root (spec 025 §9.2). Audit basis: no owning package, CLI verb, or migration at <server commit>."
```

Order multi-part series (`…-1-…`, `…-2-…`) with blocks edges, part N on N−1:

```bash
lode task block <part-N-id> --by <part-N-1-id>
```

## 3b. Full seeding (post-025 server: `lode doc` exists)

Spec 025 is draft; verb names below follow its §10 and may shift at
implementation. **First reseed on a post-025 server: reconcile everything
here that touches `lode doc` — the capability gate above, this section, and
§4's verify commands — against the implemented CLI, and update this skill in
the same change.**

1. Import specs/ADRs as accepted documents (corpus import, 025 §22).
2. Import **unexecuted** plans (§1 audit) and `lode doc accept` each —
   acceptance mints the `kind='plan'` root plus children in one transaction
   (025 §9.2). Do not hand-create execute-tasks; the mint is the seeding.
3. Skip executed plans, or import them closed if history matters.
4. Wire series ordering as `lode task block` edges between the minted roots
   (roots are tasks; blocking stays a task-level verb).
5. Planning gaps need no seeding: `lode doc list --needs-planning` answers
   from queries; create a `design` task per gap only when someone will pick
   it up.

## 4. Verify

```bash
lode task list            # expected count; no epic/plan-root in the ready set
lode task tree            # post-025: roots with children, progress derived
lode doc list --needs-planning --needs-execution   # post-025: agrees with §1 audit
```
