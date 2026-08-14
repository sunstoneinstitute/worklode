---
status: draft
covers:
  - docs/specs/026-design-doc-queries.md#sec-4.1
  - docs/specs/026-design-doc-queries.md#sec-9
  - docs/specs/026-design-doc-queries.md#sec-10
requires:
  - 2026-08-03-spec-shorthand-references.md
---
# Design-doc queries 3/3: the commit-time anchor-permanence gate

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps
> use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of the spec-026 plans. Part 1 (`…-1-corpus-and-list.md`) and
part 2 (`…-2-consolidated-show.md`) cover the Go side (`internal/designdoc`,
`lode doc`). This part is pure Python plus one hook entry, shares no code with
them, and ships independently — the only cross-plan ordering is the
`requires` above. Task numbers restart at 1 in every part.

**Goal:** `scripts/secfrozen.py` — a pre-commit gate that refuses a commit
which deletes or renames a published `{#sec-N}` anchor, breaks a frontmatter
reference or mirror edge, or closes a cycle in the section-level
`amends`/`replaces` graph (026 §4.1).

**Architecture:** The script compares the working tree against the committed
baseline (`git show HEAD:<path>`) — the check `secfmt.py` cannot do, because
it compares a document only against itself. A document is frozen when its
*committed* status is `accepted` or `superseded`; on a frozen document every
baseline anchor must survive on a heading whose number still matches, and
only disappearance and renaming are refused — additions (letter-suffix
inserts) and body edits pass. §4's reference and mirror-edge checks and the
acyclicity check run in the same pass over the whole corpus, so one
invocation answers "is this commit safe for the corpus". Unlike
`section-numbers`, it **refuses rather than rewrites**: there is no correct
automatic repair for a deleted published anchor.

**Tech Stack:** Python 3 stdlib only — the script is a git hook and runs in a
fresh checkout, mid-rebase, before anything is built or installed (026 §0).
No PyYAML, no `lode`, no Go. It imports its heading parser and shorthand
grammar from `secfmt.py`, the way `secindex.py` already does.

**Spec:** `docs/specs/026-design-doc-queries.md` §0, §4, §4.1; §7's
`scripts/secfrozen.py` row; §8's permanence and acyclicity bullets.

**Read first:**
- 026 §0 (why the gate must not need a build), §4 (reference forms, defect
  reporting), §4.1 (every rule this plan implements), §10 AC5 + AC8
- `scripts/secfmt.py` — `HEADING`, `headings()`, `split_front_matter()`,
  `status_of()`, `err()`: the shared parser; and (after the shorthand plan)
  `parse_shorthand()`, `read_project_key()`
- `scripts/secindex.py:28-30` — the established import-from-`secfmt` pattern
  this plan reuses instead of extracting a module
- `.pre-commit-config.yaml` — how `section-numbers` is wired; the new entry
  sits beside it with the opposite failure mode
- `docs/plans/2026-08-03-spec-shorthand-references.md` — this plan consumes
  its `parse_shorthand`/`read_project_key` (its task 4), hence the
  `requires` edge
- `docs/authoring-design-docs.md` — the frontmatter shapes (`amends` maps,
  subject keys, reference forms) the extractor must parse

**Parser sharing — the decision.** 026 §4.1 says `secfrozen.py` joins the
`sec*` family and shares their heading parser. The sharing mechanism already
exists: `secindex.py` does `sys.path.insert(0, <scripts dir>)` and imports
`headings`/`split_front_matter` from `secfmt`. `secfrozen.py` does exactly
that — plus `status_of`, `HEADING`, `err`, `parse_shorthand`,
`read_project_key`. **Do not extract a `seclib.py`:** it would churn
`secfmt.py` (whose hook behaviour must not change, and which the shorthand
plan is editing concurrently) and create a third file for 025's corpus
cutover to delete, for zero behavioural gain. `secfmt.py` has no import-time
side effects (`main()` is guarded), so importing it is free.

**Decisions §4.1 forces but does not spell out** (each is also flagged in the
task that implements it; revisit at the spec tier if any looks wrong):

1. **Colon-form and foreign references never refuse.** 014's
   `amends: rdf-registry:ADR-0006` is on an accepted spec and §4.2 keeps it
   until rdf-registry has a project key. A gate that treats it as a
   refusal-grade defect blocks every commit in the repo. §4.2's degradation
   rule ("a check that cannot reach the authority for a reference reports it
   unresolved. It never fails, and it never guesses") and AC8 (exit 0, named
   on stderr) govern the gate: any reference naming a corpus this checkout
   cannot reach — a foreign shorthand, or the legacy colon form — is
   `unresolved`, printed, exit unaffected.
2. **A reference value may carry a trailing parenthetical annotation.** Six
   accepted specs write `wasDerivedFrom: 003-platform-graph-design.md
   (D1–D15)`. §4's forms don't mention it; rewriting six accepted specs'
   frontmatter to appease a new lint is worse than tolerating
   `<ref> (<annotation>)` in the value grammar.
3. **Deleting (or renaming the file of) a whole frozen document refuses** —
   it is every published anchor disappearing at once.
4. **A duplicated anchor in the working tree refuses** — two headings
   carrying the same `{#sec-N}` make every inbound reference ambiguous.
5. **The cycle check ignores the §3.1 effectiveness gate** — a cycle among
   drafts is still refused. Acceptance is a status flip that never
   re-presents the edges to the gate, so waiting for effectiveness would let
   a cycle be published by a frontmatter-only commit.
6. **Frontmatter the extractor cannot parse is a refusal naming the file.**
   The corpus's frontmatter is a constrained YAML subset (scalar keys, lists,
   subject-keyed maps of lists) and the parser is stdlib-only, so exotic YAML
   is possible in principle; silently skipping it would let edges dodge the
   gate.

**Non-goals:**
- Anything in `internal/designdoc` — reference resolution deliberately
  exists twice (script as gate, Go as query layer); §8's Go test over the
  real corpus keeps them agreeing and lives in part 1, not here.
- `secfmt.py` changes. This plan only imports from it.
- The `docs/authoring-design-docs.md` and `CLAUDE.md` prose of 026 §8 —
  part 1 owns those. The one §6 item shipped here is the
  `.pre-commit-config.yaml` entry, because §4.1 mandates it and a gate that
  is not wired gates nothing; part 1's docs task should find it done.
- Portability or a post-025 story. After 025 the gate moves to the server
  and this script is **deleted, not ported** (026 §4.1). Build for today's
  git mirror only.

**Conventions:**
- Stdlib only in `scripts/` — if something seems to need a third-party
  package, stop and escalate; the constraint is 026 §0 and not negotiable.
- Tests: `python3 scripts/secfrozen_test.py` (unittest; builds throwaway git
  repos under `tempfile`). No Postgres, no server, no `lode`.
- `./scripts/secfmt.py -l` must stay clean; run `./scripts/secindex.py` and
  commit only the plans index if this file's own headings change.
- Commit after every task, imperative mood, no trailers.

## Tasks

### Task 1 — Fixture harness and the anchor-permanence core

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `scripts/secfrozen.py`
- Create: `scripts/secfrozen_test.py`

The permanence rule, end to end: baseline from `git show HEAD:<path>`,
frozen-ness from the *committed* status, refusal on any published anchor that
disappeared or moved. References come in task 2 — in this task the script's
reference pass is an empty stub returning no findings.

- [ ] **Step 1: Write the failing tests**

`scripts/secfrozen_test.py`, stdlib `unittest`. First the harness every later
task reuses:

```python
#!/usr/bin/env python3
"""Tests for secfrozen.py, run against throwaway fixture git repos."""
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "secfrozen.py"

SPEC = """\
---
status: {status}
---
# Spec 001 — Fixture

## 1. First {{#sec-1}}

Body one.

## 2. Second {{#sec-2}}

### 2.1 Nested {{#sec-2.1}}

Body two.
"""


class Repo:
    """A throwaway git repo holding a docs/specs + docs/plans corpus."""

    def __init__(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        (self.root / "docs/specs").mkdir(parents=True)
        (self.root / "docs/plans").mkdir(parents=True)
        self.git("init", "-q")
        self.git("config", "user.email", "t@t")
        self.git("config", "user.name", "t")

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True,
                       capture_output=True)

    def write(self, rel, text):
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def commit(self, msg="c"):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", msg, "--no-verify")

    def run(self, env=None):
        """Run secfrozen.py with cwd at the fixture root."""
        return subprocess.run(
            [sys.executable, str(SCRIPT)], cwd=self.root,
            capture_output=True, text=True, env=env,
        )
```

Then the permanence cases — each builds a repo, commits the baseline,
mutates the working tree, and asserts on `run()`:

| Test | Baseline (committed) | Working-tree change | Expect |
|---|---|---|---|
| `test_delete_published_anchor_fails` | `SPEC` with `status: accepted` | remove the whole `## 2.` section, heading included | exit 2; stderr names `sec-2` and the file |
| `test_rename_anchor_fails` | accepted | edit `{#sec-2}` to `{#sec-9}`, number untouched | exit 2; stderr names `sec-2` |
| `test_renumber_with_anchor_fails` | accepted | `## 2.` → `## 3.` **and** `{#sec-2}` → `{#sec-3}` together — the case `secfmt.py` cannot see | exit 2 |
| `test_letter_suffix_insert_passes` | accepted | add `### 2.1a Inserted {#sec-2.1a}` with a body | exit 0 |
| `test_body_edit_passes` | accepted | change prose under `## 2.`, headings untouched | exit 0 |
| `test_status_flip_does_not_unfreeze` | accepted | set `status: draft` **and** rename `{#sec-2}` in the same working tree | exit 2 — frozen-ness reads the committed status |
| `test_draft_doc_may_renumber` | `status: draft` committed | rename `{#sec-2}` freely | exit 0 — permanence only guards frozen docs |
| `test_delete_frozen_document_fails` | accepted | `os.remove` the file | exit 2; stderr says the document (not just an anchor) went |
| `test_new_document_is_unfrozen` | empty repo committed | add a brand-new accepted spec with any anchors, uncommitted | exit 0 — no baseline, unfrozen by construction |
| `test_duplicate_anchor_fails` | accepted | add a second heading carrying `{#sec-2}` | exit 2 |
| `test_no_head_at_all` | no commits ever | corpus present in working tree only | exit 0 — nothing has a baseline |
| `test_superseded_is_frozen` | `status: superseded` | remove `## 2.` | exit 2 — deleting a superseded section is the same failure as renaming it |
| `test_runs_without_lode` | accepted, clean tree | none | exit 0 with `PATH` reduced to a temp dir holding only `python3` and `git` symlinks — proves the gate needs no built binary |

For `test_runs_without_lode`:

```python
def test_runs_without_lode(self):
    r = Repo()
    r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
    r.commit()
    bindir = r.root / "bin"
    bindir.mkdir()
    (bindir / "python3").symlink_to(sys.executable)
    git = subprocess.run(["which", "git"], capture_output=True,
                         text=True).stdout.strip()
    (bindir / "git").symlink_to(git)
    env = {"PATH": str(bindir), "HOME": os.environ.get("HOME", "/tmp")}
    p = r.run(env=env)
    self.assertEqual(p.returncode, 0, p.stderr)
```

Run `python3 scripts/secfrozen_test.py` — every test fails because the
script does not exist. Red.

- [ ] **Step 2: Implement the script core**

`scripts/secfrozen.py`:

```python
#!/usr/bin/env python3
"""Refuse a commit that breaks the published design-doc corpus (026 §4.1).

Compares the working tree against the committed baseline (`git show
HEAD:<path>`). A document whose *committed* status is accepted or superseded
is frozen: every anchor it has published must survive, on a heading whose
number still matches. Anchors may be added (letter-suffix inserts) and bodies
may change freely; only disappearance and renaming are refused. The §4
reference, mirror-edge, and acyclicity checks run in the same pass.

Unlike secfmt.py this script REFUSES RATHER THAN REWRITES: there is no
correct automatic repair for a deleted published anchor. Fix the document by
hand — letter-suffix inserts for additions, keep superseded headings — and
commit again.

Usage: secfrozen.py [dir...]      (defaults: docs/specs docs/plans)

Exit 0: safe. Exit 2: refused, findings on stderr. References this checkout
cannot verify (a foreign shorthand, a cross-repo form) are printed as
`unresolved` and never affect the exit code (026 §4.2's degradation rule).

Deleted with the git corpus when spec 025 lands — the gate becomes an
accept-time server check and this script is not ported.
"""
import subprocess
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # importing secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import err, headings, split_front_matter, status_of  # noqa: E402

DEFAULT_ROOTS = ("docs/specs", "docs/plans")
FROZEN = {"accepted", "superseded"}


def repo_root():
    """The repo the *current directory* is in — never __file__'s repo, so the
    test suite can point the script at a fixture repo via cwd."""
    p = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                       capture_output=True, text=True)
    if p.returncode != 0:
        err("secfrozen: not inside a git repository")
        sys.exit(2)
    return Path(p.stdout.strip())


def baseline(root, rel):
    """The committed text of rel, or None when HEAD lacks it (new file, or
    no HEAD at all — both mean: no baseline, unfrozen by construction)."""
    p = subprocess.run(["git", "show", f"HEAD:{rel}"], cwd=root,
                       capture_output=True, text=True)
    return p.stdout if p.returncode == 0 else None


def head_files(root, roots):
    """Every corpus path committed at HEAD — the working tree alone cannot
    reveal a deleted document."""
    p = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", "HEAD", "--", *roots],
        cwd=root, capture_output=True, text=True)
    if p.returncode != 0:  # no HEAD yet: nothing is published
        return []
    return [f for f in p.stdout.splitlines() if f.endswith(".md")]


def anchors_of(text):
    """anchor -> section number, for every anchored heading; plus the list of
    duplicated anchors."""
    _, body = split_front_matter(text)
    out, dupes = {}, []
    for _, m in headings(body):
        if not m["anchor"]:
            continue
        if m["anchor"] in out:
            dupes.append(m["anchor"])
        out[m["anchor"]] = m["num"]
    return out, dupes


def check_permanence(root, roots):
    """The §4.1 baseline diff. Returns a list of refusal strings."""
    refusals = []
    for rel in head_files(root, roots):
        base = baseline(root, rel)
        if base is None:
            continue
        front, _ = split_front_matter(base)
        if status_of(front) not in FROZEN:
            continue
        published, _ = anchors_of(base)
        if not published:
            continue
        wt = root / rel
        if not wt.exists():
            refusals.append(
                f"{rel}: frozen document deleted — its "
                f"{len(published)} published anchors all break")
            continue
        current, dupes = anchors_of(wt.read_text())
        for a in dupes:
            refusals.append(f"{rel}: anchor #{a} appears on more than one "
                            f"heading; inbound references are ambiguous")
        for anchor, num in published.items():
            if anchor not in current:
                refusals.append(
                    f"{rel}: published anchor #{anchor} (was §{num}) is "
                    f"gone — every inbound reference to it now breaks")
            elif current[anchor] != num:
                refusals.append(
                    f"{rel}: #{anchor} moved from §{num} to "
                    f"§{current[anchor]} — the anchor no longer names "
                    f"what its citations meant")
    return refusals


def main():
    roots = sys.argv[1:] or list(DEFAULT_ROOTS)
    root = repo_root()
    refusals = check_permanence(root, roots)
    # tasks 2 and 3 extend this list with reference, mirror and cycle findings
    if refusals:
        for r in refusals:
            err(f"secfrozen: {r}")
        err("\nPublished anchors are frozen (025 §3, 026 §4.1). Restore the "
            "anchor, or insert with a letter suffix (2.1a) instead of "
            "renumbering. A superseded section keeps its heading and anchor. "
            "This gate never rewrites files.")
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 3: Make it executable and verify**

```bash
chmod +x scripts/secfrozen.py
python3 scripts/secfrozen_test.py
```

Expected: all task-1 tests pass. Then run it on the real corpus:

```bash
./scripts/secfrozen.py && echo SAFE
```

Expected: `SAFE` — a clean working tree can never violate permanence.

- [ ] **Step 4: Commit**

```bash
git add scripts/secfrozen.py scripts/secfrozen_test.py
git commit -m "Add secfrozen.py: anchor permanence against the committed baseline"
```

### Task 2 — Reference integrity and mirror edges in the same pass

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `scripts/secfrozen.py`
- Modify: `scripts/secfrozen_test.py`

026 §4's checks, gate-side: every frontmatter reference resolves, every
fragment names a real anchor, every subject key names an anchor in its own
document, and every `amends`/`replaces` edge has its mirror. This task
consumes `parse_shorthand` and `read_project_key` from `secfmt.py` — added
by `2026-08-03-spec-shorthand-references.md` task 4, which is why this plan
`requires` it.

- [ ] **Step 1: Write the failing tests**

Extend `secfrozen_test.py`. A second fixture doc pair exercises the edges;
give the fixture repo a `.worklode/config.toml` with
`project_key = "WL"` where a test needs tier-1 shorthand resolution.

| Test | Corpus (all committed or working — reference checks read the working tree) | Expect |
|---|---|---|
| `test_dangling_path_ref_fails` | spec with `requires: [009-nope.md]` | exit 2; stderr names the file, the key, and the value |
| `test_bad_fragment_fails` | plan with `implements: [docs/specs/001-fixture.md#sec-99]` | exit 2 |
| `test_all_reference_forms_resolve` | one doc referencing the same target as `001-fixture.md` (bare, same dir), `../specs/001-fixture.md` (relative, from plans), `docs/specs/001-fixture.md` and `/docs/specs/001-fixture.md` (repo-root) | exit 0 |
| `test_bad_subject_key_fails` | spec whose `amends` map is keyed `"#sec-9"` but which has no `sec-9` | exit 2 |
| `test_annotation_tolerated` | `wasDerivedFrom: 001-fixture.md (D1–D3)` | exit 0 — the corpus's six accepted `wasDerivedFrom` entries carry these |
| `test_colon_form_unresolved` | `amends: {".": ["rdf-registry:ADR-0006"]}` | exit 0; stderr contains `unresolved` and the value |
| `test_foreign_shorthand_unresolved` | `requires: [CMS-SPEC-4]`, `project_key = "WL"` | exit 0; stderr `unresolved` |
| `test_tier1_shorthand_miss_fails` | `requires: [WL-SPEC-999]`, `project_key = "WL"` | exit 2 — the key is ours and the document is not there |
| `test_tier1_shorthand_hit_passes` | `requires: [WL-SPEC-1]` against `001-fixture.md`, `project_key = "WL"` | exit 0 |
| `test_missing_mirror_fails` | A has `amends: {"#sec-1": [B.md#sec-2]}`; B has no `amendedBy` | exit 2; stderr names both files and the absent key |
| `test_mirrored_pair_passes` | the same edge recorded from both sides | exit 0 |
| `test_new_document_checked_for_references_only` | committed empty corpus; working tree adds a new spec whose anchors are arbitrary but whose `requires` dangles | exit 2 naming only the reference — and a variant with clean references exits 0 |
| `test_unparseable_frontmatter_fails` | frontmatter with a flow-style map `amends: {a: b}` nested beyond what the extractor accepts, or a tab-indented list | exit 2 naming the file — never silently skipped |

Run: red — the reference pass is still the task-1 stub.

- [ ] **Step 2: Implement**

Three additions to `secfrozen.py`.

**(a) Frontmatter edge extraction** — stdlib mini-parser over the corpus's
constrained frontmatter shape (the same shape `secfmt.retarget_own_keys`
already assumes). Recognised keys: `implements`, `requires`,
`isRequiredBy`, `wasDerivedFrom` (reference lists or scalars) and `amends`,
`amendedBy`, `replaces`, `isReplacedBy` (maps: subject key `"."` or
`"#sec-N"`, values a list of references). Everything else (`status`,
`issued`, `task`, `kind`) is ignored.

```python
REF_KEYS = ("implements", "requires", "isRequiredBy", "wasDerivedFrom")
EDGE_KEYS = ("amends", "amendedBy", "replaces", "isReplacedBy")
# `<reference>` optionally followed by a parenthetical annotation, which six
# accepted specs' wasDerivedFrom entries carry: `003-….md (D1–D15)`.
VALUE = re.compile(r"^(?P<ref>\S+)(?:\s+\((?P<note>[^)]*)\))?\s*$")


def extract_refs(rel, front):
    """Yield (key, subject, reference) triples from one frontmatter block.

    Raises Unparseable(line) on any line under a recognised key that fits
    none of the accepted shapes: a scalar value on the key line, `- item`
    list entries, or `"."`/`"#sec-N"` subject keys introducing a list. A
    silently skipped line would be an edge the gate never saw.
    """
```

Implementation notes: iterate the block line by line tracking
(current key, current subject); a top-level line whose key is not
recognised resets both. Strip surrounding quotes from subjects and values.
`Unparseable` becomes a refusal: `"{rel}: frontmatter line {n!r} not
understood — the gate cannot verify edges it cannot read"`.

**(b) Reference classification and resolution** — §4's table plus §4.2's
degradation, in one function:

```python
def resolve(root, rel, ref, corpus, project_key):
    """Classify one reference. Returns ("ok", path, frag),
    ("unresolved", reason, None) or ("defect", message, None).

    Order of decision:
    1. parse_shorthand(ref): a shorthand with our project key globs
       docs/specs/<n:03d>-*.md — one hit resolves (fragment still checked),
       zero or several is a defect; a foreign key, or no project_key
       configured, is unresolved (026 §4.2 tier 3).
    2. A non-shorthand containing ":" names a corpus no tier parses
       (014's rdf-registry:ADR-0006): unresolved, never a defect —
       the gate cannot reach the authority and must not guess (§4.2).
    3. Otherwise §4's path forms: no "/" → the referring document's
       directory; leading "./" or "../" → likewise, resolved; any other
       "/" path → repo root, leading "/" optional. Missing target: defect.
       A fragment must be an anchor in the target's source: else defect.
    """
```

`corpus` is a dict `repo-relative path -> (front, anchors)` built once from
the working tree of every root; `project_key` comes from
`read_project_key(root)`. Subject keys (`"#sec-N"`) are checked against the
referring document's own anchors.

**(c) Mirror-edge check** — canonicalise every edge to an acting→target
tuple before comparing, or every correctly mirrored pair reads as a
disagreement (and, in task 3, as a false 2-cycle):

```python
MIRROR = {"amends": "amendedBy", "replaces": "isReplacedBy"}


def canonical(rel, key, subject, path, frag):
    """(relation, acting_doc, acting_frag, target_doc, target_frag).

    amends/replaces: this document acts — subject is the acting fragment.
    amendedBy/isReplacedBy: the *value* acts on this document's subject.
    Doc-scoped ends ('.', or a value with no fragment) canonicalise to '.'.
    """
```

Collect the canonical set per relation; an edge derivable from only one of
its two documents is a defect naming the file that is missing its half.
Findings from (a)–(c) append to `main()`'s refusal list; `unresolved`
notes print to stderr as `secfrozen: unresolved: <file> <key>: <ref>` and
do not touch the exit code.

- [ ] **Step 3: Verify, including against the real corpus**

```bash
python3 scripts/secfrozen_test.py
./scripts/secfrozen.py; echo "exit $?"
```

Expected on the real corpus: `exit 0`, and exactly one stderr line — 014's
`rdf-registry:ADR-0006` reported unresolved (a 2026-08-03 survey found the
corpus otherwise clean: zero dangling references, zero missing mirrors). If
the full extractor finds defects the survey's cruder parser missed, fix them
in this commit — frontmatter edits move no anchors, so mirror backfills on
accepted specs are legal — and escalate to the planning tier if any fix
would touch a heading.

- [ ] **Step 4: Commit**

```bash
git add scripts/secfrozen.py scripts/secfrozen_test.py
git commit -m "Check reference integrity and mirror edges in secfrozen"
```

### Task 3 — Refuse a cycle in the section-level amends/replaces graph

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `scripts/secfrozen.py`
- Modify: `scripts/secfrozen_test.py`

Acting edges point new→old, so a cycle is always an authoring error and the
commit that closes the loop is the one commit where a single document is at
fault (026 §4.1). §3.2's render-time cycle *marker* (part 2 of this series)
stays as the backstop for a bypassed hook; the two are independent and
neither references the other.

- [ ] **Step 1: Write the failing tests**

| Test | Corpus | Expect |
|---|---|---|
| `test_two_document_loop_refused` | A `amends {"#sec-1": [B.md#sec-2]}`, B `amends {"#sec-2": [A.md#sec-1]}`, **both mirrors recorded** so the mirror check passes and only the cycle can fail | exit 2; stderr names the loop in order: `A.md#sec-1 -> B.md#sec-2 -> A.md#sec-1` |
| `test_mirrored_pair_is_not_a_cycle` | one edge recorded from both sides (task 2's `test_mirrored_pair_passes` corpus) | exit 0 — direction canonicalisation, not edge counting, is what distinguishes a mirror from a loop |
| `test_three_document_loop_refused` | A#1→B#2→C#3→A#1 via `replaces`, mirrors recorded | exit 2; all three hops named |
| `test_self_amendment_refused` | A `amends {"#sec-1": [A.md#sec-2]}` and A `amends {"#sec-2": [A.md#sec-1]}` with mirrors | exit 2 — a loop within one document is the same error |
| `test_doc_scoped_edges_never_cycle` | A `amends {".": [B.md]}`, B `amends {".": [A.md]}`, mirrors recorded | exit 0 — §4.1 scopes the check to the *section-level* graph; doc-scoped edges are banners §3.2 never inlines, so they cannot recurse |
| `test_draft_cycle_still_refused` | the two-document loop with both docs `status: draft` | exit 2 — effectiveness (§3.1) is a read-time gate; acceptance is a status flip that would never re-present the edges here |

Run: red.

- [ ] **Step 2: Implement**

Reuse task 2's canonical edge set. Nodes are `path#frag` pairs where **both**
ends are section-scoped (`acting_frag != "." and target_frag != "."`); the
edge runs acting → target, `amends` and `replaces` alike (both mean "this
newer section acts on that older one"). Mirrored recordings of one edge
collapse to one tuple by canonicalisation, so the graph is a set.

Iterative DFS with a recursion stack; on meeting a node already on the
stack, slice the stack from that node to produce the loop and append one
refusal:

```python
def find_cycle(edges):
    """edges: {node: sorted list of successor nodes}. Returns the first loop
    as [n1, n2, ..., n1], or None. Deterministic: nodes and successors are
    visited in sorted order, so the same corpus always names the same loop.
    """
```

Refusal text:

```
secfrozen: amends/replaces cycle: A.md#sec-1 -> B.md#sec-2 -> A.md#sec-1
  A cycle means a document claims to amend something that transitively
  amends it (026 §4.1). Remove or re-target one of these edges — no later
  reader can repair a published loop.
```

- [ ] **Step 3: Verify**

```bash
python3 scripts/secfrozen_test.py
./scripts/secfrozen.py; echo "exit $?"    # real corpus: still exit 0
```

- [ ] **Step 4: Commit**

```bash
git add scripts/secfrozen.py scripts/secfrozen_test.py
git commit -m "Refuse amends/replaces cycles in secfrozen"
```

### Task 4 — Wire the gate: pre-commit entry and CI

```yaml
kind: chore
priority: medium
blockedBy: [3]
```

**Files:**
- Modify: `.pre-commit-config.yaml`
- Modify: `.github/workflows/_lint.yml`

The gate exists only once it is wired. Docs-only PRs skip CI, so the
pre-commit entry is the real gate (026 §4.1); the `_lint.yml` line is
belt-and-braces for code PRs whose author skipped hooks, mirroring
`secfmt.py`'s presence there.

- [ ] **Step 1: Add the hook entry**

In `.pre-commit-config.yaml`, directly after the `section-numbers` hook:

```yaml
      # REFUSES RATHER THAN REWRITES — the opposite of section-numbers above.
      # There is no correct automatic repair for a deleted published anchor,
      # so there is no fix-and-retry loop here: fix the document by hand
      # (letter-suffix inserts; superseded sections keep their heading and
      # anchor) and commit again. Checks the whole corpus against HEAD:
      # anchor permanence on accepted/superseded docs, reference and
      # mirror-edge integrity, amends/replaces acyclicity (026 §4.1).
      - id: section-permanence
        name: anchor permanence & corpus integrity
        entry: scripts/secfrozen.py
        language: script
        files: ^docs/(specs|plans)/.*\.md$
        pass_filenames: false
```

`files` (not `always_run`): the corpus-wide check only needs to run when a
corpus file is staged, and `git diff --cached --name-only` lists deleted
files, so removing a spec still triggers it. `pass_filenames: false`: the
script scopes itself to the whole corpus regardless of what was staged —
one staged file can break another file's mirror edge.

- [ ] **Step 2: Add the CI lines**

In `.github/workflows/_lint.yml`, after the existing `secfmt.py -l` step:

```yaml
      - name: Design-doc corpus integrity
        run: |
          ./scripts/secfrozen.py
          python3 scripts/secfrozen_test.py
```

(In CI the checkout equals HEAD so permanence is vacuous, but the
reference, mirror and cycle checks still guard the corpus, and the test
suite guards the script.)

- [ ] **Step 3: Verify the wiring end to end**

```bash
pre-commit run section-permanence --all-files; echo "exit $?"
```

Expected: pass (exit 0). Then prove the refusal path through the real hook:

```bash
sed -i '' 's/{#sec-2}/{#sec-99}/' docs/specs/004-execution-backbone.md
git add docs/specs/004-execution-backbone.md
git commit -m "should be refused"   # expect: hook fails, names sec-2
git checkout -- docs/specs/004-execution-backbone.md
```

Confirm the commit was refused, no file was rewritten by the hook
(`git status` shows only the staged sed edit before the checkout), and the
stderr message names the anchor.

- [ ] **Step 4: Commit**

```bash
git add .pre-commit-config.yaml .github/workflows/_lint.yml
git commit -m "Wire secfrozen into pre-commit and lint CI"
```

## Done when

Maps to 026 §12 AC5 and AC8, restricted to this plan's slice:

1. `scripts/secfrozen.py` refuses: a deleted published anchor, a renamed
   one, a number changed together with its anchor, a deleted frozen
   document, and a commit that closes an `amends`/`replaces` cycle — naming
   the loop. It permits: a letter-suffix insert, a body edit on a frozen
   section, and any edit to a document whose committed status is `draft`.
2. Flipping a document's working-tree status to `draft` does not unfreeze
   it — the committed status governs.
3. A document with no baseline is checked for references only.
4. On the real corpus the script exits 0 today, with exactly one
   `unresolved` stderr note (`rdf-registry:ADR-0006`); a dangling path
   reference, bad fragment, bad subject key, or missing mirror edge
   anywhere in `docs/` makes it exit 2.
5. It is wired as the `section-permanence` pre-commit hook, never rewrites
   a file, and `python3 scripts/secfrozen_test.py` passes with `PATH`
   stripped to `python3` + `git` — no built `lode`, no Go, no packages.
6. `secfmt.py` is byte-identical to before this plan: the parser is shared
   by import, exactly as `secindex.py` already shares it.
