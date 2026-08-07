---
status: draft
issued: 2026-08-07
requires:
  - 008-worklode-plugin.md
  - 011-delivery-lifecycle.md
amends:
  ".":
    - 003-platform-graph-design.md#sec-5
    - 008-worklode-plugin.md#sec-3
    - 008-worklode-plugin.md#sec-4
    - 008-worklode-plugin.md#sec-6
    - 008-worklode-plugin.md#sec-7
    - 008-worklode-plugin.md#sec-12
    - 008-worklode-plugin.md#sec-13
    - 011-delivery-lifecycle.md#sec-2
    - 011-delivery-lifecycle.md#sec-4
    - 011-delivery-lifecycle.md#sec-6
---
# Spec 030 — Branch and worktree naming

## 0. Purpose & scope {#sec-0}

Task branches are `lode/<id>-<slug>` and worktrees live at `<git-root>/wt/<id>-<slug>`.
Both prefixes are noise: the task id already identifies the work, and `wt/` was
never a deliberate choice. This spec replaces the fixed `<prefix><id>-<slug>`
branch rule with a server-rendered template defaulting to `{{ .id }}-{{ .slug }}`,
and makes the worktree base directory a client-side setting defaulting to
`.worktrees`.

Scope: the branch name the backbone hands out, the pattern that reverses a
branch back to a task id for PR/CI correlation, and the on-disk worktree path.
Out of scope: the task state machine, the lease model, and the worktree→lease
binding, all of which are unchanged.

`.worktrees` rather than `.claude/worktrees` because a worktree is not
harness-specific — the same directory holds work driven by Claude Code, Codex,
or a human.

## 1. Branch names are a server-rendered template {#sec-1}

`LODE_BRANCH_PREFIX` is replaced by `LODE_BRANCH_TEMPLATE`, a Go `text/template`
rendering the **whole** branch name. The default is `{{ .id }}-{{ .slug }}`,
which yields `WL-3-fix-the-thing`.

The server remains the sole authority on branch names (011 §2, 008 §3). It
renders the template once per claim and returns the result; no client renders a
template, and the CLI's only fallback — used when a response carries no branch —
is the literal `<id>-<slug>`.

### 1.1 Fields {#sec-1.1}

| Field | Value |
|---|---|
| `.id` | task id, e.g. `WL-3` |
| `.slug` | slugified task title |
| `.projectId` | project id the task belongs to |
| `.kind` | task kind |

Rendering uses `missingkey=error`, so a misspelled field is an error rather
than an empty string. There is no bare `.project`: a project id, name, and key
are three different things, and a task only ever carries an id, so `.projectId`
is the only project-shaped field exposed.

`.id` and `.slug` are already ref-safe by construction, but `.projectId` and
`.kind` are not — `projects.id` is free text, not slug-shaped — so both are run
through the same slugify rule as `.slug` before rendering. A template that
uses them therefore always produces a legal ref, even when the underlying
project id contains characters git would otherwise reject.

### 1.2 Validation {#sec-1.2}

The template is parsed and test-rendered at `lode serve` startup. A bad
template fails startup rather than every claim. Three conditions:

1. It parses, and renders against sample values without error.
2. It references `.id`. A template without it produces branches that cannot be
   correlated back to a task, which would silently disable §2.
3. The rendered result is a legal git ref: non-empty, no ASCII control
   characters, no space or `~^:?*[\`, no `..`, no `//`, no leading or trailing
   `/`, no `@{`, no path component starting with `.` or ending in `.` or
   `.lock`.

Condition 3 is checked in Go rather than by shelling out to `git
check-ref-format`, since the server does not otherwise require a git binary.

## 2. Reverse parsing {#sec-2}

`store.TaskIDFromRef` maps a pushed branch or PR head ref back to a task id.
With a fixed prefix this was a literal pattern; with a template the pattern is
**derived from the same template**, built once beside it:

1. Render with sentinel values — `.id` → `\x00id\x00`, and likewise for the
   other fields. Derivation runs *after* §1.2 validation has passed, so the
   template's literal text is known to be free of control characters and a
   sentinel can never collide with a literal.
2. `regexp.QuoteMeta` the rendered string, so literal parts stay literal.
3. Substitute the sentinels back: `.id` → `([A-Z][A-Z0-9]*-[0-9]+)`, every
   other field → `[^/]*`.
4. Anchor with `^`/`$`.

The default template yields `^([A-Z][A-Z0-9]*-[0-9]+)-[^/]*$`.

This is broader than the prefixed pattern it replaces — an unrelated
`AB-12-something` branch now matches shape. It is not broader in effect: every
correlation path already gates on `taskExists` before writing a binding, so a
shape match against a task id that does not exist is dropped.

Two consequences to accept rather than work around:

- A **bare** `WL-3` branch no longer correlates under the default template,
  because the `-` before `{{ .slug }}` is a literal. Branches are always minted
  with a slug; only a hand-made branch is affected.
- A template that renders the id adjacent to another field (`{{ .projectId }}{{ .id }}`)
  produces an ambiguous pattern. This is not prevented — the id regex is
  anchored enough at its right edge (`-<digits>`) to make the common cases work,
  and a separator between fields is the obvious authoring rule.

## 3. Worktree layout {#sec-3}

### 3.1 Base directory {#sec-3.1}

The worktree base directory is `worktree_dir` in the repo-local
`.worklode/config.toml`, overridable by `LODE_WORKTREE_DIR`, defaulting to
`.worktrees`. It is interpreted relative to the git root; an absolute path or
one escaping the root via `..` is rejected.

Because `.worklode/config.toml` is repo content, it is checked out inside every
worktree too. A hook running inside a worktree therefore resolves the base
directory from its own cwd, without locating the main repository first.

The worktree path is the base directory joined with the branch name:

```
<git-root>/<worktree_dir>/<branch>
```

Under the default template that is `<git-root>/.worktrees/WL-3-fix-the-thing`.

The layout is **flat**: every worktree is exactly one directory below the base,
and under the default template that directory's name is the branch name
verbatim. A template containing `/` does not nest — the separator is flattened
to `-`, so `team/{{ .id }}-{{ .slug }}` gives
`.worktrees/team-WL-3-fix-the-thing`. Flatness is what lets the guard and the
adoption scan both be one-level operations (§3.2); a namespaced template is a
naming choice for the *branch*, and there is no reason for it to reshape the
local directory tree.

Deriving the directory from the branch rather than re-deriving it from the task
keeps 008 §3's determinism (the path is still a pure function of the task) while
leaving the client with one authority to consult instead of two.

### 3.2 The path guard {#sec-3.2}

008 §4's uniform hook guard — *no bound worktree ⇒ no Worklode behavior* —
previously asked whether a path's parent directory was named `wt`. It now asks:

1. Is the path *exactly one segment* below a segment equal to the configured
   base directory?
2. Which task does that segment belong to?

The first question is the guard; the second only extracts the id. The guard is
a pure string operation — no template, no config beyond the base directory, no
network call — which is what keeps it cheap enough to run on every hook event,
including on the paths nowhere near a worktree that make up almost all of them.

The one-segment rule follows from the flat layout (§3.1) and is what makes the
guard total: a path *inside* a worktree is not itself a worktree root, and the
handlers that need to accept one resolve it through `worktree.Root` first.

Id resolution — question 2 alone — reads the worktree's own git config, `git
config --worktree --get worklode.task-id`, which `lode next` stamps at creation
time, and falls back to the first `[A-Z][A-Z0-9]*-[0-9]+` in the segment. The
fallback is a substring match rather than a whole-segment match, so it holds
for templates that render the id adjacent to other text (`{{ .projectId }}-{{
.id }}`); the explicit field covers what no pattern can, a worktree renamed
after creation to a name carrying no id at all.

That the two questions have different costs is why they must stay separable: a
subprocess per path is affordable only for paths that already cleared the
guard, i.e. only for events already headed for a backbone call. Widening it to
answer question 1 would put a `git config` invocation on every keystroke-level
hook event, and a stamped worktree outside the base is therefore still invisible
to Worklode — the guard decides, and it decides on strings.

Scanning for adoptable worktrees (`SessionStart` outside a worktree) reads the
base directory one level deep. There is nothing deeper to find.

## 4. Configuration surface {#sec-4}

| Setting | Where | Default |
|---|---|---|
| `LODE_BRANCH_TEMPLATE` | server env | `{{ .id }}-{{ .slug }}` |
| `worktree_dir` | repo `.worklode/config.toml` | `.worktrees` |
| `LODE_WORKTREE_DIR` | client env, overrides the above | — |

The split is deliberate: a branch name is published (it reaches GitHub, PRs, and
CI) so the backbone owns it, while a worktree path is local to one checkout so
the checkout owns it.

`LODE_WORKTREE_DIR` does not persist anywhere: it is an environment variable
read fresh on each invocation, so it is for one-off and CI use only. A worktree
created with it set is invisible to any later session that starts without
it — that session resolves the base from `worktree_dir` in the repo config,
gets `.worktrees`, `Layout.ParseDir` returns `false` for the old path, and
every hook NOPs with no error (§3.2's guard fails silently, exactly the
divergence this spec exists to prevent). `worktree_dir` in
`.worklode/config.toml` is the durable setting — it is repo content, so it is
checked out into every worktree and every session sees the same value.

## 5. Cutover {#sec-5}

Clean break, no legacy recognition:

- `wl/` and `lode/` are removed from the correlation pattern.
- `wt/` is removed from the path guard and the adoption scan.
- `.gitignore` carries `.worktrees/` in place of `wt/`.
- `store.DefaultBranchPrefix`, `SetBranchPrefix`/`BranchPrefix` and
  `api.Config.BranchPrefix` are deleted.

### 5.1 Migrating an existing worktree {#sec-5.1}

Worklode worktrees exist on exactly one machine, so migration is manual, per
worktree, rather than a compatibility layer. `git worktree move` and `git
branch -m` do **not** by themselves make the worktree usable again: the lease
recorded in the backbone still names the old `<host>:<path>` identity, and
while that lease is live, `cli.ReacquireOrRenew` (`internal/cli/client.go`)
compares it against the new identity, finds no match, and falls to its
`default` case — "actively leased to a different worktree; refusing to
resume" (a warning from the hook, a hard error from `lode resume`). No CLI
command rebinds an *existing* worktree's lease; `RebindWorktree` is called
only from `lode next` (`internal/cmd/lifecycle.go`), which binds a lease at
the moment a worktree is first created. So the stale identity does not
"rebind on the next resume" — it has to be cleared first. Four steps, in
order:

1. `git worktree move <old> <new>`
2. `git branch -m <old-branch> <new-branch>`
3. `lode task release <id>` — required: the lease is still live and still
   names the old path, so a naive resume is refused rather than rebinding.
4. `lode resume <new>` — with no lease held, this re-claims and binds to the
   new identity.

### 5.2 PR and push correlation across the cutover {#sec-5.2}

`deploy/base/configmap.yaml` ships `LODE_BRANCH_TEMPLATE: ""` (the default),
so the clean break takes effect the moment the server rolls — there is no
separate opt-in step. What that means for in-flight work:

- **Already-correlated PRs are unaffected.** `store.UpsertPR`
  (`internal/store/changes.go`) sets `task_id` only on insert, or on update
  when it is currently `NULL`; the merge handler (`internal/hooks/github.go`)
  re-uses the stored id rather than re-deriving it. A PR opened, and
  correlated, before the cutover keeps landing its task after.
- **What breaks, silently, with no error surfaced anywhere:**
  - A PR **opened after** the cutover from an unmigrated `lode/`-prefixed
    branch: `store.TaskIDFromRef` no longer matches that shape, so
    `UpsertPR` never sets `task_id` and the PR never correlates.
  - Branch-push attribution (`internal/hooks/push.go`, the `TaskIDFromRef`
    call that attributes commits pushed to a task branch) — same mismatch,
    same silent miss.
  - Merge-subject attribution (`internal/hooks/push.go`, the
    `taskIDsFromMessage` path that extracts a branch name out of a
    merge-commit subject) — same mismatch.
  - In every case the task simply never leaves `in_progress`; nothing logs
    an error, because nothing failed — the pattern just no longer matches.
- **`git branch -m` is local.** It renames the branch in the worktree's own
  checkout; it does **not** rename an already-open PR's head ref on GitHub.
  Migrating a worktree does not retroactively fix a PR that was opened
  against the old branch name before migration.

**Mitigation:** deploy with `LODE_BRANCH_TEMPLATE=lode/{{ .id }}-{{ .slug }}`
first (the pre-030 shape), let every open task PR land or get migrated, then
flip to the default template. Where that sequencing isn't practical, add a
`WL-Task: <id>` line to an open PR's body — `store.TaskIDFromBody` still
correlates on that marker regardless of branch shape, so it recovers
correlation without renaming anything.

A deployment that needs namespaced branches sets `LODE_BRANCH_TEMPLATE` to
`lode/{{ .id }}-{{ .slug }}` and gets the old behaviour back, correlation
included.

## 6. Testing {#sec-6}

- Template validation: accepted and rejected templates as a table, including
  each §1.2 condition.
- Round-trip: for the default template and several custom ones, render branches
  from generated task ids and assert `TaskIDFromRef` recovers the id. This is
  the property that makes §2's derivation trustworthy — it is not enough to
  assert one hardcoded pattern string.
- Path guard: paths under the default and a custom base, a flattened
  namespaced branch, a path deeper than one segment below the base, paths
  outside any base, and a base name appearing more than once.
- Directory round-trip: for each template shape, assert the directory `Dir`
  chooses clears the guard and yields the id back. Flattening is the step that
  could plausibly break this.
- Existing tests hardcoding `lode/` (lifecycle, CLI client, hookrun, worktree)
  move to the bare form.

No new metrics: this adds no endpoint, background loop, outbound call, or store
operation. Template validation failure surfaces as a startup error.
