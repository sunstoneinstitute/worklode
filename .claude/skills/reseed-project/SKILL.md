---
name: reseed-project
description: Reseed a worklode backbone project's tasks and documents — after a task wipe or dev-database reset, when seeded tasks have drifted from what the document store says, or when importing a git corpus of specs and plans into a fresh server with "lode doc import". Manual invocation only.
disable-model-invocation: true
---

# Reseed a worklode project

Seed **genuine pending work only**. Spec 025 §1: rows are things someone made;
groupings are queries. Never seed plan-root tasks, per-spec umbrella tasks,
sprint containers, or anything whose state duplicates a coverage query.

**Prerequisites:** `lode` authenticated against the target server (`lode
login` or a minted token); for a reset, direct Postgres access to that
server's database (e.g. `kubectl port-forward` to the CNPG primary) — this is
the live DSN, not `TEST_POSTGRES_DSN`. The server must have the document store
(`lode doc --help` works); there is no pre-025 path any more.

## 1. Audit the backbone first

The document store answers both audit questions directly. Run them against the
target server, not a checkout:

```bash
lode doc list --needs-planning --json    # accepted specs with sections no accepted plan covers
lode doc list --needs-execution --json   # accepted plans whose task set still has an open task
```

`--needs-planning` returns `planning_gaps` alongside `docs`: per spec, its
section count and the uncovered anchors classified `unplanned`, `partial`,
`bound-only` or `deferred` — the last with an `owner`, the document a plan
handed the section to (026 §2.1, §5.3). `--needs-execution` is the backbone's own answer to
"which plans are unfinished" — a plan's tasks exist because accepting it minted
them, so plan checkboxes are never the evidence.

**The one case the queries cannot see: imported plans.** `lode doc import`
lands a plan at its stated status without minting tasks, so an imported plan —
spent or unexecuted — has no task set and `--needs-execution` omits it by
design. For those, evidence is implementation presence: a plan is executed iff
the package, CLI verb, or migration it promises exists. Judge that against
**the commit the server runs**, not your checkout's HEAD — the deployed image
tag is a git SHA (`kubectl -n <namespace> get deploy -o jsonpath='{..image}'`
against the live cluster; the repo's overlay file shows a stale `newTag:
latest`, and `lode --version` reports only the local CLI):

```bash
ls internal/ internal/cmd/ deploy/base/migrations/
```

Classification rules for plans the binary test doesn't fit:

- **Document-producing plans** (`*-design.md`, requirements gathering): the
  design document existing in the backbone (`lode doc list --kind spec`) — or
  the plan itself carrying `status: accepted` as an in-place design record — is
  the evidence. Executed, no tasks.
- **Cross-repo plans** (work landing in another repo/cluster): seed a task
  only if this project should track the dependency; say so in the body.
- **Acceptance/QA companion plans** fold into their primary plan's task set.
- **Infra/CI plans**: evidence is the workflow/overlay existing, not a package.

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

## 3. Seed

1. **Import the documents**, if the project has none — a git corpus of specs
   and plans goes in with one command (025 §22):

   ```bash
   lode doc import --docs <corpus-root> --project <P> --dry-run   # inspect first
   lode doc import --docs <corpus-root> --project <P>
   ```

   It walks the top level of `<corpus-root>/specs` and `<corpus-root>/plans`,
   keeps each file's frontmatter status verbatim, wires edges in a second pass,
   and is safe to re-run: an unchanged slug is left alone, a drifted body is
   updated in place where that is legal (plans, draft specs/ADRs), and a
   drifted accepted spec/ADR is reported on stderr for `lode doc revise`.
   Stating a status needs the admin-only `doc.import` permission. Import
   mints nothing.
2. **Mint the unexecuted plans' tasks.** For each plan the §1 audit found
   unexecuted, put it through the accept gate rather than hand-writing its
   tasks — acceptance mints the task set in one transaction (025 §9.2):

   ```bash
   lode doc new --kind plan --slug <slug> --file <path|->   # if not already imported; lands draft
   lode doc accept <ref>                             # mints the tasks; owner-gated
   ```

   Do not hand-create execute-tasks. The mint is the seeding.
3. **Leave spent plans alone.** Imported accepted, no task set, nothing owed.
4. **Wire series ordering** between the minted tasks — roots are tasks, and
   blocking stays a task-level verb:

   ```bash
   lode task block <part-N-id> --by <part-N-1-id>
   ```
5. **Planning gaps need no seeding.** `lode doc list --needs-planning` answers
   from queries; create a `design` task per gap only when someone will pick it
   up (the `/lode:spec-coverage` skill does exactly that).

## 4. Verify

```bash
lode task list                       # expected count; no plan-root in the ready set
lode task tree                       # roots with children, progress derived
lode doc list --needs-planning       # agrees with the §1 audit
lode doc list --needs-execution      # agrees with the §1 audit
```

The three selectors (`--needs-planning`, `--needs-execution`,
`--bare-superseded`) are mutually exclusive — one per invocation.
