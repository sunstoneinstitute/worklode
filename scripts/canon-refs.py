#!/usr/bin/env python3
"""Rewrite legacy number-slug document references to the KEY-KIND-N shorthand.

The corpus carries four spellings of a document reference. Only one of them,
`004-execution-backbone.md#sec-3`, names neither the project nor the kind, so
it resolves same-project only (025 §14.3) and breaks on a renumbering. This
rewrites that spelling to `WL-SPEC-4#sec-3` everywhere it resolves.

Out of scope, deliberately: the `025 §7.3` and `spec 025` prose spellings stay
as they are. Both are readable, both infer the project from context, and
neither appears in frontmatter, where an incomplete reference would matter.

Fenced code blocks are skipped. Most of what they hold is Go source quoted
into a plan; rewriting it there would make the quote disagree with the tree.

Usage:
    scripts/canon-refs.py export CORPUS     # pull docs and tasks via lode
    scripts/canon-refs.py scan   CORPUS     # what would change, and what won't
    scripts/canon-refs.py apply  CORPUS     # write the rewrites back
    scripts/canon-refs.py apply  CORPUS --dry-run --diff
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import tempfile
from collections import Counter, defaultdict
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

# ---------------------------------------------------------------- scanning

FENCE = re.compile(r"^\s*(```|~~~)")

# A legacy reference: an optional corpus directory, a document name, an
# optional .md, an optional section fragment. Two name shapes, because the
# corpus numbers specs and ADRs but dates plans:
#
#   004-execution-backbone.md#sec-3        spec and ADR
#   2026-08-03-design-doc-queries-1-...    plan (029 §4 puts plans on their own
#                                          sequence, so their filename never
#                                          carried a corpus number)
#
# Deliberately loose — line ranges ("125-249") and prose compounds ("256-bit")
# match the numbered shape too. Resolution is what rejects them: a candidate
# that names no document is never rewritten.
SLUGREF = re.compile(
    r"(?P<path>(?:[\w.-]+/)+)?(?<![\w.-])"
    r"(?P<name>\d{4}-\d{2}-\d{2}-[a-z0-9]+(?:-[a-z0-9]+)*"
    r"|\d{3}-[a-z0-9]+(?:-[a-z0-9]+)*)"
    r"(?P<md>\.md)?(?P<frag>#sec-[\d.]+)?(?![\w-])"
)

DATE_NAME = re.compile(r"^\d{4}-\d{2}-\d{2}-")
NUMBERED_NAME = re.compile(r"^(?P<num>\d{3})-(?P<slug>.+)$")

# What must stay a filename even though it is shaped like a reference. Both are
# checked against the text around the match rather than inside SLUGREF: the
# optional `.md` backtracks past a trailing lookahead, and the character before
# the match sits before the path group, out of a lookbehind's reach.
LINE_CITATION = re.compile(r"(?:\.md)?:\d")   # 004-execution-backbone.md:706
REVSPEC_CHARS = ":"                           # e4e2920:docs/specs/004-...md

# Where the corpus used to live, before 055 took documents out of the tree. A
# reference written as `docs/specs/004-execution-backbone.md` names a path that
# no longer exists, so the whole path is replaced by the shorthand rather than
# leaving `docs/specs/WL-SPEC-4` behind.
#
# Matched against the WHOLE path, not just its last component. `specs` alone
# would also accept `docs/superpowers/specs/` and
# `admin-cluster/docs/superpowers/specs/`, which are other repos' trees that
# this corpus quotes, and `docs/specs2/`, the fold migration's staging
# directory that WL-PLAN-78/79/80 name inside shell commands. `docs/specs/inlined/`
# is excluded for its own reason: the corpus kept a generated inline view beside
# the source, and dozens of sentences contrast the two ("amend
# `docs/specs/006-...`, never `inlined/`"). Collapsing both spellings to one
# shorthand erases the distinction the sentence exists to make.
CORPUS_PATHS = {"docs/specs", "docs/plans", "docs/adrs", "specs", "plans", "adrs"}


def corpus_path(path: str) -> bool:
    """Whether this directory prefix is a corpus tree whose filename should be
    replaced outright. Leading `./` and `../` are stripped; anything else in
    front of the tail disqualifies it."""
    cleaned = path.strip("/")
    while cleaned.startswith(("./", "../")):
        cleaned = cleaned.split("/", 1)[1] if "/" in cleaned else ""
    return cleaned in CORPUS_PATHS

# An inline code span holding a command: its argument has to stay a filename.
# `./scripts/fold.py --scaffold --only 013-reconciliation.md` is the shape, 19
# times over, almost all of it the fold migration.
SHELL_SPAN = re.compile(
    r"`[^`\n]*(?:\./|\b(?:fold\.py|grep|sed|awk|git|cat|ls|lode|python3|rg|mv|cp|diff)\s)[^`\n]*`"
)

# A markdown link. Its text and its target are two spellings of one reference,
# and only the text is inside a corpus path this rewrites — so rewriting it
# alone leaves a label reading `DP-PLAN-5` over a link to a dead file. One
# occurrence in the corpus (DP-PLAN-4), which is not worth a rule that rewrites
# a whole link construct, so the link is left intact and reported instead.
MD_LINK = re.compile(r"\[[^\]\n]*\]\([^)\n]*\)")

# Documents and tasks whose subject is the legacy spelling itself. They quote
# `001-zero-trust-gateway` and `004-execution-backbone.md` as example strings —
# WL-358 tabulates the two spellings side by side, WL-SPEC-26 defines the
# grammar, the fold plans pass the filenames to `scripts/fold.py`. Rewriting a
# quoted example destroys what the sentence is saying, and no rule separates a
# quoted example from a citation, so these are named rather than detected.
SKIP = {
    "WL-SPEC-25",   # documents in the backbone: 025 §14.3 defines the shorthand
    "WL-SPEC-26",   # the reference grammar (026 §3)
    "WL-357",       # covers: resolution bug report
    "WL-358",       # ref-form ambiguity bug report, with a spelling table
    "WL-481",       # survey of stale command names, by file and line
    "WL-720",       # coverage-edge report, by file and anchor
    "WL-PLAN-78",   # fold 1: shell commands naming spec files
    "WL-PLAN-79",   # fold 2
    "WL-PLAN-80",   # fold 3
    "WL-PLAN-88",   # fold follow-ups, with git revspecs and line numbers
    "WL-PLAN-66",   # the shorthand resolver's own plan, tabulating spellings
    "WL-845",       # this migration's own follow-up, quoting the legacy spelling
}

# SKIP is a living list. Anything written *about* this migration quotes the old
# spelling to name it, so it lands in the scan as a rewrite candidate — WL-845
# did, the hour it was filed. Re-read the scan's body-text rewrites before a
# re-run rather than assuming the set is closed.

NUMBER_PREFIX = re.compile(r"^\d{3}-")


def segments(body: str):
    """Yield (index, line, region) for every line, region in frontmatter|
    fence|text. The frontmatter is the leading `---` block; fences are the
    ``` and ~~~ blocks scanHeadings also recognises."""
    lines = body.split("\n")
    i = 0
    if lines and lines[0].strip() == "---":
        yield 0, lines[0], "frontmatter"
        i = 1
        while i < len(lines) and lines[i].strip() != "---":
            yield i, lines[i], "frontmatter"
            i += 1
        if i < len(lines):
            yield i, lines[i], "frontmatter"
            i += 1
    fence = ""
    for n in range(i, len(lines)):
        line = lines[n]
        stripped = line.lstrip()
        if fence:
            yield n, line, "fence"
            if stripped.startswith(fence):
                fence = ""
            continue
        m = FENCE.match(line)
        if m:
            fence = m.group(1)
            yield n, line, "fence"
            continue
        yield n, line, "text"


# -------------------------------------------------------------- resolution


def stem(slug: str) -> str:
    """A slug without its legacy NNN- prefix. Documents minted by `lode doc
    add` carry the bare stem ("secrets-catalog-home"); imported ones kept the
    corpus filename ("043-secrets-catalog-home")."""
    return NUMBER_PREFIX.sub("", slug)


class Corpus:
    def __init__(self, docs: list[dict]):
        self.docs = docs
        self.by_key: dict[str, list[dict]] = defaultdict(list)
        for d in docs:
            self.by_key[d["project_key"]].append(d)

    def resolve(self, key: str, name: str) -> tuple[dict | None, str]:
        """Resolve one legacy reference to the document it names.

        A date-led name is a plan filename and is matched on the slug alone:
        `2026-08-03-design-doc-queries-1-corpus-and-list` is already the plan's
        slug, and a date plus a title is specific enough that an exact match is
        the whole rule.

        A number-led name is a spec or ADR filename, where the slug has drifted
        in two ways. Documents minted by `lode doc add` carry a bare stem
        ("secrets-catalog-home") while imported ones kept the corpus filename
        ("043-secrets-catalog-home"), and some documents were renumbered. So the
        ladder runs exact slug, then stem plus number, then stem alone.

        There is no bare-number rung. A number alone matched 81 occurrences in
        the corpus and every one was wrong: test-fixture filenames
        (`001-alpha.md`) and prose compounds (`016-width`) share a leading
        number with a real spec and mean nothing like it.

        Nothing reaches into another project. The legacy spellings cannot cross
        a corpus (025 §14.3), so a reference that misses at home names something
        this corpus does not hold, and guessing across projects produced only
        false matches when tried.
        """
        pool = self.by_key.get(key, [])

        if DATE_NAME.match(name):
            found = [d for d in pool if d["slug"] == name]
            if len(found) == 1:
                return found[0], "plan-slug"
            return None, "ambiguous:plan-slug" if found else "unresolved"

        m = NUMBERED_NAME.match(name)
        if not m:
            return None, "unresolved"
        num, slug = int(m.group("num")), m.group("slug")
        full = f"{num:03d}-{slug}"
        rungs = (
            ("exact", lambda d: d["slug"] == full),
            ("stem+number", lambda d: stem(d["slug"]) == slug and d["number"] == num),
            ("stem", lambda d: stem(d["slug"]) == slug),
        )
        for rung, pred in rungs:
            found = [d for d in pool if pred(d)]
            if len(found) == 1:
                return found[0], rung
            if len(found) > 1:
                return None, f"ambiguous:{rung}"
        return None, "unresolved"


def rewrite(corpus: Corpus, key: str, body: str) -> tuple[str, list[dict]]:
    """Return body with every resolvable legacy reference canonicalised, plus
    one record per occurrence — rewritten or not — for the scan report."""
    hits: list[dict] = []
    out: list[str] = []

    for lineno, line, region in segments(body):
        if region == "fence":
            out.append(line)
            continue

        def sub(m: re.Match) -> str:
            path = m.group("path") or ""
            if path and not corpus_path(path):
                return m.group(0)
            # A line-number citation names a place in a file, which the
            # shorthand cannot express; a revspec names the file in a commit.
            if LINE_CITATION.match(line, m.end()):
                return m.group(0)
            if m.start() and line[m.start() - 1] in REVSPEC_CHARS:
                return m.group(0)
            if any(c.start() <= m.start() < c.end() for c in SHELL_SPAN.finditer(line)):
                return m.group(0)
            if any(c.start() <= m.start() < c.end() for c in MD_LINK.finditer(line)):
                return m.group(0)
            doc, rule = corpus.resolve(key, m.group("name"))
            named = bool(m.group("md") or m.group("frag") or path)
            hits.append(
                {
                    "line": lineno + 1,
                    "region": region,
                    "text": m.group(0),
                    "rule": rule,
                    "named": named,
                    "target": doc["ref"] if doc else None,
                }
            )
            if not doc:
                return m.group(0)
            return doc["ref"] + (m.group("frag") or "")

        out.append(SLUGREF.sub(sub, line))

    return "\n".join(out), hits


# ------------------------------------------------------------------- lode


def lode(*args: str) -> str:
    r = subprocess.run(["lode", *args], capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(f"lode {' '.join(args)} failed: {r.stderr.strip()}")
    return r.stdout


def cmd_export(corpus_dir: Path) -> None:
    (corpus_dir / "docs").mkdir(parents=True, exist_ok=True)
    (corpus_dir / "doclist.json").write_text(lode("doc", "list", "--project=", "--json"))
    (corpus_dir / "tasklist.json").write_text(
        lode("task", "list", "--project=", "--status", "all", "--json")
    )
    docs = json.loads((corpus_dir / "doclist.json").read_text())["docs"]

    # One `lode doc show` per document, and there is no bulk endpoint that
    # carries bodies. Sequentially that is minutes; the server is the only
    # thing doing work, so fetch them concurrently.
    def fetch(d: dict) -> None:
        (corpus_dir / "docs" / f"{d['ref']}.json").write_text(
            lode("doc", "show", d["ref"], "--json")
        )

    with ThreadPoolExecutor(max_workers=10) as pool:
        for i, _ in enumerate(pool.map(fetch, docs), 1):
            if i % 100 == 0:
                print(f"  {i}/{len(docs)} docs", file=sys.stderr)
    tasks = json.loads((corpus_dir / "tasklist.json").read_text())["tasks"]
    print(f"exported {len(docs)} docs, {len(tasks)} tasks to {corpus_dir}")


def load(corpus_dir: Path) -> tuple[Corpus, list[dict], list[dict]]:
    docs = json.loads((corpus_dir / "doclist.json").read_text())["docs"]
    tasks = json.loads((corpus_dir / "tasklist.json").read_text())["tasks"]
    details = [
        json.loads((corpus_dir / "docs" / f"{d['ref']}.json").read_text()) for d in docs
    ]
    return Corpus(docs), details, tasks


def entities(corpus_dir: Path):
    """Yield (name, project_key, body, kind, status, version) for every doc
    and task. A task's project key is the prefix of its id; tasks carry no
    version, so their compare-and-swap falls back to updated_at."""
    corpus, details, tasks = load(corpus_dir)
    for d in details:
        if d["ref"] in SKIP:
            continue
        yield corpus, d["ref"], d["project_key"], d.get("body") or "", d["kind"], d[
            "status"
        ], d["version"]
    for t in tasks:
        if t["id"] in SKIP:
            continue
        yield corpus, t["id"], t["id"].rsplit("-", 1)[0], t.get("body") or "", "task", t[
            "state"
        ], t["updated_at"]


# ------------------------------------------------------------------ report


def cmd_scan(corpus_dir: Path, show_unresolved: bool) -> None:
    rules: Counter = Counter()
    regions: Counter = Counter()
    changed: dict[str, int] = {}
    leftover: Counter = Counter()
    leftover_where: dict[tuple, set] = defaultdict(set)

    for corpus, name, key, body, *_ in entities(corpus_dir):
        new, hits = rewrite(corpus, key, body)
        n = sum(1 for h in hits if h["target"])
        if n:
            changed[name] = n
        for h in hits:
            if h["target"]:
                rules[h["rule"]] += 1
                regions[h["region"]] += 1
            elif h["named"]:
                leftover[(key, h["text"], h["rule"])] += 1
                leftover_where[(key, h["text"], h["rule"])].add(name)

    total = sum(rules.values())
    print(f"{total} references to rewrite across {len(changed)} documents and tasks\n")
    print("by rule:")
    for r, n in rules.most_common():
        print(f"  {r:<22}{n:>6}")
    print("\nby region:")
    for r, n in regions.most_common():
        print(f"  {r:<22}{n:>6}")

    print(f"\n{sum(leftover.values())} references left alone that look like refs")
    print("(a .md suffix or a #sec- fragment, but naming no document):\n")
    for (key, text, rule), n in leftover.most_common(40 if not show_unresolved else 999):
        where = sorted(leftover_where[(key, text, rule)])
        print(f"  {key:<6}{text:<48}{n:>4}  {', '.join(where[:3])}")


# ------------------------------------------------------------------ apply


def cmd_apply(corpus_dir: Path, dry_run: bool, diff: bool, limit: int | None,
              revise_gated: bool = False) -> None:
    import difflib

    done = failed = 0
    for corpus, name, key, body, kind, status, version in entities(corpus_dir):
        new, hits = rewrite(corpus, key, body)
        if new == body:
            continue
        n = sum(1 for h in hits if h["target"])
        if diff:
            print(
                "\n".join(
                    difflib.unified_diff(
                        body.split("\n"), new.split("\n"),
                        fromfile=name, tofile=name, lineterm="", n=1,
                    )
                )
            )
        if dry_run:
            print(f"[dry-run] {name}: {n} references")
            done += 1
        else:
            try:
                write_back(name, kind, status, version, new, revise_gated)
                print(f"{name}: {n} references")
                done += 1
            except Exception as e:  # noqa: BLE001 - one failure must not stop the run
                print(f"{name}: FAILED {e}", file=sys.stderr)
                failed += 1
        if limit and done >= limit:
            break
    print(f"\n{done} written, {failed} failed")


def _rule_of(err: Exception) -> str:
    """The §8.3 rule name out of a refusal message, for the run log."""
    m = re.search(r"\((new-dependency|ns-term|surface-token|acceptance-criteria|referrer)\)", str(err))
    return m.group(1) if m else "refused"


def write_back(name: str, kind: str, status: str, version, body: str,
               revise_gated: bool = False) -> None:
    """Write one rewritten body back, refusing if the row moved since export.

    Documents carry a version, so the check is exact. Tasks do not; updated_at
    is the only staleness signal they offer, so that is what is compared.
    """
    if kind == "task":
        current = json.loads(lode("task", "show", name, "--json"))
        if current["updated_at"] != version:
            raise RuntimeError(f"changed since export (updated_at {version} -> {current['updated_at']})")
    else:
        current = json.loads(lode("doc", "show", name, "--json"))
        if current["version"] != version:
            raise RuntimeError(f"changed since export (version {version} -> {current['version']})")

    with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
        f.write(body)
        path = f.name
    try:
        if kind == "task":
            lode("task", "edit", name, "--body-file", path, "--no-upload")
        elif kind != "plan" and status == "accepted":
            # An accepted spec or ADR is amended in place (025 §8.4), which
            # needs a note.
            try:
                lode("doc", "edit", name, "--file", path,
                     "--note", "canonicalise legacy number-slug references to KEY-KIND-N")
            except RuntimeError as e:
                # §8.3 refuses this edit on every accepted spec whose
                # frontmatter it touches, and the rule that fires is
                # new-dependency: it compares the literal strings in `requires:`,
                # so rewriting `017-task-secrets.md` to `WL-SPEC-17` reads as one
                # dependency dropped and another gained. The refusal names the
                # way through itself — "revise it with `lode doc revise`" — which
                # runs the 025 §6 anchor gate this edit does not disturb. Opt-in,
                # because it lands a new version without asking the reviewers.
                if not (revise_gated and "cannot be patched" in str(e)):
                    raise
                print(f"  {name}: patch refused ({_rule_of(e)}), landing via revise",
                      file=sys.stderr)
                # Three steps, not two: `--file` updates an *open* candidate and
                # 404s when there is none, so the bare form opens it first. A
                # candidate left open by an earlier failed run makes the open a
                # no-op error, which is why it is tolerated rather than raised.
                try:
                    lode("doc", "revise", name)
                except RuntimeError:
                    pass
                lode("doc", "revise", name, "--file", path)
                lode("doc", "revise", name, "--accept")
        else:
            lode("doc", "edit", name, "--file", path)
    finally:
        Path(path).unlink(missing_ok=True)


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)
    for cmd in ("export", "scan", "apply"):
        s = sub.add_parser(cmd)
        s.add_argument("corpus", type=Path)
        if cmd == "scan":
            s.add_argument("--all", action="store_true",
                           help="list every unresolved reference, not the top 40")
        if cmd == "apply":
            s.add_argument("--dry-run", action="store_true")
            s.add_argument("--diff", action="store_true")
            s.add_argument("--limit", type=int)
            s.add_argument("--revise-gated", action="store_true",
                           help="land an accepted spec that §8.3 refuses through "
                                "the revision cycle instead of failing it")
    a = p.parse_args()
    if a.cmd == "export":
        cmd_export(a.corpus)
    elif a.cmd == "scan":
        cmd_scan(a.corpus, a.all)
    else:
        cmd_apply(a.corpus, a.dry_run, a.diff, a.limit, a.revise_gated)
    return 0


if __name__ == "__main__":
    sys.exit(main())
