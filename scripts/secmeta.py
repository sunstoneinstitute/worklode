#!/usr/bin/env python3
"""Check design-document frontmatter against the schema in
docs/authoring-design-docs.md. Reports; never rewrites.

What it enforces, per document:

  * only known keys, each on the tree that may carry it (`covers` is a
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

A plan's `covers` entry takes either form (033 section 3): a bare reference,
meaning `coverage: full`, or the qualified mapping of `spec` + `coverage` and
an optional `fullCoverageWith`. Two findings need more than one document and so
run over the corpus rather than per document -- that every `fullCoverageWith`
target is accepted and contributes `full` or `partial` to the same section (033
section 2 checks closure, never trusts it), and that a section covered by
several accepted plans is written in the qualified form, which is the only one
that can say how the work divides.

A plan's `defers` entry (026 section 5.3) hands one section to a named owner:
a mapping of `spec` + `to`, no bare form -- a deferral without an owner is
just an uncovered section, which needs no syntax. Both keys are required, the
`spec` needs a `#sec-N` fragment and the `to` must not carry one, and a
section deferred twice to different owners is a contradiction the frontmatter
cannot mean.

Usage: secmeta.py [path ...]        # defaults to docs/specs and docs/plans
"""

import argparse
import os
import re
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # importing secfmt must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import generated, split_front_matter  # noqa: E402

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
# `covers` is the plan's undertaking to realise a section (026 §5); `implements`
# is its retired spelling, still parsed so in-flight branches merge.
PLAN_COVERAGE = {"covers", "implements"}
# The qualified entry form (026 §5.1). `full` and `none` leave nothing to
# complete, so `fullCoverageWith` is only meaningful beside `partial`.
COVERAGE_KEYS = {"spec", "coverage", "fullCoverageWith"}
COVERAGE_LEVELS = ("full", "partial", "none")
CLOSED_LEVELS = {"full", "none"}
# `blocks`/`blockedBy` order whole plan documents (025 §5, §9.3) -- the
# ordering edge that replaces a container row above a plan's tasks.
PLAN_ORDERING = {"blocks", "blockedBy"}
# `defers` (026 §5.3) hands one section to a named owner. It is a list of
# {spec, to} mappings -- unlike `covers` it has no bare shorthand, so it is
# neither a LIST_REFS nor a MAP_REFS key and gets its own check.
DEFERS = {"defers"}
DEFER_KEYS = {"spec", "to"}
LIST_REFS = {"requires", "isRequiredBy"} | PLAN_COVERAGE | PLAN_ORDERING
MAP_REFS = {"amends", "amendedBy", "replaces", "isReplacedBy"}
PLAIN = {"status", "issued", "task", "kind"}
SPEC_ONLY = {"wasDerivedFrom"}
PLAN_ONLY = PLAN_COVERAGE | PLAN_ORDERING | DEFERS
KNOWN = SCALAR_REFS | LIST_REFS | MAP_REFS | PLAIN | DEFERS


def sections_of(path):
    """Anchors a document publishes, read from its own source."""
    text = (REPO / path).read_text()
    return set(re.findall(r"\{#(sec-[\w.]+)\}", text))


def as_list(v):
    return v if isinstance(v, list) else [v]


def load_frontmatter(rel):
    """(data, error) for one document; data is None when error is set."""
    fm, _ = split_front_matter((REPO / rel).read_text())
    if not fm:
        return None, "no frontmatter"
    try:
        data = yaml.safe_load(fm[4:-5]) or {}  # drop the --- fences
    except yaml.YAMLError as e:
        return None, f"frontmatter is not valid YAML: {e}"
    return (data, None) if isinstance(data, dict) else (None, "frontmatter is not a mapping")


def resolve_ref(ref, home):
    """A reference as `<repo-relative path>[#fragment]`, or None when it names
    no file in this repo -- NO-SPEC, the cross-project shorthand, or prose.
    Resolution only; check_ref is what reports."""
    ref = str(ref)
    if ref.upper() == NO_SPEC or SPEC_ZERO.match(ref) or SHORTHAND.match(ref):
        return None
    if not REFERENCE.match(ref):
        return None
    path, _, frag = ref.partition("#")
    if "/" not in path:
        path = f"{home}/{path}"
    return f"{path}#{frag}" if frag else path


def coverage_key(data):
    """The plan-coverage key a document carries, `covers` winning over its
    retired spelling. Both at once is reported separately, as an error."""
    for key in ("covers", "implements"):
        if key in data:
            return key
    return None


def is_plan_path(ref):
    if not isinstance(ref, str):
        return False
    path, separator, _ = ref.partition("#")
    if separator or any(part in {"", ".", ".."} for part in path.split("/")):
        return False
    normal = os.path.normpath(path)
    return path == normal and path.startswith(f"{PLANS}/") and path.endswith(".md")


def check_ref(ref, home, where, out, anchors):
    """Resolve one reference and check its fragment."""
    ref = str(ref)
    if ref.upper() == NO_SPEC or SPEC_ZERO.match(ref):
        if ref != NO_SPEC:
            out.append(("error", f"{where}: write {ref} as {NO_SPEC} — spec 0 is the "
                                 "absence of a spec, so it carries no project key"))
        elif where not in PLAN_COVERAGE:
            out.append(("error", f"{where}: {NO_SPEC} means \"no governing spec\" "
                                 "and is only meaningful on a plan's covers"))
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


def check_coverage_entry(entry, home, where, out, anchors):
    """Check one qualified `covers` entry (026 §5.1). Whether a named
    fullCoverageWith plan really covers the section needs that plan's own
    frontmatter, so cross_check does it."""
    unknown = sorted(str(k) for k in set(entry) - COVERAGE_KEYS)
    if unknown:
        out.append(("error", f"{where}: unknown key(s) {', '.join(unknown)} — an entry takes "
                             "spec, coverage and (beside partial) fullCoverageWith"))

    level = entry.get("coverage")
    if level is None:
        out.append(("error", f"{where}: no coverage — one of " + "/".join(COVERAGE_LEVELS)))
    elif str(level) not in COVERAGE_LEVELS:
        out.append(("error", f"{where}: coverage {level!r} is not one of "
                             + "/".join(COVERAGE_LEVELS)))
    if "fullCoverageWith" in entry and str(level) in CLOSED_LEVELS:
        out.append(("error", f"{where}: fullCoverageWith is invalid beside coverage: {level} — "
                             "only a partial entry has anything left for another plan to close"))

    spec = entry.get("spec")
    if spec is None:
        out.append(("error", f"{where}: no spec — a qualified entry names the section it covers"))
    elif str(spec).upper() == NO_SPEC or SPEC_ZERO.match(str(spec)):
        out.append(("error", f"{where}: {NO_SPEC} is unqualified — the absence of a governing "
                             "spec has no sections to cover, so write it as a bare entry"))
    else:
        spec = str(spec)
        check_ref(spec, home, f"{where}.spec", out, anchors)
        if (REFERENCE.match(spec) or SHORTHAND.match(spec)) and "#sec-" not in spec:
            out.append(("error", f"{where}.spec: {spec} names no section — a plan claiming a "
                                 "whole document says nothing a coverage query can use"))

    completions = entry.get("fullCoverageWith")
    if completions is not None:
        if isinstance(completions, list) and not completions:
            out.append(("error", f"{where}: fullCoverageWith must name at least one plan"))
        elif (not isinstance(completions, list) or
                not all(isinstance(target, str) and is_plan_path(target)
                        for target in completions)):
            out.append(("error", f"{where}: fullCoverageWith must be a list of "
                                 "repo-relative plan paths"))
        else:
            for i, target in enumerate(completions):
                check_ref(target, home, f"{where}.fullCoverageWith[{i}]", out, anchors)


def check_defers_entry(entry, home, where, out, anchors):
    """Check one `defers` entry (026 §5.3): a plan's explicit handoff of one
    section to a named owner. Unlike a qualified `covers` entry it carries no
    level -- only `spec` and `to` are legal keys, and both resolve through the
    same reference machinery `covers` uses."""
    unknown = sorted(str(k) for k in set(entry) - DEFER_KEYS)
    if unknown:
        out.append(("error", f"{where}: unknown key(s) {', '.join(unknown)} — an entry "
                             "takes spec and to"))

    spec = entry.get("spec")
    if spec is None:
        out.append(("error", f"{where}: no spec — a deferral names the section handed off"))
    else:
        spec = str(spec)
        check_ref(spec, home, f"{where}.spec", out, anchors)
        if (REFERENCE.match(spec) or SHORTHAND.match(spec)) and "#sec-" not in spec:
            out.append(("error", f"{where}.spec: {spec} names no section — a whole-document "
                                 "deferral would silently defer future sections too (026 §5.3)"))

    to = entry.get("to")
    if to is None:
        out.append(("error", f"{where}: no to — a deferral without an owner is just an "
                             "uncovered section, which needs no syntax (026 §5.3)"))
    else:
        to = str(to)
        check_ref(to, home, f"{where}.to", out, anchors)
        if "#" in to:
            out.append(("error", f"{where}.to: {to} names a section — the owner is a "
                                 "document, no fragment (026 §5.3)"))


def coverage_of(rel, data):
    """A plan's coverage entries as (section, bare, level, [completing plan path]),
    each already resolved to a repo-relative path. Unresolvable references are
    None here and reported by check(). Completions are carried only for a
    `partial` entry -- elsewhere they are the malformed frontmatter check()
    reports, and there is no closure claim left to test."""
    home = str(Path(rel).parent)
    key = coverage_key(data)
    entries = []
    for entry in as_list(data.get(key)) if key else []:
        if isinstance(entry, dict):
            level = str(entry.get("coverage"))
            completions = entry.get("fullCoverageWith") if (
                level == "partial" and
                isinstance(entry.get("fullCoverageWith"), list)
            ) else []
            entries.append((resolve_ref(entry.get("spec", ""), home), False, level,
                            [p for p in (resolve_ref(c, home) for c in completions
                                         if is_plan_path(c)) if p]))
        else:
            entries.append((resolve_ref(entry, home), True, "full", []))
    return entries


def cross_check(index):
    """Findings that need more than one document, as (document, severity,
    message). They depend on other files resolving, so they are `unresolved`:
    the plan that would settle them may still be on another branch."""
    out = []
    accepted = {}  # section -> the accepted plans covering it
    for rel, (_, status, entries) in index.items():
        for section, _bare, _level, _with in entries:
            if section and status == "accepted":
                accepted.setdefault(section, set()).add(rel)

    for rel, (shown, status, entries) in sorted(index.items()):
        for section, bare, _level, completions in entries:
            if section and bare and status == "accepted" and "#sec-" in section:
                # A whole-document reference names no section, so it is outside
                # this rule; 026 §5.1 requires the qualified form only where a
                # section is shared, which is the case the bare form cannot say.
                others = accepted.get(section, set()) - {rel}
                if others:
                    out.append((shown, "unresolved",
                                f"{section} is also covered by {', '.join(sorted(others))}; "
                                "more than one accepted plan on a section needs the qualified "
                                "form (026 §5.1)"))
            for target in completions:
                if target == rel:
                    out.append((shown, "error",
                                "fullCoverageWith cannot name its own plan — a partial plan "
                                "cannot close itself (026 §2.1)"))
                    continue
                if target not in index:
                    path, _, _ = target.partition("#")
                    if (REPO / path).is_file():
                        out.append((shown, "unresolved",
                                    f"fullCoverageWith names {target}, which does not itself cover "
                                    f"{section} — closure is checked, never trusted (026 §2.1)"))
                    continue  # no file — check() reports that
                target_status = index[target][1]
                if target_status != "accepted":
                    out.append((shown, "unresolved",
                                f"fullCoverageWith names {target_status} plan {target}; only "
                                "accepted plans can close coverage (026 §2.1)"))
                    continue
                same_section = [level for s, _, level, _ in index[target][2]
                                if section and s == section]
                if not same_section:
                    out.append((shown, "unresolved",
                                f"fullCoverageWith names {target}, which does not itself cover "
                                f"{section} — closure is checked, never trusted (026 §2.1)"))
                elif not any(level in {"full", "partial"} for level in same_section):
                    out.append((shown, "unresolved",
                                f"fullCoverageWith names {target}, whose coverage: none contributes "
                                f"nothing to {section} closure (026 §2.1)"))
    return out


def check(path, anchors):
    """Return [(severity, message)] for one document."""
    out = []
    rel = os.path.relpath(Path(path).resolve(), REPO)  # may escape the repo; that is the caller's business
    home = str(Path(rel).parent)
    is_plan = home == PLANS

    data, err = load_frontmatter(rel)
    if err:
        return [("error", err)]

    for key in data:
        if key not in KNOWN:
            out.append(("error", f"unknown key {key!r}"))
        elif key in SPEC_ONLY and is_plan:
            out.append(("error", f"{key} is a spec key, not a plan key"))
        elif key in PLAN_ONLY and not is_plan:
            out.append(("error", f"{key} is a plan key, not a spec key"))

    if PLAN_COVERAGE <= set(data):
        out.append(("error", "covers and implements are the same key under two "
                             "names (026 §5.1) — keep covers"))
    elif "implements" in data:
        out.append(("unresolved", "implements is retired on a plan; write covers "
                                  "(026 §5)"))

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
        for i, ref in enumerate(as_list(v)):
            if key in PLAN_COVERAGE and isinstance(ref, dict):
                check_coverage_entry(ref, home, f"{key}[{i}]", out, anchors)
            else:
                check_ref(ref, home, key, out, anchors)
                ref = str(ref)
                if (key in PLAN_COVERAGE and ref.upper() != NO_SPEC and
                        not SPEC_ZERO.match(ref) and
                        (REFERENCE.match(ref) or SHORTHAND.match(ref)) and
                        "#sec-" not in ref):
                    # Before 033, plans commonly named a whole spec. Keep those
                    # legacy headers checkable, but report that a section query
                    # cannot consume the claim. New qualified entries fail the
                    # same condition in check_coverage_entry above.
                    out.append(("unresolved", f"{key}: {ref} names no section — a plan "
                                "claiming a whole document says nothing a coverage query can use"))

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

    if "defers" in data:
        v = data["defers"]
        if not isinstance(v, list):
            out.append(("error", "defers takes a list of spec/to entries (026 §5.3)"))
        else:
            owners = {}  # resolved section -> (owner as authored, resolved owner)
            for i, entry in enumerate(v):
                where = f"defers[{i}]"
                if not isinstance(entry, dict):
                    out.append(("error", f"{where}: a defers entry is a mapping of spec and to"))
                    continue
                check_defers_entry(entry, home, where, out, anchors)
                spec, to = entry.get("spec"), entry.get("to")
                if spec is None or to is None:
                    continue
                section = resolve_ref(spec, home) or str(spec)
                target = resolve_ref(to, home) or str(to)
                prior = owners.get(section)
                if prior and prior[1] != target:
                    out.append(("error", f"{where}: {spec} is already deferred to {prior[0]} — "
                                         "one owner per section per plan (026 §5.3)"))
                else:
                    owners.setdefault(section, (to, target))

    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("paths", nargs="*", default=list(DEFAULT_ROOTS))
    a = ap.parse_args()

    files = []
    for p in a.paths:
        p = Path(p)
        files.extend(
            sorted(q for q in p.rglob("*.md") if not generated(q))
            if p.is_dir()
            else [p]
        )
    files = [f for f in files if f.name != "index.yaml"]

    anchors, problems = {}, {}
    index = {}  # plan path -> (as named on the command line, status, coverage entries)
    for f in files:
        problems[f] = check(f, anchors)
        rel = os.path.relpath(Path(f).resolve(), REPO)
        if str(Path(rel).parent) != PLANS:
            continue
        data, err = load_frontmatter(rel)
        if not err:
            index[rel] = (f, str(data.get("status")), coverage_of(rel, data))

    for f, sev, m in cross_check(index):
        problems[f].append((sev, m))

    bad = 0
    for f in files:
        errors = [m for sev, m in problems[f] if sev == "error"]
        unresolved = [m for sev, m in problems[f] if sev == "unresolved"]
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
