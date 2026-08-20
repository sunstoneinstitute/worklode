#!/usr/bin/env python3
"""Refuse a commit that breaks the published design-doc corpus (026 §4.1).

Compares the working tree against the committed baseline (`git show
HEAD:<path>`). A document whose *committed* status is accepted or superseded
is frozen: every anchor it has published must survive, on a heading whose
number still matches. Anchors may be added (letter-suffix inserts) and bodies
may change freely; only disappearance and renaming are refused. §4's
mirror-edge check runs in the same pass: an `amends`/`replaces` edge recorded
by only one of its two documents is refused, naming the file missing its half.
Reference *resolution* is not repeated here — `secmeta.py` owns it, and can,
because it is free to import PyYAML where this gate is not (026 §0).

Unlike secfmt.py this script REFUSES RATHER THAN REWRITES: there is no
correct automatic repair for a deleted published anchor. Fix the document by
hand — letter-suffix inserts for additions, keep superseded headings — and
commit again.

Usage: secfrozen.py [dir...]      (defaults: docs/specs docs/plans)

Exit 0: safe. Exit 2: refused, findings on stderr. Edge ends this checkout
cannot verify (a foreign shorthand, the colon form, a file that is not there)
are printed as `unresolved`, kept out of the graph, and never affect the exit
code (026 §4.2's degradation rule).

Deleted with the git corpus when spec 025 lands — the gate becomes an
accept-time server check and this script is not ported.
"""
import os
import posixpath
import re
import subprocess
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # importing secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import (  # noqa: E402
    err, generated, headings, split_front_matter, status_of,
)

DEFAULT_ROOTS = ("docs/specs", "docs/plans")
FROZEN = {"accepted", "superseded"}

# The four map-shaped edge keys, as two mirrored pairs. The left-hand key is
# the *acting* one: 025 amends 004, so 025 is what changed and 004 is what was
# changed. Nothing else in the frontmatter is read here -- secmeta.py owns
# reference resolution, the `covers` shape and the rest of the schema; this
# gate only asks whether both halves of an edge were written down.
MIRROR = {"amends": "amendedBy", "replaces": "isReplacedBy"}
INVERSE = {v: k for k, v in MIRROR.items()}
EDGE_KEYS = set(MIRROR) | set(INVERSE)

TOP_KEY = re.compile(r"^(?P<key>[A-Za-z_][\w-]*):[ \t]*(?P<rest>.*)$")
SUB_KEY = re.compile(r"^(?P<key>\S[^:]*):[ \t]*(?P<rest>.*)$")
ITEM = re.compile(r"^-[ \t]+(?P<value>.*)$")
# A reference may carry a trailing parenthetical annotation naming which
# decisions were inherited (026 section 4). Its content is opaque; it is
# stripped before the path is read and otherwise ignored.
ANNOTATION = re.compile(r"\s*\([^)]*\)\s*$")
# A YAML inline comment needs whitespace before its `#`; a `#sec-N` fragment
# has none, so stripping the one never eats the other.
INLINE_COMMENT = re.compile(r"\s+#.*$")


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


class Unparseable(Exception):
    """A line under an edge key fits none of the shapes the corpus writes.

    Carries the offending line. A silently skipped line would be an edge the
    gate never saw, so this is a refusal, not a warning.
    """


def value_of(s):
    """One frontmatter value: inline comment dropped, then unquoted.

    An uncommented `- 004-….md#sec-1.4  # why` would carry `sec-1.4  # why`
    as its fragment, entering the graph as an end no mirror can match and
    manufacturing a refusal out of a comment.
    """
    return unquote(INLINE_COMMENT.sub("", s))


def unquote(s):
    """Drop one layer of matching quotes, as the corpus writes `"#sec-1"`."""
    s = s.strip()
    if len(s) >= 2 and s[0] == s[-1] and s[0] in "\"'":
        return s[1:-1]
    return s


def extract_edges(front):
    """Yield (key, subject, value) for the four edge keys of one frontmatter.

    A stdlib mini-parser, not YAML: this gate runs in a fresh checkout before
    anything is installed (026 section 0), so it cannot import PyYAML the way
    secmeta.py does. It reads only the shapes the corpus actually writes --

        amends:
          "#sec-15":
            - 004-execution-backbone.md#sec-1.4   # 4-space list
          "#sec-2":
          - 017-task-secrets.md#sec-1             # 2-space list

    -- and every other top-level key (`status`, `covers`, `requires`, ...) ends
    the block and is skipped whole, nested lines included. Raises Unparseable
    on a line under an edge key that fits none of those shapes.
    """
    key = subject = None
    for raw in front.splitlines():
        line = raw.rstrip()
        if not line or line == "---":
            continue
        lead = line[:len(line) - len(line.lstrip(" \t"))]
        stripped = line[len(lead):]
        if stripped.startswith("#"):  # a YAML comment; subjects are quoted
            continue
        if "\t" in lead:
            if key is not None:
                raise Unparseable(raw)
            continue
        indent = len(lead)
        if indent == 0:
            m = TOP_KEY.match(stripped)
            if m and m["key"] in EDGE_KEYS:
                if m["rest"]:  # a flow-style or scalar value on the key line
                    raise Unparseable(raw)
                key, subject = m["key"], None
                continue
            if m:
                key = subject = None
                continue
        if key is None:
            continue
        item = ITEM.match(stripped)
        if item:
            if subject is None:  # the edge keys are maps, never bare lists
                raise Unparseable(raw)
            value = item["value"].strip()
            if value:
                yield key, subject, value_of(value)
            continue
        if indent == 0:
            raise Unparseable(raw)
        sub = SUB_KEY.match(stripped)
        if sub:
            subject = unquote(sub["key"])
            rest = value_of(" " + sub["rest"])
            if rest:
                yield key, subject, rest
            continue
        raise Unparseable(raw)


def normalise(rel, ref):
    """Repo-relative (path, fragment) for one edge value, or None.

    None means this checkout cannot turn the value into a path -- the colon
    form (`rdf-registry:ADR-0006`), a project shorthand (`WL-SPEC-4`), a
    sentinel. Under 026 section 4.2 those are `unresolved`, never a defect: the
    authority to ask is absent, not the author mistaken. Path forms follow
    section 4's table exactly.
    """
    ref = ANNOTATION.sub("", ref).strip()
    if not ref or ":" in ref:
        return None
    path, _, frag = ref.partition("#")
    if not path.endswith(".md"):
        return None
    if "/" not in path or path.startswith(("./", "../")):
        path = posixpath.join(posixpath.dirname(rel), path)
    return posixpath.normpath(path.lstrip("/")), frag


def canonical(rel, key, subject, path, frag):
    """(relation, acting_doc, acting_frag, target_doc, target_frag).

    Both halves of a correctly mirrored edge canonicalise to one tuple, which
    is what keeps a mirror from reading as a disagreement (and, for the cycle
    check, from reading as a 2-cycle).

    `amends`/`replaces`: this document acts, so the subject is the acting
    fragment. `amendedBy`/`isReplacedBy`: the *value* acts on this document's
    subject. Doc-scoped ends -- subject `"."`, or a value with no fragment --
    canonicalise to `"."`.
    """
    here = subject[1:] if subject.startswith("#") else "."
    there = frag or "."
    if key in MIRROR:
        return (key, rel, here, path, there)
    return (INVERSE[key], path, there, rel, here)


def corpus_files(root, roots):
    """Every working-tree corpus document; generated views are not authored."""
    out = []
    for r in roots:
        base = root / r
        if not base.is_dir():
            continue
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = [d for d in dirnames if not generated(d)]
            for name in filenames:
                if not name.endswith(".md"):
                    continue
                out.append(Path(dirpath, name).relative_to(root).as_posix())
    return sorted(out)


def edge_set(root, roots):
    """Canonical edges of the working-tree corpus: (edges, refusals, notes).

    `edges` maps a canonical 5-tuple to the set of documents that recorded it
    -- the reusable edge set task 3's acyclicity check reads. An end this
    checkout cannot resolve, or one naming a file that is not there (a dangling
    reference is secmeta.py's finding, not this gate's), keeps the edge out of
    the graph and produces a `note` instead.
    """
    edges, refusals, notes = {}, [], []
    present = set(corpus_files(root, roots))
    for rel in sorted(present):
        front, _ = split_front_matter((root / rel).read_text())
        if not front:
            continue
        try:
            triples = list(extract_edges(front))
        except Unparseable as e:
            refusals.append(
                f"{rel}: frontmatter line {e.args[0]!r} not understood — the "
                f"gate cannot verify edges it cannot read")
            continue
        for key, subject, value in triples:
            if subject != "." and not subject.startswith("#"):
                continue  # a subject naming no section: secmeta.py's finding
            target = normalise(rel, value)
            if target is None or target[0] not in present:
                notes.append(f"{rel} {key}: {value}")
                continue
            edges.setdefault(canonical(rel, key, subject, *target),
                             set()).add(rel)
    return edges, refusals, notes


def subject_key(frag):
    """How a fragment is spelt as a subject key: `"."` or `"#sec-N"`."""
    return "." if frag == "." else f"#{frag}"


def endpoint(doc, frag):
    """One end of an edge, as a reference: `004-….md#sec-1.4` or `004-….md`."""
    return doc if frag == "." else f"{doc}#{frag}"


def check_mirrors(root, roots):
    """026 section 4: an edge derivable from only one of its two documents is
    a refusal naming the file that is missing its half."""
    edges, refusals, notes = edge_set(root, roots)
    for edge in sorted(edges):
        relation, acting, acting_frag, target, target_frag = edge
        seen = edges[edge]
        if acting in seen and target in seen:
            continue
        if acting in seen:
            missing, key, frag = target, MIRROR[relation], target_frag
        else:
            missing, key, frag = acting, relation, acting_frag
        refusals.append(
            f"{sorted(seen)[0]}: the edge "
            f"{endpoint(acting, acting_frag)} {relation} "
            f"{endpoint(target, target_frag)} is recorded from one side only "
            f"— {missing} needs a matching {key} entry keyed "
            f"\"{subject_key(frag)}\"")
    return refusals, notes


def main():
    roots = sys.argv[1:] or list(DEFAULT_ROOTS)
    root = repo_root()
    permanence = check_permanence(root, roots)
    mirrors, notes = check_mirrors(root, roots)
    # task 3 extends this with the acyclicity finding over edge_set()
    for note in notes:
        err(f"secfrozen: unresolved: {note}")
    if not (permanence or mirrors):
        return 0
    for r in permanence + mirrors:
        err(f"secfrozen: {r}")
    if permanence:
        err("\nPublished anchors are frozen (025 §3, 026 §4.1). Restore the "
            "anchor, or insert with a letter suffix (2.1a) instead of "
            "renumbering. A superseded section keeps its heading and anchor. "
            "This gate never rewrites files.")
    if mirrors:
        err("\nAn amends/replaces edge must be recorded from both sides "
            "(026 §4): add the mirror key to the document named above, or "
            "drop the half that is wrong. This gate never rewrites files.")
    return 2


if __name__ == "__main__":
    sys.exit(main())
