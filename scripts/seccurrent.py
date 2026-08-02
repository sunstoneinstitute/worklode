#!/usr/bin/env python3
"""Print the current view of the spec corpus: which section of which spec still
states the design.

Reads docs/specs/index.yaml (written by secindex.py) and the amendment and
supersession frontmatter of every spec, then drops what no longer holds:

  * a document with `status: superseded` -- listed in a footer, sections dropped
  * a section some other document replaced -- named in the footer with what
    replaced it

Amendments do not drop a section; they annotate it, because an amended section
still states the design, just not alone.

A claim only takes effect once the document making it is `accepted`: a draft's
`replaces` is a proposal, so its target stays current and is marked `pending`.
`--with-drafts` treats draft claims as effective, which answers the other
question -- what the corpus looks like once the open drafts land.

Both directions of each edge are read (`replaces` and `isReplacedBy`, `amends`
and `amendedBy`) and unioned, so a half-maintained mirror still registers.

Usage: seccurrent.py [--with-drafts] [--show-dropped] [index.yaml]
"""

import argparse
import re
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # importing secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import split_front_matter  # noqa: E402

try:
    import yaml
except ImportError:
    sys.exit("seccurrent: needs PyYAML (pip install pyyaml)")

REPO = Path(__file__).resolve().parent.parent
SPECS = "docs/specs"
EFFECTIVE = {"accepted", "superseded"}


def parse_ref(ref, home):
    """Split a reference into (repo-relative path, anchor or None).

    A reference is a bare filename within the same directory and a
    repo-relative path across directories (authoring-design-docs.md).
    """
    path, _, anchor = str(ref).partition("#")
    if "/" not in path:
        path = f"{home}/{path}"
    return path, (anchor or None)


def frontmatter(path):
    """Parse a document's YAML front matter; {} when it has none."""
    fm, _ = split_front_matter(path.read_text())
    return (yaml.safe_load(fm[4:-5]) or {}) if fm else {}  # drop the --- fences


def load(index_path):
    """Return {path: {"sections": {...}, "status": str, "fm": dict}}."""
    out = {}
    for path, entry in (yaml.safe_load(index_path.read_text()) or {}).items():
        fm = frontmatter(REPO / path)
        out[path] = {
            "sections": entry.get("sections") or {},
            "status": str(fm.get("status", "unknown")),
            "fm": fm,
        }
    return out


def status_of(path, docs):
    """Status of a referenced document, which may live outside the index."""
    if path in docs:
        return docs[path]["status"]
    f = REPO / path
    return str(frontmatter(f).get("status", "unknown")) if f.is_file() else None


def edges(docs, forward, backward):
    """Collect {(target path, anchor or None): {(source path, anchor)}}.

    `forward` is the key naming what this document does to others (`replaces`),
    `backward` the mirror others record about themselves (`isReplacedBy`).
    """
    out = {}
    for path, doc in docs.items():
        home = str(Path(path).parent)
        for own, refs in (doc["fm"].get(forward) or {}).items():
            src = (path, own.lstrip("#") if own != "." else None)
            for ref in refs if isinstance(refs, list) else [refs]:
                out.setdefault(parse_ref(ref, home), set()).add(src)
        for own, refs in (doc["fm"].get(backward) or {}).items():
            tgt = (path, own.lstrip("#") if own != "." else None)
            for ref in refs if isinstance(refs, list) else [refs]:
                p, a = parse_ref(ref, home)
                out.setdefault(tgt, set()).add((p, a))
    return out


def name(path, anchor=None):
    stem = Path(path).name
    return f"{stem}#{anchor}" if anchor else stem


def sources_for(edge, path, anchor):
    """Edges landing on exactly this target. A doc-level edge (anchor None) is
    reported on the document, not smeared across each of its sections."""
    return sorted(edge.get((path, anchor), set()), key=lambda s: (s[0], s[1] or ""))


def main():
    a = argparse.ArgumentParser()
    a.add_argument("--with-drafts", action="store_true")
    a.add_argument("--show-dropped", action="store_true")
    a.add_argument("index", nargs="?", default=REPO / SPECS / "index.yaml", type=Path)
    a = a.parse_args()

    if not a.index.is_file():
        sys.exit(f"seccurrent: {a.index} missing -- run scripts/secindex.py")
    docs = load(a.index)
    replaced = edges(docs, "replaces", "isReplacedBy")
    amended = edges(docs, "amends", "amendedBy")

    def effective(src_path):
        st = status_of(src_path, docs)
        return st is None or st in EFFECTIVE or (a.with_drafts and st == "draft")

    def notes_for(path, anchor):
        """Amendments, plus replacements whose author has not been accepted yet."""
        n = [f"amended by {name(*s)}" for s in sources_for(amended, path, anchor)]
        n += [
            f"pending {name(*s)}"
            for s in sources_for(replaced, path, anchor)
            if not effective(s[0])
        ]
        return n

    out, dropped, superseded, dangling = [], [], [], []
    kept = 0
    for path, doc in sorted(docs.items()):
        whole = [s for s in sources_for(replaced, path, None) if effective(s[0])]
        if doc["status"] == "superseded" or whole:
            superseded.append((name(path), [name(*s) for s in whole]))
            continue
        lines = []
        for anchor, heading in doc["sections"].items():
            reps = [s for s in sources_for(replaced, path, anchor) if effective(s[0])]
            if reps:
                dropped.append((name(path, anchor), [name(*s) for s in reps]))
                continue
            lines.append((anchor, heading, notes_for(path, anchor)))
        kept += len(lines)
        out.append((path, doc["status"], lines, notes_for(path, None)))

    known = {(p, k) for p, d in docs.items() for k in d["sections"]}
    cited = set()
    for edge in (replaced, amended):
        cited |= set(edge) | {s for srcs in edge.values() for s in srcs}
    for ref in sorted(cited, key=lambda t: (t[0], t[1] or "")):
        if ref[1] and ref[0].startswith(SPECS) and ref not in known:
            dangling.append(name(*ref))

    print(f"# Current spec sections -- {kept} sections across {len(out)} documents")
    print("# generated by scripts/seccurrent.py; superseded material is listed at the end")
    for path, status, lines, doc_notes in out:
        head = "; ".join([status] + [n + " (whole document)" for n in doc_notes])
        print(f"\n{path}  ({head})")
        for anchor, heading, notes in lines:
            text = re.sub(r"^#+ ", "", heading)
            note = "   [" + "; ".join(notes) + "]" if notes else ""
            print(f"  {anchor:<12} {text}{note}")

    if superseded:
        print("\n# Superseded documents")
        for doc, by in superseded:
            print(f"  {doc}  ->  {', '.join(by) or 'no successor recorded'}")
    if dropped:
        print(f"\n# Replaced sections ({len(dropped)}), omitted above")
        if a.show_dropped:
            for sec, by in dropped:
                print(f"  {sec}  ->  {', '.join(by)}")
    if dangling:
        print(
            "seccurrent: references to sections that do not exist: "
            + ", ".join(dangling),
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
