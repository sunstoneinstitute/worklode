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
amendedBy:
  "#sec-2.1":
    - 033-plan-section-coverage.md#sec-2
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
`--needs-execution`, `lode doc show --resolved` — spelled `lode show` here, §3) and 025
§7, as amended by 033, fixes the semantics.
What 025 does not do is make them available before the backbone document store exists —
its own implementation is a long way out, and these queries are needed to plan it.

This spec implements the read-only subset of 025 §10 against the git mirror that
`docs/specs/` and `docs/plans/` still are, using `internal/designdoc`. The verb names,
flags, and semantics are 025's, so when the store lands the change is the data source and
nothing else.

**In scope:** `lode doc list`, `lode show`, `lode doc sections`, corpus loading and
reference resolution in `internal/designdoc`, one new pre-commit script (§4.1), the
frontmatter keys the queries depend on, tests.

**The commit-time gates stay Python scripts and `lode` is never one of them.** A git hook
must run in a fresh checkout, mid-rebase, before anything is built; depending on a build
artifact makes the gate fail exactly when the tree is in the state worth checking. So
`secfmt.py` keeps its hook, §4.1's permanence check joins it as a second script, and the Go
commands are the **read surface only** — they report the defects they
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
| Which spec sections a plan claims | `covers` in plan frontmatter | `doc_edges` |
| Whether a plan has been executed | `task` in plan frontmatter, resolved against the tracker | the state of the tasks referencing the doc |
| What amends or replaces a section | `amends`/`amendedBy`/`replaces`/`isReplacedBy` maps | `doc_edges` |
| Whether a claim is in force yet | `status` of the *claiming* document (§3.1) | `docs.status` |

Only the third needs the server, and it uses the existing task API. Everything else is local
file reading and answers offline.

**This spec is a stated exception to the metrics rule** (022; `CLAUDE.md`), which requires
`worklode_*` metrics of any change adding an HTTP endpoint, background loop, outbound call, or
store operation with meaningful outcomes. `--needs-execution`'s task-state fetch is an outbound
call, and is the one thing here that reads as a trigger — but the rule governs **server-side**
changes, and everything this spec adds runs in the CLI, where no `prometheus.Registerer` is
threaded: it reaches `lode serve` alone. So no endpoint, no background loop, no store
operation, and no metric. An implementer who adds one has misread this section; the exception
is recorded here rather than left to be re-derived, because the absence otherwise reads as an
oversight to every later reviewer.

The corpus root is the directory holding the repo-local `.worklode/config.toml` (spec 019's
walk, which already ran to resolve the project), with `specs/` and `plans/` under `docs/`.
`--docs <dir>` overrides it. No new config key: a repo whose docs live elsewhere passes the
flag, and there is exactly one such repo today (none).

## 2. `lode doc list` {#sec-2}

```
lode doc list                       every document: kind, id, status, title
lode doc list --kind spec|adr|plan
lode doc list --status draft|accepted|superseded
lode doc list --needs-planning      accepted specs with sections not fully planned
lode doc list --needs-execution     accepted plans with no closed execution task
```

Filters compose. `--json` (the root persistent flag) emits the same rows as objects.

Every document in the corpus carries a `status` (§2.2), so the listing has no "unset" case: a
document without one is a defect (§4), reported rather than quietly rendered as `—`.

### 2.1 `--needs-planning` {#sec-2.1}

Plans and sections are many-to-many, and coverage is the qualified, three-valued
`covers` relation from 033. For each section of an accepted spec, consider only
accepted plans naming that exact `#sec-N` anchor:

- `full` makes the section fully planned;
- `partial` makes it partially planned, unless it carries a non-empty
  `fullCoverageWith` whose every target is an accepted plan contributing `full`
  or `partial` to the same section;
- `none` records a governing constraint and contributes no coverage.

A whole-document claim contributes nothing: it cannot say which present section
it undertakes and would silently claim future sections. A spec is listed when at
least one current section is not fully planned; the output gives the gap count,
anchors, and whether each is partial, bound-only, or unplanned.

Nothing here counts plans per spec or expects a partition. Overlap is legal and unremarked:
two plans claiming the same section means two plans touch it, not a modelling error.

```
docs/specs/007-drift-and-overview.md      accepted   2/9 need planning   sec-3.4(partial) sec-5(unplanned)
```

A plan whose `covers` is **`NO-SPEC`** declares that no spec governs it (§4.2a). It
contributes coverage to nothing and is never itself a planning gap — the sentinel is how a
standalone plan says so out loud, instead of carrying no `covers` and being
indistinguishable from one that forgot.

`--needs-planning` implies `status: accepted` — planning follows acceptance (025 §3), and a
draft spec is not a planning gap. Combining it with a conflicting `--status` is an error
rather than an empty result, because an empty result would read as "nothing to plan". So is
combining it with `--needs-execution`: the two select disjoint kinds, so the conjunction is
always empty, and it is the same silent lie in a different flag.

The acceptance test applies to both ends: a draft spec is not yet owed planning,
and a draft plan has not yet undertaken work. Counting a draft plan would let an
unapproved split hide a real gap. Closure targets obey the same gate; a named
draft plan, a plan covering another section, or a `none` entry leaves the source
partial and is reported rather than trusted.

The convention is not new: the other side of the same claim, `.worklode/implements.yaml`
(014 §6), is **already section-scoped** — its entries key on `section:` with a pinned
document version, precisely so a claim narrows to the part of the spec it satisfies. Plan
coverage is therefore section-scoped too, making the two halves — what was planned,
and what was built — addressable at the same granularity. Without that,
`lode doc coverage` (§9) could never join them.

### 2.2 `--needs-execution` {#sec-2.2}

A plan needs execution when its frontmatter carries `status: accepted` and either it has no
`task` key, or the task it names is not in a closed state (`closedStates`,
`internal/store/tasks.go`). Task states are fetched in one request; the command fails rather
than degrading if the server is unreachable, since a plan whose task state is unknown is
exactly the case the caller is asking about.

**Every plan carries a `status`; there is no legacy bucket.** An earlier draft of this spec
exempted the forty-odd plans authored before §5 required one, on the grounds that backfilling
them meant either inventing statuses nobody reviewed or reporting twenty-five shipped plans as
outstanding. The exemption was the more expensive answer: it put a permanent branch in the
query, a `status: —` case in the output, and a class of document the corpus had to keep
explaining. The plans were backfilled instead — a **spent** plan is `superseded` (025 §4: a
plan is spent once executed), an authorised but unexecuted one is `accepted` — so the selector
is the plain reading of the frontmatter and a statusless plan is simply a defect.

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

## 3. Showing a document: `lode show` {#sec-3}

```
lode show <ref> [--resolved] [--section|-s <anchor>]
```

`--section` (short `-s`) takes an anchor in any of three spellings: `sec-3`, `#sec-3`,
or the bare number `3`, which is expanded to `sec-3`.

`<ref>` is a path, a bare filename, a spec number (`014`, `014-design-documents`), or 014
§11.3's shorthand (`WL-SPEC-14`, and `WL-SPEC-14#sec-2.1` as sugar for `--section sec-2.1`)
that matches exactly one document; an ambiguous ref is an error listing the candidates.
A shorthand naming another project is reported unresolved rather than fetched — §4.2's tier 2
is dormant until 025. Without
`--resolved` this is `cat` with ref resolution, which is worth having only because it takes
the same ref forms as everything else.

A kind flag (`--spec 15`, `--adr 7`, 019 §4.3a) is an equivalent spelling of a
local ref: in a keyed corpus, `--spec 15` and `WL-SPEC-15` resolve and render
identically, so a reader who knows only the kind and the number never has to
build the typed id by hand. A kind flag always means the local corpus, so
without a `project_key` it still resolves by bare number, whereas a
positional shorthand whose key cannot be established gets §4.2's tier-3
`unresolved` instead.

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
for 011. A section is always its whole subtree: inlining `#sec-2` carries `#sec-2.1` and
`#sec-2.3` with it, each consolidated in its own right, because a claim against a parent is a
claim against what it contains.

With `--resolved`, an anchor that has been replaced **forward-resolves**: the consolidation
printed is that of what replaced it, so a citation that has gone stale still yields current
text rather than superseded text or an error, and a split forwards to every replacement in
the §3.2 order. A *pending* replacement never forwards (§3.1). Without `--resolved`,
`--section` stays `cat` semantics and prints the anchor asked for, whatever has happened to
it — the two flags mean "give me this text" and "tell me what is true", and only the second
should follow an edge.

### 3.1 A claim takes effect when its author is accepted {#sec-3.1}

An `amends` or `replaces` in a **draft** document is a proposal. Its target still states the
design until the claiming document is accepted, so `--resolved` and `lode doc sections` mark
such a claim `pending` and leave the target intact. A claim from an `accepted` or
`superseded` document is effective; so is one from a document outside the corpus, which
cannot be status-checked and is trusted rather than dropped.

`--with-drafts` treats draft claims as effective, which answers the other useful question:
what the corpus says once the open drafts land. Both readings are legitimate and neither is
safe as the only one — specs 014 and 015 are draft today, and reading their claims on 000 §2
and 005 §1 as settled would have the corpus describe a vocabulary that does not exist.

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
reader sees what was replaced next to what replaced it. **Document-scoped** edges — either end
lacking a section, whether the acting key is `"."` or the value carries no fragment — stay
banners and are never inlined; they carry no section-shaped payload.

**Backfill: the traversal runs both ways.** For each effective section-scoped edge on which
`s` is the *acting* end, the target's consolidation is inlined beneath `s` as marked context.
Without this the walk follows target→actor only, so entering the newer document of an
amendment pair never surfaces the still-live older text, and the root-independence claimed
below would hold for replacement chains and fail for every amendment. A backfilled block's
marker names the section whose body it carries — `**[amended text 006#sec-1.2]:**` — because
in this direction the acting section is the one the reader is already inside. Backfill is
skipped where the target is already on the current expansion path, and obeys body-once like
everything else.

**Consolidation of a document** — its preamble, then each section's consolidation in source
order.

Three rules make that total and bounded without a depth limit:

- **Deterministic order.** Multiple edges onto one section are inlined by the acting
  document's `issued` date, then filename, then anchor; a document carrying no `issued` sorts
  first. Two effective replacements of one section is a *split*, not a defect: both are shown,
  ordered and attributed.
- **Body-once.** Within one rendering a section's body is emitted at its first occurrence;
  every later occurrence is a back-reference marker. This is what bounds a diamond — one
  amendment targeting two sections, or two documents amending one — and it caps output at
  the size of the live corpus rather than at the size of the walk.
- **Cycle marker.** A visited set along each expansion path; revisiting a section emits
  `[cycle: 014#sec-1 → 0NN#sec-2 → 014#sec-1]` and stops, and the cycle is reported through
  §4's defect machinery. Acting edges point new→old, so cycles are near-impossible today and
  become constructible under revision; the visited set is cheaper than the incident.

Because every emitted body is either live or shown beside its successor, and because backfill
makes the traversal symmetric, the choice of root document changes *where* a body appears,
never *whether* it appears: entering spec 006's consolidation and entering 014's surface the
same live sections of the lineage they share. The property is over that shared component and
not over the corpus — a section no edge touches is reachable only from its own document, so
each root necessarily also carries material the other cannot. That is what makes "start from
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

A reference may carry a **trailing parenthetical annotation** — `wasDerivedFrom:
003-platform-graph-design.md (D1–D15)`, naming which of that document's decisions were
inherited. It is stripped before resolution and otherwise ignored. Six accepted specs (000,
004, 005, 006, 007, 008) already write it, so the alternative to admitting it is editing
frozen documents to satisfy a parser.

The rule is exhaustive and has no legacy branch: a path with a `/` in it is repo-relative
unless it explicitly says otherwise with `./` or `../`. That is what the corpus's 36
cross-directory references already mean, so nothing needs rewriting, and it leaves exactly
one way to express each intent.

A reference that does not resolve is a **defect in the corpus, reported, never dropped**.
Both commands print unresolvable references to stderr with the referring file and key, and
exit non-zero when any exist, after printing the results they could compute. Silently
skipping a dangling `covers` would understate `--needs-planning` — precisely the failure
mode these commands exist to remove.

The single exception is a reference naming **a project this checkout cannot reach**, which is
`unresolved` rather than a defect and leaves the exit code alone (§4.2). It covers any syntax,
including the pre-shorthand colon form: what makes such a reference unresolvable is the
absence of an authority to ask, not a mistake by its author, and nothing in the referring
repository can repair it.

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

It runs as a `.pre-commit-config.yaml` entry alongside `section-numbers`, but unlike it
**refuses rather than rewrites**: there is no
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
in a colon form no tier parses. It is **reported `unresolved`, exit code unaffected** — the
tier-3 treatment, reached by §4's exception rather than by a tier, since the syntax never
parses far enough to select one. Making it a defect would be unenforceable as well as wrong:
no edit to this repository can make the reference *resolve* while rdf-registry has no project
key — only delete the claim or leave it — and a §4.1 gate that refused on it would block every
commit to the corpus in the meantime. It becomes an ordinary tier-2 reference, and an ordinary
defect when it dangles, once that key exists and it is rewritten `<KEY>-ADR-6`.

### 4.2a `NO-SPEC` means "no governing spec" {#sec-4.2a}

Some plans answer to no spec — a mechanical refactor, a build fix, a convention change too
small to design. Leaving `covers` off says nothing: it reads the same as a plan whose
author forgot, and neither the coverage queries nor a reviewer can tell the two apart. Spec
number **0** is reserved to say it explicitly, and it is written:

```yaml
covers: NO-SPEC
```

**The sentinel carries no project key.** Spec 0 is not one project's zeroth spec — it is the
absence of a spec, and absence is the same fact in every corpus. So `NO-SPEC` is the canonical
spelling in any project, and `<KEY>-SPEC-0` in any project *means* it: the tier table of §4.2
never runs, because there is no key to dispatch on. A renderer showing a reference to spec 0
prints `NO-SPEC` whatever the source wrote.

It names no file **by construction** — there is no spec 0 and there will not be one — so it is
the one reference that resolves to nothing without being a defect. Everything else about tier 1
is unchanged: a `WL-SPEC-<n>` for any other `n` must hit a file in this corpus.

Three constraints, all checked by `scripts/secmeta.py`:

- **Only on a plan's `covers`.** On `requires`, `amends` or `replaces` it would assert a
  relationship to a document that does not exist, which is a dangling reference wearing a
  sentinel's clothes.
- **Written `NO-SPEC`, not `<KEY>-SPEC-0`.** The keyed form is recognised and reported so it
  can be corrected, rather than silently accepted into two spellings of one thing. Zero-padding
  is moot here for the same reason it is an error elsewhere in worklode's shorthand (014
  §11.3) — though a *foreign* key such as `rdf-registry:ADR-0006` pads by its own convention,
  which is none of our business.
- **It is not a wildcard.** `NO-SPEC` means no spec governs the plan. A governed plan names
  one or more of its governing spec's sections; naming the sentinel instead is wrong in a way
  no tool can catch, which is the cost of having the sentinel at all.

## 5. Plans carry `status` and `task` {#sec-5}

Two frontmatter keys move from optional to expected on plans.

**`status`** — 025 §4 gives plans the same draft → review → accept gate as specs, and §2.2
needs the accepted state to be readable. `wl:status`'s domain is
`wl:DesignDoc | wl:Plan | wl:Section` (033 §4); the value set is unchanged (`draft`,
`accepted`, `superseded`) and plans take no per-section status because they have no
addressable sections. A plan is `draft` while it is being written and reviewed, and
`accepted` from the moment its execution is authorised.

**`task`** — already documented as transitional (`docs/authoring-design-docs.md`), already
carried by one spec. On a plan it names the task the plan's execution hangs off in today's
tracker — the git-mirror stand-in for the `plan_doc` reference 025 §5's accept transaction will
put on each of the plan's tasks, which is why a single id suffices here and nothing is built on
it being one row. It retires with the files, as 025 §11 already records.

Both keys are backfilled across the existing corpus (§2.2): every plan carries a `status`, and
the seventeen with a stand-in execution task carry a `task` naming it.

## 6. Documentation changes {#sec-6}

`docs/authoring-design-docs.md`:

- the frontmatter table gains `status` on plans and `task` on plans, with §5's meanings;
- the references section gains the four reference forms of §4, 014 §11.3's shorthand with the
  rule that distance decides the canonical form, and a line requiring section-scoped
  `covers` on new plans, with §2.1's reason;
- a line on writing amendments as self-contained, section-shaped payloads, so they
  consolidate cleanly (§3.2) instead of reading as a diff against text the reader cannot see;
- a short section pointing at `lode doc` and `lode show` as the way to answer these
  questions — orientation via `doc sections`, reading via a consolidated `lode show`, raw
  files as the deliberate act — replacing the `secfmt.py`-only "Checks" advice with the full
  set.

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
| `internal/designdoc/resolve.go` | §4.2: parse, normalise, tier selection, `kind` verification |
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
  four reference forms of §4, valid section-scoped and invalid whole-document `covers`, an
  amendment, a supersession, a dangling ref, a missing mirror edge.
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
- `NeedsPlanning`: only accepted plans contribute; accepted `full` discharges its exact
  section; an unclosed `partial` is reported as partial; a non-empty `fullCoverageWith`
  closes its source only when every target is accepted and contributes `full` or `partial`
  to that same section; draft, `none`, wrong-section, or missing targets and an empty
  completion set leave the source partial; `none` alone is bound-only; a section with no
  accepted plan is unplanned; a whole-document claim contributes nothing and is reported
  because the query requires a `#sec-N` section; a draft spec never appears.
- `NeedsExecution`: `accepted` with no `task` → listed; `accepted` with an open task →
  listed; closed task → absent; `superseded` (spent) → absent regardless of `task`; a plan
  carrying no `status` at all → reported as a defect, not silently skipped.
- `--resolved`: section-scoped inlining, doc-scoped banner, superseded body retained beside
  its replacement, attribution on every borrowed block.
- Consolidation (§3.2): an amendment of an amendment is expanded transitively; a diamond
  emits the body once and a back-reference thereafter; a constructed cycle emits the cycle
  marker, is reported as a defect, and terminates; two effective replacements of one section
  both render, in the defined order.
- Root-independence: the consolidations of 006 and of 014 surface the same set of live
  sections **within the lineage they share** — compared as a set of `<doc>#anchor` keys
  (emitted or back-referenced), not as rendered strings, since body-once means the two
  orderings legitimately differ. Sections outside the shared component are excluded rather
  than expected to match. 014 is `draft`, so this runs with `--with-drafts`; without it the
  edges under test are `pending` and the assertion is vacuous.
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
- **Backfilling section-scoped `covers`** across the existing plans. `status` and `task`
  *were* backfilled (§2.2, §5) — that is what removed the legacy branch — but re-deriving
  which sections each shipped plan actually covered is the guesswork §2.1 refuses.
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

1. `lode doc list --needs-planning` exits 0 and lists every accepted spec having at least one
   section that is not fully planned, reporting each gap anchor as partial, bound-only, or
   unplanned. Only accepted plans count; `full` or a verified non-empty closed `partial` set
   discharges the exact section. It hardcodes no document: the criterion this replaced
   asserted one specific spec and became untrue the moment that spec was deleted. A plan
   carrying `covers: NO-SPEC` (§4.2a) is absent from the output and adds coverage to nothing.
2. `lode doc list --needs-execution` lists every plan with `status: accepted` whose `task` is
   absent or open, and no `superseded` plan; every plan in the corpus carries a `status`, and
   one that does not is a reported defect.
3. `lode show 018 --resolved` shows 025 §6's doc-wide amendment as a banner and labels
   its output a consolidated view naming its sources; in the fixture corpus a chain of three
   section-scoped amendments is fully expanded, and no rendering is depth-truncated.
4. A dangling reference anywhere in `docs/` fails `go test ./internal/designdoc`.
5. `scripts/secfrozen.py` refuses a commit that removes or renames a published anchor on a
   document whose committed status is `accepted` or `superseded`, refuses one that closes a
   cycle in the `amends`/`replaces` graph, and permits a letter-suffix insert; it is wired
   into `.pre-commit-config.yaml`, never rewrites a file, and runs with no `lode` binary
   present.
6. Every query is computed without contacting the server except `--needs-execution`.
7. `lode show WL-SPEC-14#sec-2.1` prints 014 §2.1, and `secfmt.py` rewrites a
   `requires: WL-SPEC-4` in a worklode document to `004-execution-backbone.md` while leaving
   a foreign `CMS-SPEC-4` untouched.
8. With no server reachable, `secfmt.py` and `secfrozen.py` both exit 0 on a corpus whose only
   unresolvable references name unreachable projects — including 014's colon-form
   `rdf-registry:ADR-0006` — and both name them on stderr as `unresolved`.
9. Entering the consolidation of 006 and of 014 surfaces the same live sections of the lineage
   they share, under `--with-drafts`, with backfill supplying the older text when the newer
   document is the root.
10. `lode doc sections` matches `scripts/currentspec.py` on the current corpus, and that
   script — and only that one — is deleted in the same change; `secfmt.py` keeps its hook and
   `secindex.py` stays a manual regeneration.
11. Specs 014 and 015 being `draft` leaves their targets' sections listed as current and
   marked `pending`, for `amends` as much as for `replaces`; `--with-drafts` applies both.
12. The verb names, flags and semantics match 025 §10 and §7 as amended by 033, so replacing
   `LoadCorpus` with a store-backed loader is the whole of the migration.
