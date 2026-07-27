# Spec 008 — Worklode plugin (Claude Code integration)

**Status:** spec · **Umbrella:** `000-umbrella-architecture.md` · **Source:** D13, D14, D15 ·
**Depends on:** 004 (execution backbone, worktree-bound leases), 005 (`claim --next`, `concern`/`focus`)
**Amended by:** 014 (design docs as graph objects)

> **Prefix renamed by 014 §1.** Read `ls:governs` / `ls:affects` below as `wl:governs` / `wl:affects`.

## Purpose & scope

The Claude Code integration for Worklode: how an agent (or human in an agent session) picks up
work, holds it, and puts it down — with coordination pushed entirely into deterministic machinery
so the model only spends tokens on judgment. Covers the worktree-bound lease lifecycle, the compiled
hook binaries, `lode task brief`, the slash commands, the judgment skills, and the optional worker
subagent.

**Out of scope (consumed, not defined here):** the ranking function and `claim --next` internals
(005), the lease transaction and Postgres schema (004), the graph model and projection (006). This spec
*calls* `lode task claim --next` and `lode task brief`; it does not implement them.

## Design lens (D14)

**Push coordination into deterministic, token-free machinery; spend model tokens only on judgment.**
Three applications, in force throughout:

- **CLI over MCP** — agents drive `lode --json`; no per-tool schema tokens in context, deterministic
  greppable output.
- **Hooks over prompts** — lease renewal, resume, and release are compiled hooks firing on editor
  events, not instructions the model must remember to follow.
- **Server-side selection over agent reasoning** — `claim --next` ranks and leases in one
  transaction (005); the agent never lists-picks-claims. The user can still pick a **specific**
  task — `lode task claim <id>` (or `/lode-next <id>`) claims that one directly; `--next` is just
  the default when no id is given.

The division of labor: **machinery** does acquire / bind / renew / resume / release / context-assembly.
The **model** does only done / block / release-judgment and design authoring. Everything a hook can do
deterministically, a hook does.

## CLI naming (D13)

Product = **Worklode**. CLI = **`lode`**. Slash commands and skills use the `lode-` prefix.

## Worktree-bound lease lifecycle

**The worktree is the unit of Worklode work. The lease binds to the git worktree, not the session.**
This is the core mechanic and the reason Worklode is strictly opt-in: a plain Claude Code session,
in a normal checkout, never touches Worklode. You enter Worklode mode *only* by claiming, which spins
up a worktree.

**Deterministic naming.** A claimed task's worktree is named `wt/<id>-<slug>` — derived from the task
*after* the lease is held (the id and slug are known only post-claim). The name is a pure function of
the task, so any hook can map worktree ↔ task ↔ lease with a path parse, no lookup table.

Lifecycle:

1. **Acquire** — `/lode-next` → atomic `claim --next` (005) returns the leased task → create worktree
   `wt/<id>-<slug>` → **bind the lease to that worktree** (backbone records the worktree path/id on the
   lease) → inject `lode task brief`.
2. **Hold** — the lease lives and dies with the worktree, *not* the session. Sessions open and close;
   the lease persists while the worktree exists. Renewal is the commit-cadence heartbeat (below), not
   a timer the session must service.
3. **Resume** — a lease can **expire** (the sweeper reclaims a stalled worktree per 004). Because the
   worktree still exists and its name is deterministic, `/lode-resume` (or the `EnterWorktree` /
   `SessionStart` hooks) re-acquires *that same task's* expired lease trivially. Re-acquiring your own
   stalled worktree is safe and distinct from auto-*claim*, which stays opt-in.
4. **Release** — `ExitWorktree` / worktree removal → **auto-release**. Removing the worktree is the
   canonical "I'm done holding this" signal; `/lode-done` and `/lode-block` also release explicitly.

**Guard invariant:** *no bound worktree ⇒ no Worklode behavior.* Everything keys off the deterministic
worktree name and the backbone's worktree→lease binding.

## Hooks

Every hook is a **NOP outside a Worklode worktree** (uniform guard: parse cwd for a `wt/<id>-<slug>`
worktree with a backbone-bound lease; absent ⇒ `exit 0` immediately). Worklode is invisible to ordinary
sessions.

| Event | Action | Guard / condition |
|---|---|---|
| `EnterWorktree` | **Auto-resume** — re-acquire the bound lease | In a Worklode worktree **and** no already-running session for it **and** the bound lease has **expired**. (Safe: re-acquiring your own stalled worktree.) |
| `SessionStart` (in worktree) | **Resume** — inject `lode task brief <id> --json`; re-acquire lease if expired | In a Worklode worktree. **Resume-only — never a silent claim** (Q14.3). |
| `SessionStart` (outside worktree) | **Offer** to resume an abandoned worktree | Cheap compiled scan finds a worktree with a bound, **expired** lease and no running session. Prompt, if any, is Haiku/script-level — never an expensive call. Offer only; never auto-claims. |
| `PreToolUse` on `git commit` | `lode task renew` (commit-cadence heartbeat, D8) | NOP when the session isn't on a Worklode task. |
| `ExitWorktree` / `SessionEnd` | **Release** the worktree's lease **if idle** | In a Worklode worktree with a held lease. |

Notes:

- **Auto-resume vs. offer-to-resume.** *Inside* a Worklode worktree, entering/starting re-acquires the
  bound lease automatically (it's provably yours). *Outside*, the session only gets a cheap *offer* to
  adopt an abandoned worktree — acquisition stays a deliberate act.
- **Heartbeat, not timer.** Renewal is driven by `PreToolUse` on `git commit` (D8): renew right before
  each commit-batch. No session-side timer; a stalled session simply stops committing and its lease
  ages out to the sweeper.
- **`SessionStart` outside-worktree scan must stay fast** — a compiled `lode` scan of existing worktrees
  plus lease state; no graph reads, no model call beyond a script-level Haiku prompt.

## Hook implementation & coexistence

- **Compiled Go binaries.** Hook executables are compiled Go (fast startup, no interpreter warmup on
  every editor event). They share the `lode` codebase; the hook binary is effectively `lode hook <event>`.
- **Daisy-chain, don't terminate.** Each hook takes `--next <cmd> [argv…]`; instead of `exit(0)` it
  `execve`s the next command, passing the hook payload through. This lets Worklode's hook compose with
  whatever hooks a repo already has rather than owning the event.
- **`lode install`** wires the commit-cadence heartbeat for **editor-agnostic** use (plain
  `git`, not just Claude Code). It **must coexist — chain existing hooks, never clobber them** (Q14.2):
  an existing `.git/hooks/pre-commit` is preserved and invoked via the `--next`/`execve` chain.
- **Sensible default:** if `.pre-commit-config.yaml` exists in the repo root, **always chain
  `pre-commit`** — Worklode's heartbeat runs, then `execve`s the `pre-commit` framework so its checks
  still run. Installer is idempotent (detect an already-installed Worklode link; re-point, don't
  duplicate).

## `lode task brief <id> --json`

> **Amended by 014.** The brief carries the governing **Spec section** (a `wl:Section` node, bounded by construction), not a Spec/Plan excerpt.

**Deterministic context assembly — the biggest single token win.** One bounded, machine-assembled
payload replaces the model spelunking through files to reconstruct what a task is about. Consumed from
005/006; this spec specifies what the plugin injects and when.

Payload (bounded, JSON):

- **Task** — id, title, `concern`, `priority`, `needs-decomposition` flag, current status.
- **Governing design** — the excerpt of the governing **Spec/Plan** (via `ls:governs`, 006), not the
  whole doc.
- **Affected components** — the `ls:affects` component set (006).
- **Definition-of-done** — the declared Deliverable target (D7).
- **Branch** — the worktree's branch / `wt/<id>-<slug>` name.

Injected by `SessionStart`/resume and by `/lode-next` right after claim. **No file spelunking** — the
brief is the context contract. If the brief is insufficient, that's a signal the task needs
decomposition (D15), not that the agent should go reading the repo.

## Slash commands

| Command | Action |
|---|---|
| `/lode-next [--project P] [--strict-focus]` | Atomic `claim --next` → create `wt/<id>-<slug>` → bind lease → inject brief. The one way to *enter* Worklode mode. |
| `/lode-resume` | Re-acquire an **expired** lease for an existing worktree (the "came back after the sweeper reclaimed it" case). |
| `/lode-done` | Mark the task done (Deliverable met) → release lease. Worktree cleanup per finishing-a-branch flow. |
| `/lode-block <id>` | Record a blocking dependency / mark blocked → release lease so the frontier reflects reality. |
| `/lode-status` | Show the current worktree's task, lease state, and heartbeat freshness (read-only). |
| `/lode-spec` | Enter graduated design authoring (D15) — produce only the artifacts the task warrants; see *authoring-design-as-graph*. |

> **Amended by 014.** `/lode-spec` produces {ADR, Spec, task subtree} — never a Plan document.

Renewal has **no** slash command — it's hook-enforced (heartbeat). Commands are thin wrappers over
`lode … --json`; the judgment lives in the skills, not the commands.

## Skills (judgment only)

> **Amended by 014 §2.** There is no Plan artifact: the graduated output is {nothing, task subtree, Spec/ADR}, and durable rationale is promoted into a Spec or ADR before the tasks close.

Skills carry only what needs model judgment; anything deterministic is a hook or CLI call.

- **`working-under-worklode`** — the done/block/release judgment loop. *When* is a task actually done
  (Deliverable met, not "code written")? *When* to block vs. push through? *When* to release a worktree
  vs. keep it. Explicitly **does not** cover renewal (hook-enforced) — the skill tells the model *not*
  to think about heartbeats.
- **`authoring-design-as-graph`** — graduated to task complexity (D15): most tasks need only a **Plan**,
  some need a **Spec/ADR**, many need neither; do not ceremony-tax small tasks. Get **crit** review;
  write the **declared-layer** edges (`ls:governs`, `ls:affects`, `dct:requires`/`hasPart`) into
  the graph (006). Sets `needs-decomposition` when projected context would blow the ~100k budget (D15),
  routing to decomposition-as-a-Worklode-task.
- **`architectural-review`** — reads the **knowledge graph** (existing ADRs/Specs/Components and their
  edges, 006) to review a new design against the *existing* architecture: pushes back to keep the new
  work aligned, or surfaces when the architecture itself must change. **Modeled on the `grill-with-docs`
  skill pattern** (challenge a plan against the documented domain model + decisions, sharpen terms,
  update docs as decisions crystallise) — but **fed from the graph, not loose files**: the glossary and
  ADR set are graph queries, and resolved decisions are written back as graph edges rather than only
  Markdown. This is a first real payoff of ADRs/Specs being first-class graph objects.

Decomposition itself reuses existing **superpowers** skills (`writing-plans`, `brainstorming`,
`subagent-driven-development`), re-emitting the results as `lode` tasks with `concern` + `priority`.

## Subagent (optional)

**`lode-worker`** — a headless subagent for 24/7 unattended loops: `claim --next` → work → `done`/`block`
→ repeat, with no human in the loop. Safe *because* the coordination is deterministic — the atomic
`claim --next` (no list→pick→claim collision window, D8), the worktree-bound lease, and the
commit-cadence heartbeat mean many workers can run in parallel on a well-spec'd project without
stepping on each other. Optional in v1; the plugin works fully in interactive sessions without it.

## No MCP in v1 (Q14.1)

v1 is **CLI + hooks only**. No MCP server. Rationale: MCP would put per-tool JSON schemas into every
agent's context (token cost) for no coordination benefit over `lode --json`, whose output is
deterministic and greppable. An **MCP shim is deferred** to a later spec for *non–Claude-Code* clients
that can't drive a CLI + editor hooks; it would wrap the same `lode` commands, not replace them.

## Dependencies

- **Spec 004** — worktree-bound lease model (bind/expire/sweeper/release), task state machine, events.
  This spec assumes the backbone can record a worktree→lease binding and expire it.
- **Spec 005** — `lode task claim --next` (atomic rank+lease), `concern`/`focus`, `--strict-focus`,
  `needs-decomposition` sizing and the ~100k budget.
- **Spec 006** — `lode task brief` content (`ls:governs`/`ls:affects` edges, Deliverable/definition-of-done)
  and the declared-layer edges the authoring skill writes.
- **External** — Claude Code hook events (`EnterWorktree`, `SessionStart`, `SessionEnd`, `ExitWorktree`,
  `PreToolUse`), the `--next`/`execve` daisy-chain contract, and (optionally) the `pre-commit` framework.

## Open questions

- **Q008.1 — Worktree removal vs. `/lode-done` ordering.** When a session `/lode-done`s, does the plugin
  remove `wt/<id>-<slug>` itself, or leave removal to the human/finishing-a-branch flow? Auto-release
  fires on removal either way, but *who removes* affects whether a done-but-unmerged worktree lingers.
- **Q008.2 — `EnterWorktree` availability across editors — RESOLVED: acceptable for v1.** Auto-resume
  is Claude-Code-only; non–Claude-Code editors and the plain-`git` path get the `install`
  heartbeat (renewal) but not auto-resume. Degraded coverage is accepted for v1.
- **Q008.3 — "No already-running session" detection.** Auto-resume and offer-to-resume both need to know
  whether a worktree already has a live session. Confirm the backbone (004) exposes a cheap liveness signal
  (e.g. a session-holds-worktree marker) the compiled hook can read without a model call.
- **Q008.4 — Task ↔ GitHub Issue mirror** *(new; from 007 review).* The plugin creates and keeps the
  GitHub Issue mirroring a backbone Task in sync (backbone-authoritative), so a PR's native `Closes #N`
  drives the PR→Task join (spec 007). Open: create the Issue on task-create or on first PR; how the
  hook authenticates to the GitHub API; reconciling manual Issue edits.

## Acceptance criteria

1. `/lode-next` in a repo produces a `wt/<id>-<slug>` worktree with a backbone-bound lease and an injected
   brief; the same flow in a plain checkout (no claim) leaves the session completely untouched.
2. Removing the worktree (or `ExitWorktree`) auto-releases the lease; a `git commit` inside the worktree
   renews it; neither fires outside a Worklode worktree.
3. A worktree whose lease the sweeper expired is re-acquired by `/lode-resume` (and by `EnterWorktree`
   auto-resume) with no new claim and no collision.
4. `lode install` in a repo with an existing `pre-commit` hook installs the Worklode heartbeat
   **and** still runs the pre-existing hook (and chains `pre-commit` when `.pre-commit-config.yaml` is
   present); re-running is idempotent.
5. `lode task brief <id> --json` returns one bounded payload (task + concern/priority + governing
   Spec/Plan excerpt + affected components + definition-of-done + branch) sufficient to start work
   without reading repo files.

> **Amended by 014.** The criterion reads "governing Spec **section**"; there is no Plan excerpt to assemble.

6. Hooks are compiled binaries that daisy-chain via `--next`; a downstream hook still runs after
   Worklode's.
7. The three skills exist and carry judgment only (no renewal logic in `working-under-worklode`;
   `architectural-review` reads the graph, not loose files).
