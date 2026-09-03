---
name: follow-up
description: Take the highest-priority item from docs/follow-ups.md, do it in a worktree, and merge it to main without a PR. Invoke as /follow-up; an argument overrides the pick.
argument-hint: "[P0|P1|P2|P3|P4 | substring of the item]"
disable-model-invocation: true
---

# Working off docs/follow-ups.md

One item per run, landed on `main` with no PR and no approval gate. The
pre-commit hooks are the entire safety net: docs-only changes skip CI, and
nothing here opens a PR, so `gofmt`, `go vet`, the migration-number check and
the design-doc frontmatter check are the only review the change gets. Do not
`--no-verify`.

## 1. Refuse to start on a contended file

```bash
git -C "$(git rev-parse --show-toplevel)" status --porcelain docs/follow-ups.md
```

Non-empty output means another session is already editing the list. Stop and
say so — every run rewrites this file, and merging over a concurrent edit
loses it.

## 2. Pick the item

Read `docs/follow-ups.md`. An item is a top-level bullet whose `[P0]`–`[P4]`
or `[gated]` tag is followed by a **bold title**.

- The header's priority key is six bullets carrying the same tags with no bold
  title (`` - `[P0]` exposure or silent wrongness — schedule now ``). Those are
  the legend, not work. The bold title is what tells them apart.
- Skip every `[gated]` item. The file's own header says don't schedule them.
- Otherwise take the lowest `Pn`, and among equals the first in file order.
- With an argument: a priority tag restricts the pick to that tier; anything
  else is matched as a substring against the item's bold title.

State which item you took and why before doing anything else.

## 3. Triage the size before writing any code

Judge whether the item is finishable in one sitting with a diff you would be
willing to land unreviewed. Items that describe new schema, a new subsystem,
or a modelling decision (`a delivery-state-per-repo table`, `ingest
registry_package webhooks`) are not — regardless of their priority tag.

**If it is too big, do not attempt it.** Instead:

1. Draft the plan in a scratch file per the `worklode-docs-authoring` skill —
   frontmatter first, keys ordered lifecycle → `covers` → dependency:
   `status: draft`, then `covers:` naming the spec sections it undertakes, or
   `covers: NO-SPEC` when nothing governs it. Load
   `superpowers:writing-plans` to write the body. Then create the document in
   the backbone, which is where plans live:

   ```bash
   lode doc lint <scratch.md>                                     # local lint
   lode doc add --kind plan --slug YYYY-MM-DD-<slug> --file <scratch.md>
   ```

   Leave it draft: accepting it mints its tasks, which is a decision for
   whoever picks the work up.
2. Replace the follow-ups entry with one that keeps the original priority tag
   and title, whose body is a single sentence naming the new plan by slug,
   and that it should be executed with subagents.
3. Land that entry as the change. The plan itself is already in the backbone,
   so the commit is the follow-ups edit alone — docs-only.

This is a normal outcome, not a failure — say plainly that the item was
converted to a plan rather than fixed.

## 4. Work it in a worktree

```bash
git worktree add -b follow-up/<slug> .worktrees/follow-up-<slug>
```

The directory name must not contain a `WL-<n>`-shaped id: `worktree.Layout.TaskID`
extracts an id from any `[A-Z][A-Z0-9]*-[0-9]+` in the segment below
`.worktrees/`, and a match would make the worklode hook guards treat this as a
task worktree with a lease it does not have. Keep the `follow-up-` prefix and
strip any id from the slug.

Do the work there. Strike the item from `docs/follow-ups.md` **in the same
commit** — a fix that leaves its entry behind reads as unfixed. Write the
commit message about the defect and the fix, not about the follow-ups file.

Then, from the worktree:

```bash
go build ./... && gofmt -l internal/ && go vet ./...
go test ./internal/<touched>/...
```

Store tests need Postgres with pgvector and skip silently without it — a green
run proves less than it looks like. If the change touches `internal/store`,
say whether Postgres was actually reachable.

## 5. Land it

If `main` moved while you worked, rebase the branch onto it and re-run the
checks from step 4 before going further. Then, from the root checkout:

```bash
git merge follow-up/<slug>
git worktree remove .worktrees/follow-up-<slug>
git branch -d follow-up/<slug>
```

If the root's working tree is too dirty to merge into, stop there and leave
the branch and worktree in place. Report what is blocking the merge — never
stash, reset, or force the user's uncommitted work out of the way.

## 6. Report

Name the item, what changed, the check results, and the commit. If you skipped
items to reach this one, say which and why.
