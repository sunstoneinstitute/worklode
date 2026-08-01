---
status: accepted
issued: 2026-07-24
amends:
  ".":
    - 004-execution-backbone.md
---
# Spec 010 — Per-project task keys (Jira-style IDs)

## Why

Task IDs are `WL-<n>` for every project: the prefix is the literal `"WL-"`
(`internal/store/tasks.go:93`) and `<n>` comes from a single global counter
(`task_seq`, one row — `0001_baseline.up.sql:62`). A second project would get
`WL-12`, not its own code counting from 1. We want Jira-style per-project codes:
`WL-1…` for worklode, `SW-1…` for the next project.

## Decisions (settled)

| Decision | Choice |
|---|---|
| Code (`key`) | Required on project creation, unique, uppercase, immutable |
| Key format | `^[A-Z][A-Z0-9]{1,9}$` (letter first, 2–10 chars) |
| Numbering | Per-project counter, starting at 1 |
| Global `task_seq` | Dropped |
| Existing `WL-1…11` | Preserved; worklode's key backfilled to `WL`, counter to 12 |
| ID format | `<KEY>-<n>` (e.g. `WL-12`, `SW-1`) |

Immutable because the key is baked into permanent task IDs, `wl/<id>` branch
names, and `WL-Task:` PR markers — changing it would orphan those references.

## Data model — migration `0003_project_keys`

```sql
ALTER TABLE projects ADD COLUMN key text;
ALTER TABLE projects ADD COLUMN next_task_num bigint NOT NULL DEFAULT 1;

-- Backfill from existing task-id prefixes (data-driven, not hardcoded):
-- worklode -> key 'WL', next_task_num max(n)+1 = 12.
UPDATE projects p SET key = s.prefix, next_task_num = s.maxnum + 1
FROM (SELECT project_id,
             split_part(id, '-', 1)               AS prefix,
             max(split_part(id, '-', 2)::bigint)   AS maxnum
      FROM tasks GROUP BY project_id, split_part(id, '-', 1)) s
WHERE p.id = s.project_id;

-- Fallback for projects with no tasks yet (none today): derive from id.
UPDATE projects
SET key = upper(substr(regexp_replace(id, '[^a-zA-Z0-9]', '', 'g'), 1, 4))
WHERE key IS NULL;

ALTER TABLE projects ALTER COLUMN key SET NOT NULL;
ALTER TABLE projects ADD CONSTRAINT projects_key_unique UNIQUE (key);
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$');

DROP TABLE task_seq;
```

The `.down.sql` recreates `task_seq` (seeded from `max(next_task_num)`), drops
the two columns, and reverses the constraints.

## ID generation — `internal/store/tasks.go`

`CreateTask` replaces the global-sequence read with a per-project one, in the
same transaction:

```sql
UPDATE projects SET next_task_num = next_task_num + 1
WHERE id = $1 RETURNING key, next_task_num - 1
```

then `id := fmt.Sprintf("%s-%d", key, n)`. IDs stay globally unique (unique key
× per-project number). A missing project row surfaces the existing
`ErrInvalidInput`/not-found path.

## API / CLI

- `store.CreateProject(ctx, id, name, key)` — new `key` param
  (`internal/store/projects.go:35`).
- `POST /api/v1/projects` — `key` required; validate format server-side, map a
  unique-violation to a 400 "project key already in use" rather than a 500
  (`internal/api/admin.go:52`). `projectJSON` gains `key`
  (`admin.go:23`, `listProjects` at `admin.go:81`).
- `lode project add <id> --name … --key WL` — new required `--key` flag
  (`internal/cmd/project.go:38`); `project list` gains a `KEY` column.

## Generalize the hardcoded `WL-` parsers

Replace the literal `WL-` with `[A-Z][A-Z0-9]*-\d+`:

- `internal/worktree/worktree.go:18` — `dirRe` for `wt/<id>[-slug]`.
- `internal/store/changes.go:51` — `refTaskIDPattern` for `wl/<id>` branches.
- `internal/store/changes.go:67` — `bodyTaskIDPattern`: keep the literal
  `WL-Task:` marker label (a fixed convention, not the id prefix); generalize
  only the captured id.
- `internal/store/ranking.go:167` — `numericTaskID`: parse the digits after the
  last `-` (`id[strings.LastIndex(id,"-")+1:]`) instead of `TrimPrefix(id,"WL-")`.

Branch naming already interpolates the id, so it becomes `wl/<id>-<slug>` (e.g.
`wl/SW-3-…`) with no code change beyond the regex.

## Testing

- Per-project counters: two projects each start at 1 and increment independently.
- Key validation: rejects bad format, duplicate key, and missing key.
- Backfill migration: an existing `WL-1…11` project yields key `WL`,
  `next_task_num` 12; a task-less project gets the id-derived fallback.
- Parser generalization: `SW-3` matches branch/dir/body/ranking helpers.
- Update existing tests that assert literal `WL-` where a second project is now
  in play.

## Out of scope

Renaming/re-keying an existing project (immutable by decision); renumbering
existing IDs; any UI beyond the `KEY` column in `project list`.
