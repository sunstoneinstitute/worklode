# Spec 07 — Skills registry, task↔skill binding & friction loop

**Status:** spec · **Umbrella:** `00-umbrella-architecture.md` ·
**Depends on:** 01 (events, task rows), 02 (`claim --next`, D15 sizing), 03 (`ls:` vocabulary,
projection), 04 (drift queries), 05 (brief injection, hooks) · **Cross-repo:** claude-plugins
(skill source of truth), data-platform (graph hosting + embedding store, 06), agent-feedback
(vent stream, v2).

## Purpose & scope

Make org skills first-class Worklode objects so that (a) a task can explicitly name the skills
an agent should use, (b) the server suggests those skills and a human reviews the suggestions,
(c) skill versions stay consistent with the claude-plugins release machinery, and (d — **v2**)
the friction agents report against skills flows back as maintenance work. Skills stop being a
static, always-loaded catalog and become task-scoped context, assembled deterministically.

**In scope (v1):** the skill catalog and its ingestion from claude-plugins version tags; the
`ls:Skill` graph entity; the `usingSkill` binding with its suggest → review → accept lifecycle;
embedding-based suggestion; front-loading bound skills through `lode task brief`.
**In scope (v2):** friction-vent ingestion, aggregation, and the maintenance-task policy —
specified here for coherence, but **not gating the v1 release**.

**Out of scope (consumed, not defined here):** the brief injection mechanics and hook events
(05), the projection pipeline and IRI grammar (03), drift query plumbing (04), the
`report-friction` emit side (claude-plugins — unchanged by this spec), and skill *distribution*
to Claude surfaces (org plugin sync and the skill-zips pipeline stay as they are).

## Design lens (D14)

Same division of labor as 05 — machinery coordinates, the model judges:

- **Selection is server-side.** Embedding search and ranking run on the backbone; the agent
  never scans a skill catalog in-context.
- **Binding is human judgment.** Suggestions are *offers*; a human accepts or rejects them.
  Nothing auto-binds, mirroring "offer to resume, never auto-claim" (05).
- **Loading is deterministic.** Bound skills ride the same hook-injected brief the session
  already receives — not trigger matching, not model initiative.

## The problem with the static catalog

Every installed skill costs ~100 tokens of always-loaded description in every session,
org-wide, and Claude Code has no built-in search or deferral for skills (only for MCP tools).
At the current ~50 skills that is ~5k tokens per session; it grows linearly, and past a hundred
skills the model's ability to pick the right description degrades before the token cost hurts.
Worse, trigger-based loading is probabilistic — precisely the coordination-by-hope D14 rejects.
For Worklode work the task itself is the best retrieval signal: the brief already knows the
title, concern, components, and governing design. Skills join that assembly.

## Skill catalog & versioning

**claude-plugins is the source of truth.** Skills live as `plugins/<plugin>/skills/<skill>/`
in the marketplace repo; Worklode holds an ingested copy, never an editable one.

**Version = plugin version.** Skills are not versioned individually; the marketplace bump
machinery tags `<plugin>-<version>` on every merge. A skill's version is its plugin's version,
which keeps Worklode's numbers identical to what the org actually installs.

**Ingestion on version tags.** The GitHub App (already worklode's ingest path) subscribes to
tag-create events on claude-plugins. On a `<plugin>-<version>` tag:

1. Fetch the plugin subtree at that tag (contents API; no clone).
2. Parse `plugin.json` and each `skills/*/SKILL.md` frontmatter + body.
3. Upsert the backbone catalog: `skills(plugin, skill)` identity plus
   `skill_versions(plugin, skill, version, description, body, body_sha, surfaces, tagged_at)`.
   An unchanged `body_sha` (a sibling skill drove the bump) records the version row but skips
   re-embedding.
4. Request embeddings for changed skills (data-platform call, below).
5. Project to the graph: upsert the `ls:Skill` node with `dct:hasVersion` = latest version.

A plugin description containing `DEPRECATED` marks all its skills deprecated in the catalog
(mirrors the skill-zips rule); deprecated skills are excluded from suggestion but existing
pinned bindings keep resolving.

Bootstrap: `lode skills sync` performs the same parse against a repo ref for initial load and
webhook-gap repair (idempotent by `(plugin, skill, version)`).

## Graph entity (03 extension)

Mint **`ls:Skill`** as a standalone class — deliberately **not** a `ls:DesignDoc` subclass.
Subclassing was considered and rejected: `rdfs:subClassOf` is an is-a claim, and a skill is
not a design document. A DesignDoc asserts *what to build* and moves through a
draft→accepted lifecycle; a skill prescribes *how to work* and is versioned by release tags.
What they share (markdown-in-repo content with an RDF descriptor, component coverage) is a
common *pattern*, not ancestry — so it is modeled as parallel properties, not inheritance:

```turtle
ls:Skill a owl:Class ;             # practice doc: "how to work on X"; content in claude-plugins
    ls:layer lsc:intent .

ls:covers a owl:ObjectProperty ;   # skill → component it teaches how to work on
    rdfs:domain ls:Skill ; rdfs:range ls:Component ;
    ls:layer lsc:intent .
```

`ls:Skill` joins the top-level AllDisjointClasses set (Component / DesignDoc / Task /
Deliverable / Workstream). Lifecycle needs no SKOS scheme: a live skill is simply present at
its current `dct:hasVersion`; a `DEPRECATED` plugin marks its skills with the standard
`owl:deprecated true` annotation. Spec 04's staleness query ("component changed materially
since its governing doc was last updated") gains a sibling clause over `ls:covers` — same
machinery, one more property in the pattern.

IRI: `lsid:skill/<plugin>/<skill>` (ADR-0006 grammar; instances under `/id/…`).

**Binding property — mint `ls:usingSkill`:**

```turtle
ls:usingSkill a owl:ObjectProperty ;              # task → skill it should be executed with
    rdfs:domain ls:Task ; rdfs:range ls:Skill ;
    ls:layer lsc:execution .

ls:atVersion a owl:AnnotationProperty .           # version pin, RDF-1.2 triple-term annotation
```

The version pin annotates the `usingSkill` triple (`<< :task ls:usingSkill :skill >>
ls:atVersion "1.6.0"`), the same RDF-1.2 mechanism 03 uses for `ls:supersededSection` —
no SkillVersion node bloat. `ls:covers` edges are ingested from a `components:` frontmatter
list when present (optional in v1 — suggestion works without it; staleness detection is what
it buys), authored as part of skill review in claude-plugins.

Authority split (D2/D3): the catalog and bindings are **backbone-owned** (bindings are
execution facts consumed transactionally at brief time, like Task itself, D11); `ls:Skill`
nodes and `ls:usingSkill` edges are projections. No dual ownership — everything derives from
claude-plugins events and backbone writes.

## Suggestion → review → accept

**Embedding index (decided: self-hosted, Lance on the data-platform).** One vector per skill
(latest version): description + body, embedded at ingest by a **self-hosted embedding model**
on the data-platform and stored there as **Lance files** (06). The backbone stays authoritative
for the catalog; Lance holds only derived vectors, rebuildable at any time by re-embedding from
catalog rows. The interface is two calls the backbone makes to the data-platform: `embed` (at
skill ingest and once per suggestion query) and top-k cosine search over the index. Scale is
small (10²–10³ vectors), so a modest open model behind an internal endpoint suffices.

**Suggestion.** On task create and on material edit (title/description/decomposition), the
server embeds *task title + description + governing Spec/Plan excerpt* and stores the top-k
(default k=5) catalog matches above a similarity floor as **suggestions** with scores.
Suggestions are visible (`lode task skills`, web UI) but inert — they never reach the brief.

**Review.** A human accepts or rejects:

```
lode task skills <task-id>                    # list: accepted bindings + pending suggestions with scores
lode task skills accept <task-id> <plugin:skill>[@version]
lode task skills reject <task-id> <plugin:skill>
lode task skills repin  <task-id> <plugin:skill>@<version>
```

Accept writes the binding backbone-side (`task_skills(task_id, plugin, skill, version,
accepted_by, …)` + a provenance event) and projects `ls:usingSkill`/`ls:atVersion`. The
version defaults to latest-at-accept; `repin` is the explicit upgrade path. Bindings pinned to
a version the plugin has since superseded surface in a standing query (04 pattern) — re-pinning
stays a human act, aided by `repin`. Rejects are remembered so re-suggestion doesn't nag.

The natural review moments are task refinement and `/lode-spec` authoring —
*authoring-design-as-graph* (05) gains one step: confirm or adjust the suggested skill set
alongside the asserted edges. Direct accepts of unsuggested skills are always allowed;
embeddings propose, they don't gate.

## Front-loading through the brief (05 extension)

The `lode task brief` payload (05) gains one section:

- **Skills** — the task's *accepted* bindings, each `{plugin, skill, version, base_path,
  body}` with the SKILL.md body inlined at the pinned version.

**Mechanism: the existing injection path.** Bound skills enter context exactly the way the
brief itself does — injected by the `SessionStart`/resume hooks and by `/lode-next` right after
claim (05). This is the most reliable loading mechanism Claude Code offers: hook-injected
context is unconditional and deterministic, where skill *triggering* is a model decision
against a description.

**Explicitly rejected: placing bound skills into `.claude/skills/`.** Placement only makes a
skill *available for triggering* — loading would again be probabilistic, per-session scans may
not even notice mid-session additions, and a repo-local copy can skew against the org-installed
plugin version. A binding is a reviewed instruction to *use* the skill, so its body goes
straight into the context window.

**Supporting files.** `lode` materializes each bound skill's full directory (references/,
examples/, scripts/) at the pinned version into the worktree under
`.lode/skills/<plugin>/<skill>/`, excluded from git via `info/exclude` (worktree-local, not a
repo `.gitignore` change). The injected body is prefixed with its `base_path` so the skill's
relative references resolve; references and scripts stay on-demand — progressive disclosure is
preserved for everything but the body. The preamble also notes that these skills are already
loaded, so the model does not additionally invoke a marketplace copy that may be installed at
another version (the pinned, injected copy governs for the task).

**Budget (D15 tie-in).** Marketplace rules aim skills at ≤3k words (~4k tokens hard worst
case, most far smaller), so the 16k-token inline budget comfortably fits any plausible
reviewed set — overflow is an exceptional signal, not a routine guard. A task whose reviewed
skill set genuinely blows the budget is oversized by definition: set `needs-decomposition`
(02/D15) rather than trimming silently; the reviewer can also drop bindings. Overflow is
reported at accept time (`lode task skills` shows the running total), not discovered at claim.

## Friction loop (v2 — does not gate the v1 release)

v1 ships the catalog, bindings, and front-loading only. The emit side already runs org-wide
and keeps accumulating signal in the agent-feedback stream in the meantime, so v2 ingestion
starts with history rather than from zero. When built, skills become the first surface where
Worklode closes emit → aggregate → act → measure:

- **Emit (unchanged).** Agents vent via the `report-friction` skill; reports carry
  `surface_id` (`plugin:skill`) and `plugin_versions`. The emit-side promise — fire-and-forget,
  never individually actioned — stands; action attaches to *aggregates* only, which keeps
  report quality honest.
- **Ingest.** Worklode consumes vents as a fourth source beside the GitHub App, Flux, and the
  pod watcher, storing them as backbone events keyed by surface_id, category, severity, and
  plugin version. Transport (Q07.3): **piggyback on the existing OTel collectors** — the org
  already runs an external traces/logs endpoint, and a vent maps naturally onto a structured
  log record (body = report text; attributes = the frontmatter fields), so the emit side
  ships OTLP into the collector and worklode consumes from that pipeline instead of growing a
  second public ingest surface.
- **Aggregate.** Standing queries: friction density per skill per version
  (severity-weighted); skills whose `ls:covers` components changed since their last version
  (staleness, via 04); bindings pinned to superseded versions.
- **Act.** A skill crossing the density threshold (tunable; severity-weighted count per
  window) auto-creates a maintenance task — kind `chore`, in a standing *skill-maintenance*
  `ls:OngoingMaintenance` workstream, `ls:usingSkill` bound to the offending skill, with the
  anonymized report bodies attached so the brief hands the fixer the actual evidence. Creation
  is idempotent per (skill, window): one open task per skill, later vents append. The task
  enters `ready` like any other; nothing auto-claims it. The fix is an ordinary claude-plugins
  PR; the merge tag re-ingests the new version and closes the loop.
- **Measure.** Because vents carry versions and the catalog has them, friction-rate
  before/after a version bump is a single query — the empirical check that a skill fix
  actually fixed anything.

## Dependencies

- **Spec 01** — event log for vents and binding provenance; task rows for `task_skills`.
- **Spec 02** — `needs-decomposition` (budget overflow); ranking picks up maintenance tasks
  with no special casing.
- **Spec 03** — `ls:` additions above ship in the same rdf-registry PR; projection pipeline
  extended with Skill nodes, `ls:covers`, and `ls:usingSkill` edges.
- **Spec 04** — standing-query machinery for staleness (over `ls:covers`), superseded pins,
  and (v2) friction density.
- **Spec 05** — brief payload and injection hooks carry the skills section; `/lode-spec`
  review step.
- **Spec 06 / data-platform** — the self-hosted embedding service and the Lance vector store
  (06 §Skills embedding store).
- **External** — claude-plugins tag workflow (`bump-versions-on-merge.yml`) as the version
  signal; GitHub App tag events; (v2) the OTel collector pipeline as vent transport (Q07.3).

## Open questions

- **Q07.1 — Skill vs DesignDoc — RESOLVED: standalone class.** `rdfs:subClassOf` would claim
  a skill *is* a design document; it isn't — it only shares patterns with one. `ls:Skill` is
  standalone with a minted `ls:covers`; staleness gets one extra clause in 04.
- **Q07.2 — Embedding provider — RESOLVED: self-hosted.** A self-hosted model on the
  data-platform; vectors stored as Lance files there. No external embedding vendor.
- **Q07.3 — Vent transport (v2).** Confirm the OTel piggyback specifics: the vent→log-record
  attribute mapping, how the collector routes vents to worklode (dedicated pipeline/exporter
  vs worklode reading the log store), and whether `vent.py` emits OTLP directly or the
  agent-feedback service forwards into the collector.
- **Q07.4 — Suggestion refresh triggers.** Create + material-edit is spec'd; should
  decomposition of a parent re-suggest for children automatically?
- **Q07.5 — Non-Code surfaces.** Bindings can't be front-loaded on web/desktop/cowork (no
  hooks, no `lode`); those surfaces keep the static skill-zips path. Accepted degradation for
  v1 — Worklode work is `code`-surface work.
- **Q07.6 — Threshold tuning (v2).** Initial friction-density threshold and window; start
  conservative (high threshold) and lower it once report volume is understood.

## Acceptance criteria

**v1:**

1. Tagging `<plugin>-<version>` in claude-plugins yields, without manual steps, updated
   catalog rows, embeddings for changed skills, and an updated `ls:Skill` node; `lode skills
   sync` from a clean database converges to the same state.
2. Creating a task with a descriptive title produces suggestions with scores; none appear in
   the brief until accepted; accept/reject/repin round-trip via `lode task skills` and emit
   provenance events; `ls:usingSkill` with `ls:atVersion` appears in the graph on accept.
3. `/lode-next` on a task with accepted bindings injects the pinned SKILL.md bodies in the
   brief and materializes `.lode/skills/…` at the pinned versions — with no copy under
   `.claude/skills/` and no reliance on skill triggering; a task with no bindings gets a brief
   without a skills section.
4. Accepting bindings past the inline budget flags the task `needs-decomposition` and reports
   the overflow at accept time.
5. A component change after a skill's last version surfaces that skill in the staleness
   standing query (given its `ls:covers` edges exist).

**v2 (friction loop):**

6. Vents ingest as events via the OTel pipeline; the density query aggregates by skill and
   version; crossing the threshold creates exactly one open maintenance task per skill with
   reports attached, and a subsequent version tag allows the pre/post friction-rate query to
   run.
