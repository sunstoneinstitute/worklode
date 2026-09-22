# Design queries, intents, decision decks, meetings, and attachments

This document describes the layers that sit around the base document model of 05-documents.md. It covers the derived queries over the design corpus (which specs need a plan, which plans need executing, what a spec says once its amendments are folded in), the plan-side coverage relation those queries read, the intent layer above specs, the rendering of a decision task's body as a slide deck, the meeting and minutes record, and the blob store that carries images and attachments on task bodies. The document model itself (kinds, sections, anchors, lifecycle, `doc_edges`, decision tasks) is defined in 05-documents.md and is only referenced here.

## 1. Design-doc queries

### 1.1 Where the answers come from

Every query reads the backbone's `docs`, `doc_sections` and `doc_edges` rows through the server. There is no offline corpus: a client that cannot reach the server gets an error, never a partial answer.

| Question | Answered from |
|---|---|
| What documents exist, and their status | `docs` rows |
| Which spec sections a plan covers or defers | `doc_edges` of type `covers` and `defers` |
| Whether a plan has been executed | the server-computed `closed` flag on the plan's execution task |
| What amends or replaces a section | `doc_edges` of type `amends` and `replaces` |
| Whether a claim is in force | the `status` of the claiming document (§2.3) |

Task closure is never derived client-side from a state string. `model.Task` carries a server-computed `closed` boolean, evaluated with the repo's own done-state predicate (03-tasks-and-execution.md), and every query here reads that flag. The closure read carries no `worklode_*` metric: its only outcomes are success and error. The queries run in the CLI and add no server endpoint or background loop, so they are a recorded exception to the metrics rule in 01-system-and-deployment.md.

### 1.2 `lode doc list`

```
lode doc list                       every document: kind, id, status, title
lode doc list --kind spec|plan
lode doc list --status draft|accepted|superseded
lode doc list --needs-planning      accepted specs with sections not fully planned
lode doc list --needs-execution     accepted plans with no closed execution task
lode doc list --bare-superseded     superseded documents whose sections nothing replaces
```

Filters compose. `--json` emits the same rows as objects. Every document carries a `status`; a document without one is a defect and is reported, never rendered as blank.

Each derived selector implies a status, and a contradicting `--status` is an error rather than an empty result, because an empty result would read as "nothing to do". `--needs-planning` and `--needs-execution` select disjoint kinds and cannot be combined.

### 1.3 `--needs-planning`

For an accepted spec section `S`, the query looks at every plan that is `accepted` or `superseded` and names that exact `#sec-N` anchor in `covers` or hands it off in `defers`. Draft plans never count: an unapproved split must not hide a gap.

| Outcome | Rule |
|---|---|
| fully planned | the governing spec is decomposed (§4.4) and some such plan covers `S` at `full` or `partial` |
| partially planned | some such plan covers `S`, but the governing spec is not decomposed |
| bound only | `S` is claimed only at `none` |
| deferred | no plan claims `partial` on `S`, and some plan `defers` `S` to a named owner |
| unplanned | no such plan covers `S` |

Rules:

- A superseded plan is a spent plan (accepted, then executed), so it discharges what it covered. Counting only `accepted` plans would report every shipped section as unplanned.
- A `none` claim contributes no coverage. It records that the section was read.
- A deferral discharges nothing. It names who is owed the rest, and the report prints the owner beside the anchor. A deferral is delivered when any plan discharges `S`; the owner is never a gate on who may deliver.
- An undischarged section reports the strongest status that applies: `partial` over `deferred` over `bound only` over `unplanned`.
- A whole-document `covers` (no `#sec-N`) contributes nothing and is reported.
- Overlap is legal: two plans covering one section is two plans touching it.
- A plan whose `covers` is `NO-SPEC` (§3.3) contributes to nothing and is never itself a gap.

A spec is listed when at least one current section is not fully planned, with the gap count, the anchors, and each anchor's outcome:

```
WL-SPEC-7   accepted   3/9 need planning   sec-2.4(partial) sec-4(unplanned) sec-6(deferred:WL-SPEC-6)
```

Planning coverage and implementation coverage are different questions. `covers` says someone undertook to build a section; `implements` (a component's claim in `.worklode/implements.yaml`) says the running code satisfies it. `lode doc coverage` joins the two and is defined in 05-documents.md.

### 1.4 `--needs-execution`

A plan needs execution when its status is `accepted` and either it names no execution task or the task it names is not closed. Task closure is fetched in one request for the whole project. If the server is unreachable the command fails. A `superseded` plan is spent and is never listed. A plan with no `status` is a defect.

### 1.5 `lode doc sections`

```
lode doc sections [--with-drafts] [--show-dropped]
```

The corpus-wide orientation map: every section that still states the design, across every spec, with what acts on it. A section is dropped when an effective `replaces` names it. A whole document is dropped when its status is `superseded`. Both are summarised in a footer. A section an effective `amends` names is kept and annotated.

### 1.6 `--bare-superseded`

A superseded section must carry an explanation of what replaced it (05-documents.md). A section is explained when a `replaces` edge names it directly or names its whole document. A section is bare when its document is `superseded` and nothing replaces either the section or the document. The successor's own status is irrelevant: the edge is the explanation. A `replaces` edge resolving to an external target explains nothing here.

The explanation is read only from the forward spelling, `replaces` on the successor. `isReplacedBy` in frontmatter writes no edge, so a pair whose ends disagree reports as bare. `--kind spec` narrows the answer; `--kind plan` conflicts, because a plan carries no sections.

### 1.7 `lode doc todo`

```
lode doc todo <ref> [--deps] [--json]
```

One ordered work list for a named document, joining the planning gap, the unexecuted plans, and the ordering between them. The walk goes spec → current sections → covering plans with their §1.3 outcome → each plan's execution task and its `closed` flag. Every item is typed by the act that discharges it:

| Type | Condition | Discharged by |
|---|---|---|
| `unplanned` | no plan covers the section | writing a plan |
| `partial` | covered only `partial`, governing spec not decomposed | running `lode doc decompose` |
| `plan-draft` | a plan covers it at `full` or `partial` with `status: draft` | a human accepting the plan |
| `unexecuted` | covering plan accepted, task absent or open | executing the plan |
| `blocked` | covering plan accepted, a plan it `requires` is not discharged | the blocking plan |

Rules:

- A section covered `none` yields no item at any plan status.
- A draft plan claiming `full` suppresses the section's `unplanned` and `partial` items. The pending act is accepting that plan.
- A document's `unplanned` items collapse to one item naming their anchors, and so do its `partial` items. Both rank ahead of that document's plan items.
- A superseded covering plan yields no item.
- `blocked` keys on `requires`, the key plans actually write. Nothing keys on `blocks`.
- Items sort topologically over plan `requires`; within a rank, document order over the spec's sections breaks the tie, so two runs over an unchanged corpus agree exactly.
- `--deps` follows `requires` transitively. Without it the walk stops at the named document and a footer names the unfollowed edges. The `requires` graph may contain cycles; the walk marks visited documents and reports a cycle in the footer.
- A `draft` spec emits a `plan-draft` item against the document itself, ranked first, and the walk continues. An empty list means the spec is finished, and only a finished spec may print one.
- `NO-SPEC` is an error here.
- The exit status is `0` whether or not work remains. `--json` emits the items plus the footer diagnostics as a sibling key.

The cost is one `GET /docs/{id}` per document, issued concurrently, and one task-closure request per project.

## 2. Reading a document: `lode show`

```
lode show <ref> [--inline] [--section|-s <anchor>]
```

`--section` takes `sec-3`, `#sec-3`, or the bare number `3`.

### 2.1 Reference forms

`<ref>` is a filename, a repo-relative path, a corpus number with kind (`--spec 25`, `--plan 7`), a slug (`design-doc-queries`), or the shorthand `WL-SPEC-25` (with `WL-SPEC-25#sec-9` as a shorter `--section`). A ref must match exactly one document; an ambiguous ref is an error listing the candidates. The candidates are the documents bearing the name asked for and nothing else: a number-led slug names the document whose slug it is, and where none carries it the ref names none.

A bare number is the one ref form that is not universal. At `lode show` it is a task id; at `lode doc <verb>` it is a document id. In the ref grammar it is a per-kind corpus number, so `26` naming a spec and a plan is a real ambiguity. Every other form resolves identically on `lode show`, `lode doc todo`, `lode doc show`, and `lode doc versions`. A shorthand naming another project resolves against that project's backbone docs (§3.2 tier 2). A ref with no key resolves against the current project first, then the org's documents.

A kind flag always means the local corpus and resolves by bare number even without a `project_key`. A positional shorthand whose key cannot be established is `unresolved`.

### 2.2 Inlining

Without `--inline`, `lode show` is `cat` with ref resolution. With `--inline` it folds in, under each affected section, the sections elsewhere that act on it:

- Section-scoped edges (`amends` or `replaces` targeting `#sec-N`) inline the acting section's body beneath the target. A replaced section keeps its own text and anchor, so the reader sees what was replaced beside what replaced it.
- Document-scoped edges (either end without a section) render once as a banner above the body, as references.
- Only effective claims are inlined (§2.3). A pending claim is listed as a reference marked `pending`.
- Inlining is transitive: an amendment that is itself amended is expanded.
- Every inlined block leads with its attribution on its own line, naming the acting section: `**[superseding 025#sec-11]:**`, `**[amending 012#sec-4]:**`. The marker is also the citation.
- The output opens with a banner naming every document it drew from and stating that it is a consolidated view, never a source document. Nothing writes it back.

`--section` prints one section with its whole subtree; a claim against a parent is a claim against what it contains. With `--inline`, a replaced anchor forward-resolves to its replacement's consolidation, and a split forwards to every replacement in order. A pending replacement never forwards. Without `--inline`, `--section` prints the anchor asked for whatever has happened to it.

### 2.3 A claim takes effect when its author is accepted

An `amends` or `replaces` in a `draft` document is a proposal. Its target still states the design until the claiming document is accepted. A claim from an `accepted` or `superseded` document is effective. A claim from a document outside the corpus cannot be status-checked and is trusted. `--with-drafts` treats draft claims as effective, answering what the corpus says once the open drafts land. Both directions of every edge are unioned, so a half-maintained mirror still registers the claim; the disagreement is reported separately (§3).

### 2.4 Consolidation is a fixpoint

Consolidation of a section `s`: emit `s`'s body, then for each effective section-scoped edge targeting `s`, inline the acting section's own consolidation, recursively. Backfill runs the other way as well: for each effective section-scoped edge on which `s` is the acting end, the target's consolidation is inlined beneath `s`, marked `**[amended text 006#sec-1.2]:**`. Backfill skips a target already on the current expansion path. Consolidation of a document is its preamble, then each section's consolidation in source order.

Three rules make this total and bounded:

| Rule | Effect |
|---|---|
| Deterministic order | multiple edges onto one section inline by the acting document's `issued` date, then document id, then anchor; a document without `issued` sorts first. Two effective replacements of one section is a split, and both render. |
| Body-once | within one rendering a section's body is emitted at its first occurrence; later occurrences are back-reference markers. This bounds diamonds and caps output at the size of the live corpus. |
| Cycle marker | a visited set per expansion path; revisiting a section emits `[cycle: A → B → A]`, stops, and reports the cycle as a defect. |

Because backfill makes traversal symmetric, the choice of root document changes where a body appears within a shared lineage, never whether it appears. The document stays the unit of authorship, acceptance and identity; the section lineage is the unit of reading. Raw documents stay readable; the consolidated view is the default reading path.

## 3. Reference resolution and integrity

### 3.1 Forms and defects

| Form | Resolved against | Example |
|---|---|---|
| Path or filename | its basename, less `.md`, matched as the document's slug, then by leading number | `004-execution-backbone.md` |
| Slug or number | the `docs` rows of the current project (§2.1) | `design-doc-queries`, `26` |
| Shorthand | §3.2 | `WL-SPEC-4#sec-5` |

`#sec-N` narrows any form to an anchor that must exist in the target. A reference may carry a trailing parenthetical annotation, which is stripped before resolution and otherwise ignored.

A reference that does not resolve is a defect: reported to stderr with the referring document and key, and the command exits non-zero after printing what it could compute. Silently skipping a dangling `covers` would understate `--needs-planning`. Mirror-edge disagreement (an `amends` with no matching `amendedBy`) is reported the same way and changes no answer. The one exception is a reference to a project the backbone does not know (§3.2 tier 3), which is `unresolved` and leaves the exit code alone.

### 3.2 Shorthand tiers

`<PROJECTKEY>-<TYPE>-<n>` resolves by its key, never by caller choice:

| Tier | Key | Resolved by | Unresolvable is |
|---|---|---|---|
| 1 | the current project (`project_key` in `.worklode/config.toml`) | this project's `docs` rows | a defect |
| 2 | another registered project | that project's `docs` rows, via the project list's key→project mapping | a defect |
| 3 | a key no registered project carries | shape validation alone | `unresolved: project <KEY> not known here`, exit code unaffected |

`<TYPE>` is checked against the target's `kind`; a mismatch is a defect. `.worklode/config.toml` carries `current_project` and an optional `project_key`; a checkout without the key still resolves through the backbone. The resolver never reads another repo off disk. `--strict-refs` promotes tier 3 to a defect for a CI job with the backbone reachable. A colon-form reference such as `rdf-registry:ADR-0006` parses under no tier and is reported `unresolved`.

### 3.3 `NO-SPEC`

A plan that answers to no spec writes `covers: NO-SPEC`. Spec number 0 is reserved for it in every project and carries no key: `<KEY>-SPEC-0` means the same thing and is reported so it can be corrected. It resolves to nothing without being a defect. Three constraints: it is legal only on a plan's `covers`; it is written `NO-SPEC`; and it is never a wildcard, so a governed plan names its governing sections.

### 3.4 Anchor permanence

A published anchor stays put. A document is frozen when its status is `accepted` or `superseded`. On a frozen document an existing anchor must remain, on a heading whose number still matches it. Anchors may be added (a letter-suffix insert) and bodies may change; disappearance and renaming are refused. A superseded section keeps its heading and anchor. A cycle in the section-level `amends`/`replaces` graph is refused, naming the loop. These checks run inside the accept transaction (05-documents.md), the only way an anchor becomes published. The §2.4 cycle marker is the renderer's backstop for a corpus that got past the gate.

## 4. Plan coverage

A plan is a document with no anchors of its own. It names spec sections in frontmatter and implements none of them: `covers` is the plan's undertaking, `implements` is a component's evidence, and `produces` is a task's work.

### 4.1 `covers`

```yaml
covers:
  - spec: WL-SPEC-32#sec-2
    coverage: full
  - spec: WL-SPEC-32#sec-3
    coverage: partial
  - spec: WL-SPEC-32#sec-11
    coverage: none
```

| Key | Required | Value |
|---|---|---|
| `spec` | yes | a reference with a `#sec-N` fragment. A whole-document claim says nothing a coverage query can use. |
| `coverage` | yes | `full`, `partial`, or `none` |

| Level | Meaning |
|---|---|
| `full` | after this plan executes, the section is satisfied |
| `partial` | this plan covers part of the section |
| `none` | the plan is bound by the section but builds nothing in it |

A bare string (`covers: WL-SPEC-4#sec-5.2`) means `coverage: full`. The object form is required where more than one accepted plan covers the same section, so series parts use it from the first part. A `partial` entry names no completing sibling; the sibling set is bounded by §4.4 and derived from the other plans covering the section. `implements` on a plan is an error; write `covers`. Key order: lifecycle, `covers`, `defers`, dependency, amendment, supersession.

### 4.2 `defers`

```yaml
defers:
  - spec: WL-SPEC-25#sec-12
    to: WL-SPEC-6
```

| Key | Required | Value |
|---|---|---|
| `spec` | yes | a reference with a `#sec-N` fragment: the section handed off |
| `to` | yes | the owner document, whose plans are expected to cover the section. Usually a spec; a plan is legal when the successor exists. No fragment. |

Each entry asserts that this plan explicitly defers that section to that owner. The deferring plan contributes no coverage and the section stays a gap, reported `deferred` with its owner. Rules: section-scoped (a whole-document deferral is refused); the owner is mandatory; plan-only; never to itself; one owner per section per plan. A superseded plan's deferral stands. Each entry becomes one `doc_edges` row of type `defers` with the owner as a column on the edge; an unresolvable `spec` is kept verbatim as an external reference and reads as unplanned.

### 4.3 `status` and execution task

Every plan carries `status`: `draft` while written and reviewed, `accepted` from the moment execution is authorised, `superseded` once spent. Plans take no per-section status. The tasks a plan's acceptance mints carry the plan reference (05-documents.md); the plan's execution state is read from those tasks' `closed` flags.

### 4.4 Decomposition

A `partial` claim is legal only inside a complete sibling set: every other plan or task needed to cover the parent must exist at the same time, even as a stub. What is forbidden is an unbounded remainder. A document is decomposed when every section of it carries a claim; `wl:decomposedAt` records when that was last established.

```
lode doc decompose WL-SPEC-29      # do the plans cover every section of the spec?
lode doc decompose WL-PLAN-29-1    # do the tasks implement every part of the plan?
```

The command validates and stamps, or refuses and names the sections nobody claimed. A later `partial` claim clears the stamp until `decompose` runs again. Drafting is unrestricted; accepting a plan whose governing spec is not decomposed is refused.

A section is planned when it is claimed inside a decomposed spec. A section is closed when every downstream plan and task referring to it is closed, by the same `taskClosed` predicate that serves blocking and roll-up. Tasks declare the plans they belong to and the sections they implement, so closure walks from section to task directly. With the set complete by construction, `full` and `partial` become a read hint: `full` means this document answers the section, `partial` means siblings contribute and the CLI and cockpit can list them.

### 4.5 Frontmatter checks

The document write path reports, and never rewrites:

- a `covers` entry missing `spec` or `coverage`, or carrying an unknown key;
- a `coverage` value outside the three;
- a `spec` reference without a `#sec-N` fragment;
- the bare form on a section another accepted plan also covers;
- `implements` on a plan (write `covers`);
- a `defers` entry missing `spec` or `to`, or with an unknown key; a `to` carrying a fragment; `defers` on a spec; one section deferred to two owners.

### 4.6 Ontology terms

Terms live in `ns/` (05-documents.md). All are `wl:layer wlc:execution`. A predicate is named so that the triple reads aloud as a true sentence: the plan covers the section, the task produces the deliverable, the component implements the section.

| Term | Kind | Domain → Range | Says |
|---|---|---|---|
| `wl:covers` | ObjectProperty | Plan → Section | intent: someone undertook this |
| `wl:Coverage` | Class | qualified `wl:covers`: `wl:coveringPlan`, `wl:coveredSection`, `wl:coverageLevel` (a `skos:Concept` from `wlc:CoverageLevel`: `wlc:full`, `wlc:partial`, `wlc:none`) | how completely one plan covers one section |
| `wl:decomposedAt` | DatatypeProperty | DesignDoc → xsd:dateTime | when the decomposition stamp was last established |
| `wl:defers` | ObjectProperty | Plan → Section | explicit handoff of a section the plan will not build |
| `wl:Deferral` | Class | qualified `wl:defers`: `wl:deferringPlan`, `wl:deferredSection`, `wl:deferredTo` (DesignDoc ∪ Plan) | which document owns the handed-off section |
| `wl:produces` | ObjectProperty | Task → Deliverable | work: this is what makes it exist |
| `wl:implements` | ObjectProperty | Component → Section | evidence: the code meets the section |
| `wl:cutFrom` | ObjectProperty | Artifact → Commit | the commit a release was cut from |
| `wl:status` | ObjectProperty | (DesignDoc ∪ Plan ∪ Section) → skos:Concept | document status |

The qualified nodes name both their ends and have no `Plan → Coverage` predicate. SHACL requires a `wl:Coverage`'s plan to also assert the direct `wl:covers` edge, and the same for `wl:Deferral`. No `wl:fullyImplements` and no `wl:completedWith` exist. `wl:Plan` is a top-level class disjoint from `wl:DesignDoc`. Issue and PullRequest reach a deliverable only through the task they are bound to. These triples are the contract for the document projector (07-knowledge-graph-and-search.md).

## 5. Intents

An intent is a durable statement of an outcome that owns the specs written to reach it. It is the layer above specs.

| Level | Question | Kind |
|---|---|---|
| Intent | why at all | intent |
| Spec | what | spec |
| Plan | how | plan |
| Task | who, when | task |

An intent carries exactly three things: an outcome (one sentence, stated in the world), a boundary (what is deliberately excluded), and a status (`open`, `satisfied`, `abandoned`). It is neither a milestone (a date), a tag (cannot carry a boundary or be abandoned), nor a container task (groupings are queries). Intents are few: fewer than fifteen as a working ceiling.

A spec declares exactly one intent in frontmatter:

```yaml
serves: agent-work
```

`serves:` is required on a spec. A spec that seems to need two intents is doing two jobs, or an intent boundary is wrong. The `wl:` ontology carries a class and a property for it.

| Slug | Outcome | Specs |
|---|---|---|
| `agent-work` | An agent from any harness arrives, gets the right task with the credentials and skills it needs, works it, and the session is attributed and priced. | 004, 005, 008, 012, 016, 017, 037, 041, 042, 044, 045, 046, 052, 054, 063 |
| `verified-work` | A human can accept work without reading every line, and a bug marked done is actually gone. | 057, 058, 059, 060 |
| `org-wide-visibility` | Anyone can see what the whole org is working on, across repos and projects, and find anything in it, without asking a person. | 007, 032, 040 |
| `writing-surface` | Anything written down has one home everyone can reach. Filing a note costs nothing. | 025, 026, 055, 062, 064 |
| `journalism` | A journalist runs an investigation end to end in Worklode, with no parallel toolchain. | 021, 029 |
| `queryable-facts` | Every Worklode object has an IRI that resolves and serves RDF, so a Worklode row and a knowledge-graph node are the same subject. | 002, 006 |
| `familiar-surfaces` | An agent does the setup, and every surface behaves the way whoever reaches for it next already expects. | 013, 019, 020, 061 |
| `operable-service` | Someone who did not build Worklode can run it and fix it, because the runbook lives in skills. | 001, 022, 038, 039, 053 |
| `human-attention` | What actually needs a person is in front of them, and nothing else is. | 056 |

Spec counts record where attention has gone. A one-spec intent is as likely to be an untouched area as a small one. One candidate intent, a system that changes itself, is held outside `operable-service` and gated on intents becoming enforceable.

The layer buys three queries: orphans (specs serving no intent), intent coverage (`lode doc todo` aggregated over an intent's specs), and abandonment (setting an intent `abandoned` condemns every spec under it in one act).

A decision's context and alternatives are rationale on the constraints of the spec owning its surface; a reversal is an `amends` or supersession on the section. Plans cover spec sections, never intents. Milestones are the time axis.

## 6. Decision decks

A `decision` task (05-documents.md) is the undertaking, and its `decisions` rows are the questions. A deck is that task's body rendered as slides, so the meeting's slides and its decisions are one object seen twice.

### 6.1 `mode`

A task body's front matter names its rendering engine under one key:

```markdown
---
mode: reveal
---
```

| Value | Renders as |
|---|---|
| `doc` (default) | a document |
| `reveal` | a slide deck through reveal.js |

An unknown value renders as `doc` and is reported by `lode doctor`. The server parses the front matter on every body write with `mdrender.DocMeta` and stores the result:

```sql
ALTER TABLE tasks ADD COLUMN body_mode text NOT NULL DEFAULT 'doc'
    CHECK (body_mode IN ('doc', 'reveal'));
CREATE INDEX tasks_body_mode_idx ON tasks (project, body_mode) WHERE body_mode <> 'doc';
```

`lode task list --mode reveal` uses the index. The mode picks which trusted renderer runs; every fragment of author content still passes the same bluemonday policy (§8.6). No mode introduces a markup path around it.

### 6.2 Deck body

The source format is Slidev's: slides separated by `---`, each optionally opening with its own front matter block. The first block is the body's own front matter; every later block belongs to the slide that follows. A `doc`-mode reader sees a document with thematic breaks.

Layouts are code and content is data. The sanitiser strips `class` globally, so markdown cannot carry layout. Layouts are `templ` components in `internal/ui` holding every class and grid; markdown fills their slots after the normal render-and-sanitise pass. A slide's front matter picks a layout by name and passes a small typed set of values. An unknown layout renders as `statement` and is reported by `lode doctor`.

| Layout | Holds |
|---|---|
| `cover` | deck title, subtitle, one full-bleed image |
| `section` | a numbered divider |
| `figure` | one headline number, a caption, a source line, an optional proportion bar |
| `statement` | a heading and prose; the fallback |
| `list` | an ordered or unordered list, optionally with expandable detail per item |
| `decisions` | the task's decision rows as answerable rows (§6.3) |
| `close` | a closing image and short text |

`image:` on a layout names a blob by its `/blob/<hash>` reference (§8), so deck art uses the existing blob store, rewriting and garbage collection. The brand supergraphic ovals are drawn by the layouts, never supplied as images. The set is closed and grows by amendment.

### 6.3 Components

A component is a trusted element inside a slide's prose, written as a PascalCase custom element in the markdown:

```markdown
<Compare left="Lead-time test" right="As commissioned" step="1">

- **Left:** needs only the published commissioning date

</Compare>
```

A registry in `internal/ui` holds one entry per element: allowed attributes with anchored patterns, and whether it may hold children. The registry feeds both bluemonday's allowlist and the vendored upgrade script `/assets/deck-components.js`, which finds each registered element by tag name and renders it in the browser. Rules:

- Tag matching is case-insensitive; the renderer restores the registered casing on output.
- The registry refuses any name that is an HTML element.
- Nesting is allowed where the entry says so; content between tags is ordinary markdown under CommonMark's HTML-block rule (tags on their own lines, blank line between tag and content).
- `step` (`\A[0-9]+\z`) is allowed on every registered element and maps to reveal's fragments.
- Never allowed on any element: `class`, `style`, `id`, any `on*` handler, any URL attribute except through the existing `linkHref` and `blobSrc` patterns.
- An unregistered element or unlisted attribute is stripped. The registry is closed and grows by amendment, each entry with its test.

The first entry is `<Decision key="…">`: no children, one attribute matching a row `key`, rendering that row's question and widget inline. A `mermaid` fence is a code block and is unchanged.

### 6.4 Decision rows

A `decisions` slide renders the task's own `decisions` rows in `position` order. It carries no question list. With `group:` it renders only rows whose `group` column matches. `<Decision key>` renders the same row, widget and answer anywhere in prose; a row shown twice is one row. A `key` naming no row renders a visible "no such question" marker. Adding a question is `lode decision add` on the task; the body never changes.

Each row's widget follows its own `response_type` (`yes_no` renders two radios, `single_select` its options with descriptions, `freetext` a text box, and so on for the six). The deck carries no response schema. Where a type takes notes, a Why control writes to `answer.notes`. A row shows its `question`; its `context` is hidden until expanded. A filled or hollow dot shows whether it is answered.

Answering writes through the backbone:

```
POST /api/v1/tasks/{id}/decisions/{key}/decide   {"picked": [...], "notes": "..."}
```

The handler writes `answer`, `decided_by` and `decided_at` in one transaction, and the answer that leaves no row unanswered closes the task in the same transaction. The deck keeps a `localStorage` draft of an unsent note; a failed save is shown on the row. Unanswered questions stay open rows on an open task; deferring one is re-parenting the row to the task that will decide it. Concurrent answers to one row are last-write-wins on an immutable row: the second write is refused.

### 6.5 Rendering, theme, CSP, print

`internal/mdrender` provides `Slides(keys ProjectKeys, body string) ([]Slide, error)` beside `Body` and `DocBody`. A `Slide` carries its layout name, parsed front matter, and content as sanitised `template.HTML`. `Slides` splits on top-level `---`, parses each slide's front matter as raw text, and renders the rest through the same goldmark instance and bluemonday policy as `Body`, auto-links included. `internal/ui` holds layouts and components; `internal/api` serves the page.

reveal.js (MIT) is vendored at a pinned version as two static files in `internal/ui/assets/` (`reveal.js`, 117 KB; `reveal.css`, 54 KB), served from `/assets/` with no build step. No reveal theme is used. The deck stylesheet sits on `reveal.css` in `app.css` with the Sunstone palette (Dark Blue `#0E1937`, Logo Orange Tint `#FDE6D3`, Logo Yellow `#FAD604`, Cool Grey Light `#F4F4F4`), DM Sans for UI and body, Source Serif 4 for headings, both self-hosted. A deck ignores `prefers-color-scheme` and `data-theme`. The canvas is 1280×720; type sizes are absolute within it.

The cockpit CSP (`script-src 'self'; style-src 'self'`, no `unsafe-inline`) does not change. reveal creates runtime stylesheets only in its PDF view and in `data-auto-animate`, and neither is used. A deck feature needing an inline stylesheet is dropped.

Printing uses `@media print` and `@page` for one slide per page, then appends a record: every row the deck's `decisions` slides presented, its answer, who decided and when, and its rationale, generated from the task's rows. An unanswered row prints as "not answered".

### 6.6 Metrics and authoring

`internal/api` exposes `worklode_deck_renders_total{project, outcome}` (`ok`, `parse_error`, `unknown_layout`) and `worklode_deck_render_seconds`. Answers are counted once by `worklode_decisions_total{op="answer"}`.

```bash
lode task add --kind decision --title "Approve The Grid's scope"
lode decision add WL-643 --key x-distribution --group exclude --question "Exclude distribution networks?" --type yes_no
lode task edit WL-643 --body-file deck.md
```

`lode decision list` is the plain view. Bulk-creating rows from one file is deferred. Non-goals: a presentation build tool (Slidev cannot render untrusted markdown safely; Quarto computes nothing a deck needs), decks that create questions, reproducing Figma or Slides output, `mode` on design documents, and live shared state across viewers.

## 7. Meetings, minutes, and notes

### 7.1 Entities

A `meeting` is one occurrence of a Calendar event or a manually entered event. Recurring events produce one meeting per occurrence. One meeting belongs to one project, may link to one task or decision task, and may have one `minutes` document. A `minutes` document belongs to one meeting. A `note` belongs directly to one project with no meeting. Both are working knowledge in the backbone document store, with no section anchors, acceptance, coverage, reviewer gates, plan task declarations, or task minting.

Identity: for Google Calendar, the durable key is the calendar identity plus the occurrence event ID. For Google Meet, the event's conference data resolves to the stable `spaces/{space}` resource; a Meet code is an alias. When a call starts the meeting also records its `conferenceRecords/{conference}` identity, which routes transcript and smart-notes events without Drive search.

### 7.2 Drafts and revisions

Creating minutes or a note creates one mutable working draft and no revision. Collaborative editing changes the draft. A permitted actor explicitly finishes it: finishing an empty document is refused, finishing creates immutable revision 1 and closes the shared editing session. Scheduled meeting end never finishes minutes. A correction starts one shared draft from the latest revision, and finishing it creates the next revision. At most one working draft exists per document. Readers see the latest finished revision by default and the working draft only when they may edit it. Every finish records actor and time. Prior revisions stay readable to anyone with current access. BlockNote and its collaboration transport are implementation choices behind this contract.

### 7.3 Access

The organizer of a Calendar-backed meeting must map by verified email to a Worklode actor who is a Crew member of the resolved project; this authorizes the meeting-project link. Other invitees need not be Crew. An actor may read, edit, finish, or correct minutes when they are a current Calendar invitee mapped by verified email, or hold the project's meeting-administrator permission. Invitee access is stored on the meeting and never copied onto minutes or revisions. Calendar stays authoritative for the invitee set while the event exists; removing an invitee removes access to the whole minutes history on the next synchronization, and re-adding restores it. Notes use ordinary project document permissions. An actor-level disabled state denies over every grant, invalidates sessions and tokens, and is the offboarding control; Calendar sync is not.

### 7.4 Privacy

Worklode uses Google Workspace domain-wide delegation with the narrowest read-only scopes. The current-meeting lookup impersonates only the signed-in actor and reads only a narrow time window. Before activation nothing is persisted and no event detail is exposed to another user. The cockpit shows a generic green indicator, including for private events, and reveals details only to that actor after interaction. Clicking persists nothing; Worklode shows the proposed project and metadata and creates the meeting and minutes only after confirmation. Every delegated read is audited (actor, operation, external key, time, outcome) without copying the payload. An actor may disable meeting awareness for their account.

### 7.5 Resolving project context

An event's title and description are scanned for `KEY-<n>` (task), `KEY-TYPE-<n>` (typed entity), and `Worklode project: <key-or-name>`. The grammar `\b([A-Z]+)(-[A-Z]+)?-(\d+)\b` is followed by semantic validation: the key must resolve to a project, a full reference must resolve, and the organizer must be Crew of that project. One valid result preselects its project; none requires a choice; several conflicting results require a choice and never create several links. Confirmation creates the meeting.

### 7.6 Synchronization and resources

Activation copies only the fields Worklode uses: durable identifiers, scheduled times, organizer actor, mapped invitees, conference identity, and the agenda metadata. Calendar change notifications trigger reconciliation; a periodic pass repairs missed or expired channels. The participant set is replaced atomically. If Calendar is unavailable the last confirmed set is kept and marked stale, and no access is granted from an unverified change; the UI names the last successful sync. A cancelled event marks the meeting cancelled and suppresses the indicator; minutes and revisions are kept. If the organizer leaves Crew, sync stops authorizing new meeting actions while a meeting administrator keeps recovery access.

A meeting has zero or more provider-owned resources of kind `agenda`, `transcript`, or `smart_notes`, each with meeting, kind, provider, external ID, optional title, body and URL, provider generation time, and last update time. Provider plus kind plus external ID make ingest idempotent. An activated Calendar event gets one read-only agenda resource (title, description, event URL) that Calendar changes replace. Meet transcript and Gemini smart-notes events, subscribed through the stored space, upsert a resource with provider ID, title, generated time, and Google Docs URL. Those URLs render as metadata beside the minutes, never in the body, and create no revision. Drive stays the content and sharing authority.

### 7.7 Surfaces and events

The cockpit polls a bounded current-meeting endpoint for the signed-in actor. The indicator opens an activation view that shows only what this actor may see, resolves context hints, and asks for confirmation; confirmation atomically creates or finds the meeting and its draft and opens the editor. The editor shows working versus finished state with explicit `Finish minutes` and `Start correction` actions and shows resources outside the body. From a project context, minutes creation preselects that project. A notes surface uses the same editor for a project note.

API and CLI operations: query the current meeting without persisting it; activate an event; create manual minutes or a note; show a meeting with participants, links, resources and sync status; edit a draft collaboratively; finish and start a correction; link or unlink a task or decision; disable or enable meeting awareness. Document creation and reading also expose notes and minutes; their lifecycle actions are separate from submit and accept.

The event log records activation, link changes, participant-set changes, cancellation, resource upserts, draft starts, and finishes. Replay is idempotent and payloads exclude raw Calendar and transcript content. The RDF projection (07-knowledge-graph-and-search.md) types meetings, documents, actors, projects and resources as nodes with organizer, project, linked task or decision, minutes, participants, times and resources; participation is a time-varying observed relation with Calendar provenance, and provider identifiers stay literals.

Deferred: creating Calendar events from Worklode, automatic activation, copying or indexing transcript text, generating minutes from transcripts, a general attachment system for non-meeting documents, user-defined kinds or properties, and a personal knowledge workspace.

## 8. Images and attachments on tasks

Blobs serve two jobs. An embedded blob is an image or video the body cites inline and is rendered in place. An attached blob is a file (log, HAR, dataset, core dump) that is downloadable and never rendered. Every attachment is a blob; only image and video blobs are embeddable. Attachments on design documents are not built; `blobs` is unscoped so a later `section_blobs` table can reference the same bytes.

### 8.1 Storage

Bytes live in Hetzner Object Storage (S3-compatible, path-style). Postgres holds the index and reference graph. The object key is a pure function of the hash, sharded two hex characters deep: `blobs/<hash[0:2]>/<hash>`.

```sql
CREATE TABLE blobs (
    hash       text PRIMARY KEY,   -- sha256, lowercase hex
    media_type text NOT NULL,      -- server-sniffed
    size       bigint NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE TABLE task_blobs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hash       text NOT NULL REFERENCES blobs(hash) ON DELETE RESTRICT,
    filename   text NOT NULL,
    embedded   boolean NOT NULL DEFAULT false,  -- derived: the body cites /blob/<hash>
    attached   boolean NOT NULL DEFAULT false,  -- declared: lode task attach
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, hash),
    CONSTRAINT task_blobs_referenced CHECK (embedded OR attached)
);
CREATE INDEX task_blobs_hash_idx ON task_blobs (hash);
```

`embedded` is recomputed on every task create and update from the body's `/blob/<hash>` references, in the same transaction. `attached` is set by `lode task attach` and cleared by `lode task detach`, and survives body edits. A row with neither flag is deleted by reconciliation. `ON DELETE RESTRICT` makes a GC bug an error instead of a broken image.

### 8.2 Reference syntax and serving

Bodies store a root-relative permanent URL: `![alt](/blob/9f2a…c1)`. `GET /blob/{hash}` authenticates, then 302-redirects to a presigned object-storage URL with a 5-minute lifetime; the redirect carries `Cache-Control: private, max-age=60` and `Referrer-Policy: no-referrer`. Bytes never pass through the application. The route sits outside `/api/v1`.

| Header on the presigned response | Source |
|---|---|
| `Content-Type` | set as object metadata at upload and overridden per request via `response-content-type` from `blobs.media_type` |
| `Content-Length` | the object store's own |
| `Content-Disposition` | `response-content-disposition`: `inline` for embeddable types, `attachment` otherwise, plus `filename="…"` when the reference names one |
| `Cache-Control` | `response-cache-control: private, max-age=31536000, immutable` |

The filename travels with the reference. Every surface that lists a reference appends `?filename=crash.log`, and the route echoes it into the disposition, RFC 6266-encoded and cleaned (control characters stripped, last path segment only, invalid UTF-8 or over 200 bytes refused). Body text is never rewritten this way, so an embedded `](/blob/<hash>)` keeps matching the anchored grammar that pins it against GC. The parameter is unsigned on our side; it changes neither the bytes, the media type, nor the inline/attachment token.

### 8.3 Surfaces

| Surface | Purpose |
|---|---|
| `POST /api/v1/blobs` | streamed raw upload; returns `{hash, media_type, size, url}` plus `poster_url` for a video. Idempotent: identical bytes return `200` and the existing row. |
| `GET /blob/{hash}` | authenticate, then redirect to a presigned URL |
| `GET /api/v1/tasks/{id}/blobs` | list a task's blobs: hash, filename, media type, size, embedded, attached |
| `POST /api/v1/tasks/{id}/blobs` | attach an uploaded hash (`attached = true`) |
| `DELETE /api/v1/tasks/{id}/blobs/{hash}` | clear `attached`; the row goes if `embedded` is also false |
| `lode task attach <id> <file>…` | upload, then attach. Images also get `![<basename>](/blob/…)` appended to the body; videos get a `<video src poster controls preload="metadata">` element. `--no-embed` suppresses the append. |
| `lode task attach <id> -` | one blob from stdin |
| `lode task detach <id> <hash>` | the inverse; warns if the body still embeds it |
| `lode task add --body-file`, `lode task edit --body-file` | reference rewriting (§8.5) |
| `lode blob gc [--apply]` | both GC sweeps; reports unless `--apply` |

### 8.4 Auth

`/blob/{hash}` serves both a browser `<img>` (session cookie) and a CLI or agent fetch (bearer token) through one `eitherAuth` middleware that mirrors the web UI's auth model: with a web auth provider configured, `401` with neither credential; with none configured, `401` unless `LODE_WEB_OPEN` is set. An unknown bearer token is `401` on every instance. The refusal is a status code, never an HTML page or login redirect. The content hash is deduplication, never access control. The bucket stays private; presigned URLs are the only anonymous read path. Session cookies are `SameSite=Lax`, so a cross-site `<img>` gets `401`.

### 8.5 Upload and reference rewriting

Upload streams and never buffers: `http.MaxBytesReader` at `maxBlobBytes = 100 MiB`, spool to a temp file in `LODE_BLOB_SPOOL_DIR` through a `TeeReader` into sha256, capture 512 bytes for `http.DetectContentType`, then look up the hash. Present: discard the temp file and return the existing row. Absent: `PutObject` from the rewound file, then insert the row. Write order is object-then-row, so failure leaves an orphan object (swept by §8.7), never a dangling row. The client `Content-Type` is never trusted or stored. Nothing is rejected on type.

| Class | Types | Treatment |
|---|---|---|
| embeddable image | `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/svg+xml` | rendered inline; `inline` |
| embeddable video | `video/mp4`, `video/webm` | rendered via `<video>` with a poster frame; `inline` |
| attachment | anything else | never rendered; `attachment` |

A video upload also runs `ffmpeg` over the spooled file before the dedup check and stores the first frame as an ordinary JPEG blob, returned as `poster_url`. Without `ffmpeg` the video is stored and `poster_url` is omitted; `worklode_video_poster_extractions_total` records which happened. SVG is embeddable deliberately; a script inside it runs in the object store's origin and an SVG loaded through `<img>` runs no script.

Reference rewriting: `lode task add --body-file` and `lode task edit --body-file` walk the parsed markdown for image destinations that are local relative paths (no scheme, no leading `/`), resolve them against the body file's directory, upload each, and rewrite the destination to `/blob/<hash>` before the body is sent. Only images are rewritten; a local file link is a warning. Absolute paths and schemes are untouched. Traversal above the body file's directory is an error. A missing file fails the whole command before the task is written. `--body` does not rewrite. `--no-upload` opts out.

### 8.6 Serving hardening and rendering

Per-object hardening is `Content-Disposition` alone: no CSP and no `X-Content-Type-Options` are set on a served blob, because S3 exposes no `response-*` override for them and user metadata becomes `x-amz-meta-*`, which no browser acts on. Uploads send no user metadata. The redirect lands in a different origin from the app, so a hostile payload executes there. The task page CSP lists the object-storage endpoint in `img-src` and `media-src`.

Task bodies are untrusted (imported issue text lands in `tasks.body`). The web UI renders with goldmark (GFM, `html.WithUnsafe()` so raw HTML reaches the sanitiser) then bluemonday `UGCPolicy()` with tightenings: `img[src]`, `video[src]`, `source[src]` must match `^/blob/[0-9a-f]{64}$`; `a[href]` limited to `http`, `https`, `mailto` with `rel="nofollow"`; `video` allowed with `controls`, `preload`, `poster`. Every page carries a CSP: `default-src 'self'`, `script-src 'self'`, `style-src 'self'`, `img-src` and `media-src` plus the blob origin, `object-src`, `base-uri`, `frame-ancestors` `'none'`, `form-action 'self'`. Form POSTs pass a same-origin check (`Sec-Fetch-Site`/`Origin`) on top of the `SameSite=Lax` cookie (10-cockpit.md). Board and project pages show titles only.

The CLI rewrites `/blob/…` to `<server>/blob/…` so URLs are clickable, and `lode task show` lists attachments under the body (filename, size, media type, URL). Glamour renders an image as alt text plus link. An authenticated browser hand-off (`lode task show --web` opening a logged-in tab via the CLI auth flow) is the deferred v2; inline terminal images are not planned.

`lode task brief --json` gains a `blobs` array, each with absolute `url`, `filename`, `media_type`, `size`, and `embedded`, fetchable with the agent's bearer token (08-agent-harness-and-sessions.md). Alt text stays in the body.

### 8.7 Garbage collection

`lode blob gc` runs two sweeps, each with a 24-hour grace period.

- Unreferenced blobs: a `blobs` row with no `task_blobs` row sharing its hash and older than the grace period. The row is deleted first, inside a transaction that re-checks the zero-reference condition, then the object. When `section_blobs` exists, the `NOT EXISTS` grows a second clause.
- Orphan objects: any key under `blobs/` whose hash has no `blobs` row and whose `LastModified` is older than the grace period. The implementation lists serially; the two-character shard allows a 256-way split later.

Reporting is the default and `--apply` deletes. Deleting a task cascades its `task_blobs` rows.

### 8.8 Mirroring on import

`POST /api/v1/inbox/promote` is where an issue body first becomes `tasks.body`, and it fetches every remote image reference and rewrites it to `/blob/<hash>` through the upload path. The repo named must map to a known project, and the project's own installation token is sent only to `user-images.githubusercontent.com`, `private-user-images.githubusercontent.com`, `raw.githubusercontent.com`, and the path prefix `github.com/user-attachments/` (judged on the decoded, dot-segment-resolved path). SSRF guards: `https` only, host allowlist `*.githubusercontent.com` and `github.com`, IP checked against private, loopback, link-local and metadata ranges on every hop, at most 3 redirects, `maxBlobBytes` cap, 30-second timeout. On failure the original URL stays and is logged; the renderer drops it rather than beaconing.

### 8.9 Configuration

| Key | Example |
|---|---|
| `LODE_BLOB_ENDPOINT` | `https://hel1.your-objectstorage.com` |
| `LODE_BLOB_BUCKET` | `sunstone-worklode-blobs` |
| `LODE_BLOB_REGION` | `hel1` |
| `LODE_BLOB_ACCESS_KEY` / `LODE_BLOB_SECRET_KEY` | 1Password via ESO (02-identity-actors-and-secrets.md) |
| `LODE_BLOB_SPOOL_DIR` | temp-file directory; defaults to `os.TempDir()` |

`aws-sdk-go-v2` with `UsePathStyle: true`. With no endpoint set, uploads return `501` and everything else is unaffected; `lode doctor` reports the absence. Once endpoint and bucket are set, both keys are required and a missing one fails startup.

## Sources

WL-SPEC-26 (design-doc queries and plan coverage), WL-SPEC-70 (intents), WL-SPEC-62 (decision decks), WL-SPEC-64 (meetings, minutes, notes), WL-SPEC-21 (images and attachments on tasks).

## Open questions

- 070 Q13: what makes an intent enforceable rather than descriptive; every boundary today is a sentence.
- 070 Q14: the ranking function behind `human-attention`.
- 070 Q15: whether spec 007's drift detection stays under `org-wide-visibility` or feeds `human-attention`.
- 070 Q16: whether one intent per spec survives in lightly worked areas.
- 070 Q17: whether a unit of work can be an investigation rather than a ticket.
- 070: `journalism` has no boundary; `familiar-surfaces` has no persona tie-break rule.
- 021 Q021.1: alt text as an accessibility contract (`--alt` flag or lint), v2.
- 021 Q021.3: bucket per environment or prefix per environment.
- 021: presigned `response-*` overrides are unverified against a live Ceph RGW bucket (WL-206, blocked by WL-802).
- 062: bulk-creating a meeting's decision rows from one file.
