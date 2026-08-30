# Skill integrations: superpowers and mattpocock-skills over Worklode

Design brief, revised after crit round 1. Decides how Worklode users get
`/grilling` and the superpowers design flow (brainstorm → spec → plan →
execute) with Worklode as the system of record for the documents those skills
produce and consume. Input to one or more specs; not itself a spec.

## 0. The ask

Two upstream plugins, both now in the official Claude marketplace:

- **`mattpocock-skills`** — take `/grilling` and make it aware of Worklode
  design documents, so a grilling session can read specs, ADRs and plans the
  way it reads files, and land its result as one.
- **`superpowers`** — take brainstorming, writing-plans, executing-plans and
  the visual brainstorming companion, and make them write **lode documents**
  rather than files under `docs/superpowers/`.

Neither change can land upstream. Superpowers' contributor guide rejects
"project-specific or personal configuration" and "domain-specific skills"
outright and directs them to a standalone plugin. Both plugins are MIT
licensed (Jesse Vincent 2025; Matt Pocock 2026), so vendoring is permitted with
the copyright notice and licence text carried along.

## 1. Where we are today

**Installed and enabled.** `.claude/settings.json` in this repo enables
`lode@worklode` and `mattpocock-skills@claude-plugins-official`; superpowers is
enabled at user scope and injects `using-superpowers` verbatim through a
`SessionStart` hook. Cached versions: superpowers 6.2.0, mattpocock-skills
1.2.1.

**What the upstream skills do with files.**

| Skill | File behaviour |
|---|---|
| `superpowers:brainstorming` | writes `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md` and commits |
| `superpowers:writing-plans` | writes `docs/superpowers/plans/YYYY-MM-DD-<feature>.md` |
| `superpowers:subagent-driven-development` | reads the plan file, keeps a ledger beside it |
| `mattpocock-skills:grilling` | writes nothing; dispatches sub-agents to find facts |

`grilling` is 22 lines and touches no files, which makes it the cheap half of
this work — it needs a source of facts and a stated destination, and no rewrite
of its interview logic.

**The symlink is gone (WL-147).** `docs/superpowers` was a committed symlink to
`.`, so superpowers' hardcoded paths resolved to `docs/specs/` and
`docs/plans/`. It half worked — plan filenames matched this repo's convention,
while brainstorming's `YYYY-MM-DD-<topic>-design.md` did not match a corpus
requiring `NNN-kebab-slug.md` with frontmatter and frozen `{#sec-N}` anchors —
and it has been removed ahead of the corpus cutover, since a write target that
is about to be deleted is worse than none. The authoring path in this repo is
`lode doc new`; §5's deliverables still stand for the vendored skills
themselves.

**What `lode` gives us.** Not the git → backbone sync this brief was drafted
against: `lode doc sync` and `POST /api/v1/docs/sync` were deleted, and 025 §16
and §5.1 are withdrawn. What shipped instead is 025's authoring surface, with
the backbone — not git — as the place a document lives:

- `lode doc new` / `edit` / `submit` / `accept` / `revise` — create a spec, ADR
  or plan in the backbone and move it through its lifecycle.
- `lode doc list` / `lode doc get` — list documents, and read one back with its
  body, sections and edges.
- `lode doc import` — the one-shot, admin-gated corpus walker that seeded the
  backbone from git. A migration, not a standing sync.
- `lode show WL-SPEC-25#sec-9` — renders a section, resolving the ref against
  the backbone rather than local files, so it works in a checkout holding no
  documents on disk.
- API: `POST /api/v1/docs`, `GET /api/v1/docs`, `GET /api/v1/docs/{id}`,
  `PUT /api/v1/docs/{id}/body`, `POST /api/v1/docs/{id}/submit`,
  `POST /api/v1/docs/{id}/accept`, `POST /api/v1/docs/{id}/revise`,
  `PUT /api/v1/docs/{id}/revision`, `POST /api/v1/docs/{id}/revision/accept`,
  plus the session-gated `/docs` cockpit pages. The single-doc GET returns
  `body` (frontmatter included), `sections`, `edges` and a `version` integer.

Both gaps this brief named have since closed. There is a CLI read path from the
backbone — `lode doc get`, and `lode show` reading through the same API — and
there is an authoring path, so "write a lode spec" no longer means "write a
file and sync it". What has *not* moved is the corpus of record: `docs/specs/`
and `docs/plans/` are still where these documents are edited in this repo, so
the backbone and the files coexist.

## 2. Skill inventory and conflicts

Both plugins are broad, and several skills answer the same question
differently. Adopting them wholesale means adopting the collisions. The
inventory below is the ground for a conflict pass, and the pass itself is work
this brief schedules rather than completes.

**Direct overlaps.** Each row is two skills that will both consider themselves
applicable to the same trigger:

| superpowers | mattpocock-skills | Conflict |
|---|---|---|
| `test-driven-development` | `tdd` | Same job, different loops |
| `systematic-debugging` | `diagnosing-bugs` | Same job, different loops |
| `writing-skills` | `writing-for-agents` | Same job; mattpocock's is the sharper reference and covers `CLAUDE.md`/`AGENTS.md` too |
| `requesting-code-review` / `receiving-code-review` | `code-review` | Both also collide with this repo's `/code-review` and with crit |
| `brainstorming` | `grilling` | **The important one.** Both are "interview the user before building" |

**Worklode-adjacent skills we had not counted.** mattpocock 1.2.1 ships more
than the enabled subset suggests: `to-spec`, `to-tickets`, `triage`,
`implement`, `wayfinder`, `handoff`, `domain-modeling`. `to-spec` and
`to-tickets` sit squarely on Worklode's spec → plan → task decomposition, and
`domain-modeling` writes ADRs and a glossary into a repo that already has
`ns/*.ttl` as its vocabulary.

### 2.1 The ruling {#ruling}

Settled, and it is not "pick a winner per pair":

- **Keep as-is:** `handoff` and `domain-modeling`. They do a job neither
  superpowers nor this repo duplicates, and they carry over unchanged.
- **Remix:** planning, brainstorming, writing-agent-instructions, TDD, and
  debugging. Take both upstream treatments of each and write one variant that
  beats either, customised for Worklode and the Sunstone Way. This is the
  substance of the vendoring work — the point of copying the skills in is that
  we can merge them, and the pairs in the table above are precisely the
  material to merge.

Concretely, the remix set is `writing-plans` + `to-spec`/`to-tickets`;
`brainstorming` + `grilling`; `writing-skills` + `writing-for-agents`;
`test-driven-development` + `tdd`; `systematic-debugging` + `diagnosing-bugs`.
Each merge has real material on both sides — brainstorming contributes a
phased lifecycle ending in a written document, grilling contributes a
breadth-first design tree with a recommended answer per question, and neither
alone is what we want.

**Customised for the Sunstone Way** means the remixed skills know the seven
stages and where the work sits in them, so a brainstorm for a data-science
project reaches for a Topic Intake Brief and a Gate rather than a software
design doc. That is a second axis beyond Worklode-awareness and it needs the
`sunstone-core` skills as input.

**The always-on prior.** `using-superpowers` is injected at every
`SessionStart` and says any applicable skill *must* be invoked. That prior
fights every suppression applied from outside the plugin, which is one more
reason §3 lands where it does and why §6 disables the upstream set outright.

### 2.2 Retrieval, if we route skills by relevance {#retrieval}

Holding every skill description in context does not scale, and embeddings alone
are not precise enough to replace it. The design to aim at is **hybrid
retrieval**: lexical (Postgres FTS, BM25-like) and semantic (pgvector) paths
run in parallel, fused with Reciprocal Rank Fusion in a single SQL query using
CTEs and window functions, then LLM cross-encoder reranking over the fused
candidate pool, with a score threshold and a compressed per-skill
representation before anything enters the agent's context. A worked
Postgres/pgvector + pgx implementation is in `~/notes/Notes/2026-08-17.md`.

016 §2's schema is a partial start rather than a foundation: `skill_embeddings`
chunks by `(skill_id, chunk_index)`, but there is no FTS index for the lexical
path, and the `embedding` column deliberately carries no dimension typmod —
which makes it an unindexed exact scan, so there is no HNSW index either and
adding one requires fixing the dimension first. The reranker is a new
dependency with no home in the current design.

This is its own project, larger than either track here, and it should not be
folded into the skill-integration work. What belongs here is the eval harness
(§8.2): whichever way retrieval goes, the remixed skills of §2.1 need a way to
show they beat the originals, and the same harness would measure whether hybrid
retrieval beats description-based triggering.

## 3. How you extend a skill you do not own

Claude Code has no patch or subclass mechanism for skills. Four mechanisms
exist:

1. **Namespace and compose.** Skills are addressed `plugin:skill`, so a thin
   skill can name an upstream one and add constraints. mattpocock ships the
   canonical example: `grill-with-docs/SKILL.md` is two lines — *"Run a
   `/grilling` session, using the `/domain-modeling` skill"* — with
   `disable-model-invocation: true` so it fires only on request.
2. **`skillOverrides` in `settings.json`.** A map of skill name → `"off"`,
   observed in use at user scope with bare names (`find-skills`, `to-prd`).
3. **`SessionStart` hook context injection.** How superpowers delivers
   `using-superpowers`, and how `lode` announces abandoned worktrees.
4. **Vendor and patch.** Copy the skills into this repo under our own plugin,
   edit them freely, carry the MIT notice, and re-sync on upstream release.

**Decision: vendor and patch (4).** Composition keeps the diff small but pays
for it every session: an overlay that delegates to upstream loads both skill
bodies to run one, and it cannot reach instructions baked into the upstream
body — `writing-plans` is the clear case, where the file layout is inseparable
from the writing guidance. Once one skill in the flow must be vendored, the
flow's coherence argues for vendoring all of it. Release cadence makes this
affordable, and vendoring dissolves three separate problems at once:

- No `skillOverrides` gymnastics — an unvendored skill is simply absent.
- No dependence on `skillOverrides` accepting `plugin:skill` or working at
  project scope (§8.2 was an open question; vendoring retires it).
- Version control is ours: `lode install` pins which upstream revision a repo
  gets, so a superpowers release cannot change how a repo plans work without an
  explicit bump.

**What vendoring costs, stated plainly.** We own the merge on every upstream
release, and the conflict rulings of §2 become ours to make and maintain rather
than the model's to arbitrate at runtime. A sync script comparing our vendored
tree against the upstream cache — the same shape as
`scripts/sync-codex-marketplace.py --check` — keeps the drift visible.

## 4. Track A — `/grilling` over Worklode documents

Cheap, independent of the authoring path, and useful on day one.

**What grilling needs.** It answers frontier questions from environment facts
without asking the user, dispatching sub-agents that grep the filesystem.
Against a Worklode project the facts it should reach are the specs, ADRs and
plans that govern the thing being grilled, including ones in other repos'
corpora.

**Two cases.**

- *Corpus is checked out here.* The documents are already files; grilling needs
  pointers — the corpus directories, the `WL-SPEC-N` shorthand, and `lode show`
  for section rendering. No cache, no staleness.
- *Corpus is not checked out here* — a data-science repo grilling an
  engineering spec, or any cross-project read. A local mirror is the only
  option and `.worklode/cache/` is the right place.

**`lode doc fetch`** is the second case's CLI: a front end for
`GET /api/v1/docs/{id}`, with a `--all` form that enumerates through
`GET /api/v1/docs` first, writing plain markdown into
`.worklode/cache/docs/<KEY>-<TYPE>-<n>.md`, gitignored. It should refetch
conditionally rather than unconditionally: every editable storage object
carries a monotonic `version` (or `generation`) column, the client sends the
one it holds, and the server answers with a not-modified equivalent. The doc
type already has a `version` field, so this generalises to task and plan bodies
as a single mechanism rather than a doc-specific optimisation. The triplicate
this brief warned about is gone — `model.Doc` is now the one declaration, per
ADR 036 — so there is a home to build on. Specified in 037 §7.

**Grilling writes back.** Confirmed: a settled design tree is exactly the input
to a document, and losing it to the transcript is the current waste. The
destination is named by the prompt that starts the session — a spec, a plan, a
task body, or a loose idea — and the skill writes there when the frontier
empties. That destination choice is part of the skill's contract, so it belongs
in track A rather than deferred.

**Rounds are answered in crit, not the terminal.** A round asks the whole
frontier at once, which is the right shape for asking and the wrong one for
answering: the questions scroll away while the answer is being typed, and an
answer can only be attached to its question by repeating a number. Crit's model
maps onto it directly — a round is a review round, a question is a block, an
answer is an anchored comment, and a question resolved without a comment takes
its recommendation. That last property is what pays: twelve questions with nine
good recommendations cost three comments. Crit is a separate install, so the
skill detects it and falls back to terminal rounds. Specified in 037 §6.4.

**Deliverables:** vendored `grilling` patched with the fact-source section and
the write-back contract; `lode doc fetch` with the conditional-fetch mechanism
and the `.worklode/cache/` layout.

## 5. Track B — the superpowers design flow, lode-first

**The direction is settled: `lode` is the authoring surface.** Git-first
document storage is a stop-gap. Specs and plans become Worklode objects created
and edited through `lode`, and `docs/specs/` and `docs/plans/` go away as the
place work happens. This brief therefore designs against lode-first documents
and does not preserve the file corpus.

The sequencing consequence this brief stated has since been discharged. 025 as
written made the backbone a projection of reviewed git, so lode-first authoring
needed that model amended and a write path built before these skills had
anywhere to write. Both landed: the projection model is withdrawn with §16, and
`lode doc new`/`edit`/`submit`/`accept`/`revise` is the write path. Track B is
no longer blocked on `lode` — what remains is the vendoring itself, and the fact
that this repo still edits its own corpus as files, so a skill writing only to
the backbone would bypass the reviewed corpus rather than replace it.

**The flow to rewire.** brainstorming → writing-plans → executing-plans /
subagent-driven-development, plus using-git-worktrees, requesting-code-review
and finishing-a-development-branch, which assume ownership of the branch
lifecycle `lode worktree next`/`done` already owns.

**Deliverables:**

- **Vendored `brainstorming`**, merged with grilling's question format per §2,
  whose output step creates a lode spec through the CLI: title, status,
  sections with anchors, and the frontmatter keys as document properties.
  Filename and ordinal allocation stop being the skill's problem once the
  backbone assigns identity. The visual companion carries over unchanged.
- **Vendored `writing-plans`**, emitting a lode plan with `covers:` and the
  `## Tasks` contract that plan acceptance mints from, and deferring to the
  existing `lode:splitting-specs-into-plans` skill for series.
- **Vendored `executing-plans`**, with its plan-file ledger replaced by task
  state — `lode worktree next`/`done` already hold it. Check first whether the existing
  `lode-worker` agent and `working-under-worklode` skill make this redundant.
- ~~**Retire the `docs/superpowers` symlink**~~ — done in WL-147, ahead of the
  corpus cutover rather than with it.

## 6. Delivery shape

**One plugin: `lode`.** The vendored skills join `plugins/claude/lode/`, which already
ships in this repo's marketplace, already versions with the binary, and already
carries `working-under-worklode`. Users installing `lode@worklode` get the CLI
wrappers and the design flow together.

**`lode install` owns the versions.** It already writes per-repo hooks and
settings, so it is the natural place to pin which vendored revision a repo
uses and to record the upstream revision each was taken from.

**`lode install` suppresses the upstream set unconditionally.** It writes
`skillOverrides` entries into `.claude/settings.local.json` turning off every
superpowers skill, whether or not superpowers is installed in that repo yet —
so a user who installs it later does not silently acquire a second, unvendored
copy of the flow. Pre-emptive suppression is the only ordering that works,
because the alternative depends on the user re-running `lode install` after
every plugin change.

**The mattpocock half needs the same treatment for part of its set.** The
premise that mattpocock skills are invoked explicitly holds for most of the
plugin but not all of it: 15 of its 36 skills carry no
`disable-model-invocation`, and the model-invocable ones include exactly the
collisions — `grilling`, `tdd`, `diagnosing-bugs`, `code-review`,
`domain-modeling`, `writing-for-agents`, `codebase-design`, `prototype`,
`research`, `resolving-merge-conflicts`, `wizard`. So the suppression list is
"all of superpowers, plus the model-invocable mattpocock skills we remixed",
and `handoff` and `domain-modeling` need a decision: `handoff` is
explicit-only and can stay, while `domain-modeling` is model-invocable and
would fire alongside its remixed counterpart unless kept deliberately.

Suppression lands in `settings.local.json` rather than `settings.json` because
it is machine-local state rather than a repo convention, which also keeps it
out of the committed file.

**Degradation.** Vendoring removes the soft dependency that an overlay would
have created — nothing here requires superpowers or mattpocock-skills to be
installed.

## 7. What `lode` has to grow

Four server- or CLI-side changes this design depends on. The fourth has since
shipped; the first three do not exist yet.

1. **The skill registry must key on `plugin:skill`.** `0007_skills.up.sql`
   declares `CONSTRAINT skills_name_unique UNIQUE (name)`, and `skillsync`
   takes `name` from SKILL.md frontmatter — a bare name — so two plugins each
   shipping `brainstorming` collide, and `skillsync` already warns on
   "duplicate skill name" within a source. `GET /api/v1/skills/{name}` routes
   on the same bare name. `skillhash.ValidName` permits `:` (it rejects only
   `/`, `\`, `.`, `..` and a leading dot), so the qualified form is storable
   and routable. What is missing is the qualifier itself: it has to come from
   the nearest `.claude-plugin/plugin.json`, because one source repo holds many
   plugins, and the scanner drops every file above skill-dir depth so the
   manifest never reaches the store today (037 §4.2). This is a prerequisite for shipping any skill
   whose name matches an upstream one.
2. **`lode doc fetch` with conditional refetch** (§4), generalised to a
   `version`/`generation` column on editable storage objects.
3. **`lode install` writes the suppression list** (§6) into
   `.claude/settings.local.json`, unconditionally and idempotently.
4. ~~**A lode-first document authoring path** (§5) — create and edit specs and
   plans through the API, and the 025 amendment that permits it.~~ Shipped:
   `POST /api/v1/docs` and the lifecycle routes behind `lode doc new`/`edit`/
   `submit`/`accept`/`revise`, with 025 §16 withdrawn.

## 8. Open questions

1. **What does each remix actually look like?** §2.1 settles that we merge
   rather than choose; the merges themselves are unwritten, and the Sunstone
   Way axis in particular has no draft.
2. **The eval harness.** Whichever way retrieval goes, the remixed skills need
   to show they beat both originals. Build it once and it protects every
   future vendor re-sync as well.
3. **Does `domain-modeling` survive alongside its remix?** It is kept in §2.1
   and model-invocable, so it will fire next to whatever the
   writing-agent-instructions remix becomes unless that overlap is designed
   away.
4. **Does the org skill registry (016) distribute these, or only the plugin?**
   The registry is the org-wide discovery surface; a skill that only makes
   sense inside a Worklode repo may belong in the plugin, in the registry, or
   in both with the registry as discovery.
5. **Where do vendored skills live in the tree**, and what does the drift check
   compare against once the upstream cache is version-pinned?

## 9. Proposed spec split

**The first spec was written as 037** — `037-vendored-design-skills.md`, still
`status: draft`. The second is unwritten.

- **First spec — vendored design skills.** The vendoring mechanism and drift
  check, the §2.1 remixes, `lode install`'s version pinning and suppression
  list, the registry's `plugin:skill` change, and track A end to end: the
  grilling remix, its write-back contract, `lode doc fetch` and conditional
  refetch. Ships without the authoring path.
- **Second spec — lode-first brainstorming and planning.** Track B: the
  vendored spec/plan/execution skills against lode-first documents, and
  retiring the file corpus. The 025 amendment and the authoring path it was
  gated on have both landed (§1); what still gates it is retiring the file
  corpus in this repo.

Two specs rather than one, because the first was shippable without the
authoring path.

## 10. Sources

- `~/.claude/plugins/cache/claude-plugins-official/superpowers/6.2.0/` — skills,
  hooks, `CLAUDE.md` contribution policy, `LICENSE` (MIT)
- `~/.claude/plugins/cache/claude-plugins-official/mattpocock-skills/1.2.1/` —
  36 skills under `skills/`, `LICENSE` (MIT)
- `internal/cmd/doc.go`, `internal/cmd/docrender.go`, `internal/cli/client.go`,
  `internal/api/router.go`, `internal/designdoc/resolve.go`
- `internal/skillsync/skillsync.go`, `internal/skillhash/skillhash.go`,
  `deploy/base/migrations/0007_skills.up.sql`
- `docs/specs/025-documents-in-the-backbone.md` §14, §16;
  `docs/specs/026-design-doc-queries.md` §4; `docs/specs/008-worklode-plugin.md`
  §13, §18; `docs/specs/016-org-wide-skills.md` §2;
  `docs/specs/036-one-model-across-packages.md`
- `docs/authoring-design-docs.md`
- `~/notes/Notes/2026-08-17.md` — hybrid retrieval (FTS + pgvector, RRF in one
  query, cross-encoder rerank) for §2.2
