#!/usr/bin/env python3
"""Renumber design-document sections and keep their {#sec-N} anchors in sync.

Sections are identity (spec 014 §3): the anchor carries the section number, and
an inbound claim pins `<file>.md#sec-4.3`. This formatter derives both from
position so they cannot drift apart by hand.

Usage: secfmt.py [-l] [-w] [-d] [--assign|--assign-all] [--start N] [--force]
                 [--update-refs] [--depth N] [path...]

  -l              list files whose numbering differs (CI mode; exit 1 if any)
  -w              write the result back to the file
  -d              print a unified diff
  --force         allow renumbering an accepted/superseded document
  --assign        number a document that has no numbering yet (one-time)
  --assign-all    number every heading down to --depth, including a document
                  that is already partly numbered (one-time)
  --start N       first top-level number (0 or 1); defaults to 1 for --assign
                  and to the document's existing first number for --assign-all
  --update-refs   with -w, repoint inbound `file.md#sec-old` references
  --depth N       deepest addressable level (default 3, per 014 §7)

With no flag the formatted document goes to stdout. Paths default to
docs/specs and docs/plans; a directory is walked for *.md.

Two rules the numbering obeys, both from 014 §3:

  * A letter-suffixed section (`2.1a`) is a deliberate insert. It keeps its
    number and consumes no counter slot.
  * Numbering is frozen once a document is accepted, because renumbering
    re-points published anchors at different subject matter. Frozen documents
    are reported and skipped unless --force.

Top-level numbering normally starts at 1. A document may number its orientation
section `0.` ("0. Purpose & scope") so the body still starts at 1; writing that 0
by hand is enough, the sequence follows it.

Numbers are normalised, never introduced: an unnumbered heading stays
unnumbered, so a document that keeps an unnumbered preamble above numbered body
sections keeps it. Two one-time migrations introduce numbering: --assign for a
document with no numbering at all, and --assign-all for one whose numbering
stops short of --depth (numbered `##`, unnumbered `###`). Both number every
heading down to --depth, so --assign-all can move anchors an existing scheme
already published — pair it with --update-refs.
"""

import argparse
import difflib
import os
import re
import sys
from pathlib import Path


def err(message):
    """Print a diagnostic to stderr, red when a terminal is there to read it."""
    if sys.stderr.isatty() and not os.environ.get("NO_COLOR"):
        message = f"\033[31m{message}\033[0m"
    print(message, file=sys.stderr)

# Plans are not DesignDocs (014 §2), so their sections are not addressable
# nodes. Pass docs/plans explicitly if a design record kept there needs anchors.
DEFAULT_ROOTS = ("docs/specs",)
# Anchors are searched wider than they are written, so a moved anchor can be
# repointed wherever it is cited.
REF_ROOTS = ("docs/specs", "docs/plans")
FROZEN = {"accepted", "superseded"}

# "## 4.1a Title {#sec-4.1a}" -> hashes, number, title, anchor.
# The trailing dot is optional because the house style is "1." at the top level
# and "1.1" below it; it is stripped before the number is compared or emitted.
HEADING = re.compile(
    r"^(?P<hashes>#{2,6})[ \t]+"
    r"(?:(?P<num>\d+(?:\.\d+)*[a-z]?)\.?[ \t]+)?"
    r"(?P<text>.*?)"
    r"(?:[ \t]*\{#(?P<anchor>[\w.\-]+)\})?"
    r"[ \t]*$"
)


def label(number, level):
    """House style: "1." at the top level, "1.1" below it."""
    return f"{number}." if level == 1 else number


class Refusal(Exception):
    """A frozen document would have an existing number or anchor changed."""


class Defect(Exception):
    """A numbered section has no numbered ancestor, so its prefix is undecidable."""


def split_front_matter(text):
    """Return (front_matter_including_fences, body). Front matter may be empty."""
    if not text.startswith("---\n"):
        return "", text
    end = text.find("\n---\n", 3)
    if end == -1:
        return "", text
    return text[: end + 5], text[end + 5 :]


def status_of(front_matter):
    m = re.search(r"^status:[ \t]*(\S+)", front_matter, re.M)
    return m.group(1).strip("\"'") if m else None


def headings(body):
    """Yield (line_index, match) for ATX headings outside fenced code."""
    fence = None
    for i, line in enumerate(body.splitlines()):
        stripped = line.lstrip()
        if fence:
            if stripped.startswith(fence):
                fence = None
            continue
        if stripped.startswith("```") or stripped.startswith("~~~"):
            fence = stripped[:3]
            continue
        m = HEADING.match(line)
        if m:
            yield i, m


def renumber(text, depth, force=False, assign=False, start=None, assign_all=False):
    """Return (new_text, moves) where moves maps old anchor -> new anchor.

    Numbers are normalised, never introduced: a heading the author left
    unnumbered stays unnumbered, so a document that mixes numbered body sections
    with an unnumbered preamble keeps it. `assign` is the one-time migration for
    a document with no numbering at all, `assign_all` the one for a document
    numbered only down to some level above `depth`; both number every heading.

    `start` fixes the first top-level number, defaulting to the number the
    document already opens with (so `assign_all` keeps an existing `0.`) and to
    1 for a document with nothing to read it from.

    Raises Refusal listing every existing number or anchor that would change in
    a frozen document, unless force suspends that check.
    """
    front, body = split_front_matter(text)
    lines = body.splitlines(keepends=True)

    hs = list(headings(body))
    tops = [m["num"] for _, m in hs if len(m["hashes"]) - 1 == 1 and m["num"]]
    numbering = assign_all or (
        assign and not any(m["num"] for _, m in hs if len(m["hashes"]) - 1 <= depth)
    )

    # Top-level numbering starts at 1. It starts at 0 when the author numbered
    # the first section `0.` — the orientation section ("Purpose & scope") is
    # numbered 0 so the body still starts at 1. Only the top level may start at
    # 0; subsections always start at 1.
    if start is not None:
        first = start
    else:
        first = 0 if tops and tops[0] == "0" else 1

    counters = [first - 1] + [0] * (depth - 1)
    # `seen` tracks whether a level has been numbered yet, which `counters`
    # cannot express once 0 is a legal section number.
    seen = [False] * depth
    moves, breaks, defects = {}, [], []

    for i, m in hs:
        level = len(m["hashes"]) - 1  # H2 is level 1
        if level > depth:
            continue  # legal, but not addressable (014 §7)

        if not m["num"] and not numbering:
            # Opens an unnumbered section; any deeper sequence restarts under it.
            for j in range(level, depth):
                counters[j], seen[j] = 0, False
            continue

        if not all(seen[: level - 1]):
            defects.append(
                f"  §{m['num']} {m['text']!r} is numbered but its parent is not — "
                f"the prefix cannot be derived"
            )
            continue
        prefix = counters[: level - 1]

        if m["num"] and re.search(r"[a-z]$", m["num"]):
            # A letter-suffixed insert (014 §3) keeps its number and consumes no slot.
            number = m["num"]
            seen[level - 1] = True
            parent = number.rsplit(".", 1)[0] if "." in number else ""
            if parent != ".".join(str(c) for c in prefix):
                defects.append(
                    f"  §{m['num']} {m['text']!r}: insert's parent moved; "
                    f"renumbering it would defeat the suffix"
                )
                continue
        else:
            counters[level - 1] += 1
            seen[level - 1] = True
            for j in range(level, depth):
                counters[j], seen[j] = 0, False
            number = ".".join(str(c) for c in counters[:level])

        anchor = f"sec-{number}"
        if m["num"] != number:
            was = f"§{m['num']}" if m["num"] else "unnumbered"
            breaks.append(f"  {was} -> §{number}  ({m['text']})")
        if m["anchor"] and m["anchor"] != anchor:
            breaks.append(f"  #{m['anchor']} -> #{anchor}  ({m['text']})")
            moves[m["anchor"]] = anchor

        eol = "\n" if lines[i].endswith("\n") else ""
        lines[i] = f"{m['hashes']} {label(number, level)} {m['text']} {{#{anchor}}}{eol}"

    if defects:
        raise Defect("\n".join(defects))
    if breaks and status_of(front) in FROZEN and not force:
        raise Refusal("\n".join(breaks))
    return retarget_own_keys(front, moves) + "".join(lines), moves


def anchor_alternation(moves):
    """One regex branch per moved anchor, longest first.

    Applied in a single pass: replacing sequentially would re-rewrite anchors a
    previous substitution had just produced, so shifting §4->§5 and §5->§6 in a
    document would land both on §6.

    The lookaheads stop `sec-4` matching the leading part of `sec-4.3` or
    `sec-10` without swallowing a sentence-ending period, so a reference at the
    end of a sentence still matches.
    """
    keys = sorted(moves, key=len, reverse=True)
    return "(" + "|".join(re.escape(k) for k in keys) + r")(?![\w-])(?!\.\w)"


def retarget_own_keys(front, moves):
    """Follow a moved anchor in this document's own frontmatter subject keys.

    A bare `"#sec-4.3":` key is scoped to this document (014 §11); a qualified
    `other.md#sec-4.3` value belongs to another one and is left for update_refs.
    """
    if not moves:
        return front
    return re.sub(
        rf'^(\s*)["\']?#{anchor_alternation(moves)}["\']?(\s*:)',
        lambda m: f'{m.group(1)}"#{moves[m.group(2)]}"{m.group(3)}',
        front,
        flags=re.M,
    )


def collect(paths):
    out = []
    for p in paths:
        p = Path(p)
        if p.is_dir():
            out += sorted(q for q in p.rglob("*.md"))
        elif p.suffix == ".md":
            out.append(p)
    return out


def update_refs(roots, basename, moves):
    """Repoint `basename#old` -> `basename#new` across every doc. Returns hits."""
    if not moves:
        return []
    # Bare-name and path-qualified references both end in the basename.
    pat = re.compile(re.escape(basename) + "#" + anchor_alternation(moves))
    hits = []
    for f in collect(roots):
        text = original = f.read_text()
        text = pat.sub(lambda m: f"{basename}#{moves[m.group(1)]}", text)
        if text != original:
            f.write_text(text)
            hits.append(f)
    return hits


def main():
    ap = argparse.ArgumentParser(add_help=True, description=__doc__.split("\n")[0])
    ap.add_argument("paths", nargs="*", default=list(DEFAULT_ROOTS))
    ap.add_argument("-l", action="store_true", help="list files that differ")
    ap.add_argument("-w", action="store_true", help="write result to file")
    ap.add_argument("-d", action="store_true", help="print a unified diff")
    ap.add_argument("--force", action="store_true", help="renumber frozen docs")
    ap.add_argument("--assign", action="store_true",
                    help="one-time: number a document that has no numbering yet")
    ap.add_argument("--assign-all", action="store_true",
                    help="one-time: number every heading down to --depth, even "
                         "where the document is already partly numbered")
    ap.add_argument("--start", type=int, choices=(0, 1), default=None,
                    help="first top-level number; defaults to the document's "
                         "existing first number, else 1")
    ap.add_argument("--update-refs", action="store_true", help="repoint inbound anchors")
    ap.add_argument("--depth", type=int, default=3, help="addressable depth (default 3)")
    a = ap.parse_args()

    if a.start is not None and not (a.assign or a.assign_all):
        ap.error("--start only applies with --assign or --assign-all")

    roots = a.paths or list(DEFAULT_ROOTS)
    files = collect(roots)
    if not files:
        err("secfmt: no markdown files found")
        return 2

    differed, refused, defective, all_moves = [], [], [], {}

    for f in files:
        original = f.read_text()
        try:
            formatted, moves = renumber(
                original, a.depth, a.force, a.assign, a.start, a.assign_all
            )
        except Refusal as e:
            refused.append((f, str(e)))
            continue
        except Defect as e:
            defective.append((f, str(e)))
            continue

        if formatted == original:
            continue
        differed.append(f)
        if moves:
            all_moves[f.name] = moves

        if a.l:
            print(f)
        if a.d:
            sys.stdout.writelines(
                difflib.unified_diff(
                    original.splitlines(keepends=True),
                    formatted.splitlines(keepends=True),
                    fromfile=str(f),
                    tofile=str(f) + " (formatted)",
                )
            )
        if a.w:
            f.write_text(formatted)
        if not (a.l or a.d or a.w):
            sys.stdout.write(formatted)

    if a.w and a.update_refs:
        for basename, moves in all_moves.items():
            for hit in update_refs(REF_ROOTS, basename, moves):
                for old, new in moves.items():
                    print(f"repointed {hit}: {basename}#{old} -> #{new}", file=sys.stderr)

    for f, why in defective:
        err(f"secfmt: {f} numbers a section whose parent is unnumbered:\n{why}")
    if defective:
        err(
            "\nA subsection number implies a parent number. Either number the parent\n"
            "section or drop the number from the subsection — the choice changes which\n"
            "numbers inbound references must use, so it is not made automatically."
        )

    for f, why in refused:
        err(f"secfmt: {f} is accepted; renumbering would move published anchors:\n{why}")
    if refused:
        err(
            "\nSections are frozen once accepted (014 §3). Insert with a letter suffix\n"
            "(2.1a) instead, or re-run with --force --update-refs if the anchors have\n"
            "never been published."
        )
    if refused or defective:
        return 2

    if all_moves and not a.update_refs:
        err(
            "secfmt: anchors moved; inbound references are now stale. "
            "Re-run with --update-refs."
        )
        return 2

    return 1 if (differed and (a.l or a.d or a.w)) else 0


if __name__ == "__main__":
    sys.exit(main())
