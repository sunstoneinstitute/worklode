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
# The separator is captured (not consumed with `.strip()`) so an
# entirely-comment item's `#` still has the leading whitespace INLINE_COMMENT
# requires -- `-[ \t]+` alone would eat that whitespace and leave `# why`
# looking like a value with no comment to strip (WL-210).
ITEM = re.compile(r"^-(?P<sep>[ \t]+)(?P<value>.*)$")
# An unquoted subject key (`#sec-1:`) reads, to the comment-skip branch
# below, exactly like the YAML comments this gate's own convention writes
# with a space after `#` -- so it is matched first, narrowly: no space
# between `#` and the key, and a colon ending it, which a prose comment
# essentially never does (WL-210).
UNQUOTED_SUBJECT = re.compile(r"^#\S+:")
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
    return [f for f in p.stdout.splitlines()
            if f.endswith(".md") and not generated(f)]


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
        dupeset = set(dupes)
        for a in dupes:
            refusals.append(f"{rel}: anchor #{a} appears on more than one "
                            f"heading; inbound references are ambiguous")
        for anchor, num in published.items():
            if anchor not in current:
                refusals.append(
                    f"{rel}: published anchor #{anchor} (was §{num}) is "
                    f"gone — every inbound reference to it now breaks")
            elif anchor not in dupeset and current[anchor] != num:
                # A duplicated anchor already produced its own refusal above;
                # the section number the dict happens to keep (whichever
                # heading's match ran last) is not a rename and would
                # misdirect the fix if reported as one (WL-210).
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
        if key is not None and UNQUOTED_SUBJECT.match(stripped):
            raise Unparseable(raw)
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
            value = value_of(item["sep"] + item["value"])
            if value:
                yield key, subject, value
            continue
        if indent == 0:
            raise Unparseable(raw)
        sub = SUB_KEY.match(stripped)
        if sub:
            subject = unquote(sub["key"])
            # A flow-style ("[...]"/"{...}") or empty-flow ("[]") value on the
            # subject line itself is unreadable the same way the key-line
            # case already is (WL-210): normalise() would silently reject the
            # bogus fragment it decodes to, degrading a real defect into an
            # unresolved note instead of a refusal.
            raw_rest = sub["rest"].strip()
            if raw_rest[:1] in ("[", "{"):
                raise Unparseable(raw)
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

    `edges` maps a canonical 5-tuple to {"docs": ..., "sides": ...} -- the
    documents that recorded it, and which of the two mirror roles ("acting":
    an `amends`/`replaces` entry, "passive": an `amendedBy`/`isReplacedBy`
    entry) were among those recordings. `docs` alone cannot tell a mirrored
    self-edge from a half-recorded one: when acting doc == target doc, a
    single recording puts that one document in `docs` twice over, satisfying
    a `docs`-only completeness test with only half the mirror written
    (WL-210). `sides` is what `check_mirrors` actually tests for
    completeness; `docs` remains for naming which file to blame. The
    acyclicity check (task 3) only reads the keys of this dict, so its shape
    is unaffected by what the values hold. An end this checkout cannot
    resolve, or one naming a file that is not there (a dangling reference is
    secmeta.py's finding, not this gate's), keeps the edge out of the graph
    and produces a `note` instead.
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
            entry = edges.setdefault(canonical(rel, key, subject, *target),
                                     {"docs": set(), "sides": set()})
            entry["docs"].add(rel)
            entry["sides"].add("acting" if key in MIRROR else "passive")
    return edges, refusals, notes


def subject_key(frag):
    """How a fragment is spelt as a subject key: `"."` or `"#sec-N"`."""
    return "." if frag == "." else f"#{frag}"


def endpoint(doc, frag):
    """One end of an edge, as a reference: `004-….md#sec-1.4` or `004-….md`."""
    return doc if frag == "." else f"{doc}#{frag}"


def check_mirrors(edges):
    """026 section 4: an edge derivable from only one of its two documents is
    a refusal naming the file that is missing its half. `edges` is the
    caller's single edge_set() call, shared with the cycle check so the
    corpus is parsed once."""
    refusals = []
    for edge in sorted(edges):
        relation, acting, acting_frag, target, target_frag = edge
        seen, sides = edges[edge]["docs"], edges[edge]["sides"]
        if "acting" in sides and "passive" in sides:
            continue
        if "acting" in sides:
            missing, key, frag = target, MIRROR[relation], target_frag
        else:
            missing, key, frag = acting, relation, acting_frag
        refusals.append(
            f"{sorted(seen)[0]}: the edge "
            f"{endpoint(acting, acting_frag)} {relation} "
            f"{endpoint(target, target_frag)} is recorded from one side only "
            f"— {missing} needs a matching {key} entry keyed "
            f"\"{subject_key(frag)}\"")
    return refusals


def find_cycle(graph):
    """graph: {node: sorted list of successor nodes}. Returns the first loop
    as [n1, n2, ..., n1], or None. Deterministic: nodes and successors are
    visited in sorted order, so the same corpus always names the same loop.

    Iterative DFS with an explicit stack of [node, next-successor-index]
    frames -- no recursion, so no risk of exhausting Python's call stack on
    a pathological corpus. WHITE/GRAY/BLACK is the standard three-colour
    scheme: GRAY means "on the current DFS path", and meeting a GRAY node
    again is exactly a cycle back to it.
    """
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {}
    for start in sorted(graph):
        if color.get(start, WHITE) != WHITE:
            continue
        color[start] = GRAY
        stack = [[start, 0]]
        while stack:
            node, i = stack[-1]
            succs = graph.get(node, ())
            if i >= len(succs):
                color[node] = BLACK
                stack.pop()
                continue
            stack[-1][1] += 1
            succ = succs[i]
            state = color.get(succ, WHITE)
            if state == GRAY:
                idx = next(j for j, (n, _) in enumerate(stack) if n == succ)
                return [n for n, _ in stack[idx:]] + [succ]
            if state == WHITE:
                color[succ] = GRAY
                stack.append([succ, 0])
    return None


def check_cycles(edges):
    """026 section 4.1: refuse a corpus whose amends/replaces edges close a
    loop. Scoped to the section-level graph only (ruling 1): a node is
    entered only when both its ends are section-scoped, since doc-scoped
    edges are section 3.2 banners that are never inlined and so cannot
    recurse. `amends` and `replaces` both mean "this newer section acts on
    that older one" and share one graph (ruling 2); acting -> target.

    Reuses the caller's edge_set() tuples -- a correctly mirrored edge is
    already one canonical tuple (task 2), which is what keeps a mirror pair
    from reading as a false 2-cycle here.
    """
    graph = {}
    for relation, acting, acting_frag, target, target_frag in edges:
        if acting_frag == "." or target_frag == ".":
            continue
        graph.setdefault(endpoint(acting, acting_frag), set()).add(
            endpoint(target, target_frag))
    graph = {node: sorted(succs) for node, succs in graph.items()}
    cycle = find_cycle(graph)
    if cycle is None:
        return []
    return [
        f"amends/replaces cycle: {' -> '.join(cycle)}\n"
        "  A cycle means a document claims to amend something that "
        "transitively\n  amends it (026 §4.1). Remove or re-target one of "
        "these edges — no later\n  reader can repair a published loop."
    ]


def main():
    roots = sys.argv[1:] or list(DEFAULT_ROOTS)
    root = repo_root()
    permanence = check_permanence(root, roots)
    edges, edge_refusals, notes = edge_set(root, roots)
    mirrors = edge_refusals + check_mirrors(edges)
    cycles = check_cycles(edges)
    for note in notes:
        err(f"secfrozen: unresolved: {note}")
    if not (permanence or mirrors or cycles):
        return 0
    for r in permanence + mirrors + cycles:
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
