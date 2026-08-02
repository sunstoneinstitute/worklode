# Authoring specs and plans

Mechanics for creating and editing anything under `docs/specs/` or `docs/plans/`
so it passes `scripts/secfmt.py`. Spec 014 §3 and §11 own the *why*; this file
is the operational checklist. Read it before adding a spec, adding a plan, or
amending an existing section.

> **Direction (spec 025):** these files move into the backbone — docs as
> Worklode objects, frontmatter as columns, plan acceptance minting the task
> subtree. Until 025 is implemented, this checklist governs unchanged.

## Which tree

| | `docs/specs/` | `docs/plans/` |
|---|---|---|
| Holds | Specs and ADRs — statements that stay true after implementation | Implementation plans — spent once executed |
| Filename | `NNN-kebab-slug.md`, next free number, flat (no subdirectories) | `YYYY-MM-DD-kebab-slug.md`; a series adds `-N-part`, e.g. `2026-07-30-task-secrets-1-server-core.md` |
| Numbered sections + anchors | Yes | No — plans are not DesignDocs (014 §2), so their sections are not addressable |
| Checked by `secfmt.py` | Yes | No (pass the path explicitly if a design record kept here needs anchors) |

A plan carrying durable rationale means the spec was incomplete: promote the
reasoning into the governing spec or a new ADR rather than preserving the plan.

## Frontmatter

Keys are **ontology property local names** (spec 006 as amended by 014/015), so
the frontmatter is not a second vocabulary. A key with no term behind it means
the ontology is missing one — raise that rather than inventing a key. The term
set is extracted to `ns/ontology.ttl` (classes and properties) and
`ns/concept.ttl` (the SKOS enums), so a key is checkable without re-reading
three specs.

| Key | Term | Shape | On |
|---|---|---|---|
| `status` | `wl:status` | one of `draft`, `accepted`, `superseded` (`proposed` retired by 025 §3 — a doc under review stays `draft`) | specs, design records |
| `issued` | `dct:issued` | `YYYY-MM-DD` of first publication | specs, design records |
| `implements` | `wl:implements` | scalar or list of spec references | **plans** |
| `requires` / `isRequiredBy` | `dct:requires` / `dct:isRequiredBy` | list of references | both |
| `wasDerivedFrom` | `prov:wasDerivedFrom` | scalar reference | specs |
| `amends` / `amendedBy` | — (see 014 §11) | **map**, see below | both |
| `replaces` / `isReplacedBy` | `dct:replaces` / `dct:isReplacedBy` | **map**, see below | both |
| `task` | — | `WL-<n>` | transitional only |

`task` records the lode task that implements a spec while plans still live in
git. It is not an ontology term and goes away when plans become task subtrees
(spec 025 §5 — the binding becomes the accept-minted root's doc reference).
**If you set it, the lode task body and the document must stay in sync** —
nothing enforces that yet.

Order keys as in the table: lifecycle, then `implements`, then dependency, then
amendment, then supersession.

### References

A reference is a **bare filename** within the same directory and a
**repo-relative path** across directories. Append `#sec-N` to narrow it to a
section:

```yaml
# in docs/plans/…  ->  crosses into docs/specs/
implements: docs/specs/011-delivery-lifecycle.md
# in docs/specs/…  ->  same directory
requires:
  - 004-execution-backbone.md
```

Every reference must resolve, and every fragment must name an anchor that exists
in the target's source. A dangling fragment is a broken reference.

## Section numbering and anchors

Every section of every spec is numbered, and the anchor is the number. The
orientation section is **`0.`**, so the body still starts at 1:

```markdown
# Spec 023 — Title             <- H1, never numbered

## 0. Purpose & scope {#sec-0}
## 1. First body section {#sec-1}
### 1.1 Subsection {#sec-1.1}
## 2. Second body section {#sec-2}
```

- Top level is `N.` **with** a trailing dot; deeper levels are `N.M` **without**.
- Every numbered heading carries `{#sec-<number>}`.
- **Depth 3 max** (`H2`/`H3`/`H4`). Deeper headings are legal and render fine,
  they are simply content inside their nearest anchored ancestor (014 §7), so
  they take no number and no anchor.
- A numbered subsection under an unnumbered parent is an error — the prefix
  cannot be derived. Number the parent or unnumber the child.

**`0.` is opt-in and only for orientation** — the section a reader needs before
the substance (`Purpose & scope`, `Why`, `Problem`, `Summary`, `Context`).
Writing the `0.` is enough; the sequence follows it, and `secfmt.py` defaults to
starting at 1 otherwise. Three specs have no `0.` because they open with
substance rather than orientation: 000 (`Architecture in one screen`), 003
(`Resolved decisions`) and 007, whose `Purpose & scope` is the design's payoff
argument.

Write a new spec numbered from `## 0.` or `## 1.`. If you inherit an unnumbered
draft, run `secfmt.py --assign -w` once to number it (add `--start 0` to begin at
`0.`); `--assign` only fires on a document with no numbering at all, so it can
never re-number an existing scheme.

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
# in 014 — its §6 replaces one section of 013
replaces:
  "#sec-6":
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
```

The pre-commit hook runs `-l -w`: like `gofmt`, it rewrites the files, names
them and fails, so you `git add` and commit again. Docs-only PRs skip CI, so the
hook is the real gate.

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
answers "which section covers X" without opening 26 specs. It is generated; a
pre-commit hook rewrites it and fails so you re-stage, like `secfmt.py`.

`currentspec.py` reads that index plus the amendment and supersession
frontmatter and prints the corpus as it currently stands: superseded documents
and replaced sections drop out, amended sections carry a note. A `replaces`
claim only takes effect once the document making it is `accepted`, so a draft's
claim shows as `pending` on the target; `--with-drafts` shows the corpus as it
will be once the open drafts land. It also names references that point at
sections which do not exist. Pass a spec — `23`, `023`,
`023-keycloak-primary-auth.md`, or the repo-relative path — to see just that
document; supersession is still resolved against the whole corpus.
