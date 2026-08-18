# Authoring specs and plans

Mechanics for creating and editing anything under `docs/specs/` or `docs/plans/`
so it passes `scripts/secfmt.py`. Spec 025 §3 and §11 own the *why*; this file
is the operational checklist. Read it before adding a spec, adding a plan, or
amending an existing section.

> **Direction (spec 025):** these files move into the backbone — docs as
> Worklode objects, frontmatter as columns, plan acceptance minting the plan's
> tasks. Until 025 is implemented, this checklist governs unchanged.

## Which tree

| | `docs/specs/` | `docs/plans/` |
|---|---|---|
| Holds | Specs and ADRs — statements that stay true after implementation | Implementation plans — spent once executed |
| Filename | `NNN-kebab-slug.md`, next free number, flat (no subdirectories) | `YYYY-MM-DD-kebab-slug.md`; a series adds `-N-part`, e.g. `2026-07-30-task-secrets-1-server-core.md` |
| Numbered sections + anchors | Yes | No — plans are not DesignDocs (025 §2), so their sections are not addressable |
| Checked by `secfmt.py` | Yes | No (pass the path explicitly if a design record kept here needs anchors) |

A plan carrying durable rationale means the spec was incomplete: promote the
reasoning into the governing spec or a new ADR rather than preserving the plan.

## Declaring a plan's tasks

A plan body carries exactly one `## Tasks` section, holding nothing but one
`### Task <N> — <title>` subsection per task (em dash; the text after it is the
task's title). `N` runs 1, 2, 3… in document order **within this file** — every
part of a plan series restarts at 1. Each subsection is a YAML metadata block,
then prose (what to do, which files, the test that proves it), then optional
`- [ ]` steps. Spec 025 §9.1 owns the semantics; accepting the plan mints one
task per subsection.

````markdown
### Task 1 — Short imperative title

```yaml
kind: feature            # feature | bug | chore | design
priority: medium         # critical | high | medium | low
skills:                  # skills the executing agent loads before starting
  - superpowers:test-driven-development
blockedBy: [ ]           # task numbers within this plan
```

Prose: files to touch, the test that proves it.

- [ ] step
- [ ] step
````

| Key | Required | Default | Values |
|---|---|---|---|
| `kind` | yes | — | `feature`, `bug`, `chore`, `design` — never `review`/`spike`, which plans don't mint |
| `priority` | no | `medium` | `critical`, `high`, `medium`, `low` |
| `skills` | no | none | skill-registry names, `plugin:skill` form where plugin-shipped |
| `blockedBy` | no | none | task numbers **in this file**; becomes `blocks` edges at mint |

No other keys. The prose after the block is the task body verbatim; the steps
are executor guidance, never tracked state — the minted task's state is the
only execution state. Ordering across files (series parts, other plans) is a
document-level `blocks` edge, never a task number.

## Frontmatter

Keys are **ontology property local names** (spec 006, as amended by 025), so
the frontmatter is not a second vocabulary. A key with no term behind it means
the ontology is missing one — raise that rather than inventing a key. The term
set is extracted to `ns/ontology.ttl` (classes and properties) and
`ns/concept.ttl` (the SKOS enums), so a key is checkable without re-reading
three specs.

| Key | Term | Shape | On |
|---|---|---|---|
| `status` | `wl:status` | one of `draft`, `accepted`, `superseded` (`proposed` retired by 025 §7 — a doc under review stays `draft`) | specs, design records |
| `issued` | `dct:issued` | `YYYY-MM-DD` of first publication | specs, design records |
| `covers` | `wl:covers` | scalar or list of spec references, or the qualified form (026 §5.1) | **plans** |
| `requires` / `isRequiredBy` | `dct:requires` / `dct:isRequiredBy` | list of references | both |
| `wasDerivedFrom` | `prov:wasDerivedFrom` | scalar reference | specs |
| `amends` / `amendedBy` | — (see 025 §14) | **map**, see below | both |
| `replaces` / `isReplacedBy` | `dct:replaces` / `dct:isReplacedBy` | **map**, see below | both |
| `task` | — | `WL-<n>` | transitional only |
| `kind` | — | `adr` on ADRs, absent on specs (026 §4.2) | specs, ADRs |

`kind` here is the resolver's document kind — it distinguishes an ADR from a
spec for the `WL-SPEC-<n>`/`WL-ADR-<n>` shorthand's `<TYPE>` check (026 §4.2,
`internal/designdoc/resolve.go`). It is a different key from the plan-body
task `kind` (`feature`/`bug`/`chore`/`design`) documented above; the two share
a name and nothing else.

`task` records the lode task that implements a spec while plans still live in
git. It is not an ontology term and goes away when plan acceptance mints the
tasks (spec 025 §9.2 — the binding becomes the minted tasks' doc reference).
**If you set it, the lode task body and the document must stay in sync** —
nothing enforces that yet.

Order keys as in the table: lifecycle, then `covers`, then dependency, then
amendment, then supersession.

### References

A reference is a **bare filename** within the same directory and a
**repo-relative path** across directories. Append `#sec-N` to narrow it to a
section:

```yaml
# in docs/plans/…  ->  crosses into docs/specs/
covers: docs/specs/004-execution-backbone.md#sec-5.2
# in docs/specs/…  ->  same directory
requires:
  - 004-execution-backbone.md
```

Every reference must resolve, and every fragment must name an anchor that exists
in the target's source. A dangling fragment is a broken reference.

### The `WL-SPEC-1` shorthand

No path crosses a repository, so a reference into another project's corpus uses
the shorthand spec 025 §14.3 defines:

```
<PROJECTKEY>-SPEC|ADR-<n>[#sec-<anchor>]
```

`WL-SPEC-1` · `WL-SPEC-25#sec-9` · `WL-ADR-7` · `CMS-SPEC-4`

`<PROJECTKEY>` is the project's key (the `WL` in `WL-42`), and `<n>` is the
document's own number, unpadded — `004-execution-backbone.md` is
`WL-SPEC-4`. The `SPEC`/`ADR` token is what keeps `WL-SPEC-4` from reading as
task `WL-4`; it is checked against the document's kind, so it has to be right.

**Distance decides which form is canonical.** Within one corpus, write the
filename — it carries the slug, so the reference says what it points at. Across
corpora, write the shorthand. Both parse in either position and `secfmt.py`
rewrites each to its canonical form, so getting it wrong costs a re-stage rather
than a review comment.

Plans have no shorthand: they have no number, and no root task exists to stand
in for one (025 §9.2 mints nothing above a plan's tasks). Reference a plan by
path.

**`NO-SPEC` is reserved for "no governing spec".** A plan that answers to no
spec — a mechanical refactor, a build fix, a convention too small to design —
writes `covers: NO-SPEC` rather than omitting the key, because an absent
`covers` reads identically to a forgotten one. There is no spec 0 and there
will not be one, so it is the only reference that resolves to nothing without
being a defect. It takes no project key in any corpus — absence of a spec is not
one project's spec 0 — so `WL-SPEC-0` is recognised but reported: write
`NO-SPEC`. Valid on a plan's `covers` and nowhere else (026 §4.3).

A shorthand naming a project this checkout cannot reach is reported as
`unresolved`, not as an error. Commit hooks run without a network or a built
`lode`, so a cross-project reference is never hard-checked at commit time; `lode
show <ref>` is what verifies one.

## Section numbering and anchors

Every section of every spec is numbered, and the anchor is the number. The
orientation section is **`0.`**, so the body still starts at 1:

```markdown
# Spec 004 — Title             <- H1, never numbered

## 0. Purpose & scope {#sec-0}
## 1. First body section {#sec-1}
### 1.1 Subsection {#sec-1.1}
## 2. Second body section {#sec-2}
```

- Top level is `N.` **with** a trailing dot; deeper levels are `N.M` **without**.
- Every numbered heading carries `{#sec-<number>}`.
- **Depth 3 max** (`H2`/`H3`/`H4`). Deeper headings are legal and render fine,
  they are simply content inside their nearest anchored ancestor (025 §6), so
  they take no number and no anchor.
- A numbered subsection under an unnumbered parent is an error — the prefix
  cannot be derived. Number the parent or unnumber the child.

**`0.` is opt-in and only for orientation** — the section a reader needs before
the substance (`Purpose & scope`, `Why`, `Problem`, `Summary`, `Context`).
Writing the `0.` is enough; the sequence follows it, and `secfmt.py` defaults to
starting at 1 otherwise. A spec that opens directly with substance simply
omits it.

Write a new spec numbered from `## 0.` or `## 1.`. If you inherit an unnumbered
draft, run `secfmt.py --assign -w` once to number it (add `--start 0` to begin at
`0.`); `--assign` only fires on a document with no numbering at all, so it can
never re-number an existing scheme.

For a draft numbered only part-way down — numbered `##`, unnumbered `###` —
`--assign` is a no-op for exactly that reason. Use `secfmt.py --assign-all -w
--update-refs` instead: it numbers every heading down to `--depth`, keeping the
document's existing first top-level number unless you override it with
`--start`. Unlike `--assign` it *can* move anchors that were already published,
so run it with `-d` first and keep `--update-refs` on.

`secfmt.py` otherwise **normalises but never introduces** numbering: a heading
you leave unnumbered stays unnumbered.

> **Changing a section number moves its anchor.** Edit the *number* and leave
> the `{#sec-N}` alone, then let `secfmt.py -w --update-refs` derive the move and
> repoint every inbound reference. Hand-editing both at once hides the move from
> the tool, and inbound references silently keep pointing at whatever now holds
> the old number.

### Numbering is frozen once accepted

An anchor is assigned at first publication and never moves: inbound claims pin
`file.md#sec-4.3`, and renumbering re-points them at different subject matter.
So on an `accepted` or `superseded` document:

- **Never renumber.** To insert between `2.1` and `2.2`, use a **letter suffix**:
  `### 2.1a New section {#sec-2.1a}`. It consumes no counter slot, and a reader
  correctly infers it was added after acceptance.
- Adding a *missing* anchor to an already-correctly-numbered section is safe and
  `secfmt.py` will do it.

## Amending a section

Three edits, all required, so the claim is discoverable from either document:

1. **An inline note next to the affected heading** in the amended document,
   naming your section:

   ```markdown
   ## 2. Lease lifecycle {#sec-2}

   > **Amended by spec 012.** Closing a lease also stamps `ended_at` on every
   > open `agent_sessions` row for it.
   ```

2. **`amends` in your frontmatter**, keyed by *your* section.
3. **`amendedBy` in theirs**, keyed by *their* section.

Both keys take a map: the key is the subject — which part of **this** document
acts — with `"."` meaning the document as a whole. Each value names the **other**
document's section.

```yaml
# in 012-agent-sessions.md — 012 as a whole amends one section of 004
amends:
  ".":
    - 004-execution-backbone.md#sec-2
```
```yaml
# in 004-execution-backbone.md — the mirror
amendedBy:
  "#sec-2":
    - 012-agent-sessions.md
```

Both directions are maintained deliberately. It duplicates the edge, but an
agent asking "what still constrains this section?" must answer from the file it
already has open; a one-way edge turns that into a scan of every sibling.

Use doc-level (`"."`, or a value with no fragment) only when the amendment
genuinely is doc-wide or spans a range of sections. Do not expand a range into a
false list of sections.

## Superseding

Same shape, with `replaces` / `isReplacedBy`, and it works per-section:

```yaml
# in 025 — its §11 replaces one section of 013
replaces:
  "#sec-11":
    - 013-reconciliation.md#sec-2.3
```

A superseded **section** keeps its heading and anchor — never delete it — and
carries a note saying what replaced it or why it went away. A bare superseded
section is a broken promise to whoever linked it.

When a **whole document** is superseded, set `status: superseded` and list the
successors under `isReplacedBy` at `"."`; each successor records `replaces`.

## Checks

```bash
./scripts/secfmt.py -l          # list docs whose numbering/anchors are off
./scripts/secfmt.py -d <file>   # diff what it would change
./scripts/secfmt.py -l -w       # fix them and report what changed
./scripts/secmeta.py            # check frontmatter against this file's schema
```

The pre-commit hook runs `-l -w`: like `gofmt`, it rewrites the files, names
them and fails, so you `git add` and commit again. Docs-only PRs skip CI, so the
hook is the real gate.

`secmeta.py` is the second hook and the opposite kind of tool — it reports and
never rewrites, because a frontmatter value is a claim about the corpus and a
wrong one is fixed by deciding what it should say. It checks the key set and the
tree each key belongs on, the `status` and `issued` rules above, that a
scalar-reference key holds a bare reference rather than prose wrapped around one,
that the map-shaped keys are keyed by a section of the document making the claim,
and that every reference and `#sec-N` fragment resolves. A cross-project
shorthand is reported on stderr as `unresolved` and does not fail the run.

Exit codes: `1` means formatting differs (`-l`/`-d`); `2` means it refused.
Two refusals, neither of which you should paper over with `--force`:

- **"is accepted; renumbering would move published anchors"** — you renumbered a
  frozen document. Use a letter-suffix insert instead. `--force --update-refs`
  exists only for anchors that have never been published, and repoints inbound
  references repo-wide plus the document's own subject keys.
- **"numbers a section whose parent is unnumbered"** — decide whether the parent
  gets a number or the child loses one. The choice changes which numbers inbound
  references use, so it is never made automatically.

## The section index and the current view

```bash
./scripts/secindex.py           # regenerate docs/{specs,plans}/index.yaml
./scripts/currentspec.py        # print what still holds, after supersession
./scripts/currentspec.py 23     # ...for one spec: number, filename or path
```

`index.yaml` is every document's sections keyed by anchor — the lookup that
answers "which section covers X" without opening 26 specs. It is generated, and
no hook regenerates it: run `secindex.py` yourself after adding, renaming or
renumbering a section, and commit the result. `secindex.py --check` reports a
stale or missing index without writing one.

`currentspec.py` reads that index plus the amendment and supersession
frontmatter and prints the corpus as it currently stands: superseded documents
and replaced sections drop out, amended sections carry a note. A `replaces`
claim only takes effect once the document making it is `accepted`, so a draft's
claim shows as `pending` on the target; `--with-drafts` shows the corpus as it
will be once the open drafts land. It also names references that point at
sections which do not exist. Pass a spec — `4`, `004`,
`004-execution-backbone.md`, or the repo-relative path — to see just that
document; supersession is still resolved against the whole corpus.

## The consolidated views (`docs/specs/inlined/`)

```bash
./scripts/inlinespec.py           # regenerate docs/specs/inlined/
./scripts/inlinespec.py --check   # report a stale or missing file (exit 1)
```

`currentspec.py` says *which* section still states the design;
`inlinespec.py` renders the corpus it describes. For each spec it writes one
file under `docs/specs/inlined/` holding that spec's text with every amendment
and supersession that is in force folded in: an amended section keeps its text
and gains the amending text beneath it, a replaced section keeps its heading
and loses its body, and every borrowed block leads with `**[amending …]**` or
`**[superseding …]**` naming the section it came from. Inlining is transitive,
and only claims from documents that are already effective are folded in — a
draft's proposal is listed as `pending`. This is spec 026 §3.2's `lode show
--resolved` rendering, written to files so a reader who has no shell (a
document-ingesting tool that takes URLs, for instance) gets the same view.

**The views are generated, and are not the corpus.** `docs/specs/` remains the
source of record: never edit a file under `inlined/`, never cite one as a
source, and never amend one. Fix the spec and regenerate.

Unlike `index.yaml`, a pre-commit hook keeps them current: touching any file
under `docs/specs/` regenerates every view and fails the commit, so you stage
the result alongside the edit that caused it. It regenerates all of them and
never only the files you touched, because a view is a function of the whole
corpus — adding an `amends` to 031 changes 001's view without changing 001. CI
runs the same script with `--check` on mixed PRs; docs-only PRs skip that
workflow, so the hook is the real gate.

The generator takes its corpus from `docs/specs/index.yaml`, so a spec that is
not in the index would render as if it did not exist. Rather than quietly
omitting it, the script exits naming the file and pointing at `secindex.py`.
That is the one case where you have to run two scripts in order.

`secfmt.py` and `secmeta.py` both skip the directory (`secfmt.generated`),
because its files carry no frontmatter and their headings are one spec's
numbering with borrowed text folded in; a walk that picked them up would report
the generator's output as your defect.
