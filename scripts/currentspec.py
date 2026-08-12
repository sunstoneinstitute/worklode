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

A claim only takes effect once the document making it is `accepted`, and this
gate applies to `amends` as much as to `replaces`: a draft's claim is a
proposal, so its target stays current and the claim is marked `pending`.
`--with-drafts` treats draft claims as effective, which answers the other
question -- what the corpus looks like once the open drafts land.

Both directions of each edge are read (`replaces` and `isReplacedBy`, `amends`
and `amendedBy`) and unioned, so a half-maintained mirror still registers.

Usage: currentspec.py [--with-drafts] [--show-dropped] [spec]

`spec` narrows the output to one document; omit it for the whole corpus. It
takes any of the forms a spec is referred to in practice -- `23`, `023`,
`023-keycloak-primary-auth.md`, `docs/specs/023-keycloak-primary-auth.md`.
Supersession is still resolved against the whole corpus, because what replaced
a section usually lives in another document.
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
    sys.exit("currentspec: needs PyYAML (pip install pyyaml)")

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


def resolve_spec(token, docs):
    """Map a spec reference to its path in `docs`, or exit with what went wrong.

    Accepts a number (`23`, `023`), a filename, or a repo-relative path; the
    number is what identifies the spec, so a stale slug still resolves.
    """
    stem = Path(str(token).strip()).name.removesuffix(".md")
    digits = re.match(r"\d+", stem)
    if digits:
        prefix = f"{int(digits.group()):03d}-"
        hits = [p for p in docs if Path(p).name.startswith(prefix)]
    else:
        hits = [p for p in docs if Path(p).name == f"{stem}.md"]
    if not hits:
        sys.exit(f"currentspec: no spec matches {token!r} in {SPECS}")
    if len(hits) > 1:
        sys.exit(f"currentspec: {token!r} matches {', '.join(sorted(hits))}")
    return hits[0]


def sources_for(edge, path, anchor):
    """Edges landing on exactly this target. A doc-level edge (anchor None) is
    reported on the document, not smeared across each of its sections."""
    return sorted(edge.get((path, anchor), set()), key=lambda s: (s[0], s[1] or ""))


def effective(src_path, docs, with_drafts):
    """True when a `replaces`/`amends` claim made by the document at
    `src_path` already holds: the document lives outside the loaded index
    (nothing to gate on), or its own status is accepted/superseded always,
    draft only under --with-drafts. The one definition every effectiveness
    test in this module shares, so a future status added to EFFECTIVE (or
    any other rule change) cannot apply in one place and not another."""
    st = status_of(src_path, docs)
    return st is None or st in EFFECTIVE or (with_drafts and st == "draft")


def dropped_whole(docs, with_drafts):
    """{path: [(src_path, src_anchor), ...]} for every document dropped in
    its entirety in the --with-drafts view: `status: superseded`, or an
    effective `replaces` naming the whole document. The replacer sources are
    returned (not a bare bool) because main()'s superseded footer names
    them; an empty list means status alone dropped it, no replacer recorded.
    `live_sections` and `main` both call this, so "is this document dropped
    whole" is decided in exactly one place."""
    replaced = edges(docs, "replaces", "isReplacedBy")
    out = {}
    for path, doc in docs.items():
        whole = [s for s in sources_for(replaced, path, None) if effective(s[0], docs, with_drafts)]
        if doc["status"] == "superseded" or whole:
            out[path] = whole
    return out


def live_sections(docs, with_drafts):
    """{(path, anchor): heading} for every section that still states the
    design -- the same test `main()` prints, factored out so another script
    (fold.py) can import the live view instead of shelling out. A document
    `dropped_whole` reports as dropped contributes none of its sections;
    every other section is live unless something effective replaces it.
    `with_drafts` mirrors the CLI flag: True treats a draft's own claims as
    already in force."""
    replaced = edges(docs, "replaces", "isReplacedBy")
    dropped = dropped_whole(docs, with_drafts)

    out = {}
    for path, doc in sorted(docs.items()):
        if path in dropped:
            continue
        for anchor, heading in doc["sections"].items():
            reps = [s for s in sources_for(replaced, path, anchor) if effective(s[0], docs, with_drafts)]
            if not reps:
                out[(path, anchor)] = heading
    return out


def main():
    a = argparse.ArgumentParser()
    a.add_argument("--with-drafts", action="store_true")
    a.add_argument("--show-dropped", action="store_true")
    a.add_argument("spec", nargs="?", help="spec number, filename or path; all specs when omitted")
    a = a.parse_args()

    index = REPO / SPECS / "index.yaml"
    if not index.is_file():
        sys.exit(f"currentspec: {index} missing -- run scripts/secindex.py")
    docs = load(index)
    only = resolve_spec(a.spec, docs) if a.spec else None
    replaced = edges(docs, "replaces", "isReplacedBy")
    amended = edges(docs, "amends", "amendedBy")

    def notes_for(path, anchor):
        """Claims landing here: effective ones annotate, pending ones are named
        as proposals. The gate applies to `amends` exactly as to `replaces`."""
        n = [
            f"amended by {name(*s)}" if effective(s[0], docs, a.with_drafts) else f"pending {name(*s)}"
            for s in sources_for(amended, path, anchor)
        ]
        n += [
            f"pending {name(*s)}"
            for s in sources_for(replaced, path, anchor)
            if not effective(s[0], docs, a.with_drafts)
        ]
        return n

    live = live_sections(docs, a.with_drafts)
    dropped_docs = dropped_whole(docs, a.with_drafts)

    out, dropped, superseded, dangling = [], [], [], []
    kept = 0
    for path, doc in sorted(docs.items()):
        if only and path != only:
            continue
        if path in dropped_docs:
            superseded.append((name(path), [name(*s) for s in dropped_docs[path]]))
            continue
        lines = []
        for anchor, heading in doc["sections"].items():
            if (path, anchor) in live:
                lines.append((anchor, heading, notes_for(path, anchor)))
                continue
            reps = [s for s in sources_for(replaced, path, anchor) if effective(s[0], docs, a.with_drafts)]
            dropped.append((name(path, anchor), [name(*s) for s in reps]))
        kept += len(lines)
        out.append((path, doc["status"], lines, notes_for(path, None)))

    known = {(p, k) for p, d in docs.items() for k in d["sections"]}
    cited = set()
    for edge in (replaced, amended):
        cited |= set(edge) | {s for srcs in edge.values() for s in srcs}
    for ref in sorted(cited, key=lambda t: (t[0], t[1] or "")):
        if only and ref[0] != only:
            continue
        if ref[1] and ref[0].startswith(SPECS) and ref not in known:
            dangling.append(name(*ref))

    if only:
        print(f"# Current sections of {name(only)} -- {kept} sections")
    else:
        print(f"# Current spec sections -- {kept} sections across {len(out)} documents")
    print("# generated by scripts/currentspec.py; superseded material is listed at the end")
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
            "currentspec: references to sections that do not exist: "
            + ", ".join(dangling),
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
