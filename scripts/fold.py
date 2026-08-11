#!/usr/bin/env python3
"""Fold docs/specs/ into docs/specs2/ per the hand-written placement file
docs/specs2/fold.yaml (2026-08-11-spec-corpus-consolidation-1-fold-tooling).

fold.yaml is the only place a folding decision is recorded; everything else
-- the skeleton documents, docs/specs2/mapping.yaml, and the checks -- is
derived from it. This module owns that derivation. `load_fold` parses and
validates one fold.yaml; `derive_mapping` turns a validated Fold into
mapping.yaml's structure, from fold.yaml alone, never by reading the written
prose in docs/specs2/*.md.

One fold.yaml document entry can list several `sources:` (an N:1 merge) and
each of its sections can list several `from:` anchors (several old sections
folded into one new one); `derive_mapping` fans both out so every old anchor
and every old source file gets its own row.

`--check` proves fold.yaml accounts for every section currentspec.py's
--with-drafts view still counts as live in docs/specs/: every live anchor
must appear exactly once across all `from:` and `dropped:` refs in
fold.yaml, and every `from:`/`dropped:` ref must name a live anchor.
`--check --partial` narrows the "every live anchor is placed" half of that
to the documents fold.yaml currently declares (via `sources:`), so parts 2-4
can run the check while the fold is still incomplete; the "no ref is
duplicated or dangling" half always runs over the whole fold.

`--check` also guards the other side of the fold once the rewrite pass
starts: for every fold.yaml document already written to docs/specs2/<to>, its
declared `new:` anchors must match the anchors secindex.sections_of() finds
in the written file, reported as two separate directions -- `missing`
(declared, dropped by the rewrite) and `undeclared` (written, never
declared). A document fold.yaml declares but that has no file yet at
docs/specs2/<to> is unstarted work, not drift, and is skipped silently; this
is what lets the check run continuously as parts 2-4 write documents one at a
time. Only anchor presence is compared -- a heading whose title text the
rewrite improved is not drift.

Usage: fold.py --mapping | --check [--partial] [--ids] | --scaffold [--only FILE]

  --mapping   derive docs/specs2/mapping.yaml from docs/specs2/fold.yaml
  --check     prove fold.yaml accounts for every live section of docs/specs/
  --partial   with --check, only require full coverage of the documents
              fold.yaml currently declares
  --ids       with --check, also verify every backticked identifier and
              fenced code block from the source sections survives into the
              written docs/specs2/ prose
  --scaffold  write docs/specs2/<to> skeleton documents from fold.yaml
  --only      with --scaffold, write only the named <to> document

--scaffold writes a skeleton per declared document: frontmatter
(`status: draft` plus a computed `requires:`), numbered headings from each
section's `heading`, and each section's source text pasted in verbatim
behind an HTML-comment provenance marker, ready for the rewrite pass that
turns it into prose. It refuses to overwrite an existing docs/specs2/<to> --
regenerating after the rewrite has started would lose the rewrite -- and
never writes any document when any target of the invocation already exists.

--check --ids (Task 6) adds a sixth report group, "dropped ids": for every
document already written to docs/specs2/<to>, every identifier (an inline
`` `...` ``/``` ``...`` ``` backtick span, or the full verbatim content of a
triple-backtick fenced code block -- a spec's fenced block is typically its
authoritative schema or CLI surface, so a rewrite dropping the whole block is
the same failure as dropping a span) collected from the source sections
fold.yaml places there must still appear, exact-text, somewhere in the
written prose -- scoped to the whole document, since a legitimate merge may
move a term between sections. A per-document `allow_dropped_ids:` mapping
(identifier -> reason, reason required) exempts specific drops. `--ids` only
modifies `--check`; combined with `--mapping` or `--scaffold` it is rejected
at the CLI, and omitting it leaves the report at five groups, unchanged.
"""

import argparse
import re
import sys
import tomllib
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("fold: needs PyYAML (pip install pyyaml)")

sys.dont_write_bytecode = True  # importing currentspec/secindex/secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
import currentspec  # noqa: E402
import secfmt  # noqa: E402
import secindex  # noqa: E402

REPO = Path(__file__).resolve().parent.parent
FOLD_PATH = Path("docs/specs2/fold.yaml")
MAPPING_PATH = Path("docs/specs2/mapping.yaml")
DEFAULT_SPECS_DIR = "docs/specs"
DEFAULT_SPECS2_DIR = "docs/specs2"
DEFAULT_PROJECT_KEY = "WL"

# fold.yaml's own top-level keys (see the plan's "The fold.yaml schema") and
# the keys one `sections:` entry may carry, and the keys one `dropped:` entry
# may carry. `from` cannot be a dataclass field (it is a Python keyword), so
# section and dropped entries stay plain dicts rather than growing a parallel
# class for two field names.
FOLD_KEYS = {"version", "corpus", "documents"}
DOCUMENT_KEYS = {"to", "title", "sources", "sections", "dropped", "allow_dropped_ids"}
SECTION_KEYS = {"new", "heading", "from"}
DROPPED_KEYS = {"ref", "reason"}


class FoldError(Exception):
    """A malformed fold.yaml. The message names the offending ref or key and
    the document it was found in."""


@dataclass
class Document:
    to: str
    title: str
    sources: list
    sections: list  # each {"new": str, "heading": str, "from": [str, ...]}
    dropped: list = field(default_factory=list)  # each {"ref": str, "reason": str}
    allow_dropped_ids: dict = field(default_factory=dict)  # {identifier: reason}


@dataclass
class Fold:
    version: int
    corpus: dict  # {"from": <repo-relative dir>, "to": <repo-relative dir>}
    documents: list  # [Document, ...]


# A plain top-level number ("2") needs no parent. A dotted number ("2.1")
# needs its number with the last component dropped ("2"). A letter-suffixed
# insert (014 section 3's "2.1a") keeps its host's position and needs that
# host declared ("2.1"). This mirrors secfmt.py's renumber() parent rule,
# restated for fold.yaml's flat `new:` list rather than heading depth.
SECTION_NUMBER = re.compile(r"^(\d+(?:\.\d+)*)([a-z])?$")


def parent_of(number):
    """The `new:` number that must already be declared for `number` to be
    well-formed, or None when `number` is top-level."""
    m = SECTION_NUMBER.match(number)
    if not m:
        raise FoldError(f"{number!r} is not a well-formed section number")
    digits, letter = m.groups()
    if letter:
        return digits
    if "." in digits:
        return digits.rsplit(".", 1)[0]
    return None


SPEC_NUMBER = re.compile(r"^(\d+)-")


def spec_id(filename, key):
    """<key>-SPEC-<n> for a spec filename, n being its leading number with
    leading zeros stripped (004-execution-backbone.md -> WL-SPEC-4)."""
    m = SPEC_NUMBER.match(Path(filename).name)
    if not m:
        raise FoldError(f"{filename!r} has no leading number to derive a spec id from")
    return f"{key}-SPEC-{int(m.group(1))}"


def project_key():
    """The design-doc project key from .worklode/config.toml's `project_key`,
    defaulting to WL when the file or the key is absent."""
    path = REPO / ".worklode" / "config.toml"
    if not path.is_file():
        return DEFAULT_PROJECT_KEY
    data = tomllib.loads(path.read_text())
    return str(data.get("project_key") or DEFAULT_PROJECT_KEY)


def load_fold(path) -> Fold:
    """Parse and validate one fold.yaml. Raises FoldError, naming the
    offending ref or key and the document, on:

      * an unknown key at the fold-document level, within a section entry,
        or within a dropped entry
      * a document missing `to` or `title`
      * a dropped entry missing `ref` or `reason`
      * an `allow_dropped_ids` entry with no reason
      * a `new:` number whose parent is not declared in the same document
      * a duplicate `new:` within a document
      * a `from:` ref with no `#sec-` fragment
    """
    path = Path(path)
    raw = yaml.safe_load(path.read_text()) or {}
    if not isinstance(raw, dict):
        raise FoldError(f"{path.name}: fold document is not a mapping")

    unknown = sorted(str(k) for k in set(raw) - FOLD_KEYS)
    if unknown:
        raise FoldError(f"{path.name}: unknown key {unknown[0]!r}")

    corpus = raw.get("corpus") or {}
    documents = []
    for i, doc in enumerate(raw.get("documents") or []):
        unknown = sorted(str(k) for k in set(doc) - DOCUMENT_KEYS)
        if unknown:
            raise FoldError(f"documents[{i}]: unknown key {unknown[0]!r}")
        to = doc.get("to")
        if not to:
            raise FoldError(f"documents[{i}]: missing 'to'")
        title = doc.get("title")
        if not title:
            raise FoldError(f"{to}: missing 'title'")
        sources = list(doc.get("sources") or [])
        raw_sections = list(doc.get("sections") or [])

        dropped = []
        for d in doc.get("dropped") or []:
            missing = sorted(DROPPED_KEYS - set(d))
            if missing:
                raise FoldError(f"{to}: dropped entry missing {missing[0]!r}")
            unknown = sorted(str(k) for k in set(d) - DROPPED_KEYS)
            if unknown:
                raise FoldError(f"{to}: unknown key {unknown[0]!r} in a dropped entry")
            dropped.append({"ref": str(d["ref"]), "reason": str(d["reason"])})

        # A mapping, not a list like `dropped:` -- one reason per identifier,
        # never a list of {ref, reason} pairs, since the identifier itself
        # (not a synthetic ref) is already the natural unique key here.
        allow_dropped_ids = {}
        raw_allow = doc.get("allow_dropped_ids") or {}
        for ident, reason in raw_allow.items():
            reason = str(reason).strip() if reason is not None else ""
            if not reason:
                raise FoldError(f"{to}: allow_dropped_ids entry {str(ident)!r} has no reason")
            allow_dropped_ids[str(ident)] = reason

        # First pass: every key is known and every `new:` is declared once,
        # so the second pass can check each section's parent against the
        # document's full declared set regardless of list order.
        declared = set()
        for entry in raw_sections:
            unknown = sorted(str(k) for k in set(entry) - SECTION_KEYS)
            if unknown:
                raise FoldError(f"{to}: unknown key {unknown[0]!r} in a section entry")
            new = str(entry["new"])
            if new in declared:
                raise FoldError(f"{to}: duplicate section number {new!r}")
            declared.add(new)

        sections = []
        for entry in raw_sections:
            new = str(entry["new"])
            refs = [str(r) for r in (entry.get("from") or [])]
            for ref in refs:
                if "#sec-" not in ref:
                    raise FoldError(f"{to}: {ref!r} has no #sec- fragment")
            parent = parent_of(new)
            if parent is not None and parent not in declared:
                raise FoldError(f"{to}: section {new!r} has no declared parent {parent!r}")
            sections.append({"new": new, "heading": entry.get("heading", ""), "from": refs})

        documents.append(Document(to=to, title=title, sources=sources,
                                   sections=sections, dropped=dropped,
                                   allow_dropped_ids=allow_dropped_ids))

    return Fold(version=raw.get("version", 1), corpus=corpus, documents=documents)


def derive_mapping(fold: Fold) -> dict:
    """mapping.yaml's structure (see the plan's "Derived mapping.yaml"),
    derived from `fold` alone. `documents:` has one row per source file (an
    N:1 merge fans out here), `sections:` maps every `from:` anchor onto its
    section's new anchor, and `dropped:` is fold.yaml's dropped entries
    concatenated in document order."""
    key = project_key()
    documents, sections, dropped = [], {}, []
    for doc in fold.documents:
        to_id = spec_id(doc.to, key)
        for source in doc.sources:
            documents.append({
                "from": source,
                "to": doc.to,
                "from_id": spec_id(source, key),
                "to_id": to_id,
            })
        for section in doc.sections:
            target = f"{doc.to}#sec-{section['new']}"
            for ref in section["from"]:
                sections[ref] = target
        dropped.extend(doc.dropped)

    return {
        "version": fold.version,
        "corpus": {"from": fold.corpus.get("from"), "to": fold.corpus.get("to")},
        "documents": documents,
        "sections": sections,
        "dropped": dropped,
    }


def dump_mapping(mapping) -> str:
    """Stable, sorted-key-free YAML so two derivations of the same fold.yaml
    emit byte-identical files and diffs stay readable."""
    return yaml.safe_dump(mapping, sort_keys=False, default_flow_style=False, allow_unicode=True)


def load_live_corpus(specs_dir: Path) -> dict:
    """{(path, anchor): heading} for currentspec.py's --with-drafts view of
    `specs_dir` -- the live corpus fold.yaml must fully account for. Raises
    FoldError, naming the index, when docs/specs/index.yaml is missing or
    stale, rather than silently checking against an out-of-date view."""
    specs_dir = specs_dir.resolve()
    index_path = specs_dir / "index.yaml"
    rel = index_path.relative_to(REPO)
    if not index_path.is_file():
        raise FoldError(f"{rel} is missing -- run scripts/secindex.py")
    if index_path.read_text() != secindex.render(specs_dir, REPO):
        raise FoldError(f"{rel} is stale -- run scripts/secindex.py")
    docs = currentspec.load(index_path)
    return currentspec.live_sections(docs, with_drafts=True)


def run_check(fold: Fold, partial: bool, ids: bool = False) -> list:
    """[(label, [ref, ...]), ...] comparing fold.yaml's declared placements
    against the live corpus and, separately, against the written docs/specs2/
    prose -- five groups today: "unplaced", "placed twice", "dangling"
    (fold.yaml vs. docs/specs/, see below), and "missing"/"undeclared"
    (fold.yaml vs. docs/specs2/, see below). A ref is `<filename>#<anchor>`,
    fold.yaml's own `from:`/`ref:`/`new:`-derived spelling.

    `ids=True` appends a sixth group, "dropped ids" (see check_ids()), over
    the same per-document written-file loop that computes missing/undeclared
    -- reusing its existence gate rather than a second pass over
    fold.documents. `ids=False` (the default) leaves the report at five
    groups, byte-identical to before this parameter existed.

    `partial` narrows only the unplaced check, to anchors belonging to a file
    some fold.yaml entry already accounts for -- named in a `sources:` list,
    *or* the source of a placed or dropped ref, since a `from:`/`dropped:`
    ref against a file is itself a declaration that the file is in play,
    whether or not its name also happens to be in `sources:`. Without the
    union, a document whose `sources:` undershoots what it actually places
    exempts the rest of that file's anchors instead of catching them as
    unplaced. Placed-twice and dangling always run over everything fold.yaml
    declares, because a duplicate or a phantom ref is a fold.yaml defect
    regardless of how much of the corpus is folded yet.

    missing/undeclared are unaffected by `partial` for the same reason: each
    is already scoped to exactly the documents fold.yaml declares, minus
    whichever of those have no file yet at docs/specs2/<to> -- that document
    is unstarted work, not drift, so it is skipped rather than reported."""
    specs_dir = REPO / (fold.corpus.get("from") or DEFAULT_SPECS_DIR)
    live = load_live_corpus(specs_dir)
    live_refs = {f"{Path(path).name}#{anchor}" for path, anchor in live}

    placements = []
    for doc in fold.documents:
        for section in doc.sections:
            placements.extend(section["from"])
        placements.extend(d["ref"] for d in doc.dropped)

    counts = Counter(placements)
    placed_twice = sorted(ref for ref, n in counts.items() if n > 1)

    declared = set(placements)
    dangling = sorted(ref for ref in declared if ref not in live_refs)

    if partial:
        sources = {s for doc in fold.documents for s in doc.sources}
        sources |= {ref.split("#", 1)[0] for ref in placements}
        scope = {ref for ref in live_refs if ref.split("#", 1)[0] in sources}
    else:
        scope = live_refs
    unplaced = sorted(scope - declared)

    out_dir = REPO / (fold.corpus.get("to") or DEFAULT_SPECS2_DIR)
    missing, undeclared, dropped_ids = [], [], []
    for doc in fold.documents:
        written = out_dir / doc.to
        if not written.is_file():
            continue  # unstarted work, not drift
        declared_anchors = {f"sec-{section['new']}" for section in doc.sections}
        written_anchors = {key for key, _heading in secindex.sections_of(written)}
        missing.extend(f"{doc.to}#{a}" for a in sorted(declared_anchors - written_anchors))
        undeclared.extend(f"{doc.to}#{a}" for a in sorted(written_anchors - declared_anchors))
        if ids:
            dropped_ids.extend(check_ids(doc, specs_dir, written))

    groups = [
        ("unplaced", unplaced),
        ("placed twice", placed_twice),
        ("dangling", dangling),
        ("missing", missing),
        ("undeclared", undeclared),
    ]
    if ids:
        groups.append(("dropped ids", dropped_ids))
    return groups


def _number_parts(new: str) -> tuple:
    """(digit tuple, letter-or-empty) for a fold.yaml `new:` number -- the same
    decomposition parent_of() already trusts for validation, reused here to
    put a document's sections in numeric order and to derive heading depth
    (how many dot-separated components the number has)."""
    m = SECTION_NUMBER.match(new)
    if not m:
        raise FoldError(f"{new!r} is not a well-formed section number")
    digits, letter = m.groups()
    return tuple(int(p) for p in digits.split(".")), (letter or "")


def _heading_level(new: str) -> int:
    """1 for a top-level `new:` ("0", "1"), 2 for one dot ("1.1"), 3 for two
    ("1.1.2") -- H2/H3/H4 per 014 §7's depth-3 addressable limit."""
    digits, _letter = _number_parts(new)
    return len(digits)


def _split_ref(ref: str) -> tuple:
    """A fold.yaml ref ("<filename>#<anchor>") split into its parts. load_fold
    already rejected any ref with no '#sec-' fragment, so `#` is always present
    by the time this runs."""
    filename, _, anchor = ref.partition("#")
    return filename, anchor


def slice_section(path: Path, anchor: str) -> str:
    """The verbatim body of one `#anchor` section of `path`: the lines between
    its heading and the next heading at any level, or the end of the document
    -- located via secindex.py's own anchor table (secindex.sections_of),
    never a second markdown parser, so a slice boundary can never disagree
    with what secindex.py and secfmt.py already call that section. Surrounding
    blank lines are trimmed; interior text is untouched."""
    if not path.is_file():
        raise FoldError(f"{path.name}: no such source document")
    keys = [key for key, _heading in secindex.sections_of(path)]
    if anchor not in keys:
        raise FoldError(f"{path.name}: no section '#{anchor}'")
    _front, body = secfmt.split_front_matter(path.read_text())
    lines = body.splitlines(keepends=True)
    heading_lines = [i for i, _m in secfmt.headings(body)]
    idx = keys.index(anchor)
    start = heading_lines[idx] + 1
    end = heading_lines[idx + 1] if idx + 1 < len(heading_lines) else len(lines)
    return "".join(lines[start:end]).strip("\n")


# --check --ids (Task 6): the exact-text identifier-preservation guard.
# Inline spans use Markdown's own two forms -- `` `...` `` (single) and
# `` ``...`` `` (double, for a span that itself contains a backtick) --
# matched only outside fenced code, since a bare backtick inside example code
# is a literal character, not span markup. A fenced (triple-backtick)
# block's full verbatim content is tracked as its own identifier alongside
# the inline spans: a spec's code block is typically its authoritative
# schema or CLI surface, and a rewrite dropping the whole block -- not
# reformatting it, dropping it -- is exactly the failure this guard exists
# to catch (see the plan's Task 6 brief). Nothing in this path normalises
# case, punctuation or whitespace; every comparison is exact-text.
FENCE_RE = re.compile(r"```[^\n]*\n(.*?)\n```", re.DOTALL)
SPAN_RE = re.compile(r"``([^`]+)``|`([^`\n]+)`")


def extract_identifiers(text: str) -> list:
    """Every backticked span and fenced-block body found in `text`, in the
    order they appear. Fenced blocks are located and stripped out first, so
    an inline single/double backtick inside example code is never mistaken
    for span markup. May contain duplicates within one section --
    collect_identifiers folds those across a document."""
    blocks = [m.group(1) for m in FENCE_RE.finditer(text)]
    stripped = FENCE_RE.sub("", text)
    spans = [m.group(1) if m.group(1) is not None else m.group(2)
             for m in SPAN_RE.finditer(stripped)]
    return spans + blocks


def collect_identifiers(doc: Document, specs_dir: Path) -> dict:
    """{identifier: ref} for every identifier found across every source
    section fold.yaml places in `doc`. Scoped to the whole document, not per
    section -- a legitimate rewrite may move a term between sections while
    merging, so only "dropped from the document" is a real loss. The first
    `from:` ref an identifier appears under, in section/from order, is what a
    dropped-id report points readers at."""
    identifiers = {}
    for section in doc.sections:
        for ref in section["from"]:
            filename, anchor = _split_ref(ref)
            text = slice_section(specs_dir / filename, anchor)
            for ident in extract_identifiers(text):
                identifiers.setdefault(ident, ref)
    return identifiers


def format_dropped_id(ident: str, ref: str) -> str:
    """One "dropped ids" report line: the identifier -- backtick-quoted, or
    fence-quoted when it is a dropped whole code block (spans a newline) --
    followed by the source ref an author can find it at."""
    quoted = f"```\n{ident}\n```" if "\n" in ident else f"`{ident}`"
    return f"{quoted} (from {ref})"


def check_ids(doc: Document, specs_dir: Path, written: Path) -> list:
    """Sorted "dropped ids" report lines for one already-written document:
    every identifier collect_identifiers() finds in doc's source sections
    that is neither exempted by doc.allow_dropped_ids nor still present,
    exact-text, anywhere in `written`'s full contents. Callers gate this on
    `written.is_file()` themselves (run_check reuses the same per-document
    existence check it already does for missing/undeclared)."""
    identifiers = collect_identifiers(doc, specs_dir)
    written_text = written.read_text()
    out = [
        format_dropped_id(ident, ref)
        for ident, ref in identifiers.items()
        if ident not in doc.allow_dropped_ids and ident not in written_text
    ]
    return sorted(out)


def compute_requires(doc: Document, fold: Fold, specs_dir: Path) -> list:
    """`requires:` for one scaffolded document: the union of every source's
    own `requires:`, minus any target whose filename is itself a `sources:`
    entry somewhere in this fold (that dependency folds internal to
    docs/specs2/), the rest left pointing at docs/specs/ -- refmap.py
    repoints them at cutover; docs/specs/ still exists today so they still
    resolve, and translating them to docs/specs2/ paths now would dangle."""
    internal = {s for d in fold.documents for s in d.sources}
    specs_root = fold.corpus.get("from") or DEFAULT_SPECS_DIR
    seen, out = set(), []
    for source in doc.sources:
        fm = currentspec.frontmatter(specs_dir / source)
        raw = fm.get("requires")
        refs = raw if isinstance(raw, list) else ([raw] if raw else [])
        for ref in refs:
            ref = str(ref)
            target, _, frag = ref.partition("#")
            if Path(target).name in internal:
                continue
            full_path = target if "/" in target else f"{specs_root}/{target}"
            full = f"{full_path}#{frag}" if frag else full_path
            if full not in seen:
                seen.add(full)
                out.append(full)
    return sorted(out)


def build_frontmatter(fields: dict) -> str:
    """One frontmatter block (`---`-fenced, PyYAML block style, matching
    dump_mapping's convention for generated YAML in this module)."""
    body = yaml.safe_dump(fields, sort_keys=False, default_flow_style=False, allow_unicode=True)
    return f"---\n{body}---\n"


def scaffold_text(doc: Document, fold: Fold, specs_dir: Path) -> str:
    """The full skeleton docs/specs2/<doc.to> text: frontmatter (`status:
    draft` plus computed `requires:`), the H1 title, then every declared
    section in document order (sorted by `new:`, not necessarily fold.yaml's
    own list order) with its heading and its `from:` segments pasted in
    verbatim behind a provenance marker naming each source anchor."""
    m = SPEC_NUMBER.match(Path(doc.to).name)
    if not m:
        raise FoldError(f"{doc.to!r} has no leading number to derive a spec title from")
    number = m.group(1)  # kept as the filename's own digit string -- house style
    # zero-pads ("# Spec 005 — ..."), and int() would silently strip that.

    fields = {"status": "draft"}
    requires = compute_requires(doc, fold, specs_dir)
    if requires:
        fields["requires"] = requires

    parts = [build_frontmatter(fields), f"# Spec {number} — {doc.title}\n"]
    for section in sorted(doc.sections, key=lambda s: _number_parts(s["new"])):
        level = _heading_level(section["new"])
        hashes = "#" * (level + 1)
        label = secfmt.label(section["new"], level)
        anchor = f"sec-{section['new']}"
        parts.append(f"\n{hashes} {label} {section['heading']} {{#{anchor}}}\n")
        for ref in section["from"]:
            filename, source_anchor = _split_ref(ref)
            text = slice_section(specs_dir / filename, source_anchor)
            parts.append(f"\n<!-- from: {ref} -->\n{text}\n")

    return "".join(parts)


def scaffold_targets(fold: Fold, only) -> list:
    """The documents --scaffold should write: all of fold.yaml's, or just the
    one named by --only. Raises FoldError when --only names nothing there."""
    docs = fold.documents
    if only is None:
        return docs
    docs = [d for d in docs if d.to == only]
    if not docs:
        raise FoldError(f"--only {only!r} names no document in fold.yaml")
    return docs


def build_scaffolds(fold: Fold, specs_dir: Path, out_dir: Path, only=None) -> dict:
    """{to: text} for every document this --scaffold invocation would write,
    computed in full before any write happens. Raises FoldError -- writing
    nothing -- when any target already exists, so a second --scaffold over a
    partially-written docs/specs2/ refuses the whole invocation rather than
    silently completing it (regenerating after the rewrite pass has started
    would lose the rewrite)."""
    docs = scaffold_targets(fold, only)
    existing = sorted(d.to for d in docs if (out_dir / d.to).exists())
    if existing:
        raise FoldError(f"refusing to overwrite existing {', '.join(existing)}")
    return {d.to: scaffold_text(d, fold, specs_dir) for d in docs}


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--mapping", action="store_true",
                     help="derive docs/specs2/mapping.yaml from docs/specs2/fold.yaml")
    ap.add_argument("--check", action="store_true",
                     help="prove fold.yaml accounts for every live section of docs/specs/")
    ap.add_argument("--partial", action="store_true",
                     help="with --check, only require full coverage of the documents "
                          "fold.yaml currently declares")
    ap.add_argument("--ids", action="store_true",
                     help="with --check, also verify every backticked identifier and "
                          "fenced code block survives into the written docs/specs2/ prose")
    ap.add_argument("--scaffold", action="store_true",
                     help="write docs/specs2/<to> skeleton documents from fold.yaml")
    ap.add_argument("--only", metavar="FILE",
                     help="with --scaffold, write only the document whose 'to' is FILE")
    a = ap.parse_args(argv)

    modes = (a.mapping, a.check, a.scaffold)
    if sum(bool(m) for m in modes) > 1:
        ap.error("--mapping, --check and --scaffold are mutually exclusive")
    if a.partial and not a.check:
        ap.error("--partial only makes sense with --check")
    if a.ids and not a.check:
        ap.error("--ids only makes sense with --check")
    if a.only and not a.scaffold:
        ap.error("--only only makes sense with --scaffold")
    if not any(modes):
        ap.error("no mode selected (--mapping, --check, --scaffold)")

    try:
        fold = load_fold(REPO / FOLD_PATH)
        if a.check:
            groups = run_check(fold, partial=a.partial, ids=a.ids)
        elif a.scaffold:
            specs_dir = REPO / (fold.corpus.get("from") or DEFAULT_SPECS_DIR)
            out_dir = REPO / (fold.corpus.get("to") or DEFAULT_SPECS2_DIR)
            scaffolds = build_scaffolds(fold, specs_dir, out_dir, only=a.only)
    except FoldError as e:
        print(f"fold: {e}", file=sys.stderr)
        return 2

    if a.check:
        ok = True
        for label, refs in groups:
            if refs:
                ok = False
                print(f"fold: {label} ({len(refs)}):", file=sys.stderr)
                for ref in refs:
                    print(f"  {ref}", file=sys.stderr)
        print(f"fold: check {'passed' if ok else 'failed'}", file=sys.stderr)
        return 0 if ok else 1

    if a.scaffold:
        for to, text in scaffolds.items():
            path = out_dir / to
            path.write_text(text)
            print(f"fold: wrote {path.relative_to(REPO)}", file=sys.stderr)
        return 0

    mapping = derive_mapping(fold)
    out = REPO / MAPPING_PATH
    out.write_text(dump_mapping(mapping))
    print(f"fold: wrote {MAPPING_PATH}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
