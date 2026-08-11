#!/usr/bin/env python3
"""Rewrite the repository's inbound references from the old docs/specs/
corpus to the folded docs/specs2/ corpus
(2026-08-11-spec-corpus-consolidation-1-fold-tooling task 5).

Consumes docs/specs2/mapping.yaml -- scripts/fold.py --mapping's output (see
the plan's "Derived mapping.yaml") -- never fold.yaml, and never by reading
docs/specs2/*.md prose. mapping.yaml tabulates only cross-document moves
(`documents:`) and cross-section moves (`sections:`); this module derives the
two forms mapping.yaml leaves untabulated -- the whole-document reference and
the `<KEY>-SPEC-<n>` shorthand -- from those two tables, per the plan.

Four reference spellings are recognised, all resolved through the same two
tables:

  1. repo-relative path, with or without a `#sec-N` fragment
     (docs/specs/033-....md, docs/specs/033-....md#sec-3)
  2. bare filename, recognised only when the file *being scanned* itself
     lives under the old corpus dir (docs/specs/*.md frontmatter refers to
     its siblings this way; a bare filename anywhere else is not a
     reference and is left alone)
  3. `<KEY>-SPEC-<n>` shorthand, with or without `#sec-<a>` (WL-SPEC-14,
     WL-SPEC-14#sec-2.1). `<KEY>` is read from mapping.yaml's own from_id/
     to_id, never hardcoded. `<KEY>-ADR-<n>` is recognised syntactically (so
     it is never split or mis-parsed) but always left alone -- ADRs are not
     part of this fold and never appear in mapping.yaml. A shorthand whose
     key does not match this corpus's own project key (cross-project, e.g.
     "CMS-SPEC-4") is likewise left alone.
  4. prose form, `[spec ]<n> §<a>` (spec 014 §7.2, 014 §5, 006 §1.4)

Every recognised occurrence must resolve or the whole run fails -- a regex
that is too greedy corrupts files, one too narrow leaves a stale reference
with no trace, and both are equally invisible across ~1,700 sites without
this discipline. Each form's pattern always captures the *maximal* digit or
anchor run via a character class (`\\d+`, `[\\w.]+`), never matches one of a
hand-built alternation of known ids or anchors, and is bounded by a lookaround
excluding surrounding word/path characters -- so `WL-SPEC-1` cannot match
inside `WL-SPEC-14`, `004-....md` cannot match inside a longer filename, and
`#sec-1` cannot match the `#sec-1` prefix of `#sec-1.3` (the pattern always
captures "1.3" whole and looks *that* up, never "1" plus a dangling tail).
The prose form's leading boundary additionally excludes "-" and "." (not
just word characters), so an ADR number ("ADR-0006 §3"), a version string
("v1.3 §3") or a date ("2026-08-11 §3") is never captured as a spec number;
a zero-padded number longer than three digits is rejected outright rather
than int()-collapsed, for the same reason applied where the boundary alone
cannot catch it (e.g. "spec 0006 §3"). Every branch's trailing boundary
tolerates one bare "." with nothing filename-like after it, so a reference
ending a sentence ("....md.", "WL-SPEC-14.", "014 §5.") still matches --
while "....md.bak" and "....md-old" still correctly do not.

A recognised reference that resolves into docs/specs/ but has no match in
`sections:` (fragment form) or `documents:` (whole-document form, including
the shorthand and prose forms once resolved to a source filename) points at
material the fold either dropped or has not folded yet. This is a hard
failure, not a warning: every one is listed with its file and line, and the
whole run refuses to write -- not even the substitutions that did resolve --
so a run can never leave the tree half-rewritten. `--allow-dropped` narrows
this by one case: a reference whose target is a *recorded* `dropped:` entry
(a human decision that it is fine to leave stale) passes through unchanged
instead of failing; a never-mapped reference still fails regardless of the
flag. `--dry-run` (the default) computes the same full plan and reports the
same failure without writing; `-w` writes, and only when the plan is
entirely clean.

No substitution in any of the four forms is guaranteed self-inverse: old
and new spec numbering both start at 1, so a rewritten id or path can
coincidentally look like a fresh *old*-corpus reference to a second `-w`
pass (WL-SPEC-4 -> WL-SPEC-6, and WL-SPEC-6 is itself a real from_id
elsewhere, mapping to WL-SPEC-11). This is true of the path form too under
`--corpus-to` set equal to `corpus_from` -- exactly Part 5's cutover mode --
where the output prefix stops being distinguishable from the input prefix
(025-....md -> 006-....md on one run, 006-....md -> 011-....md on the
next, both under `docs/specs/`). An earlier version of this module claimed
the path form was naturally idempotent and gated only on a clean git
worktree; both claims were wrong for cutover mode specifically, so `-w`
refuses to write a *second* time against the same tree by a different,
durable mechanism: MARKER_PATH (docs/specs2/.refmap-applied), written on
every successful `-w` and checked, unconditionally, before any other work
happens. Unlike a git-dirty check, the marker survives a commit -- that is
the whole point, since the unsafe sequence is exactly "-w, commit, -w
again" (a clean tree after the first commit gives a dirty-only check
nothing left to say). `--force` bypasses it, for re-running after amending
mapping.yaml; deleting the marker file works too. The clean-worktree check
(`worktree_state`) still runs as a *secondary* guard -- not because it
buys idempotency, but because starting from a clean tree keeps the
resulting diff attributable to this run alone and trivially revertable
before it's committed -- and `--force` bypasses that too. `--dry-run` is
unaffected by either guard, since it never writes.

.worktrees/ and .claude/worktrees/ are pruned unconditionally, at any depth,
along with .git/; everything else in the repo is scanned, including
docs/plans/ (`covers:`/`requires:` frontmatter is repointed, nothing else
about a plan changes) and docs/specs2/ itself (a scaffolded document's own
`requires:` still points at docs/specs/ until this runs -- see fold.py's
compute_requires) -- except docs/specs2/mapping.yaml, this tool's own input,
which is never scanned or written (its from_id/to_id fields are literal
shorthand text that the shorthand branch would otherwise try to resolve).
A file that is not valid UTF-8 is skipped as non-text, silently.

`--corpus-to` overrides mapping.yaml's `corpus.to` for output paths only
(input recognition still keys off `corpus.from`, unaffected). Part 5's
cutover runs `refmap.py -w --corpus-to docs/specs` *before* `git mv`: at
that point docs/specs2/ is still the corpus's real on-disk location (so
`--root` must still resolve it there), but the references being written
need to point at the name the corpus will have *after* the same commit's
`git mv` -- not the transitional docs/specs2/ name, which `git mv` would
otherwise leave dangling in every reference this tool just wrote. Do not
reorder that sequence: running `git mv` first collides `corpus.from` with
the newly-moved corpus, `MAPPING_PATH` (docs/specs2/mapping.yaml) would no
longer resolve, and the bare-filename gate would start treating the new
corpus's own siblings as old-corpus references.

Usage: refmap.py [--dry-run | -w] [--root .] [--corpus-to DIR]
                 [--allow-dropped] [--force]

  --dry-run        report the rewrite plan without writing anything (default)
  -w               write the plan; refuses to write anything if any
                   reference is unmapped, if docs/specs2/.refmap-applied
                   already exists (this tree has already been rewritten
                   once), or if --root is not a clean git worktree
  --root           repository root to scan and rewrite (default: .)
  --corpus-to      override mapping.yaml's corpus.to for output paths only
  --allow-dropped  pass a recorded dropped: reference through unchanged
                   instead of failing (never-mapped references still fail)
  --force          bypass the idempotency marker and the clean-worktree
                   check (re-running -w after amending mapping.yaml)
"""

import argparse
import os
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("refmap: needs PyYAML (pip install pyyaml)")

MAPPING_PATH = Path("docs/specs2/mapping.yaml")
# Fixed alongside MAPPING_PATH, deliberately *not* under corpus_to: its job
# ("has -w already been run against this tree") does not depend on which
# output prefix that run used, and a marker whose own location moved with
# --corpus-to could miss a run made under a different override (see the
# module docstring's idempotency paragraph).
MARKER_PATH = Path("docs/specs2/.refmap-applied")
DEFAULT_CORPUS_FROM = "docs/specs"
DEFAULT_CORPUS_TO = "docs/specs2"
DEFAULT_PROJECT_KEY = "WL"

# Pruned unconditionally, at any depth (see module docstring). ".worktrees"
# is pruned by name alone; "worktrees" is pruned only when its immediate
# parent is ".claude", so an unrelated "worktrees" directory elsewhere in the
# tree is still scanned.
PRUNE_ANYWHERE = {".git", ".worktrees"}


class RefmapError(Exception):
    """A malformed mapping.yaml. The message names the offending key."""


@dataclass
class Mapping:
    corpus_from: str
    corpus_to: str
    documents: list  # [{"from":..., "to":..., "from_id":..., "to_id":...}, ...]
    sections: dict  # {"<old file>#<old anchor>": "<new file>#<new anchor>"}
    dropped: dict  # {"<ref>": "<reason>"}


DOCUMENT_KEYS = {"from", "to", "from_id", "to_id"}


def load_mapping(path: Path) -> Mapping:
    """Parse docs/specs2/mapping.yaml (scripts/fold.py --mapping's own output
    shape). Raises RefmapError, naming the offending key, when a documents[]
    or dropped[] entry is missing a key the derivation below needs -- this is
    not a general schema validator (that is fold.py's job over fold.yaml);
    it only guards the fields this module actually reads."""
    raw = yaml.safe_load(path.read_text()) or {}
    if not isinstance(raw, dict):
        raise RefmapError(f"{path}: mapping is not a mapping")

    corpus = raw.get("corpus") or {}
    documents = []
    for i, doc in enumerate(raw.get("documents") or []):
        missing = sorted(DOCUMENT_KEYS - set(doc))
        if missing:
            raise RefmapError(f"documents[{i}]: missing {missing[0]!r}")
        documents.append({k: str(doc[k]) for k in DOCUMENT_KEYS})

    dropped = {}
    for i, d in enumerate(raw.get("dropped") or []):
        if "ref" not in d:
            raise RefmapError(f"dropped[{i}]: missing 'ref'")
        dropped[str(d["ref"])] = str(d.get("reason", ""))

    sections = {str(k): str(v) for k, v in (raw.get("sections") or {}).items()}

    return Mapping(
        corpus_from=str(corpus.get("from") or DEFAULT_CORPUS_FROM),
        corpus_to=str(corpus.get("to") or DEFAULT_CORPUS_TO),
        documents=documents,
        sections=sections,
        dropped=dropped,
    )


FILENAME = r"\d{3,}-[\w-]+\.md"
# Word segments joined by internal dots, never ending on one: matches
# "3", "1.3", "2.1a" in full, but "3." (a sentence-ending period) leaves the
# trailing "." unconsumed for the caller's own trailing boundary to see,
# instead of swallowing it into the anchor (a bug fixed at cutover review --
# see the task-5-report.md "C2" entry).
FRAGMENT = r"#sec-[\w]+(?:\.[\w]+)*"
# Boxes every branch's match on the right: refuses a continuation character
# (word/hyphen), with or without a leading period first -- so "...md-old" and
# "...md.bak" both still refuse (a "." followed by a filename/word character
# is a real continuation), but "...md." at the end of a sentence, with
# nothing filename-like after the period, is accepted (fixed at cutover
# review -- "C2": the original `(?![\w.-])` excluded any trailing "." at
# all, so a path or shorthand or prose reference ending a sentence was
# silently never recognised as a reference in the first place).
TRAILING = r"(?!\.?[\w-])"
LEADING_NUMBER = re.compile(r"^(\d+)-")
# A zero-padded prose spec number longer than this many digits is not a
# plausible spec number in this corpus (today's max is 034) -- rejected
# outright rather than int()-collapsed, so "spec 0006" is never quietly
# read as spec 6 (fixed at cutover review -- "C1").
MAX_PROSENUM_DIGITS = 3


def spec_number_str(filename: str) -> str:
    """A filename's own leading number, zero-padding kept (fold.py's H1 title
    keeps "005", never collapses it to "5"; prose output matches that)."""
    m = LEADING_NUMBER.match(filename)
    if not m:
        raise RefmapError(f"{filename!r} has no leading number")
    return m.group(1)


@dataclass
class Index:
    mapping: Mapping
    project_key: str
    doc_by_from: dict  # from filename -> to filename
    id_by_from_id: dict  # from_id -> to_id
    filename_by_from_id: dict  # from_id -> from filename
    to_id_by_filename: dict  # to filename -> to_id
    pattern: re.Pattern


FROM_ID = re.compile(r"^([A-Za-z][\w]*)-SPEC-\d+$")


def project_key_of(mapping: Mapping) -> str:
    """The project key mapping.yaml's own ids were minted with (fold.py's
    spec_id()), read back from the first from_id rather than re-deriving it
    from .worklode/config.toml -- mapping.yaml is the one source of truth
    this module consumes (see the module docstring)."""
    for doc in mapping.documents:
        m = FROM_ID.match(doc["from_id"])
        if m:
            return m.group(1)
    return DEFAULT_PROJECT_KEY


def build_pattern(corpus_from: str, key: str) -> re.Pattern:
    """One regex, three mutually exclusive named-group branches (path,
    shorthand, prose). Each branch captures a maximal digit/anchor run via a
    character class and is boxed by a lookaround excluding adjacent word/path
    characters -- see the module docstring's word-boundary-safety paragraph.
    Never build this from a hand-listed alternation of known filenames, ids
    or anchors: that is the shape of bug this module exists to avoid."""
    path = (
        r"(?<![\w./-])"
        r"(?:(?P<pathprefix>" + re.escape(corpus_from) + r")/)?"
        r"(?P<filename>" + FILENAME + r")"
        r"(?P<pathfrag>" + FRAGMENT + r")?"
        + TRAILING
    )
    shorthand = (
        r"(?<![\w-])" + re.escape(key) + r"-(?P<kind>SPEC|ADR)-(?P<num>\d+)"
        r"(?P<shortfrag>" + FRAGMENT + r")?"
        + TRAILING
    )
    # Leading boundary excludes "-" and "." as well as \w (not just \w, the
    # original bug -- "C1"): a bare digit run is the least distinctive of
    # the three forms, so an ADR number ("ADR-0006 §3"), a version string
    # ("v1.3 §3") or a date ("2026-08-11 §3") must not be captured as if the
    # trailing digits were a spec number.
    prose = (
        r"(?<![\w.-])(?P<proseprefix>spec )?(?P<prosenum>\d+) §(?P<prosesec>\d+(?:\.\d+)*)"
        + TRAILING
    )
    return re.compile(f"{path}|{shorthand}|{prose}")


def build_index(mapping: Mapping) -> Index:
    doc_by_from, id_by_from_id, filename_by_from_id, to_id_by_filename = {}, {}, {}, {}
    for d in mapping.documents:
        doc_by_from[d["from"]] = d["to"]
        id_by_from_id[d["from_id"]] = d["to_id"]
        filename_by_from_id[d["from_id"]] = d["from"]
        to_id_by_filename[d["to"]] = d["to_id"]

    key = project_key_of(mapping)
    return Index(
        mapping=mapping,
        project_key=key,
        doc_by_from=doc_by_from,
        id_by_from_id=id_by_from_id,
        filename_by_from_id=filename_by_from_id,
        to_id_by_filename=to_id_by_filename,
        pattern=build_pattern(mapping.corpus_from, key),
    )


def make_replacer(index: Index, file_is_in_corpus: bool, allow_dropped: bool,
                   subs: list, unmapped: list):
    """One re.sub callback closing over one file's collected substitutions
    and unmapped refs -- dispatches on which of the pattern's three branches
    fired, by which of that branch's own required groups is non-None."""
    m = index.mapping

    def fail_or_pass(ref, original):
        """A lookup miss's common tail: pass through unchanged and silently
        under --allow-dropped when `ref` is a *recorded* drop (a human
        already decided leaving it stale is fine); otherwise -- including
        every never-mapped ref, which --allow-dropped does not touch -- it
        is a hard failure regardless of the flag."""
        reason = m.dropped.get(ref)
        if not (allow_dropped and reason is not None):
            unmapped.append((ref, reason))
        return original

    def resolve_path(match):
        filename = match.group("filename")
        frag = match.group("pathfrag")
        prefixed = match.group("pathprefix") is not None
        original = match.group(0)
        if not prefixed and not file_is_in_corpus:
            return original  # bare filename outside the corpus dir: not a reference
        if frag:
            old_ref = f"{filename}{frag}"
            new_ref = m.sections.get(old_ref)
            if new_ref is None:
                return fail_or_pass(old_ref, original)
            new_tail = new_ref  # already "<newfile>#<newanchor>"
        else:
            new_filename = index.doc_by_from.get(filename)
            if new_filename is None:
                return fail_or_pass(filename, original)
            new_tail = new_filename
        replacement = f"{m.corpus_to}/{new_tail}" if prefixed else new_tail
        subs.append((original, replacement))
        return replacement

    def resolve_shorthand(match):
        original = match.group(0)
        if match.group("kind") != "SPEC":
            return original  # WL-ADR-<n>: not part of this fold, always left alone
        num = match.group("num")
        frag = match.group("shortfrag")
        from_id = f"{index.project_key}-SPEC-{int(num)}"
        if frag:
            filename = index.filename_by_from_id.get(from_id)
            if filename is None:
                return fail_or_pass(f"{from_id}{frag}", original)
            old_ref = f"{filename}{frag}"
            new_ref = m.sections.get(old_ref)
            if new_ref is None:
                return fail_or_pass(old_ref, original)
            new_filename, new_anchor = new_ref.split("#", 1)
            to_id = index.to_id_by_filename.get(new_filename)
            replacement = f"{to_id}#{new_anchor}"
        else:
            to_id = index.id_by_from_id.get(from_id)
            if to_id is None:
                return fail_or_pass(from_id, original)
            replacement = to_id
        subs.append((original, replacement))
        return replacement

    def resolve_prose(match):
        original = match.group(0)
        prefix = match.group("proseprefix") or ""
        num = match.group("prosenum")
        if len(num) > MAX_PROSENUM_DIGITS:
            return original  # not a plausible zero-padded spec number: not a reference
        sec = match.group("prosesec")
        from_id = f"{index.project_key}-SPEC-{int(num)}"
        filename = index.filename_by_from_id.get(from_id)
        if filename is None:
            return fail_or_pass(f"{from_id}#sec-{sec}", original)
        old_ref = f"{filename}#sec-{sec}"
        new_ref = m.sections.get(old_ref)
        if new_ref is None:
            return fail_or_pass(old_ref, original)
        new_filename, new_anchor = new_ref.split("#", 1)
        new_number = spec_number_str(new_filename)
        new_sec = new_anchor[len("sec-"):]
        replacement = f"{prefix}{new_number} §{new_sec}"
        subs.append((original, replacement))
        return replacement

    def replace(match):
        if match.group("filename"):
            return resolve_path(match)
        if match.group("kind"):
            return resolve_shorthand(match)
        return resolve_prose(match)

    return replace


@dataclass
class FileResult:
    rel: Path
    changed: bool
    new_text: str
    subs: list = field(default_factory=list)  # [(lineno, old, new)]
    unmapped: list = field(default_factory=list)  # [(lineno, ref, reason)]


def should_prune(parent_parts: tuple, name: str) -> bool:
    if name in PRUNE_ANYWHERE:
        return True
    return name == "worktrees" and bool(parent_parts) and parent_parts[-1] == ".claude"


def iter_files(root: Path):
    """Every file under `root`, excluding .git/, .worktrees/ and
    .claude/worktrees/ at any depth (see the module docstring), and two
    files this tool owns rather than scans: docs/specs2/mapping.yaml (its
    own from_id/to_id fields are literal "<KEY>-SPEC-<n>" text, which the
    shorthand branch matches with no file-location gating, unlike the
    bare-filename path branch -- scanning it would rewrite it out from under
    itself) and docs/specs2/.refmap-applied, the idempotency marker (its
    content is this tool's own bookkeeping, not repo prose)."""
    skip = {(root / MAPPING_PATH).resolve(), (root / MARKER_PATH).resolve()}
    for dirpath, dirnames, filenames in os.walk(root):
        rel_dir = Path(dirpath).relative_to(root)
        parts = () if rel_dir == Path(".") else rel_dir.parts
        dirnames[:] = [d for d in dirnames if not should_prune(parts, d)]
        for name in filenames:
            path = Path(dirpath) / name
            if path.resolve() in skip:
                continue
            yield path


def process_file(path: Path, root: Path, index: Index, allow_dropped: bool):
    """One file's rewrite plan, or None when the file is not text. Applies
    the pattern line by line -- references never span a line in practice,
    and line numbers are what the unmapped/substitution report needs --
    preserving each line's own ending via splitlines(keepends=True) so a
    file with no matches round-trips byte-identical."""
    try:
        text = path.read_text(encoding="utf-8")
    except (UnicodeDecodeError, IsADirectoryError):
        return None

    rel = path.relative_to(root)
    in_corpus = rel.is_relative_to(Path(index.mapping.corpus_from))

    subs, unmapped, new_lines, changed = [], [], [], False
    for lineno, line in enumerate(text.splitlines(keepends=True), start=1):
        line_subs, line_unmapped = [], []
        replacer = make_replacer(index, in_corpus, allow_dropped, line_subs, line_unmapped)
        new_line = index.pattern.sub(replacer, line)
        if line_subs:
            changed = True
        subs.extend((lineno, old, new) for old, new in line_subs)
        unmapped.extend((lineno, ref, reason) for ref, reason in line_unmapped)
        new_lines.append(new_line)

    return FileResult(rel=rel, changed=changed, new_text="".join(new_lines),
                       subs=subs, unmapped=unmapped)


def worktree_state(root: Path) -> str:
    """'clean', 'dirty', or 'not-a-repo' for `root`.

    NOT the idempotency mechanism -- see MARKER_PATH / write_marker for
    that; this is a secondary hygiene guard `-w` checks before writing, kept
    because a clean starting point makes the resulting diff attributable to
    this run alone and trivially revertable (`git checkout .`) if something
    looks wrong before it's committed. It catches nothing a committed marker
    doesn't already catch on its own, and catches nothing at all once the
    first run's output has been committed -- a clean tree only means
    "nothing uncommitted right now", not "this tree has never been
    rewritten". Fails closed: anything other than a provably clean git
    worktree refuses to write."""
    try:
        probe = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--is-inside-work-tree"],
            capture_output=True, text=True, check=False,
        )
    except FileNotFoundError:
        return "not-a-repo"
    if probe.returncode != 0 or probe.stdout.strip() != "true":
        return "not-a-repo"
    status = subprocess.run(
        ["git", "-C", str(root), "status", "--porcelain"],
        capture_output=True, text=True, check=False,
    )
    return "clean" if status.returncode == 0 and not status.stdout.strip() else "dirty"


def write_marker(root: Path, mapping: Mapping, total_subs: int, changed: int) -> None:
    """Write the durable idempotency marker after a successful -w. Deliberately
    a plain, human-readable file rather than a stamp inside mapping.yaml:
    mapping.yaml is fold.py's own generated artifact (docs/plans/....md's
    "never hand-edit it, generated from fold.yaml alone" -- a second writer,
    even one only appending a key, muddies that), and mapping.yaml is
    already excluded from every scan/rewrite pass (see iter_files) precisely
    so nothing but fold.py ever touches it. A sibling file keeps that
    invariant intact and needs no YAML round-trip (which would risk
    reformatting a file this tool does not own)."""
    (root / MARKER_PATH).write_text(
        "# Written by scripts/refmap.py -w. Its presence blocks a second -w\n"
        "# run against this tree -- old and new spec numbering both start at\n"
        "# 1, so a rewritten id or path can coincidentally look like a fresh\n"
        "# old-corpus reference on a second pass, and this file is what makes\n"
        "# that unsafe even after the first run has been committed (a clean\n"
        "# git worktree is not evidence this file's absence would be).\n"
        "#\n"
        "# Delete this file, or pass --force, to re-run after amending\n"
        "# mapping.yaml.\n"
        f"corpus_to: {mapping.corpus_to}\n"
        f"substitutions: {total_subs}\n"
        f"files_changed: {changed}\n"
    )


def report(results: list, out) -> None:
    for r in results:
        for lineno, old, new in r.subs:
            print(f"{r.rel.as_posix()}:{lineno}: {old} -> {new}", file=out)
    unmapped = [(r, lineno, ref, reason) for r in results for lineno, ref, reason in r.unmapped]
    if unmapped:
        print("", file=out)
        print("refmap: unmapped references (fold dropped, or not yet covered by "
              "mapping.yaml -- needs a human decision):", file=out)
        for r, lineno, ref, reason in unmapped:
            suffix = f" ({reason})" if reason else ""
            print(f"  {r.rel.as_posix()}:{lineno}: {ref}{suffix}", file=out)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--dry-run", action="store_true",
                     help="report the rewrite plan without writing (default)")
    ap.add_argument("-w", "--write", action="store_true", help="write the plan")
    ap.add_argument("--root", default=".", help="repository root to scan (default: .)")
    ap.add_argument("--corpus-to", metavar="DIR",
                     help="override mapping.yaml's corpus.to for output paths only "
                          "(Part 5's cutover runs -w --corpus-to docs/specs before git mv)")
    ap.add_argument("--allow-dropped", action="store_true",
                     help="pass a reference through unchanged, instead of failing, when its "
                          "target is in mapping.yaml's dropped: list (a recorded human "
                          "decision) -- a never-mapped reference still fails regardless")
    ap.add_argument("--force", action="store_true",
                     help="bypass the idempotency marker and the clean-worktree check -- "
                          "for re-running -w after amending mapping.yaml")
    a = ap.parse_args(argv)
    if a.dry_run and a.write:
        ap.error("--dry-run and -w are mutually exclusive")

    root = Path(a.root).resolve()

    if a.write and not a.force and (root / MARKER_PATH).exists():
        print(f"refmap: {MARKER_PATH} exists -- refmap.py -w has already run against this "
              "tree (this survives a commit; it is not the git-dirty check). Delete it, or "
              "pass --force, to re-run after amending mapping.yaml.")
        return 1

    mapping_path = root / MAPPING_PATH
    if not mapping_path.is_file():
        print(f"refmap: {mapping_path} not found -- run scripts/fold.py --mapping first",
              file=sys.stderr)
        return 2

    try:
        mapping = load_mapping(mapping_path)
    except RefmapError as e:
        print(f"refmap: {e}", file=sys.stderr)
        return 2

    if a.corpus_to:
        mapping.corpus_to = a.corpus_to

    index = build_index(mapping)
    files = sorted(iter_files(root), key=lambda p: p.relative_to(root).as_posix())
    results = [r for r in (process_file(p, root, index, a.allow_dropped) for p in files)
               if r is not None]

    total_subs = sum(len(r.subs) for r in results)
    total_unmapped = sum(len(r.unmapped) for r in results)
    changed = [r for r in results if r.changed]

    report(results, sys.stdout)

    if total_unmapped:
        print(f"\nrefmap: {total_unmapped} unmapped reference(s) -- fix mapping.yaml "
              "or resolve by hand; nothing written")
        return 1

    if a.write:
        state = worktree_state(root)
        if state != "clean" and not a.force:
            why = ("is not a git working tree" if state == "not-a-repo"
                    else "has uncommitted changes")
            print(f"\nrefmap: --root {why} -- refusing to write (a clean starting point "
                  "keeps this run's diff attributable and revertable). Commit or stash "
                  "first, use --dry-run to preview, or --force to override.")
            return 1
        for r in changed:
            (root / r.rel).write_text(r.new_text, encoding="utf-8")
        write_marker(root, mapping, total_subs, len(changed))
        print(f"\nrefmap: wrote {total_subs} substitution(s) across {len(changed)} file(s)")
        return 0

    print(f"\nrefmap: dry run -- {total_subs} substitution(s) across {len(changed)} "
          "file(s) would change (rerun with -w to write)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
