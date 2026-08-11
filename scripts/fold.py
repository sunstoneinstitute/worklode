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

Usage: fold.py --mapping

  --mapping   derive docs/specs2/mapping.yaml from docs/specs2/fold.yaml

--scaffold and --check are later tasks in the same plan; this module's shape
(load_fold returning a Fold, derive_mapping taking one) is designed to grow
them without reworking the parse step.
"""

import argparse
import re
import sys
import tomllib
from dataclasses import dataclass, field
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("fold: needs PyYAML (pip install pyyaml)")

REPO = Path(__file__).resolve().parent.parent
FOLD_PATH = Path("docs/specs2/fold.yaml")
MAPPING_PATH = Path("docs/specs2/mapping.yaml")
DEFAULT_PROJECT_KEY = "WL"

# fold.yaml's own top-level keys (see the plan's "The fold.yaml schema") and
# the keys one `sections:` entry may carry. `from` cannot be a dataclass
# field (it is a Python keyword), so section and dropped entries stay plain
# dicts rather than growing a parallel class for two field names.
FOLD_KEYS = {"version", "corpus", "documents"}
SECTION_KEYS = {"new", "heading", "from"}


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

      * an unknown key at the fold-document level or within a section entry
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
    for doc in raw.get("documents") or []:
        to = doc.get("to")
        title = doc.get("title")
        sources = list(doc.get("sources") or [])
        raw_sections = list(doc.get("sections") or [])
        dropped = [{"ref": d["ref"], "reason": d["reason"]} for d in (doc.get("dropped") or [])]

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
                                   sections=sections, dropped=dropped))

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


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--mapping", action="store_true",
                     help="derive docs/specs2/mapping.yaml from docs/specs2/fold.yaml")
    a = ap.parse_args(argv)

    if not a.mapping:
        ap.error("no mode selected (--mapping)")

    try:
        fold = load_fold(REPO / FOLD_PATH)
    except FoldError as e:
        print(f"fold: {e}", file=sys.stderr)
        return 2

    mapping = derive_mapping(fold)
    out = REPO / MAPPING_PATH
    out.write_text(dump_mapping(mapping))
    print(f"fold: wrote {MAPPING_PATH}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
