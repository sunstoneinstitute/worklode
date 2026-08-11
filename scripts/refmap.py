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

A recognised reference that resolves into docs/specs/ but has no match in
`sections:` (fragment form) or `documents:` (whole-document form, including
the shorthand and prose forms once resolved to a source filename) points at
material the fold either dropped or has not folded yet. This is a hard
failure, not a warning: every one is listed with its file and line, and the
whole run refuses to write -- not even the substitutions that did resolve --
so a run can never leave the tree half-rewritten. `--dry-run` (the default)
computes the same full plan and reports the same failure without writing;
`-w` writes, and only when the plan is entirely clean.

.worktrees/ and .claude/worktrees/ are pruned unconditionally, at any depth,
along with .git/; everything else in the repo is scanned, including
docs/plans/ (`covers:`/`requires:` frontmatter is repointed, nothing else
about a plan changes) and docs/specs2/ itself (a scaffolded document's own
`requires:` still points at docs/specs/ until this runs -- see fold.py's
compute_requires) -- except docs/specs2/mapping.yaml, this tool's own input,
which is never scanned or written (its from_id/to_id fields are literal
shorthand text that the shorthand branch would otherwise try to resolve).
A file that is not valid UTF-8 is skipped as non-text, silently.

Usage: refmap.py [--dry-run | -w] [--root .]

  --dry-run   report the rewrite plan without writing anything (default)
  -w          write the plan; refuses to write anything if any reference is
              unmapped
  --root      repository root to scan and rewrite (default: .)
"""

import argparse
import os
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("refmap: needs PyYAML (pip install pyyaml)")

MAPPING_PATH = Path("docs/specs2/mapping.yaml")
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
FRAGMENT = r"#sec-[\w.]+"
LEADING_NUMBER = re.compile(r"^(\d+)-")


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
        r"(?![\w.-])"
    )
    shorthand = (
        r"(?<![\w-])" + re.escape(key) + r"-(?P<kind>SPEC|ADR)-(?P<num>\d+)"
        r"(?P<shortfrag>" + FRAGMENT + r")?"
        r"(?![\w.-])"
    )
    prose = (
        r"(?<![\w])(?P<proseprefix>spec )?(?P<prosenum>\d+) §(?P<prosesec>\d+(?:\.\d+)*)"
        r"(?![\w.])"
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


def make_replacer(index: Index, file_is_in_corpus: bool, subs: list, unmapped: list):
    """One re.sub callback closing over one file's collected substitutions
    and unmapped refs -- dispatches on which of the pattern's three branches
    fired, by which of that branch's own required groups is non-None."""
    m = index.mapping

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
                unmapped.append((old_ref, m.dropped.get(old_ref)))
                return original
            new_tail = new_ref  # already "<newfile>#<newanchor>"
        else:
            new_filename = index.doc_by_from.get(filename)
            if new_filename is None:
                unmapped.append((filename, None))
                return original
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
                unmapped.append((f"{from_id}{frag}", None))
                return original
            old_ref = f"{filename}{frag}"
            new_ref = m.sections.get(old_ref)
            if new_ref is None:
                unmapped.append((old_ref, m.dropped.get(old_ref)))
                return original
            new_filename, new_anchor = new_ref.split("#", 1)
            to_id = index.to_id_by_filename.get(new_filename)
            replacement = f"{to_id}#{new_anchor}"
        else:
            to_id = index.id_by_from_id.get(from_id)
            if to_id is None:
                unmapped.append((from_id, None))
                return original
            replacement = to_id
        subs.append((original, replacement))
        return replacement

    def resolve_prose(match):
        original = match.group(0)
        prefix = match.group("proseprefix") or ""
        num = match.group("prosenum")
        sec = match.group("prosesec")
        from_id = f"{index.project_key}-SPEC-{int(num)}"
        filename = index.filename_by_from_id.get(from_id)
        old_ref = f"{filename}#sec-{sec}" if filename else f"{from_id}#sec-{sec}"
        if filename is None:
            unmapped.append((old_ref, None))
            return original
        new_ref = m.sections.get(old_ref)
        if new_ref is None:
            unmapped.append((old_ref, m.dropped.get(old_ref)))
            return original
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
    .claude/worktrees/ at any depth (see the module docstring), and
    docs/specs2/mapping.yaml itself. mapping.yaml's own from_id/to_id fields
    are literal "<KEY>-SPEC-<n>" text, which the shorthand branch matches
    with no file-location gating (unlike the bare-filename path branch) --
    scanning the tool's own input would rewrite it out from under itself, so
    it is the one file this tool never touches."""
    mapping_path = (root / MAPPING_PATH).resolve()
    for dirpath, dirnames, filenames in os.walk(root):
        rel_dir = Path(dirpath).relative_to(root)
        parts = () if rel_dir == Path(".") else rel_dir.parts
        dirnames[:] = [d for d in dirnames if not should_prune(parts, d)]
        for name in filenames:
            path = Path(dirpath) / name
            if path.resolve() == mapping_path:
                continue
            yield path


def process_file(path: Path, root: Path, index: Index):
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
        replacer = make_replacer(index, in_corpus, line_subs, line_unmapped)
        new_line = index.pattern.sub(replacer, line)
        if line_subs:
            changed = True
        subs.extend((lineno, old, new) for old, new in line_subs)
        unmapped.extend((lineno, ref, reason) for ref, reason in line_unmapped)
        new_lines.append(new_line)

    return FileResult(rel=rel, changed=changed, new_text="".join(new_lines),
                       subs=subs, unmapped=unmapped)


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
    a = ap.parse_args(argv)
    if a.dry_run and a.write:
        ap.error("--dry-run and -w are mutually exclusive")

    root = Path(a.root).resolve()
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

    index = build_index(mapping)
    files = sorted(iter_files(root), key=lambda p: p.relative_to(root).as_posix())
    results = [r for r in (process_file(p, root, index) for p in files) if r is not None]

    total_subs = sum(len(r.subs) for r in results)
    total_unmapped = sum(len(r.unmapped) for r in results)
    changed = [r for r in results if r.changed]

    report(results, sys.stdout)

    if total_unmapped:
        print(f"\nrefmap: {total_unmapped} unmapped reference(s) -- fix mapping.yaml "
              "or resolve by hand; nothing written")
        return 1

    if a.write:
        for r in changed:
            (root / r.rel).write_text(r.new_text, encoding="utf-8")
        print(f"\nrefmap: wrote {total_subs} substitution(s) across {len(changed)} file(s)")
        return 0

    print(f"\nrefmap: dry run -- {total_subs} substitution(s) across {len(changed)} "
          "file(s) would change (rerun with -w to write)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
