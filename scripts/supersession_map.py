#!/usr/bin/env python3
"""Generate a `lode clause supersede --map` file from the old-backbone section map.

docs/specs2/section-map.tsv is the hand-made map from the 47 old backbone
specs' sections to the 11 new docs/specs2 documents. This resolves each row
to a map line in the format internal/cmd/clause.go's parseSupersedeMap reads:
one `<old> -> <new> [<new> ...]` entry per line, `<old> ->` to withdraw with
no successor. Refs are written `KEY-KIND-N#anchor` (WL-SPEC-1#sec-10), which
`lode clause supersede` resolves as a section ref.

Some rows target a heading that is not one clause: `Open questions` is a
bullet list, `-` names no section at all, and one row (WL-SPEC-29 sec-6.2)
points at a section number that does not exist in its target document.
docs/specs2/ttl/residue.tsv holds the hand ruling for each of these: which
new document's preamble or Open-questions clause, or which numbered section,
is the real successor. A residue.tsv row always wins over the section map's
own new_section for the same (old_ref, old_anchor, new_file).

`lode clause supersede --map` applies the whole map in one transaction and
refuses it on any unresolvable ref. One old_anchor in section-map.tsv is a
note, not a real anchor; its row becomes a "# skipped" comment instead of a
map entry, so the run doesn't fail on it, and a count goes to stderr.

Usage:
    scripts/supersession_map.py --section-map docs/specs2/section-map.tsv \\
        --residue docs/specs2/ttl/residue.tsv --refs refs.json \\
        [--anchors anchors.json] > map.txt

refs.json maps a new document's file stem to its backbone ref:
    {"02-identity-actors-and-secrets": "WL-SPEC-80", ...}

anchors.json maps a file stem to its preamble and Open-questions anchors:
    {"02-identity-actors-and-secrets": {"preamble": "sec-0",
                                         "open-questions": "sec-11"}, ...}
It is required only when a preamble or open-questions successor is resolved.
"""

from __future__ import annotations

import argparse
import csv
import json
import re
import sys
from pathlib import Path

DROPPED = {"dropped-history", "dropped-other", "dropped-stale"}
RESIDUE_TARGETS = {"-", "Open questions"}

# A same-major numbered range, "8.1-8.4": four consecutive H3 subsections
# under one H2, the one range shape section-map.tsv actually uses.
RANGE = re.compile(r"^(\d+)\.(\d+)-(\d+)\.(\d+)$")

# The 025 §3 anchor grammar, copied from internal/designdoc/lint.go's
# anchorRE (ValidAnchor): the "sec-" prefix, then a section number or a
# lowercase slug. section-map.tsv's old_anchor is occasionally a note
# ("§2 (line 106)") rather than a real anchor; `lode clause supersede`
# resolves every old ref in one transaction and refuses the whole map on
# any unresolvable one, so a row like that cannot become a map entry.
ANCHOR_RE = re.compile(r"^sec-[a-z0-9][a-z0-9.-]*$")


def expand_sections(raw):
    """A new_section field can name more than one target: "8.7, 8.8" (a
    comma list) or "8.1-8.4" (a same-major range). Returns one raw token
    (a number or "intro") per target, in the order named."""
    if "," in raw:
        out = []
        for part in raw.split(","):
            out.extend(expand_sections(part.strip()))
        return out
    m = RANGE.match(raw)
    if m:
        major_a, minor_a, major_b, minor_b = m.groups()
        if major_a != major_b:
            raise ValueError(f"range {raw!r} crosses a major section")
        return [f"{major_a}.{i}" for i in range(int(minor_a), int(minor_b) + 1)]
    return [raw]


def read_tsv(path):
    with open(path, newline="", encoding="utf-8") as f:
        return list(csv.DictReader(f, delimiter="\t"))


def stem(new_file):
    return new_file[:-3] if new_file.endswith(".md") else new_file


def residue_index(residue_rows):
    """(old_ref, old_anchor, new_file) -> new_anchor, as residue.tsv rules it."""
    return {(r["old_ref"], r["old_anchor"], r["new_file"]): r["new_anchor"] for r in residue_rows}


def matched_residue_keys(section_rows, index):
    """The section-map rows a residue.tsv row actually resolves, in file order."""
    return [
        (r["old_ref"], r["old_anchor"], r["new_file"])
        for r in section_rows
        if r["disposition"] not in DROPPED and (r["old_ref"], r["old_anchor"], r["new_file"]) in index
    ]


def resolve_anchor(raw, file_stem, anchors, where):
    """raw is a numbered section ("9.4"), the literal "preamble"/"intro" (the
    text before a document's first H2), or "open-questions"."""
    key = "preamble" if raw in ("preamble", "intro") else raw if raw == "open-questions" else None
    if key is None:
        return f"sec-{raw}"
    if anchors is None:
        raise ValueError(f"{where}: {key!r} clause needs --anchors")
    if file_stem not in anchors:
        raise ValueError(f"{where}: {file_stem!r} is not in --anchors")
    if key not in anchors[file_stem]:
        raise ValueError(f"{where}: {file_stem!r} has no {key!r} in --anchors")
    return anchors[file_stem][key]


def sort_key(key):
    ref, anchor = key
    m = re.search(r"-(\d+)$", ref)
    prefix, num = (ref[: m.start()], int(m.group(1))) if m else (ref, 0)
    parts = tuple(int(p) for p in re.split(r"\D+", anchor) if p)
    return (prefix, num, parts)


def build_map(section_rows, residue_rows, refs, anchors):
    index = residue_index(residue_rows)
    groups: dict[tuple[str, str], list[str]] = {}
    skipped: dict[tuple[str, str], int] = {}  # old key -> section-map.tsv line number

    for line_no, row in enumerate(section_rows, start=2):  # 1 is the header
        key = (row["old_ref"], row["old_anchor"])
        if not ANCHOR_RE.match(row["old_anchor"]):
            skipped.setdefault(key, line_no)
            continue
        groups.setdefault(key, [])
        if row["disposition"] in DROPPED:
            continue

        new_file = row["new_file"]
        where = f"{key[0]}#{key[1]} -> {new_file}"
        file_stem = stem(new_file)
        if file_stem not in refs:
            raise ValueError(f"{where}: {new_file!r} is not in --refs")

        residue_key = (key[0], key[1], new_file)
        if residue_key in index:
            raws = [index[residue_key]]
        else:
            new_section = row["new_section"]
            if new_section in RESIDUE_TARGETS:
                raise ValueError(f"{where}: {new_section!r} target has no row in --residue")
            raws = expand_sections(new_section)

        for raw in raws:
            anchor = resolve_anchor(raw, file_stem, anchors, where)
            successor = f"{refs[file_stem]}#{anchor}"
            if successor not in groups[key]:
                groups[key].append(successor)

    entries = []
    for key, succ in groups.items():
        old = f"{key[0]}#{key[1]}"
        text = f"{old} -> {' '.join(succ)}" if succ else f"{old} ->"
        entries.append((sort_key(key), text))
    for key, line_no in skipped.items():
        text = (f'# skipped: {key[0]} "{key[1]}" is not a section anchor '
                 f'(section-map.tsv row {line_no})')
        entries.append((sort_key(key), text))
    entries.sort(key=lambda e: e[0])
    return [text for _, text in entries]


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--section-map", required=True, type=Path)
    p.add_argument("--residue", required=True, type=Path)
    p.add_argument("--refs", required=True, type=Path)
    p.add_argument("--anchors", type=Path)
    args = p.parse_args(argv)

    section_rows = read_tsv(args.section_map)
    residue_rows = read_tsv(args.residue)
    refs = json.loads(args.refs.read_text(encoding="utf-8"))
    anchors = json.loads(args.anchors.read_text(encoding="utf-8")) if args.anchors else None

    lines = build_map(section_rows, residue_rows, refs, anchors)
    skipped_count = sum(1 for l in lines if l.startswith("# skipped:"))

    inputs = [str(args.section_map), str(args.residue), str(args.refs)]
    if args.anchors:
        inputs.append(str(args.anchors))
    print(f"# generated by scripts/supersession_map.py from {', '.join(inputs)}")
    for line in lines:
        print(line)
    print(f"supersession_map.py: skipped {skipped_count} old section(s) with no valid anchor",
          file=sys.stderr)


if __name__ == "__main__":
    try:
        main()
    except ValueError as e:
        print(f"supersession_map.py: {e}", file=sys.stderr)
        sys.exit(1)
