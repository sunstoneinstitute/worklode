#!/usr/bin/env python3
"""Renumber design-document sections and keep their {#sec-N} anchors in sync.

Sections are identity (spec 014 §3): the anchor carries the section number, and
an inbound claim pins `<file>.md#sec-4.3`. This formatter derives both from
position so they cannot drift apart by hand.

Usage: secfmt.py [-l] [-w] [-d] [--force] [--update-refs] [--depth N] [path...]

  -l              list files whose numbering differs (CI mode; exit 1 if any)
  -w              write the result back to the file
  -d              print a unified diff
  --force         allow renumbering an accepted/superseded document
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

Documents whose sections are not numbered at all are left alone; they are
addressed by slug anchors instead and there is nothing to derive.
"""

import argparse
import difflib
import re
import sys
from pathlib import Path

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


def renumber(text, depth, force=False):
    """Return (new_text, moves) where moves maps old anchor -> new anchor.

    Raises Refusal listing every existing number or anchor that would change in
    a frozen document, unless force suspends that check.
    """
    front, body = split_front_matter(text)
    lines = body.splitlines(keepends=True)

    # Numbers are normalised, never introduced: a heading the author left
    # unnumbered stays unnumbered. Every numbered spec here opens with an
    # unnumbered "Purpose & scope" and starts the sequence at the first body
    # section, so imposing numbers by position would renumber all of them.
    counters = [0] * depth
    moves, breaks, defects = {}, [], []

    for i, m in headings(body):
        level = len(m["hashes"]) - 1  # H2 is level 1
        if level > depth:
            continue  # legal, but not addressable (014 §7)

        if not m["num"]:
            # Opens an unnumbered section; any deeper sequence restarts under it.
            for j in range(level, depth):
                counters[j] = 0
            continue

        prefix = counters[: level - 1]
        if any(c == 0 for c in prefix):
            defects.append(
                f"  §{m['num']} {m['text']!r} is numbered but its parent is not — "
                f"the prefix cannot be derived"
            )
            continue

        if re.search(r"[a-z]$", m["num"]):
            # A letter-suffixed insert (014 §3) keeps its number and consumes no slot.
            number = m["num"]
            parent = number.rsplit(".", 1)[0] if "." in number else ""
            if parent != ".".join(str(c) for c in prefix):
                defects.append(
                    f"  §{m['num']} {m['text']!r}: insert's parent moved; "
                    f"renumbering it would defeat the suffix"
                )
                continue
        else:
            counters[level - 1] += 1
            for j in range(level, depth):
                counters[j] = 0
            number = ".".join(str(c) for c in counters[:level])

        anchor = f"sec-{number}"
        if m["num"] != number:
            breaks.append(f"  §{m['num']} -> §{number}  ({m['text']})")
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


def retarget_own_keys(front, moves):
    """Follow a moved anchor in this document's own frontmatter subject keys.

    A bare `"#sec-4.3":` key is scoped to this document (014 §11); a qualified
    `other.md#sec-4.3` value belongs to another one and is left for update_refs.
    """
    for old, new in moves.items():
        front = re.sub(
            rf'^(\s*)["\']?#{re.escape(old)}["\']?(\s*:)',
            rf'\g<1>"#{new}"\g<2>',
            front,
            flags=re.M,
        )
    return front


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
    hits = []
    for f in collect(roots):
        text = original = f.read_text()
        for old, new in moves.items():
            # Bare-name and path-qualified references both end in the basename.
            text = text.replace(f"{basename}#{old}", f"{basename}#{new}")
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
    ap.add_argument("--update-refs", action="store_true", help="repoint inbound anchors")
    ap.add_argument("--depth", type=int, default=3, help="addressable depth (default 3)")
    a = ap.parse_args()

    roots = a.paths or list(DEFAULT_ROOTS)
    files = collect(roots)
    if not files:
        print("secfmt: no markdown files found", file=sys.stderr)
        return 2

    differed, refused, defective, all_moves = [], [], [], {}

    for f in files:
        original = f.read_text()
        try:
            formatted, moves = renumber(original, a.depth, a.force)
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
        print(f"secfmt: {f} numbers a section whose parent is unnumbered:", file=sys.stderr)
        print(why, file=sys.stderr)
    if defective:
        print(
            "\nA subsection number implies a parent number. Either number the parent\n"
            "section or drop the number from the subsection — the choice changes which\n"
            "numbers inbound references must use, so it is not made automatically.",
            file=sys.stderr,
        )

    for f, why in refused:
        print(f"secfmt: {f} is accepted; renumbering would move published anchors:", file=sys.stderr)
        print(why, file=sys.stderr)
    if refused:
        print(
            "\nSections are frozen once accepted (014 §3). Insert with a letter suffix\n"
            "(2.1a) instead, or re-run with --force --update-refs if the anchors have\n"
            "never been published.",
            file=sys.stderr,
        )
    if refused or defective:
        return 2

    if all_moves and not a.update_refs:
        print(
            "secfmt: anchors moved; inbound references are now stale. "
            "Re-run with --update-refs.",
            file=sys.stderr,
        )
        return 2

    return 1 if (differed and (a.l or a.d)) else 0


if __name__ == "__main__":
    sys.exit(main())
