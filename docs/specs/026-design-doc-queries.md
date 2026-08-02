---
status: draft
issued: 2026-08-02
requires:
  - 014-design-documents-as-graph-objects.md
  - 019-project-scoping.md
  - 025-documents-in-the-backbone.md
amends:
  "#sec-4.2":
    - 019-project-scoping.md#sec-2
---
# Spec 026 — `lode doc` queries over the document corpus

## 0. Why {#sec-0}

Three questions come up constantly while working this repo:

1. Which specs still need a plan?
2. Which plans still need executing?
3. What does this spec actually say, once its amendments and supersessions are folded in?

Every one of them is a deterministic function of the corpus, and every one of them is
currently answered by an agent opening 25 specs and 40 plans and reasoning about YAML.
That is expensive, non-reproducible, and wrong often enough to matter — the audit that
prompted this spec had to caveat itself as "strong evidence, not proof".

Spec 025 §10 already reserves the surface (`lode doc list --needs-planning`,
`--needs-execution`, `lode doc show --resolved`) and 025 §7 already fixes the semantics.
What 025 does not do is make them available before the backbone document store exists —
its own implementation is a long way out, and these queries are needed to plan it.

This spec implements the read-only subset of 025 §10 against the git mirror that
`docs/specs/` and `docs/plans/` still are, using `internal/designdoc`. The verb names,
flags, and semantics are 025's, so when the store lands the change is the data source and
nothing else.

**In scope:** `lode doc list`, `lode doc show`, `lode doc sections`, corpus loading and
reference resolution in `internal/designdoc`, one new pre-commit script (§4.1), the
frontmatter keys the queries depend on, tests.

**The commit-time gates stay Python scripts and `lode` is never one of them.** A git hook
must run in a fresh checkout, mid-rebase, before anything is built; depending on a build
artifact makes the gate fail exactly when the tree is in the state worth checking. So
`secfmt.py` and `secindex.py` keep their jobs, §4.1's permanence check joins them as a third
script, and the Go commands are the **read surface only** — they report the defects they
encounter so a query never quietly lies, but nothing here makes them a precondition for
committing.

`scripts/currentspec.py` is different: it is a reporting tool rather than a hook, and it
established the semantics §3 and §4 adopt — most importantly that a claim is only effective
once the claiming document is accepted. It retires into `lode doc sections`.

**Out of scope:** anything that writes a document (`doc new`, `doc accept`, `doc revise`),
`doc coverage` (it needs the implementation side, `.worklode/implements.yaml` — §9), the
backbone `docs` tables, graph projection.

---

## 1. Where the answers come from {#sec-1}

| Question | Answered from | After 025 |
|---|---|---|
| What documents exist, and their status | frontmatter in the git tree | `docs` rows |
| Which spec sections a plan claims | `implements` in plan frontmatter | `doc_edges` |
| Whether a plan has been executed | `task` in plan frontmatter, resolved against the tracker | the root task bound to the doc |
| What amends or replaces a section | `amends`/`amendedBy`/`replaces`/`isReplacedBy` maps | `doc_edges` |
| Whether a claim is in force yet | `status` of the *claiming* document (§3.1) | `docs.status` |

Only the third needs the server, and it uses the existing task API — this spec adds no
endpoint, no background loop, and no store operation, so it adds no `worklode_*` metrics.
Everything else is local file reading and answers offline.

The corpus root is the directory holding the repo-local `.worklode/config.toml` (spec 019's
walk, which already ran to resolve the project), with `specs/` and `plans/` under `docs/`.
`--docs <dir>` overrides it. No new config key: a repo whose docs live elsewhere passes the
flag, and there is exactly one such repo today (none).

## 2. `lode doc list` {#sec-2}

```
lode doc list                       every document: kind, id, status, title
lode doc list --kind spec|adr|plan
lode doc list --status draft|accepted|superseded
lode doc list --needs-planning      accepted specs with sections no plan claims
lode doc list --needs-execution     accepted plans with no closed execution task
```

Filters compose. `--json` (the root persistent flag) emits the same rows as objects.

The default listing shows `status` as `—` for a document that carries none, which is how a
plan that predates §5 is visible without a separate selector.

### 2.1 `--needs-planning` {#sec-2.1}

Plans and sections are many-to-many, and the query assumes nothing else: one plan may claim
a whole spec, five plans may split it, and one plan may claim sections of several specs.
A spec section is **planned** when *any* plan's `implements` names it — `014-…md#sec-6`
names that section, a whole-document `014-…md` names every section the document has,
present and future — and the union across all plans is what the query takes. A spec is
listed when it is `accepted` and at least one of its sections is in no plan's union; the
count and the unplanned anchors are the output.

Nothing here counts plans per spec or expects a partition. Overlap is legal and unremarked:
two plans claiming the same section means two plans touch it, not a modelling error.

```
docs/specs/000-umbrella-architecture.md   accepted   6/6 unplanned   sec-1 sec-2 sec-3 …
```

`--needs-planning` implies `status: accepted` — planning follows acceptance (025 §3), and a
draft spec is not a planning gap. Combining it with a conflicting `--status` is an error
rather than an empty result, because an empty result would read as "nothing to plan".

**Known weakness, stated rather than papered over.** No plan in the corpus today carries a
section-scoped `implements` — all 36 claim whole documents — so on the current tree this
query lists exactly one spec (000, which no plan implements) and is otherwise vacuous. A
whole-document claim is a coverage assertion that can never go stale: sections added by
later amendment are covered retroactively by a plan written before they existed. The fix is
authoring, not code — `docs/authoring-design-docs.md` gains a line requiring section-scoped
`implements` on new plans (§6) — and the query is a forward guard that becomes sharp as that
convention takes hold. Widening it by guessing which sections a plan "really" covered (from
dates, from prose) would trade a truthful empty answer for a confident wrong one.

The convention is not new: the other side of the same claim, `.worklode/implements.yaml`
(014 §6), is **already section-scoped** — its entries key on `section:` with a pinned
document version, precisely so a claim narrows to the part of the spec it satisfies. Plan
`implements` naming whole documents is the outlier, and bringing it into line makes the two
halves of coverage — what was planned, and what was built — addressable at the same
granularity. Without that, `lode doc coverage` (§9) could never join them.

### 2.2 `--needs-execution` {#sec-2.2}

A plan needs execution when its frontmatter carries `status: accepted` and either it has no
`task` key, or the task it names is not in a closed state (`closedStates`,
`internal/store/tasks.go`). Task states are fetched in one request; the command fails rather
than degrading if the server is unreachable, since a plan whose task state is unknown is
exactly the case the caller is asking about.

Plans carrying no `status` are legacy — authored before §5 required one — and are never
listed by this selector. That is deliberate: the alternative is backfilling forty files with
a status nobody reviewed, or reporting twenty-five already-shipped plans as outstanding
work. They remain visible in the default listing with `status: —`, so the omission is
discoverable, and §6 makes `status` part of the plan authoring checklist so no new plan
falls into the legacy bucket.

### 2.3 `lode doc sections` {#sec-2.3}

The corpus-wide companion to §3: every section that still states the design, across every
spec, with what acts on it.

```
lode doc sections [--with-drafts] [--show-dropped]
```

A section is dropped when an effective `replaces` (§3.1) names it, and a whole document is
dropped when its own status is `superseded`; both are summarised in a footer rather than
silently vanishing. A section an effective `amends` names is kept and annotated — an amended
section still states the design, just not alone. This is `scripts/currentspec.py`'s output
contract, and the script's tests become this command's.

It is the orientation map for anyone entering the corpus: one screen naming where each
subject is decided, in place of grepping 26 files, and the entry point to the consolidated
reading path of §3.2.

## 3. `lode doc show` {#sec-3}

```
lode doc show <ref> [--resolved] [--section <anchor>]
```

`<ref>` is a path, a bare filename, a spec number (`014`, `014-design-documents`), or 014
§11.3's shorthand (`WL-SPEC-14`, and `WL-SPEC-14#sec-2.1` as sugar for `--section sec-2.1`)
that matches exactly one document; an ambiguous ref is an error listing the candidates.
A shorthand naming another project is reported unresolved rather than fetched — §4.2's tier 2
is dormant until 025. Without
`--resolved` this is `cat` with ref resolution, which is worth having only because it takes
the same ref forms as everything else.

`--resolved` inlines, under each affected section, the text of the sections elsewhere that
act on it:

- **Section-scoped** entries — `amendedBy: {"#sec-2": [012-…md#sec-4]}` — inline
  `012 §4`'s body beneath §2. `isReplacedBy` inlines the same way, and the original body is
  kept: a superseded section keeps its text and its anchor
  (`docs/authoring-design-docs.md`), so removing it here would hide what the replacement
  replaced.
- **Document-scoped** entries (`"."`, or a value with no fragment) are rendered once as a
  banner above the body, as references rather than inlined text. A doc-wide amendment
  already carries a prose note next to the heading it affects (018's is the model), and
  inlining a whole document under another document produces something nobody reads.

Only **effective** claims are inlined (§3.1); a pending one is listed as a reference marked
`pending`, so a draft spec's proposed amendment never reads as settled design.

Inlining is **transitive** (§3.2). An amendment that is itself amended is expanded, not
listed as a reference: a view that stops partway shows a less-outdated version of the
design, which reads as current and is therefore worse than not rendering it at all.

Every inlined block leads with its attribution on its own line, so borrowed text is never
mistakable for the document's own:

```markdown
**[superseding 014#sec-6]:**<br>
<the inlined body>

**[amending 012#sec-4]:**<br>
<the inlined body>
```

The marker names the acting section, which is also its citation — a reader who wants the
source has it without a lookup. It renders in the terminal and on the web page (§9) without
a second format.

The rendering is a read view: it is never written back, and no flag writes it. It opens with
a banner naming every document it drew from. **The result is a consolidated view, not a
spec** — a spec is a source document, and this is computed from many of them; the output
says so in its header.

`--section <anchor>` prints one section, its subsections, and — with `--resolved` — that
section's consolidation. This is the cheap form: an agent that needs `011 §4` should not pay
for 011. On an anchor that has been replaced it **forward-resolves**, printing the
consolidation of what replaced it, so a citation that has gone stale still yields current
text rather than superseded text or an error.

### 3.1 A claim takes effect when its author is accepted {#sec-3.1}

An `amends` or `replaces` in a **draft** document is a proposal. Its target still states the
design until the claiming document is accepted, so `--resolved` and `lode doc sections` mark
such a claim `pending` and leave the target intact. A claim from an `accepted` or
`superseded` document is effective; so is one from a document outside the corpus, which
cannot be status-checked and is trusted rather than dropped.

`--with-drafts` treats draft claims as effective, which answers the other useful question:
what the corpus says once the open drafts land. Both readings are legitimate and neither is
safe as the only one — spec 025 is draft today, and reading its amendment of 018 as settled
would have the corpus describe a task kind that does not exist.

Both directions of every edge are read and unioned (`amends` with `amendedBy`, `replaces`
with `isReplacedBy`), so a half-maintained mirror still registers the claim; §4 reports the
disagreement separately rather than letting it change the answer.

### 3.2 Consolidation is a fixpoint, not a depth-bounded walk {#sec-3.2}

Fragmentation is intrinsic to an append-only corpus with frozen anchors: every amendment
must leave scar tissue in the source, and that is exactly what keeps a pin like
`006#sec-1.2` — or an `implements.yaml` claim against it — true forever. The source
documents will get messier over time by design. What was missing is a derived read path, not
a different corpus shape, and it is defined here.

**Consolidation of a section `s`** — emit `s`'s body, then, for each effective
section-scoped edge targeting `s`, inline the acting section's *own consolidation*,
recursively. Where `s` is replaced, `s`'s body is kept and each replacement's consolidation
is inlined beneath it, marked: a superseded section keeps its text and its anchor, so the
reader sees what was replaced next to what replaced it. Document-scoped (`"."`) edges stay
banners and are never inlined — they carry no section-shaped payload.

**Consolidation of a document** — its preamble, then each section's consolidation in source
order.

Three rules make that total and bounded without a depth limit:

- **Deterministic order.** Multiple edges onto one section are inlined by the acting
  document's `issued` date, then filename, then anchor. Two effective replacements of one
  section is a *split*, not a defect: both are shown, ordered and attributed.
- **Body-once.** Within one rendering a section's body is emitted at its first occurrence;
  every later occurrence is a back-reference marker. This is what bounds a diamond — one
  amendment targeting two sections, or two documents amending one — and it caps output at
  the size of the live corpus rather than at the size of the walk.
- **Cycle marker.** A visited set along each expansion path; revisiting a section emits
  `[cycle: 014#sec-1 → 0NN#sec-2 → 014#sec-1]` and stops, and the cycle is reported through
  §4's defect machinery. Acting edges point new→old, so cycles are near-impossible today and
  become constructible under revision; the visited set is cheaper than the incident.

Because every emitted body is either live or shown beside its successor, the choice of root
document changes only the narrative order, never the content — entering spec 006's
consolidation and entering 014's surfaces the same live text. That is what makes "start from
the newest document touching a subject and backfill" and "start from the oldest and follow
forward" the same answer, so neither needs to be a mode.

**A subject needs no new concept.** The replaces/amends edges already partition live sections
into lineage chains, and the head of a chain is what "the newest document on this subject"
means. Nothing is stored to represent it (025 §1).

**Why the document stays the unit of authorship.** Going section-centric was considered and
rejected: a section's identity is `<doc>#sec-N` (014 §3), its staleness test rides on
document-level versioning (`wl:lastRevisedIn`, 014 §4, which deliberately avoids per-section
version namespaces), and acceptance, review anchoring, status and provenance are all
document-grained — 025 §2 stores `doc_sections` as children of `docs` rows. Free-floating
sections would need their own version counters, their own accept gate, and a rename of every
inbound reference, to solve what is a rendering problem. The document remains the unit of
authorship, acceptance and identity; the **section lineage** becomes the unit of reading.

Raw documents stay readable — authoring, review, and "what did this document originally
claim" all need them. What changes is the default: `lode doc sections` for orientation and a
consolidated `show` for reading become the documented path, and opening the raw file becomes
the deliberate act.

## 4. Reference resolution and integrity {#sec-4}

Every frontmatter reference resolves through one function, by the shape of its path:

| Form | Resolved against | Example |
|---|---|---|
| No `/` at all | the referring document's own directory | `004-execution-backbone.md` |
| Leading `./` or `../` | the referring document's own directory | `../specs/011-delivery-lifecycle.md` |
| Any other path containing `/` | the repo root, leading `/` optional | `docs/specs/011-…md`, `/docs/specs/011-…md` |

`#sec-N` narrows any of them to an anchor that must exist in the target's source.

The rule is exhaustive and has no legacy branch: a path with a `/` in it is repo-relative
unless it explicitly says otherwise with `./` or `../`. That is what the corpus's 36
cross-directory references already mean, so nothing needs rewriting, and it leaves exactly
one way to express each intent.

A reference that does not resolve is a **defect in the corpus, reported, never dropped**.
Both commands print unresolvable references to stderr with the referring file and key, and
exit non-zero when any exist, after printing the results they could compute. Silently
skipping a dangling `implements` would understate `--needs-planning` — precisely the failure
mode these commands exist to remove.

Mirror-edge disagreement (an `amends` with no matching `amendedBy`) is reported the same
way, and does not change any answer — §3.1's union already registers the claim from
whichever side recorded it. `secfmt.py` checks numbering and anchors but has never checked
references; this closes that gap as a side effect rather than as a separate verb.

### 4.1 Anchor permanence, enforced at commit time {#sec-4.1}

Everything above assumes published anchors stay put. Nothing enforces that today.
`secfmt.py` refuses to *renumber* an accepted document (exit 2), which catches the accident,
but it compares a document only against itself: a section deleted outright, an anchor
hand-edited to a new value, or a number and its `{#sec-N}` changed together — the case the
authoring guide explicitly warns about, because editing both at once hides the move from the
tool — all pass. Each of them silently breaks every inbound `#sec-N` claim, including the
`implements.yaml` pins that §2.1 depends on.

`scripts/secfrozen.py` closes it by comparing against the committed baseline rather than
against the file alone. It is a script, not a `lode` verb, for the reason in §0: a hook that
needs a built binary fails in a fresh checkout and mid-rebase, which is when it matters most.
It joins `secfmt.py` and `secindex.py` in the `sec*` family and shares their heading parser.

- The baseline is `git show HEAD:<path>`; the working tree is the candidate.
- A document is **frozen** when its *committed* status is `accepted` or `superseded`. Reading
  the committed status matters: taking it from the working tree would let one commit flip a
  document to `draft` and rewrite its anchors in the same breath.
- On a frozen document, an anchor present in the baseline must be present in the candidate,
  on a heading whose number still matches it. Anchors may be **added** — that is what a
  letter-suffix insert is — and bodies may change freely; only disappearance and renaming
  are refused.
- A superseded section must keep its heading and anchor (`docs/authoring-design-docs.md`), so
  deleting one is the same failure as renaming it.
- The reference and mirror-edge checks of §4 run in the same pass, over the whole corpus, so
  one invocation answers "is this commit safe for the corpus". Docs-only PRs skip CI, so
  these have to be gated here or they are not gated at all.
- **A cycle in the section-level `amends`/`replaces` graph is refused**, naming the loop.
  Acting edges point new→old, so a cycle means a document claims to amend something that
  (transitively) amends it — always an authoring error, never a legitimate state, and one
  that no later reader can repair. Catching it at the commit that closes the loop names the
  one document that has to change; catching it at read time leaves everyone downstream
  looking at a corpus nobody can consolidate.

§3.2's cycle marker stays as the renderer's backstop rather than becoming redundant: a hook
can be bypassed (`--no-verify`), a rebase can present a tree no commit ever gated, and a
renderer that hangs or recurses without bound on a corpus in that state is worse than one
that names the loop and continues. The gate is where a cycle is *prevented*; the marker is
how the reader survives one that got through.

It runs as a `.pre-commit-config.yaml` entry alongside `section-numbers` and
`section-index`, but unlike those two it **refuses rather than rewrites**: there is no
correct automatic repair for a deleted published anchor, and inventing one would paper over
the exact mistake the check exists to surface.

Reference resolution therefore exists twice — in the script as a gate, in `designdoc` as
part of answering a query. That is duplication and worth naming rather than hiding, but it
is not avoidable while the gate must not depend on a build: §4's rules are stated once, here
and in `docs/authoring-design-docs.md`, and §8's Go test over the real corpus fails if the
two implementations ever disagree about it.

**After 025 the gate moves to the server.** Once documents are backbone rows, there is no
commit to hook and `lode doc accept` is the only way an anchor becomes published, so every
rule in this section — permanence, reference integrity, acyclicity — becomes an accept-time
check inside that transaction, which is what 025 §2 already says happens to 014 §7's
constraints. The script is the git-mirror form of a check that outlives it, and it is
deleted with the files rather than ported.

A document with no baseline — newly added in this commit — is unfrozen by construction and
checked only for references.

### 4.2 Shorthand references resolve in three tiers {#sec-4.2}

014 §11.3 adds `<PROJECTKEY>-<TYPE>-<n>` alongside §4's path forms. Which tier applies is
decided by the key, never by the caller:

| Tier | Reference | Resolved by | An unresolvable one is |
|---|---|---|---|
| 1 | key is the current project — §1's `.worklode/config.toml` walk | glob `<n>` against this corpus's filenames, which §1 fixes as `docs/specs/NNN-*.md` | a **defect**, exactly as §4 treats a dangling path |
| 2 | key is another project, backbone reachable | the `docs` rows of 025 §4 | a **defect** — the key is known and the document is not |
| 3 | key is another project, backbone unreachable | shape validation alone | **`unresolved: project <KEY> not known here`** — printed, exit code unaffected |

014 §11.3 has `<TYPE>` checked against the target's kind, and no document declares one until
025's `docs.kind` column exists. Until then a document under `docs/specs/` is a spec unless its
frontmatter carries `kind: adr` — an optional key only ADRs need, so no file is backfilled and
the corpus (which has no ADR today) is untouched. `WL-ADR-7` naming a document without it is a
kind mismatch, and a defect.

Tier 1 needs the current project's **key**, and `.worklode/config.toml` holds only
`current_project`, which is an id. It gains one optional line:

```toml
current_project = "worklode"
project_key = "WL"
```

019 §4.1's key cache does not substitute for it: the cache is in `~/.cache`, and a fresh clone
has none. Committing the key to the repo is safe because 010 §1 makes it immutable, and it is
what buys tier-1 checking. Where the key is absent every shorthand falls to tier 3, so the line
stays optional and an un-migrated repo degrades rather than failing.

**Tier 2 is dormant in this spec's window.** It reads the `docs` rows, and 025 has not landed;
§1's "this spec adds no endpoint" stands, and nothing here builds one. Until then every foreign
key falls to tier 3, and the tier exists in the table so that landing 025 changes which branch
runs rather than what a reference means.

It also cannot be shortcut by reading the other repo off disk, even where one is checked out
beside this repo. No two corpora in the org share a layout: worklode numbers specs and ADRs
together under `docs/specs/`, rdf-registry and provisioning keep four-digit ADRs in `docs/adr/`,
and all four adjacent repos put specs under `docs/superpowers/specs/`. A resolver that guessed
would need every one of those conventions and a way to find the checkout. The backbone knows a
document's identity without knowing anyone's directory layout, which is the whole reason tier 2
is where it is.

Tier 3 is the case every git hook is in, and it is why the shorthand can be admitted at all.
§0 fixes that a commit-time gate must run in a fresh checkout, mid-rebase, before anything is
built; a check that reaches another repository or the server fails precisely when the tree is
worth checking. The degradation is therefore a rule rather than each caller's judgement:

> A check that cannot reach the authority for a reference reports it unresolved. It never
> fails, and it never guesses which document was meant.

This is the only exception to §4's "a reference that does not resolve is a defect", and it is
narrow: it needs the shape to have validated and the key to name a project this checkout has
no way to reach. A malformed shorthand, a tier-1 miss, and a tier-2 miss are all defects.
`--strict-refs` promotes tier-3 to a defect for the one caller that can afford it — a CI job
with the backbone reachable. No hook passes it.

The corpus's one existing cross-project reference, 014's `amends: rdf-registry:ADR-0006`, is
in a colon form no tier parses. It stays a reported defect until rdf-registry has a project
key and it becomes `<KEY>-ADR-6`.

## 5. Plans carry `status` and `task` {#sec-5}

Two frontmatter keys move from optional to expected on plans.

**`status`** — 025 §4 gives plans the same draft → review → accept gate as specs, and §2.2
needs the accepted state to be readable. `wl:status`'s domain (`wl:DesignDoc | wl:Section`,
014 §5) widens to include `wl:Plan` in `ns/ontology.ttl`; the value set is unchanged
(`draft`, `accepted`, `superseded`) and plans take no per-section status because they have
no addressable sections. A plan is `draft` while it is being written and reviewed, and
`accepted` from the moment its execution is authorised.

**`task`** — already documented as transitional (`docs/authoring-design-docs.md`), already
carried by one spec. On a plan it names the plan's execution root, which is the git-mirror
stand-in for the doc-bound root that 025 §5's accept transaction will mint. It retires with
the files, as 025 §11 already records.

Neither key is backfilled across the existing corpus (§2.2).

## 6. Documentation changes {#sec-6}

`docs/authoring-design-docs.md`:

- the frontmatter table gains `status` on plans and `task` on plans, with §5's meanings;
- the references section gains the four reference forms of §4, 014 §11.3's shorthand with the
  rule that distance decides the canonical form, and a line requiring section-scoped
  `implements` on new plans, with §2.1's reason;
- a line on writing amendments as self-contained, section-shaped payloads, so they
  consolidate cleanly (§3.2) instead of reading as a diff against text the reader cannot see;
- a short section pointing at `lode doc` as the way to answer these questions — orientation
  via `doc sections`, reading via a consolidated `doc show`, raw files as the deliberate act
  — replacing the `secfmt.py`-only "Checks" advice with the full set.

`CLAUDE.md`'s "Specs, plans, tasks" section gains one line naming the commands.

`.pre-commit-config.yaml` gains a `section-permanence` entry running `scripts/secfrozen.py`
(`language: script`, like its neighbours), described as refusing rather than rewriting so
nobody expects the `gofmt`-shaped fix-and-retry loop the other doc hooks have (§4.1).

## 7. Implementation {#sec-7}

| Unit | Holds |
|---|---|
| `internal/designdoc/corpus.go` | `LoadCorpus(root)` — walk, parse, index by repo-relative path; `Corpus.Resolve(from, ref)`; the defect list from §4 |
| `internal/designdoc/query.go` | `NeedsPlanning`, `NeedsExecution` (taking task states as an argument, not fetching them), `CurrentSections`, the effectiveness gate and coverage set arithmetic |
| `internal/designdoc/resolved.go` | the §3 rendering: the §3.2 fixpoint, body-once set, cycle detection, attribution |
| `internal/designdoc/check.go` | §4's reference and mirror-edge reporting, used by every query |
| `internal/designdoc/shorthand.go` | §4.2: parse, normalise, tier selection, `kind` verification |
| `internal/cmd/doc.go` | cobra commands, ref forms, table and `--json` output |
| `scripts/secfmt.py` | the same shorthand grammar; tier-1 resolution and canonical-form rewriting in frontmatter |
| `scripts/secfrozen.py` | §4.1's gate: baseline diff against `HEAD`, reference integrity, acyclicity; refuses |

The query layer takes task states as input rather than reaching for a client, so every
query is testable without a server and `internal/designdoc` stays free of HTTP. `doc.go`
does the one API call.

Parsing is `designdoc.Parse`, which already round-trips the corpus byte for byte and shares
its heading grammar with `secfmt.py`.

The shorthand grammar exists twice for the same reason the heading grammar does: the hook is
Python and cannot depend on a build (§0). `testdata/shorthand.yaml` — input, expected parse
or expected error, expected canonical form — is read by both test suites, so a divergence is
a test failure rather than a corpus that means two things.

## 8. Testing {#sec-8}

- Golden fixture corpus (a handful of small specs and plans in `testdata/`) covering: all
  four reference forms of §4, section and whole-document `implements`, an amendment, a
  supersession, a dangling ref, a missing mirror edge.
- Reference resolution: a bare path resolves from the repo root and a `../` path from the
  referring document's directory, on the same target, from the same file.
- Shorthand (§4.2), Go and Python both driven by `testdata/shorthand.yaml`: `WL-SPEC-23` and
  `WL-SPEC-023` resolve to the same document and normalise to the same string; a fragment
  survives the round trip; `WL-23`, `wl-spec-23` and `WL-PLAN-3` are rejected as malformed;
  `WL-SPEC-999` is a tier-1 defect; `WL-ADR-23` against a `kind: spec` target is a defect
  naming the kind mismatch; `CMS-SPEC-4` with no backbone is reported unresolved and leaves
  the exit code at 0, and becomes a defect under `--strict-refs`.
- Canonical form: `secfmt.py` rewrites a within-project shorthand to the target's filename and
  leaves a foreign one alone; running it twice changes nothing the second time.
- `NeedsPlanning`: whole-document claim covers a later-added section; section claim covers
  only its own; a spec with no plan lists every section; a draft spec never appears.
- `NeedsExecution`: no `task` → listed; open task → listed; closed task → absent; no
  `status` → absent regardless of `task`.
- `--resolved`: section-scoped inlining, doc-scoped banner, superseded body retained beside
  its replacement, attribution on every borrowed block.
- Consolidation (§3.2): an amendment of an amendment is expanded transitively; a diamond
  emits the body once and a back-reference thereafter; a constructed cycle emits the cycle
  marker, is reported as a defect, and terminates; two effective replacements of one section
  both render, in the defined order.
- Root-independence: the consolidations of 006 and of 014 contain the same live bodies.
- `--section` on a replaced anchor forward-resolves to its replacement's consolidation.
- The §3.1 gate: a draft document's `replaces` leaves its target listed and marked
  `pending`; the same claim from an accepted document drops it; `--with-drafts` flips the
  first case to the second; a claim from a document with no status is effective.
- `lode doc sections` reproduces `scripts/currentspec.py`'s output on the real corpus, which
  is what lets the script be deleted rather than left to drift.
- A test over the **real** `docs/` tree asserting zero unresolvable references and zero
  mirror-edge disagreements. It fails the build when someone lands a broken reference, which
  is the check `secfmt.py` never had.
- Permanence (§4.1), a `secfrozen.py` test against a fixture git repo: deleting a published
  anchor fails; renaming one fails; changing a number and its anchor together fails; adding
  a letter-suffix anchor passes; editing a frozen section's body passes; flipping the
  working-tree status to `draft` in the same commit does **not** unfreeze it; a newly added
  document is checked for references only. The script runs without a built `lode`.
- Acyclicity: a two-document `amends` loop in the fixture repo is refused by the script and
  the loop is named; the same corpus renders with §3.2's cycle marker instead of hanging,
  proving the gate and the backstop are independent.
- Command tests for ref ambiguity, the `--needs-planning` + `--status` conflict, and the
  non-zero exit on corpus defects.

## 9. Out of scope {#sec-9}

- **`lode doc coverage`** (025 §10) — implemented-vs-unimplemented needs
  `.worklode/implements.yaml` and repo scanning (014 §6). `--needs-planning` answers the
  planning half only; the two must not be conflated in output or in prose.
- **Write verbs.** Nothing here creates, accepts or revises a document; acceptance stays a
  deliberate human act (025 §3).
- **Backfilling the corpus** with `status`, `task`, or section-scoped `implements`.
- **The backbone `docs` tables** and the graph projection — 025 owns both, and this spec is
  written so that landing them changes one layer.
- **The web view rendering consolidations by default**, with a view-source toggle. It is the
  same §3.2 rendering behind a different surface, and it belongs with 025's web work.
- **A `doc revise` practice note.** When a document's consolidation is mostly borrowed text,
  that is the signal to write a superseding revision (014 §5) — and the consolidation is what
  makes drafting one cheap. Worth writing down; it changes no code here.

Neither of the last two blocks this spec: a consolidated renderer that works in the terminal
is the whole of what makes them possible, and it is what ships here.

## 10. Acceptance criteria {#sec-10}

1. `lode doc list --needs-planning` against the current tree lists exactly
   `000-umbrella-architecture.md` with 6 unplanned sections, and exits 0.
2. `lode doc list --needs-execution` lists every plan with `status: accepted` whose `task`
   is absent or open, and no plan lacking a `status`.
3. `lode doc show 018 --resolved` shows 025 §6's doc-wide amendment as a banner and labels
   its output a consolidated view naming its sources; in the fixture corpus a chain of three
   section-scoped amendments is fully expanded, and no rendering is depth-truncated.
4. A dangling reference anywhere in `docs/` fails `go test ./internal/designdoc`.
5. `scripts/secfrozen.py` refuses a commit that removes or renames a published anchor on a
   document whose committed status is `accepted` or `superseded`, refuses one that closes a
   cycle in the `amends`/`replaces` graph, and permits a letter-suffix insert; it is wired
   into `.pre-commit-config.yaml`, never rewrites a file, and runs with no `lode` binary
   present.
6. Every query is computed without contacting the server except `--needs-execution`.
7. `lode doc show WL-SPEC-14#sec-2.1` prints 014 §11.1, and `secfmt.py` rewrites a
   `requires: WL-SPEC-4` in a worklode document to `004-execution-backbone.md` while leaving
   a foreign `CMS-SPEC-4` untouched.
8. With no server reachable, `secfmt.py` and `secfrozen.py` both exit 0 on a corpus whose only
   defect is an unresolvable foreign shorthand, and both name it on stderr.
7. `lode doc sections` matches `scripts/currentspec.py` on the current corpus, and that
   script — and only that one — is deleted in the same change; `secfmt.py` and
   `secindex.py` keep their hooks.
8. Spec 025 being `draft` leaves 018's sections listed as current and marked `pending`;
   `--with-drafts` drops the ones 025 replaces.
9. The verb names, flags and semantics match 025 §10 and §7, so replacing `LoadCorpus` with
   a store-backed loader is the whole of the migration.
