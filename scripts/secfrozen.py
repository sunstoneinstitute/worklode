#!/usr/bin/env python3
"""Refuse a commit that breaks the published design-doc corpus (026 §4.1).

Compares the working tree against the committed baseline (`git show
HEAD:<path>`). A document whose *committed* status is accepted or superseded
is frozen: every anchor it has published must survive, on a heading whose
number still matches. Anchors may be added (letter-suffix inserts) and bodies
may change freely; only disappearance and renaming are refused. The §4
reference, mirror-edge, and acyclicity checks run in the same pass.

Unlike secfmt.py this script REFUSES RATHER THAN REWRITES: there is no
correct automatic repair for a deleted published anchor. Fix the document by
hand — letter-suffix inserts for additions, keep superseded headings — and
commit again.

Usage: secfrozen.py [dir...]      (defaults: docs/specs docs/plans)

Exit 0: safe. Exit 2: refused, findings on stderr. References this checkout
cannot verify (a foreign shorthand, a cross-repo form) are printed as
`unresolved` and never affect the exit code (026 §4.2's degradation rule).

Deleted with the git corpus when spec 025 lands — the gate becomes an
accept-time server check and this script is not ported.
"""
import subprocess
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # importing secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import err, headings, split_front_matter, status_of  # noqa: E402

DEFAULT_ROOTS = ("docs/specs", "docs/plans")
FROZEN = {"accepted", "superseded"}


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


def main():
    roots = sys.argv[1:] or list(DEFAULT_ROOTS)
    root = repo_root()
    refusals = check_permanence(root, roots)
    # tasks 2 and 3 extend this list with reference, mirror and cycle findings
    if refusals:
        for r in refusals:
            err(f"secfrozen: {r}")
        err("\nPublished anchors are frozen (025 §3, 026 §4.1). Restore the "
            "anchor, or insert with a letter suffix (2.1a) instead of "
            "renumbering. A superseded section keeps its heading and anchor. "
            "This gate never rewrites files.")
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
