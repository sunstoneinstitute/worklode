# Similar open-source projects

Competitive survey, 2026-07-24. Question: does any open-source project already
do what worklode does — **atomic coordination of AI coding agents** plus
**specs/ADRs as first-class objects** for organizing the architecture of a
multi-repo system?

**Short answer: no single project does both.** Each pillar separately is a
crowded, fast-moving space, and the gap at the intersection is publicly
acknowledged in community discussion. Nothing found combines a shared server
with atomic task claiming, org-wide multi-repo scope, an append-only
provenance log, *and* specs/ADRs as structured objects. The closest projects
each have roughly two of the four.

Method: three parallel research sweeps (agent-coordination trackers,
spec/ADR-first tooling, community discussion on HN/Reddit) in July 2026.
Caveats: **Reddit was unreachable from the research environment** (Anthropic's
crawler is blocked at several layers), so community signal is HN-centric.
Star counts were read from rendered GitHub pages mid-sweep and are
approximate — verify before quoting externally.

---

## Pillar 1: agent coordination with atomic task claiming

Ranked by closeness to worklode's shape (shared server + atomic claim +
append-only provenance + multi-repo; per-repo markdown files = farthest).

### Tier 1 — closest shape (atomic claim + DB-backed + agent-first)

#### Beads (Steve Yegge)
- https://github.com/steveyegge/beads (canonical repo may now be under
  https://github.com/gastownhall/beads; docs https://beads.gascity.com/)
- "Distributed graph issue tracker for AI agents" — persistent structured
  memory replacing markdown plans with a dependency-aware graph for
  long-horizon agent work. The category-defining project; nearly every other
  tool here references it or was built in reaction to it.
- Storage: Dolt (version-controlled SQL DB); embedded mode or `dolt sql-server`
  mode for concurrent writers. Earlier architecture: SQLite + JSONL synced
  through git ("git IS the database" — no central server).
- Atomic claim: yes — `bd update <id> --claim` atomically sets
  assignee + in_progress; hash-based IDs avoid merge collisions.
- Scope: per-project by default; `--contributor` mode routes planning to
  separate repos. Not an org-wide shared server.
- Specs/ADRs first-class: no — epics→tasks→subtasks, not design docs.
- Activity: ~25k stars, very active. Community sentiment is notably mixed:
  recurring HN complaints about bloat ("240,000 lines") and reliability
  ("quite buggy, confuses agents"), which has spawned a wave of minimal
  clones (Trekker, ticket, beans).

#### Gas Town (Steve Yegge)
- https://github.com/steveyegge/gastown — MIT, ~17k stars
- Orchestration layer on top of Beads: coordinates 20–30 parallel Claude
  instances across multiple repos ("rigs") under one headquarters workspace.
  Work items are Beads issues bundled into "convoys"; git worktrees as
  persistent state; agent mailboxes; merge-queue "Refinery" role.
- Assignment is push-based (`gt sling` to mailboxes) rather than pull/claim.
- **The closest existing analog to org-wide multi-repo agent coordination**,
  but git/mailbox-based rather than a shared server, and widely viewed on HN
  as fascinating-but-chaotic experimental art rather than infrastructure.

#### Guild
- https://github.com/mathomhaus/guild — Apache-2.0, ~309 stars
- Shared context, memory, and task coordination across AI coding agents.
  **Single compiled Go binary + embedded SQLite + built-in MCP server** —
  nearly identical deployment shape to worklode.
- Atomic claim: yes — "atomic locks to claim tasks without stepping on each
  other" for parallel agents across different editors.
- Scope: local-first per-project (`guild init`, `~/.guild/`); not a networked
  org-wide server.
- Specs partially first-class: durable "principles" (oaths) and typed lore
  entries (decisions, observations) auto-loaded at session start; hybrid
  BM25 + vector search over shared lore.

#### Paperclip
- https://github.com/paperclipai/paperclip — MIT (reported ~75k stars —
  suspicious, verify)
- Control plane for teams/companies of AI agents with org charts, budgets,
  review gates. PostgreSQL server; org-wide, multi-tenant.
- Atomic claim: yes, emphasized — "atomic checkout with execution locks...
  no double-work and no runaway spend."
- Goal-trees with ancestry and approval workflows rather than specs/ADRs.
  Heavier-weight than a single binary; company-simulation framing.

#### agent-kanban
- https://github.com/saltbo/agent-kanban — FSL-1.1-ALv2 (converts to
  Apache-2.0 after 2 years; self-hosting allowed, competing hosted services
  prohibited), ~416 stars
- Agent-first task board; agents as first-class team members with
  cryptographic identities, roles, loadable skills.
- Atomic claim: yes, explicit — race-free claiming via Cloudflare D1 batch
  operations. Multi-repo: yes — one board tracks tasks across repositories.
- **Closest structural match on this pillar** (server + atomic + multi-repo)
  but Cloudflare-stack and not fully open-source.

### Tier 2 — claim/coordination semantics, narrower scope

- **swarm-tools** (https://github.com/joelhooks/swarm-tools, MIT, ~722 stars) —
  multi-agent swarm coordination for OpenCode. Git-backed `.hive/` +
  libSQL/SQLite with **event sourcing / append-only event logs** — the only
  other project found with worklode-style provenance. Durable locks/mailboxes.
  Per-repo.
- **swarm-protocol** (https://github.com/phuryn/swarm-protocol, MIT, ~49
  stars, alpha) — headless MCP coordination layer: intents, claims, signals;
  "No UI. No sprints. No Jira. Just state sync." PostgreSQL; conflict
  detection advisory, not enforced. Single repo per team.
- **Aqua** (https://github.com/vignesh07/aqua) — shared task queue with atomic
  claiming, file locking, inter-agent messaging; Claude Code/Codex/Gemini CLI.
  Single repo/machine.
- **Sortie** (https://github.com/sortie-ai/sortie, Apache-2.0, ~113 stars) —
  single Go binary + SQLite orchestrator that turns tickets from *existing*
  trackers (GitHub, Gitea, Linear, Jira) into agent sessions. Doesn't own the
  tracker; single-scheduler model instead of concurrent claims. Cites OpenAI
  Symphony (https://github.com/openai/symphony) as prior art.
- **Bothread** (https://github.com/AdamACE9/bothread, MIT, tiny) — local MCP
  "room"; atomic *file* claims (exclusive/shared globs) granted inside one
  synchronous SQLite transaction. Claims files, not tasks.
- **It's a Plan** (https://itsaplan.dev/, AGPL-3.0) — self-hosted issue
  tracker with agents built in; assign issues to agents like people; MCP +
  REST + webhooks. Positioned against Linear/Plane. Claim atomicity
  unverified.
- **Crew** (https://github.com/pikehouse/crew) — multiple Claude Code agents,
  each in its own worktree with assigned tasks.
- **Foolery** (Show HN, https://news.ycombinator.com/item?id=47075901) — web
  UI over Beads: dependency-aware "wave planning" into parallelizable
  batches, verification queue.

### Tier 3 — git/markdown-file trackers (mindshare competitors, farthest shape)

- **Backlog.md** (https://github.com/MrLesk/Backlog.md, MIT, ~6.3k stars) —
  markdown-native task manager + kanban (TUI/web) for any git repo;
  "one task = one context window = one PR." Notable: "docs & decisions" are
  tracked entity types alongside tasks — closest of the markdown tools to
  spec-as-object. No claim semantics.
- **CCPM** (https://github.com/automazeio/ccpm) — PRD → epic → GitHub Issues →
  parallel agents in git worktrees; issues as source of truth; conflict
  avoidance by upfront decomposition, not locking. PRDs/epics are explicit
  tracked artifacts with traceability — spec-aware, per-repo.
- **claude-task-master / Task Master** (see Pillar 2 below) — huge mindshare,
  no multi-agent concurrency story.
- **GNAP** (https://github.com/farol-team/gnap, MIT, ~72 stars) — RFC draft:
  shared git repo as task board, JSON files, git history as audit trail.
  Eventual consistency, no atomic claims.
- **Trekker** (https://github.com/obsfx/trekker, MIT, ~70 stars) — minimal
  Beads-like CLI tracker, local SQLite; built explicitly because "beads...
  codebase has grown quickly without enough care."
- Also seen: vibe-kanban (https://github.com/BloopAI/vibe-kanban, ~27.5k
  stars, **sunsetting as of July 2026** — market signal that the GUI-kanban-
  wrapper end is consolidating), openkanban, multica, Dorothy, CompanyHelm,
  ORCH, lalph, wit (function-level locking via tree-sitter), claude-flow,
  KanVibe, Emerge Factory, beans, ticket (single-file bash), Dooist,
  OpenBacklog.
- Directory worth mining:
  https://github.com/andyrewlee/awesome-agent-orchestrators

### First-party / incumbent squeeze

- **Claude Code Agent Teams** — experimental first-party shared-task-list
  multi-instance coordination (Feb 2026). https://code.claude.com/docs/en/agent-teams
- **Atlassian "Agents in Jira"** — GA May 2026 on Jira Cloud: plan specs,
  delegate to Claude/Cursor/Codex/Copilot, validate output in Jira.
- Together these commoditize the plain "queue agents can claim from" story;
  differentiation has to be more than that.

---

## Pillar 2: specs/ADRs as first-class objects

Ranked by closeness: (structured spec/ADR objects + multi-repo + agent
integration) closest; (markdown ADR templates in one repo) farthest.

### Tier 1 — structured objects + agent integration

#### mcp-adr-analysis-server
- https://github.com/tosin2013/mcp-adr-analysis-server — MIT, ~31 stars
- MCP server between agents and codebases: 73 tools to suggest implicit
  architectural decisions, generate ADRs, link code to decisions, validate
  compliance. ADRs stay markdown but are agent-queryable structured
  documents. Optional "ADR Aggregator" for **cross-repository knowledge
  graphs** — the only OSS project found doing org-wide ADR + agent
  integration. Conceptually closest to worklode's ADR pillar; negligible
  adoption.

#### Spec Workflow MCP
- https://github.com/Pimzino/spec-workflow-mcp — GPL-3.0, ~4.3k stars
- MCP server for requirements → design → tasks with a real-time dashboard.
  File-backed (`.spec-workflow/`) but with approval workflow, revision
  tracking, and lifecycle states (approval → archive). Per-repo.

#### OpenSpec
- https://github.com/Fission-AI/OpenSpec — MIT, ~62k stars, very active
- CLI + slash-command framework locking intent before implementation;
  changes archive into "living specs." Markdown-based but with a **strict
  three-phase state machine** (proposal → apply → archive) and CLI
  validation — most lifecycle-formal of the big SDD frameworks.
- **Stores (beta): planning in a separate git-shared repo enabling
  cross-repo features and shared requirements across teams** — the first OSS
  move toward multi-repo spec coordination. The single most important
  project to track on this pillar.
- Docs position explicitly against Spec Kit ("heavyweight, rigid phase
  gates") and Kiro ("IDE-locked").

#### Task Master (claude-task-master)
- https://github.com/eyaltoledano/claude-task-master — MIT + Commons Clause
  (no resale/hosting as a service), ~27.9k stars
- Parses PRDs into tasks; **most structured of the SDD tools**: tasks as
  JSON with unique IDs, dependency graph, states, subtasks, complexity
  metadata. PRD is the foundational object. Per-project, single-user; no
  concurrency story.

### Tier 2 — big SDD frameworks: agent-first, markdown-file-based, per-repo

- **GitHub Spec Kit** (https://github.com/github/spec-kit, MIT, ~123k stars) —
  the category-definer: constitution + `/speckit.specify` → plan → tasks →
  implement. Plain markdown under `.specify/`, no IDs/DB/queryability,
  single-repo. Visibly stalling by early 2026 (see sentiment below).
- **GSD / get-shit-done** (https://github.com/open-gsd/get-shit-done-redux,
  MIT) — Discuss → Plan → Execute → Verify → Ship with fresh-context
  subagents; persistent project state as markdown files. Original hit
  ~48–61k stars in months, then went unmaintained/commercial; ecosystem
  fragmented into forks — governance cautionary tale.
- **BMAD Method** (https://github.com/bmad-code-org/BMAD-METHOD, MIT, ~51k
  stars) — 12+ role agents, 34+ workflows producing PRDs/architecture/UX
  specs. Markdown + YAML, per-repo.
- **Spec Kitty** (https://github.com/Priivacy-ai/spec-kitty) — spec-kit fork
  adding kanban dashboard, per-agent git worktrees, auto-merge. Notable for
  **fusing the spec track and the coordination track** — warm HN reception.
- **Tessl** (https://tessl.io) — purest spec-as-source bet, with a hosted
  registry of 10k+ library specs. Mostly *not* OSS; commercial comparator.
- Also: MetaSpec, claude-code-spec-workflow, DinCoder, oh-my-kiro (open
  Kiro-clone with graph-based tasks.md syntax: branching, loops, parallel),
  awesome-spec-driven-development (curated list), spec-compare
  (https://github.com/cameronsjo/spec-compare — research repo comparing
  Spec-Kit/Spec Kitty/BMad/OpenSpec/Kiro/Tessl; useful secondary source).
- **Kiro** (AWS) — proprietary IDE that triggered the whole "spec mode"
  wave; the requirements.md/design.md/tasks.md triad every clone copies.

### Tier 3 — ADR management: multi-repo but no structure or no AI

- **Backstage ADR plugin**
  (https://github.com/backstage/community-plugins/tree/main/workspaces/adr/plugins/adr,
  Apache-2.0) — browse + **search ADRs across the whole org catalog**;
  discovers markdown ADRs via catalog annotations, parses MADR, indexes with
  status/type filters. The main OSS "org-wide ADR index" pattern. Zero AI
  integration; requires running Backstage — a heavy prerequisite worklode
  can position against.
- **Log4brains** (https://github.com/thomvaill/log4brains, Apache-2.0, ~1.5k
  stars) — ADRs as a static knowledge-base site; statuses inferred from text
  + git log. True multi-repo was "planned" but never shipped; self-declared
  low-maintenance mode. No AI.
- **adr-manager** (https://github.com/adr/adr-manager, MIT, ~156 stars) —
  form-based MADR editor pushing to GitHub; per-repo, no cross-repo view,
  no AI.
- **adr-tools** (https://github.com/npryce/adr-tools) — the canonical bash
  CLI; numbered markdown in `doc/adr/`. The floor of the category.
- Canonical directory: https://adr.github.io/adr-tooling/ (dotnet-adr, Talo
  — notably manages ADRs *and* RFCs/custom doc types with lifecycle —
  pyadr; all per-repo markdown CLIs, no AI).

---

## Community sentiment (HN, Nov 2025 – Jul 2026)

- **The problem is validated and loud, but the category leader is
  distrusted.** Beads' traction proves demand for agent-native work
  tracking, yet the recurring complaints (bloat, bugs, confusing agents)
  spawned a wave of minimal clones. People believe the idea, not the
  implementation — a well-engineered, boring, reliable tracker is the
  expressed unmet want.
- **The "why not Jira/GitHub Issues?" debate never settles.** Defenders of
  agent-native trackers cite local git-backed state, token efficiency, and
  atomic claiming; skeptics say your real tracker already exists. Worklode
  neutralizes this objection by construction — it sits *on* GitHub rather
  than beside it. Atlassian's Agents in Jira and Claude Code Agent Teams
  squeeze the simple-queue tools from both ends.
- **Atomic multi-agent claiming exists only in small single-repo tools**
  (Aqua, Crew, Bothread, Agent Teams). Org-level multi-repo coordination is
  essentially only Gas Town, viewed as experimental art. "Coordination
  layers — how agents interact with each other" is explicitly named as the
  next infrastructure bottleneck
  (https://news.ycombinator.com/item?id=47923668). Clearest open gap
  matching worklode's positioning.
- **SDD hype peaked and cooled.** Sep 2025 spec-kit launch hype →
  Oct 2025 criticism (verbose, review overload, "false sense of control";
  Fowler/Thoughtworks thread https://news.ycombinator.com/item?id=45610996)
  → Feb 2026 "Ask HN: Are you still using spec driven development?"
  (https://news.ycombinator.com/item?id=46864948), prompted by spec-kit
  going quiet. Survivors are lighter "spec-anchored" workflows. **Tools
  fusing specs with multi-agent execution (Spec Kitty, CCPM) get the
  warmest reception** — specs as coordination artifacts, not documentation
  ceremony.
- **Cross-repo specs/ADRs is an acknowledged hole with only hacks.** The
  standing advice is "make an architecture-registry repo the source of
  truth" — no tool in any thread treats cross-repo specs/ADRs as
  first-class, enforceable objects for agents.
- Evidence people are building toward the same product: "Show HN: I am
  building 'Jira' for AI coding agents" — SQLite tracker with verification
  gates + GitHub Issues sync (https://news.ycombinator.com/item?id=46935631).

Key threads: Beads launch https://news.ycombinator.com/item?id=46075616;
Gas Town patterns https://news.ycombinator.com/item?id=46734302;
Vibe Kanban launch https://news.ycombinator.com/item?id=44533004;
Backlog.md https://news.ycombinator.com/item?id=44483530;
Spec Kitty https://news.ycombinator.com/item?id=46515942.

---

## Cross-cutting takeaways

1. **The intersection is empty.** Agent-coordination tools with real claim
   semantics ignore specs/ADRs; spec/ADR tools are markdown-per-repo with no
   concurrency story. The only project attempting structured ADRs +
   multi-repo + agents has ~31 stars.
2. **Everything on the spec side is markdown by conviction** — the SDD wave
   deliberately rejects databases ("no lock-in"). Worklode's
   DB-backed-first-class-object approach is a genuine differentiator; the
   predictable counter-positioning is "your specs are trapped in a tracker,"
   worth having an export/git-sync answer for.
3. **Append-only provenance is rare** — only swarm-tools (event sourcing)
   and GNAP (git history as audit trail) address it at all.
4. **MCP is the de facto integration surface** for exposing tasks/specs/ADRs
   to agents (Task Master, spec-workflow-mcp, mcp-adr-analysis-server,
   Guild, swarm-protocol). The obvious interop surface for worklode.
5. **Watch list:** Beads/Gas Town (category anchor), OpenSpec Stores
   (cross-repo specs, beta), agent-kanban (server + atomic + multi-repo),
   Paperclip, Spec Kitty/CCPM (spec↔coordination fusion), Atlassian Agents
   in Jira, Claude Code Agent Teams.
6. **Category volatility is extreme** — Spec Kit went 0→123k stars and
   stalled inside ~10 months; GSD forked over governance; Vibe Kanban
   (~27.5k stars) is sunsetting. Star counts date in weeks; the durable
   value appears to be in the coordination/claim substrate, not UI wrappers.
