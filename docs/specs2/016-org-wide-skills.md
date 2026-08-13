---
status: draft
requires:
- docs/specs/004-execution-backbone.md
- docs/specs/006-knowledge-graph.md
- docs/specs/008-worklode-plugin.md
- docs/specs/014-design-documents-as-graph-objects.md
---
# Spec 016 — Org-wide agent skills

## 0. Purpose & scope {#sec-0}

Org-wide coding-agent skills become a Worklode-distributed, Worklode-recommended resource.
Discovery moves from "every session pre-ingests every skill description" to **server-side
selection** (embeddings + pins) with **deterministic delivery** (task brief + local files).
This is D14 applied to skills: machinery finds and fetches; the model only judges "is this
offered skill relevant?"

Agent-neutral by construction: skills are **never registered in any native skill registry**
(no Claude plugin/marketplace entry, no per-skill description cost in session context). Any
agent that can read a file participates — activation is "read
`~/.worklode/skills/<name>/SKILL.md`", nothing more.

**v1:** git sync, embeddings + recommendation endpoint, pins, `lode skills` CLI, brief
integration. **v2:** usage feedback loop (which sessions read which skills → ranking signal,
joined via `agent_sessions`), federation beyond a simple repo list, non-skill plugin assets.

> **Prefix renamed by 014 §1.** Read every `ls:` below as `wl:` under
> `https://worklode.io/ns/ontology#`.

## 1. Registry & git sync {#sec-1}

**Source of truth stays git.** Server config lists skill source repos (e.g. `claude-plugins`)
with a ref and path globs (`plugins/*/skills/*/SKILL.md`, `skills/*/SKILL.md`). Authoring and
review stay PR-based; Worklode is an index + distributor.

**Format:** the existing SKILL.md convention — frontmatter `name`/`description` + body, plus
optional sibling files (`references/`, scripts). No new format; existing skills ingest as-is.
The org-unique skill name is the frontmatter `name`; a name collision across source repos is
an ingest error (first-ingested wins, collision surfaced in `lode doctor`).

**Sync:** webhook push → ingest changed skill dirs; `lode reconcile` polls as fallback — the
spec-013 pattern for GitHub facts. Until 013 ships, the fallback is manual:
`lode skills sync` (admin) triggers a full resync via `POST /api/v1/skills/sync`.

**Schema (backbone, Postgres):**

```sql
CREATE TABLE skills (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL UNIQUE,
    description   text NOT NULL,
    source_repo   text NOT NULL,
    source_path   text NOT NULL,
    latest_version_id bigint,          -- FK to skill_versions, set post-insert
    deleted_at    timestamptz          -- soft delete when removed from git
);

CREATE TABLE skill_embeddings (        -- latest version only; empty if no provider
    skill_id      bigint NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    chunk_index   int NOT NULL,
    embedding     vector NOT NULL,     -- pgvector
    PRIMARY KEY (skill_id, chunk_index)
);

CREATE TABLE skill_versions (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    skill_id      bigint NOT NULL REFERENCES skills(id) ON DELETE RESTRICT,
    git_commit    text NOT NULL,
    content_hash  text NOT NULL,       -- sha256 over the canonicalized skill dir
    frontmatter   jsonb NOT NULL,
    skill_md      text NOT NULL,       -- SKILL.md body, inlined for briefs + embedding
    archive       bytea NOT NULL,      -- tar of the skill dir (skills are KB-scale)
    created_at    timestamptz NOT NULL,
    UNIQUE (skill_id, content_hash)
);
```

**Versioning: git is the version.** "Latest on the source ref" is what gets recommended and
installed; the content hash pins exactness. No semver.

**Graph projection:** an `ls:Skill` node per skill (name, description, source-repo IRI) so
design docs can assert pin edges. Content and versions stay in the backbone — the graph gets
identity, not blobs (authority split, D1–D3).

Two mints, added to 006's mint set (§1.1) and its top-level disjointness axiom (§1.2):

```turtle
wl:Skill a owl:Class ;
    wl:layer wlc:execution ;       # observed and VCS-sourced, like Issue and PullRequest
    rdfs:comment "One org-wide agent skill: identity only — name, description, source-repo IRI." .

wl:recommendsSkill a owl:ObjectProperty ;
    wl:layer wlc:intent ;          # a declared design-layer pin
    rdfs:domain wl:DesignDoc ; rdfs:range wl:Skill ;
    rdfs:comment "This design document pins that skill; see §3." .

[] a owl:AllDisjointClasses ;      # 006 §1.2, extended
   owl:members ( wl:Component wl:DesignDoc wl:Section wl:Task wl:Deliverable wl:Workstream wl:Skill ) .
```

The layers split because the two facts have different owners: a Skill node mirrors git through
the backbone, so it is observed; the pin is authored in a design document, so it is declared.
`wl:Skill` carries no version, content or embedding term — those stay relational, and a skill's
IRI is `wlid:skill/<name>` (the `skills.name` unique key, per the natural-key rule in 015 §5).

The §3 task pin — a backbone field — is projectable too: `wl:requiresSkill` (Task → Skill,
execution layer) is declared in a plan's task metadata and minted onto the task at plan accept
(025 §4.1).

## 2. Embeddings & recommendation {#sec-2}

- **Provider interface:** `Embed(texts) → vectors` behind config — default an
  OpenAI-compatible HTTP endpoint (URL, model, key via env/SOPS); dimension fixed per
  instance. Local-model providers implement the same interface later. Only the server holds
  embedding credentials.
- **Storage:** `skill_embeddings` (pgvector), latest version only, **chunked**: description +
  SKILL.md body split into overlapping chunks sized to the model window, one vector per chunk.
  A skill's match score = max cosine over its chunks. Only SKILL.md is embedded — sibling
  files (`references/`, scripts) are not, so SKILL.md itself must carry the text that should
  match tasks/designs (same discipline the frontmatter-description convention already
  demands). Re-embed on content change; the corpus is dozens–hundreds of skills, cost
  negligible. Changing provider/model invalidates all embeddings (dimension/space mismatch)
  → full re-embed on config change.
- **Endpoint:** `POST /api/v1/skills/recommend` `{task_id | text, limit}` (`doc_iri`, spec
  014's document IRI, joins the same way) → server assembles query text (task: title +
  description + governing-spec excerpt — the brief's own material), embeds it, cosine top-k
  above a server-side score floor. Returns
  `{pinned: [...], matches: [{name, description, version_hash, score}]}`; pins are never
  duplicated into matches.
- **CLI:** `lode skills recommend [--task <id> | --file <path> | --text <s>] --json` — the
  one generic surface. v1 wires recommendations into `lode task brief`; other stages
  (`/lode-spec`, architectural-review) call the same CLI with no per-stage server work.

## 3. Pins {#sec-3}

- **Task pins:** a `skills` name list on the Task (backbone field, settable at
  create/update).
- **Design-doc pins:** `skills: [name, …]` in doc frontmatter, declared as
  `ls:recommendsSkill` edges when the doc is ingested (rides spec 014).
- **Brief resolution:** task pins ∪ governing-design pins. A pin naming an unknown skill is a
  brief warning, never a failure.

## 4. Distribution & local install {#sec-4}

- **Layout:** content-addressed store + name symlink —
  `~/.worklode/skills/.store/<hash>/` holds the unpacked skill dir;
  `~/.worklode/skills/<name>` symlinks into the store. Concurrent worktrees can hold
  different versions without clobbering; hash match makes re-fetch a no-op.
- **Lazy fetch:** at brief time the compiled hook fetches whatever the brief lists that is
  missing locally (`GET /api/v1/skills/<name>/archive/<hash>`). No background sync, no full
  mirror; a sandbox gets exactly what its task needs.
- **Manual:** `lode skills install <name>[@<hash>]`, `lode skills list`, `lode skills search`
  for humans, scripts, and sandbox provisioning.

## 5. Brief integration & activation {#sec-5}

`lode task brief` gains a `skills` section:

- **`pinned`** — full SKILL.md body **inline** (served from the backbone blob, so pins
  survive local-fetch failure), plus the local path for sibling files.
- **`recommended`** — name + one-line description + score + local path only. The brief
  instructs: *"read `<path>/SKILL.md` if relevant to this task."* The relevance call is the
  model's (D14: fuzzy signal → judgment, never auto-trust).

Pinned bodies count toward the brief's bounded budget; pins alone blowing the budget is a
`needs-decomposition`-style signal surfaced in the brief (D15). Injection happens wherever
the brief is injected (spec 008 hooks for Claude Code); any agent that renders the brief gets
identical behavior.

## 6. Degradation {#sec-6}

| Condition | Behavior |
|---|---|
| No embedding provider configured | Endpoint returns pins + empty matches + `provider: none`. Pins-only mode is fully functional. |
| Provider down at recommend time | Same as unconfigured: pins + warning; never blocks a claim. |
| Archive fetch fails | Recommended skills still listed with an install hint (`lode skills install <name>`); pinned content unaffected (inline). |
| Skill removed from git | Soft-deleted: never recommended; existing pins resolve with a `deprecated` warning, briefs don't break. |

## 7. Dependencies {#sec-7}

- **004 (backbone)** — new tables, pgvector extension (aligns with the backbone-postgres
  plan), webhook ingest path.
- **008 (plugin)** — brief payload extension; hook-side lazy fetch before brief injection.
- **006 (graph)** — owns the vocabulary; §1 above adds `ls:Skill` and `ls:recommendsSkill` to
  its mint set and disjointness axiom, and the projection lands under 007's deriver contract.
- **014 (design docs as graph objects)** — frontmatter-pin ingestion. Task pins work without
  it; doc pins depend on it.
- **External** — an embedding API for the default provider; GitHub webhook/API access to the
  skill source repos (existing app auth).

## 8. Open questions {#sec-8}

- **Q15.1 — Score floor tuning.** Start conservative (few, high-confidence matches); revisit
  once the v2 usage signal exists.
- **Q15.2 — Mid-task free-text recommend.** Should agents call
  `lode skills recommend --text` mid-session as things come up? Cheap to allow (same
  endpoint); decide whether the working-under-worklode skill mentions it.
- **Q15.3 — Embedding of tasks/docs.** v1 embeds the query at recommend time. If briefs get
  hot, cache task embeddings keyed by content hash — optimization only, not semantics.

## 9. Acceptance criteria {#sec-9}

1. A push to a configured skills repo makes the skill appear in `lode skills list` with a
   fresh embedding; deleting its dir soft-deletes it; `lode reconcile` catches a dropped
   webhook.
2. `lode skills recommend --text …` returns top-k above the floor; a pinned skill never
   appears in `matches`.
3. A brief for a task with one pin and ≥1 match injects the pin's SKILL.md inline and lists
   matches as name + description + path; the hook leaves `~/.worklode/skills/` correct
   (hash store + symlink) and a re-run is a no-op.
4. An instance with no embedding provider serves pins-only briefs and recommendations
   without error.
5. Two worktrees briefed against different hashes of the same skill both resolve valid local
   paths simultaneously.
6. No Worklode-distributed skill appears in any native agent skill registry; session context
   contains zero per-skill descriptions beyond what the brief carries.
