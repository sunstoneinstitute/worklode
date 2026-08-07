#!/usr/bin/env python3
"""Check design-document frontmatter against the schema in
docs/authoring-design-docs.md. Reports; never rewrites.

What it enforces, per document:

  * only known keys, each on the tree that may carry it (`implements` is a
    plan key, `wasDerivedFrom` a spec key)
  * `status` is one of draft / accepted / superseded -- `proposed` was retired
    by 025 section 3
  * an `accepted` or `superseded` document carries an `issued` date
  * every date is `YYYY-MM-DD`
  * scalar-reference keys hold a bare reference, not prose around one: the
    decision lists that used to trail `wasDerivedFrom` parse as part of the
    filename and resolve to nothing
  * map-shaped keys (`amends`, `replaces` and their mirrors) really are maps
    keyed by a section of *this* document
  * every reference resolves, and every `#sec-N` fragment names an anchor that
    exists in the target -- except the cross-project shorthand, which is
    reported as `unresolved` rather than as an error (commit hooks run without
    a network).

Usage: secmeta.py [path ...]        # defaults to docs/specs and docs/plans
"""

import argparse
import os
import re
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # importing secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import split_front_matter  # noqa: E402

try:
    import yaml
except ImportError:
    sys.exit("secmeta: needs PyYAML (pip install pyyaml)")

REPO = Path(__file__).resolve().parent.parent
SPECS, PLANS = "docs/specs", "docs/plans"
DEFAULT_ROOTS = (SPECS, PLANS)

STATUSES = {"draft", "accepted", "superseded"}
EFFECTIVE = {"accepted", "superseded"}
DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
# <PROJECTKEY>-SPEC|ADR-<n>[#sec-<anchor>] (014 section 11.3), plus the colon form.
SHORTHAND = re.compile(r"^[A-Za-z][\w-]*[-:](SPEC|ADR)-(\d+)(#sec-[\w.]+)?$", re.I)
# Spec 0 is reserved: "this plan has no governing spec". It names no file on
# purpose, so it resolves to nothing and that is not a defect. Spelt without a
# project key -- "no spec" is not one project's spec 0, it is the absence of a
# spec anywhere -- so `<KEY>-SPEC-0` in any project canonicalises to this.
NO_SPEC = "NO-SPEC"
SPEC_ZERO = re.compile(r"^[A-Za-z][\w-]*[-:]SPEC-0+(#|$)", re.I)
# A reference is a filename or repo-relative path, optionally + #sec-N. Anything
# else -- a space, a parenthesis, a comma -- is prose, not a reference.
REFERENCE = re.compile(r"^[\w./-]+\.md(#sec-[\w.]+)?$")

SCALAR_REFS = {"wasDerivedFrom"}
LIST_REFS = {"requires", "isRequiredBy", "implements"}
MAP_REFS = {"amends", "amendedBy", "replaces", "isReplacedBy"}
PLAIN = {"status", "issued", "task"}
SPEC_ONLY = {"wasDerivedFrom"}
PLAN_ONLY = {"implements"}
KNOWN = SCALAR_REFS | LIST_REFS | MAP_REFS | PLAIN


def sections_of(path):
    """Anchors a document publishes, read from its own source."""
    text = (REPO / path).read_text()
    return set(re.findall(r"\{#(sec-[\w.]+)\}", text))


def as_list(v):
    return v if isinstance(v, list) else [v]


def check_ref(ref, home, where, out, anchors):
    """Resolve one reference and check its fragment."""
    ref = str(ref)
    if ref.upper() == NO_SPEC or SPEC_ZERO.match(ref):
        if ref != NO_SPEC:
            out.append(("error", f"{where}: write {ref} as {NO_SPEC} — spec 0 is the "
                                 "absence of a spec, so it carries no project key"))
        elif where != "implements":
            out.append(("error", f"{where}: {NO_SPEC} means \"no governing spec\" "
                                 "and is only meaningful on a plan's implements"))
        return
    if m := SHORTHAND.match(ref):
        # Unpadded is worklode's convention, not a universal one -- rdf-registry
        # numbers its own ADRs `ADR-0006`, and that is its business.
        number = m.group(2)
        if ref.upper().startswith("WL-") and number != str(int(number)):
            out.append(("error", f"{where}: {ref} zero-pads its number; "
                                 "worklode's shorthand is unpadded"))
            return
        out.append(("unresolved", f"{where}: {ref} names another project"))
        return
    if not REFERENCE.match(ref):
        out.append(("error", f"{where}: {ref!r} is not a reference "
                             "(a bare filename or repo-relative path, + optional #sec-N)"))
        return
    path, _, frag = ref.partition("#")
    if "/" not in path:
        path = f"{home}/{path}"
    if not (REPO / path).is_file():
        out.append(("error", f"{where}: {ref} resolves to no file"))
        return
    if frag:
        if frag not in anchors.setdefault(path, sections_of(path)):
            out.append(("error", f"{where}: {ref} names no anchor in {path}"))


def check(path, anchors):
    """Return [(severity, message)] for one document."""
    out = []
    rel = os.path.relpath(Path(path).resolve(), REPO)  # may escape the repo; that is the caller's business
    home = str(Path(rel).parent)
    is_plan = home == PLANS

    fm, _ = split_front_matter((REPO / rel).read_text())
    if not fm:
        out.append(("error", "no frontmatter"))
        return out
    try:
        data = yaml.safe_load(fm[4:-5]) or {}  # drop the --- fences
    except yaml.YAMLError as e:
        return [("error", f"frontmatter is not valid YAML: {e}")]

    for key in data:
        if key not in KNOWN:
            out.append(("error", f"unknown key {key!r}"))
        elif key in SPEC_ONLY and is_plan:
            out.append(("error", f"{key} is a spec key, not a plan key"))
        elif key in PLAN_ONLY and not is_plan:
            out.append(("error", f"{key} is a plan key, not a spec key"))

    status = data.get("status")
    if status is None:
        out.append(("error", "no status"))
    elif str(status) not in STATUSES:
        out.append(("error", f"status {status!r} is not one of "
                             + "/".join(sorted(STATUSES))))

    issued = data.get("issued")
    if issued is None:
        # A plan's date of publication is its filename; only specs carry `issued`.
        if str(status) in EFFECTIVE and not is_plan:
            out.append(("error", f"status is {status} but issued is missing"))
    elif not DATE.match(str(issued)):
        out.append(("error", f"issued {issued!r} is not YYYY-MM-DD"))

    published = sections_of(rel)
    for key in sorted(set(data) & (SCALAR_REFS | LIST_REFS)):
        v = data[key]
        if key in SCALAR_REFS and isinstance(v, (list, dict)):
            out.append(("error", f"{key} takes a single reference, not a list"))
            continue
        for ref in as_list(v):
            check_ref(ref, home, key, out, anchors)

    for key in sorted(set(data) & MAP_REFS):
        v = data[key]
        if not isinstance(v, dict):
            out.append(("error", f"{key} takes a map keyed by a section of this "
                                 'document ("." for the whole document)'))
            continue
        for own, refs in v.items():
            if own != "." and own.lstrip("#") not in published:
                out.append(("error", f"{key}: {own} is not a section of this document"))
            for ref in as_list(refs):
                check_ref(ref, home, f"{key}[{own}]", out, anchors)

    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("paths", nargs="*", default=list(DEFAULT_ROOTS))
    a = ap.parse_args()

    files = []
    for p in a.paths:
        p = Path(p)
        files.extend(sorted(p.rglob("*.md")) if p.is_dir() else [p])

    anchors, bad = {}, 0
    for f in files:
        if f.name == "index.yaml":
            continue
        problems = check(f, anchors)
        errors = [m for sev, m in problems if sev == "error"]
        unresolved = [m for sev, m in problems if sev == "unresolved"]
        for m in errors:
            print(f"{f}: {m}")
        for m in unresolved:
            print(f"{f}: {m}", file=sys.stderr)
        bad += bool(errors)

    if bad:
        print(f"\nsecmeta: {bad} document(s) with frontmatter errors "
              "(schema: docs/authoring-design-docs.md)")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
