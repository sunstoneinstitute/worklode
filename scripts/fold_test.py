#!/usr/bin/env python3
"""Hermetic tests for scripts/fold.py -- fold.yaml parsing, derived
mapping.yaml, the --check completeness gate, and --scaffold. Follows
scripts/secmeta_test.py's isolated-repo pattern: copy the script(s) under
test into a throwaway repo and run fold.py as a subprocess, so REPO
(script-relative) resolves inside the temp dir and nothing touches
docs/specs/ or docs/specs2/ in this repo.

--check also imports currentspec.py (for the --with-drafts live view) and
secindex.py (to prove docs/specs/index.yaml is current), so its fixtures
copy those scripts too and build a small live docs/specs/ corpus alongside
docs/specs2/fold.yaml. secindex.render() is used here both as the object
under test's own staleness oracle and, in these fixtures, to author a
byte-matching index.yaml -- there is no secindex_test.py and secindex.py
--check runs in neither pre-commit nor CI, so this suite is presently the
only thing exercising render() at all.

--check's anchor-drift half (fold.yaml vs. the written docs/specs2/ prose)
reuses the same fixtures via write_check_repo's `written` parameter: an
optional {filename: markdown} of docs/specs2/ files, on top of the fold.yaml
and docs/specs/ mini-corpus every --check test already needs. Omitting a
filename from `written` is what exercises "not written yet" -- deliberately
the default, so every completeness-gate test above already doubles as a
regression for "skip, don't report" unless it opts into `written`.

--scaffold needs no index.yaml (it slices docs/specs/ sections directly,
never through currentspec.py's live view), so its fixtures are a plain
docs/specs/ mini-corpus plus docs/specs2/fold.yaml. Its last test also
copies secmeta.py in, to prove the scaffolded output passes the house
checks (secfmt.py -d, secmeta.py) -- both must be invoked with an explicit
docs/specs2 path, since it is outside their DEFAULT_ROOTS.
"""

import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("fold_test: needs PyYAML (pip install pyyaml)")

ROOT = Path(__file__).resolve().parent.parent
sys.dont_write_bytecode = True  # importing secindex must not litter scripts/
sys.path.insert(0, str(ROOT / "scripts"))
import fold  # noqa: E402
import secindex  # noqa: E402

# A two-document fixture: one merge (three old sections -> one new section,
# plus a 1:1 section and a dropped one) and one self-fold (a document whose
# only source is itself, the 005 shape the real plan validates first).
FOLD = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 001-identity-and-authentication.md
    title: Identity and authentication
    sources: [002-github-app-auth.md]
    sections:
      - {new: "1", heading: "One to one", from: ["002-github-app-auth.md#sec-1"]}
      - {new: "2", heading: "Three to one", from: ["002-github-app-auth.md#sec-2",
                                                    "002-github-app-auth.md#sec-3",
                                                    "002-github-app-auth.md#sec-4"]}
    dropped:
      - {ref: "002-github-app-auth.md#sec-5", reason: "spent: superseded narrative"}
  - to: 005-prioritization-and-pickup.md
    title: Prioritization and pickup
    sources: [005-prioritization-and-pickup.md]
    sections:
      - {new: "0", heading: "Purpose & scope", from: ["005-prioritization-and-pickup.md#sec-0"]}
    dropped: []
"""

CONFIG = 'current_project = "worklode"\nproject_key = "WL"\n'

# fold.py imports currentspec.py and secindex.py at module level (for
# --check's live view and index-freshness proof), and both of those import
# secfmt.py -- every throwaway repo needs all four, even for --mapping-only
# tests, or the subprocess fails before argparse ever runs.
FOLD_SCRIPTS = ("fold.py", "currentspec.py", "secindex.py", "secfmt.py")


def write_repo(tmp, fold=FOLD, config=CONFIG):
    """A throwaway repo: scripts/fold.py (+ the scripts it imports),
    docs/specs2/fold.yaml, and (unless config is None) .worklode/config.toml."""
    repo = Path(tmp)
    scripts = repo / "scripts"
    scripts.mkdir(parents=True)
    for name in FOLD_SCRIPTS:
        shutil.copy2(ROOT / "scripts" / name, scripts / name)
    specs2 = repo / "docs" / "specs2"
    specs2.mkdir(parents=True)
    (specs2 / "fold.yaml").write_text(textwrap.dedent(fold))
    if config is not None:
        (repo / ".worklode").mkdir()
        (repo / ".worklode" / "config.toml").write_text(config)
    return repo


def run_fold(tmp, *args, fold=FOLD, config=CONFIG):
    """Run the real fold.py in an isolated, minimal repository."""
    repo = write_repo(tmp, fold, config)
    result = subprocess.run(
        [sys.executable, "scripts/fold.py", *args],
        cwd=repo, capture_output=True, text=True, check=False,
    )
    return repo, result


class MappingTest(unittest.TestCase):
    def load_mapping(self, repo):
        return yaml.safe_load((repo / "docs" / "specs2" / "mapping.yaml").read_text())

    def test_one_to_one_section_maps_to_new_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping")
            self.assertEqual(result.returncode, 0, result.stderr)
            mapping = self.load_mapping(repo)
            self.assertEqual(
                mapping["sections"]["002-github-app-auth.md#sec-1"],
                "001-identity-and-authentication.md#sec-1",
            )

    def test_from_list_of_three_maps_onto_same_new_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping")
            self.assertEqual(result.returncode, 0, result.stderr)
            mapping = self.load_mapping(repo)
            target = "001-identity-and-authentication.md#sec-2"
            for n in (2, 3, 4):
                self.assertEqual(
                    mapping["sections"][f"002-github-app-auth.md#sec-{n}"], target
                )

    def test_dropped_entries_are_separated_from_sections(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping")
            self.assertEqual(result.returncode, 0, result.stderr)
            mapping = self.load_mapping(repo)
            self.assertNotIn("002-github-app-auth.md#sec-5", mapping["sections"])
            self.assertIn(
                {"ref": "002-github-app-auth.md#sec-5", "reason": "spent: superseded narrative"},
                mapping["dropped"],
            )

    def test_from_and_to_id_render_as_project_spec_shorthand(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping")
            self.assertEqual(result.returncode, 0, result.stderr)
            mapping = self.load_mapping(repo)
            docs = {d["from"]: d for d in mapping["documents"]}
            merged = docs["002-github-app-auth.md"]
            self.assertEqual(merged["to"], "001-identity-and-authentication.md")
            self.assertEqual(merged["from_id"], "WL-SPEC-2")
            self.assertEqual(merged["to_id"], "WL-SPEC-1")
            self_fold = docs["005-prioritization-and-pickup.md"]
            self.assertEqual(self_fold["from_id"], "WL-SPEC-5")
            self.assertEqual(self_fold["to_id"], "WL-SPEC-5")

    def test_project_key_defaults_to_wl_when_config_absent(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping", config=None)
            self.assertEqual(result.returncode, 0, result.stderr)
            mapping = self.load_mapping(repo)
            docs = {d["from"]: d for d in mapping["documents"]}
            self.assertEqual(docs["005-prioritization-and-pickup.md"]["from_id"], "WL-SPEC-5")

    def test_project_key_is_read_from_config_not_hardcoded(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping", config='project_key = "ZZ"\n')
            self.assertEqual(result.returncode, 0, result.stderr)
            mapping = self.load_mapping(repo)
            docs = {d["from"]: d for d in mapping["documents"]}
            self.assertEqual(docs["005-prioritization-and-pickup.md"]["from_id"], "ZZ-SPEC-5")

    def test_emitting_twice_is_byte_identical(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold(tmp, "--mapping")
            self.assertEqual(result.returncode, 0, result.stderr)
            first = (repo / "docs" / "specs2" / "mapping.yaml").read_bytes()
            second_result = subprocess.run(
                [sys.executable, "scripts/fold.py", "--mapping"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertEqual(second_result.returncode, 0, second_result.stderr)
            second = (repo / "docs" / "specs2" / "mapping.yaml").read_bytes()
            self.assertEqual(first, second)


class MalformedFoldTest(unittest.TestCase):
    # Each case is a real parse-time contract of load_fold(); it would fail if
    # the corresponding validation were removed or its message stopped naming
    # the offending ref/key.
    def assert_rejected(self, fold, message):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold(tmp, "--mapping", fold=fold)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(message, result.stderr)

    def test_rejects_new_number_whose_parent_is_not_declared(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections:
                  - {new: "2.1", heading: "Orphaned", from: ["002-github-app-auth.md#sec-1"]}
            """
        self.assert_rejected(fold, "no declared parent '2'")

    def test_rejects_duplicate_new_within_a_document(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections:
                  - {new: "1", heading: "First", from: ["002-github-app-auth.md#sec-1"]}
                  - {new: "1", heading: "Duplicate", from: ["002-github-app-auth.md#sec-2"]}
            """
        self.assert_rejected(fold, "duplicate section number '1'")

    def test_rejects_from_ref_without_sec_fragment(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections:
                  - {new: "1", heading: "First", from: ["002-github-app-auth.md"]}
            """
        self.assert_rejected(fold, "'002-github-app-auth.md' has no #sec- fragment")

    def test_rejects_unknown_top_level_key(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            bogus: true
            documents: []
            """
        self.assert_rejected(fold, "fold.yaml: unknown key 'bogus'")

    def test_rejects_unknown_key_in_section_entry(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections:
                  - {new: "1", heading: "First", from: ["002-github-app-auth.md#sec-1"], extra: nope}
            """
        self.assert_rejected(fold, "001-identity-and-authentication.md: unknown key 'extra' in a section entry")

    def test_rejects_unknown_key_in_document_entry(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections: []
                extra: nope
            """
        self.assert_rejected(fold, "documents[0]: unknown key 'extra'")

    def test_rejects_document_missing_to(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections: []
            """
        self.assert_rejected(fold, "documents[0]: missing 'to'")

    def test_rejects_document_missing_title(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                sources: [002-github-app-auth.md]
                sections: []
            """
        self.assert_rejected(fold, "001-identity-and-authentication.md: missing 'title'")

    def test_rejects_dropped_entry_missing_ref(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections: []
                dropped:
                  - {reason: "spent: superseded narrative"}
            """
        self.assert_rejected(fold, "001-identity-and-authentication.md: dropped entry missing 'ref'")

    def test_rejects_dropped_entry_missing_reason(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections: []
                dropped:
                  - {ref: "002-github-app-auth.md#sec-5"}
            """
        self.assert_rejected(fold, "001-identity-and-authentication.md: dropped entry missing 'reason'")

    def test_rejects_unknown_key_in_dropped_entry(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections: []
                dropped:
                  - {ref: "002-github-app-auth.md#sec-5", reason: "spent", extra: nope}
            """
        self.assert_rejected(fold, "001-identity-and-authentication.md: unknown key 'extra' in a dropped entry")

    def test_rejects_allow_dropped_ids_entry_without_reason(self):
        fold = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - to: 001-identity-and-authentication.md
                title: Identity and authentication
                sources: [002-github-app-auth.md]
                sections:
                  - {new: "1", heading: "First", from: ["002-github-app-auth.md#sec-1"]}
                allow_dropped_ids:
                  old_column: ""
            """
        self.assert_rejected(
            fold, "001-identity-and-authentication.md: allow_dropped_ids entry 'old_column' has no reason"
        )


# --check fixtures: a live docs/specs/ mini-corpus (spec markdown + a real,
# byte-matching index.yaml so the "index is current" precondition holds) plus
# a docs/specs2/fold.yaml declaring where its anchors go. Two spec documents
# -- "alpha" (folded by every completeness fixture below) and "beta" (left
# undeclared by every fold.yaml here, used only by the --partial tests) --
# keep each test isolated to the one failure class it names.
ALPHA_SPEC = """\
---
status: accepted
issued: 2026-01-01
---
# Alpha

## 1. One {#sec-1}

One.

## 2. Two {#sec-2}

Two.

## 3. Three {#sec-3}

Three.
"""

BETA_SPEC = """\
---
status: accepted
issued: 2026-01-01
---
# Beta

## 1. One {#sec-1}

One.
"""

CHECK_FOLD_CLEAN = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped: []
"""

# sec-3 is never placed or dropped.
CHECK_FOLD_UNPLACED = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
    dropped: []
"""

# sec-1 is placed under both new:1 and new:2.
CHECK_FOLD_PLACED_TWICE = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two and one again", from: ["900-alpha.md#sec-2", "900-alpha.md#sec-1"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped: []
"""

# sec-99 does not exist in 900-alpha.md.
CHECK_FOLD_DANGLING = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three and a phantom", from: ["900-alpha.md#sec-3", "900-alpha.md#sec-99"]}
    dropped: []
"""

# Finding 2 repro (task-2 review): a two-section spec, folded by a document
# whose `sources:` names only 901-beta.md (never 900-alpha.md itself), while
# that document's `sections:` place an anchor straight from 900-alpha.md.
# --partial's scope must still cover 900-alpha.md's other live anchor from
# the *placement*, not only from `sources:`, or it silently exempts a whole
# undeclared source file.
ALPHA_TWO_SECTIONS_SPEC = """\
---
status: accepted
issued: 2026-01-01
---
# Alpha

## 1. One {#sec-1}

One.

## 2. Two {#sec-2}

Two.
"""

CHECK_FOLD_PARTIAL_UNDECLARED_SOURCE = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-beta-folded.md
    title: Beta folded
    sources: [901-beta.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
    dropped: []
"""


def write_check_repo(tmp, fold, specs, written=None):
    """A throwaway repo for --check: scripts/fold.py plus the currentspec.py
    (+secfmt.py) it imports for the live view and secindex.py (+secfmt.py) it
    imports to prove docs/specs/index.yaml is current; docs/specs2/fold.yaml;
    a docs/specs/ mini-corpus (`specs` is {filename: markdown}) with a real,
    byte-matching index.yaml built the same way secindex.py would; and,
    when given, `written` ({filename: markdown}) is the already-rewritten
    docs/specs2/ prose the anchor-drift half of --check compares fold.yaml
    against -- a filename fold.yaml declares but that is absent from
    `written` is unstarted work, not drift, and stays unwritten here too."""
    repo = Path(tmp)
    scripts = repo / "scripts"
    scripts.mkdir(parents=True)
    for name in FOLD_SCRIPTS:
        shutil.copy2(ROOT / "scripts" / name, scripts / name)
    specs2 = repo / "docs" / "specs2"
    specs2.mkdir(parents=True)
    (specs2 / "fold.yaml").write_text(textwrap.dedent(fold))
    for name, content in (written or {}).items():
        (specs2 / name).write_text(textwrap.dedent(content))
    specs_dir = repo / "docs" / "specs"
    specs_dir.mkdir(parents=True)
    for name, content in specs.items():
        (specs_dir / name).write_text(textwrap.dedent(content))
    (specs_dir / "index.yaml").write_text(secindex.render(specs_dir, repo))
    return repo


def run_fold_check(tmp, *args, fold=CHECK_FOLD_CLEAN, specs=None, written=None):
    """Run the real fold.py --check in an isolated, minimal repository."""
    repo = write_check_repo(
        tmp, fold, specs if specs is not None else {"900-alpha.md": ALPHA_SPEC}, written
    )
    result = subprocess.run(
        [sys.executable, "scripts/fold.py", "--check", *args],
        cwd=repo, capture_output=True, text=True, check=False,
    )
    return repo, result


class CompletenessCheckTest(unittest.TestCase):
    # Each case is a real contract of --check's completeness gate: it would
    # fail if the corresponding group stopped being computed, or its label
    # or the offending anchor stopped appearing in the report.
    def test_check_passes_when_every_live_anchor_is_placed_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(tmp, fold=CHECK_FOLD_CLEAN)
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_check_reports_unplaced_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(tmp, fold=CHECK_FOLD_UNPLACED)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("unplaced", result.stderr)
            self.assertIn("900-alpha.md#sec-3", result.stderr)

    def test_check_reports_placed_twice_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(tmp, fold=CHECK_FOLD_PLACED_TWICE)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("placed twice", result.stderr)
            self.assertIn("900-alpha.md#sec-1", result.stderr)

    def test_check_reports_dangling_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(tmp, fold=CHECK_FOLD_DANGLING)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("dangling", result.stderr)
            self.assertIn("900-alpha.md#sec-99", result.stderr)

    def test_check_fails_when_index_is_missing(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = write_check_repo(tmp, CHECK_FOLD_CLEAN, {"900-alpha.md": ALPHA_SPEC})
            (repo / "docs" / "specs" / "index.yaml").unlink()
            result = subprocess.run(
                [sys.executable, "scripts/fold.py", "--check"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("index.yaml", result.stderr)
            self.assertIn("missing", result.stderr)

    def test_check_fails_when_index_is_stale(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = write_check_repo(tmp, CHECK_FOLD_CLEAN, {"900-alpha.md": ALPHA_SPEC})
            index = repo / "docs" / "specs" / "index.yaml"
            index.write_text(index.read_text() + "# a hand edit secindex.py did not make\n")
            result = subprocess.run(
                [sys.executable, "scripts/fold.py", "--check"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("index.yaml", result.stderr)
            self.assertIn("stale", result.stderr)


class PartialCheckTest(unittest.TestCase):
    def test_partial_ignores_a_document_the_fold_never_declares(self):
        with tempfile.TemporaryDirectory() as tmp:
            # beta is live but no fold.yaml document names it as a source.
            _, result = run_fold_check(
                tmp, "--partial", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": ALPHA_SPEC, "901-beta.md": BETA_SPEC},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_without_partial_the_same_undeclared_document_is_unplaced(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": ALPHA_SPEC, "901-beta.md": BETA_SPEC},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("unplaced", result.stderr)
            self.assertIn("901-beta.md#sec-1", result.stderr)

    def test_partial_does_not_exempt_a_placed_anchor_whose_file_is_not_a_declared_source(self):
        # Finding 2 repro: only 901-beta.md is named in `sources:`, but the
        # document actually places an anchor from 900-alpha.md, which no
        # document's `sources:` lists. 900-alpha.md#sec-2 must still be
        # caught as unplaced -- --partial's scope may not silently drop an
        # entire file just because its name never appears in `sources:`.
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--partial", fold=CHECK_FOLD_PARTIAL_UNDECLARED_SOURCE,
                specs={"900-alpha.md": ALPHA_TWO_SECTIONS_SPEC},
            )
            self.assertNotEqual(result.returncode, 0, result.stderr)
            self.assertIn("unplaced", result.stderr)
            self.assertIn("900-alpha.md#sec-2", result.stderr)

    def test_partial_still_reports_dangling(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(tmp, "--partial", fold=CHECK_FOLD_DANGLING)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("dangling", result.stderr)
            self.assertIn("900-alpha.md#sec-99", result.stderr)

    def test_partial_still_reports_placed_twice(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(tmp, "--partial", fold=CHECK_FOLD_PLACED_TWICE)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("placed twice", result.stderr)
            self.assertIn("900-alpha.md#sec-1", result.stderr)


# Anchor-drift fixtures: CHECK_FOLD_CLEAN declares 950-alpha-folded.md with
# three sections (new: 1, 2, 3 -> #sec-1, #sec-2, #sec-3). These are written
# docs/specs2/950-alpha-folded.md bodies exercising each drift outcome; they
# are deliberately unrelated to 900-alpha.md/ALPHA_SPEC's own section bodies,
# since the drift check compares fold.yaml's declared anchors against the
# written file's actual anchors, never against the old corpus.
WRITTEN_950_CLEAN = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Rewritten one.

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# sec-3 is declared in fold.yaml but the rewrite dropped it.
WRITTEN_950_MISSING_SECTION = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Rewritten one.

## 2. Two {#sec-2}

Rewritten two.
"""

# sec-4 exists in the written file but fold.yaml never declared it.
WRITTEN_950_UNDECLARED_SECTION = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Rewritten one.

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.

## 4. Four {#sec-4}

An anchor fold.yaml never declared.
"""

# Same three anchors as WRITTEN_950_CLEAN, but every heading's title text was
# improved by the rewrite pass -- anchor presence must still read as clean.
WRITTEN_950_RETITLED = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. First thing {#sec-1}

Rewritten one.

## 2. Second thing {#sec-2}

Rewritten two.

## 3. Third thing {#sec-3}

Rewritten three.
"""


class AnchorDriftCheckTest(unittest.TestCase):
    # Each case is a real contract of --check's anchor-drift half: it would
    # fail if the corresponding direction stopped being compared, or its
    # label or the offending ref stopped appearing in the report.
    def test_check_reports_missing_anchor_dropped_by_the_rewrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, fold=CHECK_FOLD_CLEAN,
                written={"950-alpha-folded.md": WRITTEN_950_MISSING_SECTION},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("missing", result.stderr)
            self.assertIn("950-alpha-folded.md#sec-3", result.stderr)

    def test_check_reports_undeclared_anchor_added_by_the_rewrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, fold=CHECK_FOLD_CLEAN,
                written={"950-alpha-folded.md": WRITTEN_950_UNDECLARED_SECTION},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("undeclared", result.stderr)
            self.assertIn("950-alpha-folded.md#sec-4", result.stderr)

    def test_check_skips_a_document_fold_yaml_declares_but_nobody_wrote_yet(self):
        with tempfile.TemporaryDirectory() as tmp:
            # No `written=` at all -- 950-alpha-folded.md does not exist in
            # docs/specs2/. That is unstarted work, not drift, so the check
            # must pass even though none of fold.yaml's three declared
            # anchors are backed by any written file.
            _, result = run_fold_check(tmp, fold=CHECK_FOLD_CLEAN)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("missing", result.stderr)
            self.assertNotIn("undeclared", result.stderr)

    def test_check_passes_when_written_anchors_match_declared_exactly(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, fold=CHECK_FOLD_CLEAN,
                written={"950-alpha-folded.md": WRITTEN_950_CLEAN},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_retitled_heading_with_same_anchor_is_not_drift(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, fold=CHECK_FOLD_CLEAN,
                written={"950-alpha-folded.md": WRITTEN_950_RETITLED},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_partial_still_reports_missing_anchor(self):
        # The drift check is unaffected by --partial: it is already scoped to
        # exactly the documents fold.yaml declares, so --partial must not
        # suppress it.
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--partial", fold=CHECK_FOLD_CLEAN,
                written={"950-alpha-folded.md": WRITTEN_950_MISSING_SECTION},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("missing", result.stderr)
            self.assertIn("950-alpha-folded.md#sec-3", result.stderr)


# --check --ids fixtures. IDS_ALPHA_SPEC is 900-alpha.md's usual shape
# (folded by CHECK_FOLD_CLEAN into 950-alpha-folded.md, same as the
# anchor-drift fixtures above) with one inline backticked identifier added to
# sec-1, and nothing else -- kept deliberately free of a fenced code block so
# these fixtures test only the inline-span half; IDS_ALPHA_SPEC_WITH_BLOCK
# below (a different table name, so the two halves never overlap by
# substring accident) tests the fenced-block half in isolation.
IDS_ALPHA_SPEC = """\
---
status: accepted
issued: 2026-01-01
---
# Alpha

## 1. One {#sec-1}

The `task_edges` table tracks task ordering.

## 2. Two {#sec-2}

Two.

## 3. Three {#sec-3}

Three.
"""

# `task_edges` reworded and moved elsewhere in the sentence -- still present,
# so the ids check must pass.
WRITTEN_950_IDS_CLEAN = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Task ordering lives in the `task_edges` table, which records the from/to pair.

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# `task_edges` is gone -- summarised away, the realistic failure this guard
# exists to catch.
WRITTEN_950_IDS_DROPPED = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Rewritten one, summarised away.

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# `task_edges` survives, but under sec-2 instead of sec-1 -- a legitimate
# merge moving a term between sections. The check is scoped to the whole
# document, so this must pass.
WRITTEN_950_IDS_MOVED = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Rewritten one, no longer names the table here.

## 2. Two {#sec-2}

Ordering is tracked by `task_edges`.

## 3. Three {#sec-3}

Rewritten three.
"""

CHECK_FOLD_ALLOW_DROPPED_IDS = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped: []
    allow_dropped_ids:
      task_edges: "spent: renamed to task_links, see spec 900"
"""

# A spec whose sec-1 holds only a fenced SQL block (no separate inline
# backtick mention of the same table name), to test the fenced-block half of
# --ids in isolation from the inline-span half above.
IDS_ALPHA_SPEC_WITH_BLOCK = """\
---
status: accepted
issued: 2026-01-01
---
# Alpha

## 1. One {#sec-1}

Schema for the widget graph:

```sql
CREATE TABLE widget_edges (
    from_widget text NOT NULL,
    to_widget   text NOT NULL
);
```

## 2. Two {#sec-2}

Two.

## 3. Three {#sec-3}

Three.
"""

# The whole block is gone, paraphrased instead -- the failure the brief
# calls out by name ("a rewrite dropping a whole block").
WRITTEN_950_BLOCK_DROPPED = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

The widget graph schema is summarised here instead of shown verbatim.

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# The block survives byte-for-byte; only the surrounding prose changed.
WRITTEN_950_BLOCK_KEPT = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

The widget graph schema, unchanged:

```sql
CREATE TABLE widget_edges (
    from_widget text NOT NULL,
    to_widget   text NOT NULL
);
```

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# Same tokens, 2-space indent instead of 4 -- a harmless reformat that must
# not trip the guard (post-review fix: whole-block exact-substring survival
# broke on exactly this).
WRITTEN_950_BLOCK_REINDENTED = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

The widget graph schema, reindented:

```sql
CREATE TABLE widget_edges (
  from_widget text NOT NULL,
  to_widget   text NOT NULL
);
```

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# Same tokens, fence language tag changed from sql to postgres.
WRITTEN_950_BLOCK_RELABELED = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

The widget graph schema, relabelled:

```postgres
CREATE TABLE widget_edges (
    from_widget text NOT NULL,
    to_widget   text NOT NULL
);
```

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

# One column renamed (to_widget -> dest_widget) -- every other token in the
# block (CREATE, TABLE, widget_edges, from_widget, text, NOT, NULL) is still
# present verbatim, so exactly one identifier should be reported dropped.
WRITTEN_950_BLOCK_RENAMED_COLUMN = """\
---
status: draft
---
# Spec 950 — Alpha folded

## 1. One {#sec-1}

Schema for the widget graph:

```sql
CREATE TABLE widget_edges (
    from_widget text NOT NULL,
    dest_widget text NOT NULL
);
```

## 2. Two {#sec-2}

Rewritten two.

## 3. Three {#sec-3}

Rewritten three.
"""

CHECK_FOLD_ALLOW_DROPPED_BLOCK_TOKEN = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped: []
    allow_dropped_ids:
      to_widget: "spent: column renamed to dest_widget, see spec 900"
"""


class IdentifierPreservationCheckTest(unittest.TestCase):
    # Each case is a real contract of --check --ids: it would fail if the
    # identifier collection, the exact-text comparison, the escape hatch, or
    # the --ids/--check coupling stopped working as documented.
    def test_ids_reports_dropped_identifier_with_source_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC},
                written={"950-alpha-folded.md": WRITTEN_950_IDS_DROPPED},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("dropped ids", result.stderr)
            self.assertIn("`task_edges`", result.stderr)
            self.assertIn("900-alpha.md#sec-1", result.stderr)

    def test_ids_passes_when_reworded_around_the_identifier(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC},
                written={"950-alpha-folded.md": WRITTEN_950_IDS_CLEAN},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_ids_passes_when_identifier_moves_to_another_section(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC},
                written={"950-alpha-folded.md": WRITTEN_950_IDS_MOVED},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_ids_exempts_an_identifier_listed_in_allow_dropped_ids(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_ALLOW_DROPPED_IDS,
                specs={"900-alpha.md": IDS_ALPHA_SPEC},
                written={"950-alpha-folded.md": WRITTEN_950_IDS_DROPPED},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_plain_check_does_not_run_the_ids_group(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC},
                written={"950-alpha-folded.md": WRITTEN_950_IDS_DROPPED},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("dropped ids", result.stderr)

    def test_ids_skips_a_document_nobody_wrote_yet(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("dropped ids", result.stderr)

    def test_ids_reports_many_findings_for_a_wholly_dropped_block(self):
        # Token tracking (post-review): a dropped block must surface as many
        # findings, one per surviving-nowhere token, not a single opaque
        # multi-line dump -- unmissable in a report a human skims.
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC_WITH_BLOCK},
                written={"950-alpha-folded.md": WRITTEN_950_BLOCK_DROPPED},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("dropped ids", result.stderr)
            self.assertIn("`widget_edges`", result.stderr)
            self.assertIn("`from_widget`", result.stderr)
            self.assertIn("`to_widget`", result.stderr)
            self.assertIn("900-alpha.md#sec-1", result.stderr)
            findings = result.stderr.count("(from 900-alpha.md#sec-1)")
            self.assertGreaterEqual(findings, 5, result.stderr)

    def test_ids_passes_when_the_fenced_block_survives_verbatim(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC_WITH_BLOCK},
                written={"950-alpha-folded.md": WRITTEN_950_BLOCK_KEPT},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_ids_passes_when_a_block_is_reindented(self):
        # Post-review fix: whole-block exact-substring survival broke on
        # exactly this (an unchanged schema, different indentation).
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC_WITH_BLOCK},
                written={"950-alpha-folded.md": WRITTEN_950_BLOCK_REINDENTED},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_ids_passes_when_a_block_fence_is_relabelled(self):
        # sql -> postgres: the language tag is never part of a fenced
        # block's captured content (excluded by FENCE_RE before tokenizing
        # or before whole-block capture), so this already held pre-fix too
        # -- kept as a permanent regression guard, not a red/green case.
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC_WITH_BLOCK},
                written={"950-alpha-folded.md": WRITTEN_950_BLOCK_RELABELED},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_ids_reports_exactly_one_finding_for_a_renamed_column(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_CLEAN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC_WITH_BLOCK},
                written={"950-alpha-folded.md": WRITTEN_950_BLOCK_RENAMED_COLUMN},
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("dropped ids", result.stderr)
            self.assertIn("`to_widget`", result.stderr)
            findings = result.stderr.count("(from 900-alpha.md#sec-1)")
            self.assertEqual(findings, 1, result.stderr)

    def test_ids_renamed_column_finding_is_exemptable_with_one_allow_dropped_ids_entry(self):
        with tempfile.TemporaryDirectory() as tmp:
            _, result = run_fold_check(
                tmp, "--ids", fold=CHECK_FOLD_ALLOW_DROPPED_BLOCK_TOKEN,
                specs={"900-alpha.md": IDS_ALPHA_SPEC_WITH_BLOCK},
                written={"950-alpha-folded.md": WRITTEN_950_BLOCK_RENAMED_COLUMN},
            )
            self.assertEqual(result.returncode, 0, result.stderr)


# --scaffold fixtures. Three source documents exercise both directions of the
# `requires:` internal-drop rule (900 and 902 each require the other's fold
# sibling) plus one kept external target (903), and a nested `new:` number
# (900's sec-3 lands at "1.1") exercises heading-depth derivation. 950 merges
# two sources (900 sec-2 + 901 sec-1) into one section, to prove `from:`-order
# concatenation with a provenance marker per segment.
ALPHA900 = """\
---
status: accepted
issued: 2026-01-01
requires:
  - 903-external.md
---
# Alpha

## 1. One {#sec-1}

Alpha one.

## 2. Two {#sec-2}

Alpha two.

## 3. Three {#sec-3}

Alpha three.
"""

BETA901 = """\
---
status: accepted
issued: 2026-01-01
requires:
  - 902-gamma.md
---
# Beta

## 1. One {#sec-1}

Beta one.
"""

GAMMA902 = """\
---
status: accepted
issued: 2026-01-01
requires:
  - 900-alpha.md
---
# Gamma

## 1. One {#sec-1}

Gamma one.
"""

EXTERNAL903 = """\
---
status: accepted
issued: 2026-01-01
---
# External

## 1. One {#sec-1}

External one.
"""

SCAFFOLD_SPECS = {
    "900-alpha.md": ALPHA900,
    "901-beta.md": BETA901,
    "902-gamma.md": GAMMA902,
    "903-external.md": EXTERNAL903,
}

SCAFFOLD_FOLD = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-beta-folded.md
    title: Alpha and beta folded
    sources: [900-alpha.md, 901-beta.md]
    sections:
      - {new: "0", heading: "Purpose", from: ["900-alpha.md#sec-1"]}
      - {new: "1", heading: "Merged", from: ["900-alpha.md#sec-2", "901-beta.md#sec-1"]}
      - {new: "1.1", heading: "Nested", from: ["900-alpha.md#sec-3"]}
    dropped: []
  - to: 951-gamma-folded.md
    title: Gamma folded
    sources: [902-gamma.md]
    sections:
      - {new: "0", heading: "Purpose", from: ["902-gamma.md#sec-1"]}
    dropped: []
"""


def write_scaffold_repo(tmp, fold, specs, extra_scripts=()):
    """A throwaway repo for --scaffold: scripts/fold.py (+ imports, +
    `extra_scripts`), docs/specs2/fold.yaml, and a docs/specs/ mini-corpus
    (`specs` is {filename: markdown}). No docs/specs/index.yaml -- --scaffold
    slices source sections directly and never goes through the live-corpus
    view --check uses."""
    repo = Path(tmp)
    scripts = repo / "scripts"
    scripts.mkdir(parents=True)
    for name in (*FOLD_SCRIPTS, *extra_scripts):
        shutil.copy2(ROOT / "scripts" / name, scripts / name)
    specs2 = repo / "docs" / "specs2"
    specs2.mkdir(parents=True)
    (specs2 / "fold.yaml").write_text(textwrap.dedent(fold))
    specs_dir = repo / "docs" / "specs"
    specs_dir.mkdir(parents=True)
    for name, content in specs.items():
        (specs_dir / name).write_text(textwrap.dedent(content))
    return repo


def run_fold_scaffold(tmp, *args, fold=SCAFFOLD_FOLD, specs=None, extra_scripts=()):
    """Run the real fold.py --scaffold in an isolated, minimal repository."""
    repo = write_scaffold_repo(
        tmp, fold, specs if specs is not None else SCAFFOLD_SPECS, extra_scripts
    )
    result = subprocess.run(
        [sys.executable, "scripts/fold.py", "--scaffold", *args],
        cwd=repo, capture_output=True, text=True, check=False,
    )
    return repo, result


class ScaffoldTest(unittest.TestCase):
    def scaffold(self, tmp, **kw):
        repo, result = run_fold_scaffold(tmp, **kw)
        self.assertEqual(result.returncode, 0, result.stderr)
        return repo

    def read(self, repo, name):
        return (repo / "docs" / "specs2" / name).read_text()

    def test_frontmatter_has_status_draft_and_computed_requires(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = self.scaffold(tmp)
            merged = self.read(repo, "950-alpha-beta-folded.md")
            fm, _ = merged.split("\n---\n", 1)
            data = yaml.safe_load(fm[4:] + "\n")
            self.assertEqual(data["status"], "draft")
            # 900's requires (903-external.md) is kept and repointed at
            # docs/specs/; 901's requires (902-gamma.md) is dropped because
            # 902-gamma.md is itself a `sources:` entry in this same fold.
            self.assertEqual(data["requires"], ["docs/specs/903-external.md"])

            gamma = self.read(repo, "951-gamma-folded.md")
            fm, _ = gamma.split("\n---\n", 1)
            data = yaml.safe_load(fm[4:] + "\n")
            self.assertEqual(data["status"], "draft")
            # 902's own requires (900-alpha.md) is dropped the same way, in
            # the other direction -- the union is empty, so the key is absent
            # rather than emitted as `requires: []`.
            self.assertNotIn("requires", data)

    def test_headings_come_from_fold_yaml_numbered_per_new(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = self.scaffold(tmp)
            merged = self.read(repo, "950-alpha-beta-folded.md")
            self.assertIn("## 0. Purpose {#sec-0}", merged)
            self.assertIn("## 1. Merged {#sec-1}", merged)
            # a dotted `new:` number is a subsection: one level deeper (###)
            # and no trailing dot on the number, per house style.
            self.assertIn("### 1.1 Nested {#sec-1.1}", merged)

    def test_section_body_is_verbatim_in_from_order_with_provenance_markers(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = self.scaffold(tmp)
            merged = self.read(repo, "950-alpha-beta-folded.md")
            expected = (
                "<!-- from: 900-alpha.md#sec-2 -->\n"
                "Alpha two.\n\n"
                "<!-- from: 901-beta.md#sec-1 -->\n"
                "Beta one."
            )
            self.assertIn(expected, merged)
            nested = self.read(repo, "950-alpha-beta-folded.md")
            self.assertIn("<!-- from: 900-alpha.md#sec-3 -->\nAlpha three.", nested)

    def test_only_scaffolds_a_single_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold_scaffold(tmp, "--only", "950-alpha-beta-folded.md")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue((repo / "docs" / "specs2" / "950-alpha-beta-folded.md").is_file())
            self.assertFalse((repo / "docs" / "specs2" / "951-gamma-folded.md").exists())

    def test_second_scaffold_over_existing_file_exits_nonzero_without_writing(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = self.scaffold(tmp)
            before = {
                name: (repo / "docs" / "specs2" / name).stat().st_mtime_ns
                for name in ("950-alpha-beta-folded.md", "951-gamma-folded.md")
            }
            before_text = {
                name: (repo / "docs" / "specs2" / name).read_text() for name in before
            }
            result = subprocess.run(
                [sys.executable, "scripts/fold.py", "--scaffold"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            for name in before:
                self.assertEqual(
                    (repo / "docs" / "specs2" / name).read_text(), before_text[name]
                )

    def test_scaffold_refuses_whole_invocation_when_one_target_already_exists(self):
        # Simulate a partial prior run: only 951 exists. A fresh --scaffold
        # over both documents must refuse entirely, not write 950 and skip 951.
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold_scaffold(tmp, "--only", "951-gamma-folded.md")
            self.assertEqual(result.returncode, 0, result.stderr)
            result = subprocess.run(
                [sys.executable, "scripts/fold.py", "--scaffold"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((repo / "docs" / "specs2" / "950-alpha-beta-folded.md").exists())


# H1 title regression: `to:` filenames spanning a leading-zero case (005), a
# three-digit case with no leading zero (023 still needs its own leading
# zero preserved), and a >=100 case where zero-padding is a non-issue -- the
# fix must keep the filename's number string as-is, not int()-round-trip it.
H1_FOLD = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 005-padded.md
    title: Padded title
    sources: [900-alpha.md]
    sections:
      - {new: "0", heading: "Purpose", from: ["900-alpha.md#sec-1"]}
    dropped: []
  - to: 023-three-digit.md
    title: Three digit title
    sources: [900-alpha.md]
    sections:
      - {new: "0", heading: "Purpose", from: ["900-alpha.md#sec-1"]}
    dropped: []
  - to: 100-boundary.md
    title: Boundary title
    sources: [900-alpha.md]
    sections:
      - {new: "0", heading: "Purpose", from: ["900-alpha.md#sec-1"]}
    dropped: []
"""


class TitleNumberTest(unittest.TestCase):
    def test_h1_keeps_the_to_filenames_number_string_verbatim(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold_scaffold(tmp, fold=H1_FOLD, specs=SCAFFOLD_SPECS)
            self.assertEqual(result.returncode, 0, result.stderr)

            padded = (repo / "docs" / "specs2" / "005-padded.md").read_text()
            self.assertIn("# Spec 005 — Padded title\n", padded)
            self.assertNotIn("# Spec 5 —", padded)  # the int()-collapsed form

            three_digit = (repo / "docs" / "specs2" / "023-three-digit.md").read_text()
            self.assertIn("# Spec 023 — Three digit title\n", three_digit)
            self.assertNotIn("# Spec 23 —", three_digit)

            boundary = (repo / "docs" / "specs2" / "100-boundary.md").read_text()
            self.assertIn("# Spec 100 — Boundary title\n", boundary)


class ScaffoldHouseChecksTest(unittest.TestCase):
    def test_scaffolded_output_passes_secfmt_and_secmeta(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold_scaffold(tmp, extra_scripts=("secmeta.py",))
            self.assertEqual(result.returncode, 0, result.stderr)

            # docs/specs2 is not in secfmt.py's / secmeta.py's DEFAULT_ROOTS,
            # so both must be pointed at it explicitly.
            fmt = subprocess.run(
                [sys.executable, "scripts/secfmt.py", "-d", "docs/specs2"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertEqual(fmt.returncode, 0, fmt.stdout + fmt.stderr)
            self.assertEqual(fmt.stdout, "")

            meta = subprocess.run(
                [sys.executable, "scripts/secmeta.py", "docs/specs2"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertEqual(meta.returncode, 0, meta.stdout + meta.stderr)


# Preamble fixtures (task-7 review, Important 1): prose between a source's H1
# and its first `##` heading sits inside no {#sec-N} section, so anchor-based
# slicing drops it and an anchor-based check cannot see the loss. `#preamble`
# is the pseudo-anchor that makes it addressable by the same `from:`/`dropped:`
# machinery every real anchor already uses.
ALPHA_PREAMBLE_SPEC = """\
---
status: accepted
issued: 2026-01-01
---
# Alpha

> **Prefix renamed by 014 §1.** Read `ls:governs` below as `wl:governs`.

Alpha's scope, stated before the first numbered section.

## 1. One {#sec-1}

One.

## 2. Two {#sec-2}

Two.

## 3. Three {#sec-3}

Three.
"""

# Every anchor placed, the preamble accounted for by nothing.
CHECK_FOLD_PREAMBLE_UNPLACED = CHECK_FOLD_CLEAN

CHECK_FOLD_PREAMBLE_PLACED = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#preamble", "900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped: []
"""

CHECK_FOLD_PREAMBLE_DROPPED = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped:
      - {ref: "900-alpha.md#preamble", reason: "spent: prefix-rename instruction, applied in the rewrite"}
"""

# 900-alpha.md here is the plain ALPHA_SPEC, which has no preamble at all.
CHECK_FOLD_PREAMBLE_DANGLING = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#preamble", "900-alpha.md#sec-1"]}
      - {new: "2", heading: "Two", from: ["900-alpha.md#sec-2"]}
      - {new: "3", heading: "Three", from: ["900-alpha.md#sec-3"]}
    dropped: []
"""


class PreambleCheckTest(unittest.TestCase):
    """--check must account for source prose that falls outside every anchor.

    Placing it, dropping it and leaving it unaccounted-for all go through the
    same three groups every real anchor uses, so no seventh report group and
    no second concept: `<file>#preamble` is simply live corpus material like
    any other."""

    def check(self, tmp, *args, **kw):
        return run_fold_check(tmp, *args, **kw)

    def test_check_reports_preamble_no_placement_accounts_for(self):
        with tempfile.TemporaryDirectory() as tmp:
            _repo, result = self.check(
                tmp, fold=CHECK_FOLD_PREAMBLE_UNPLACED,
                specs={"900-alpha.md": ALPHA_PREAMBLE_SPEC},
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn("unplaced", result.stderr)
            self.assertIn("900-alpha.md#preamble", result.stderr)

    def test_check_passes_when_preamble_is_placed_in_a_from_list(self):
        with tempfile.TemporaryDirectory() as tmp:
            _repo, result = self.check(
                tmp, fold=CHECK_FOLD_PREAMBLE_PLACED,
                specs={"900-alpha.md": ALPHA_PREAMBLE_SPEC},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_check_passes_when_preamble_is_recorded_as_dropped(self):
        with tempfile.TemporaryDirectory() as tmp:
            _repo, result = self.check(
                tmp, fold=CHECK_FOLD_PREAMBLE_DROPPED,
                specs={"900-alpha.md": ALPHA_PREAMBLE_SPEC},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_document_without_preamble_is_never_reported(self):
        with tempfile.TemporaryDirectory() as tmp:
            _repo, result = self.check(tmp, fold=CHECK_FOLD_CLEAN)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("#preamble", result.stderr)

    def test_preamble_ref_against_a_document_without_one_is_dangling(self):
        with tempfile.TemporaryDirectory() as tmp:
            _repo, result = self.check(tmp, fold=CHECK_FOLD_PREAMBLE_DANGLING)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn("dangling", result.stderr)
            self.assertIn("900-alpha.md#preamble", result.stderr)

    def test_partial_still_scopes_preamble_to_declared_sources(self):
        # 901-beta.md carries preamble but no fold.yaml entry names it, so
        # --partial must stay silent about it while 900-alpha.md's is placed.
        beta = ALPHA_PREAMBLE_SPEC.replace("# Alpha", "# Beta")
        with tempfile.TemporaryDirectory() as tmp:
            _repo, result = self.check(
                tmp, "--partial", fold=CHECK_FOLD_PREAMBLE_PLACED,
                specs={"900-alpha.md": ALPHA_PREAMBLE_SPEC, "901-beta.md": beta},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("901-beta.md#preamble", result.stderr)


class PreambleSliceTest(unittest.TestCase):
    """The real case the review named: 018's doc-wide amendment note lives
    entirely in preamble, so an anchor-only fold drops it silently."""

    def test_slices_018s_doc_wide_amendment_note_from_the_real_corpus(self):
        text = fold.slice_section(ROOT / "docs" / "specs" / "018-task-hierarchy.md", "preamble")
        self.assertIn("Amended by spec 025 (doc-wide)", text)
        self.assertIn("`kind = 'epic'` is dropped, not renamed", text)
        self.assertNotIn("# Spec 018", text)  # the H1 is the scaffold's to write

    def test_005_has_no_preamble(self):
        # The document task 7 already folded. If this ever grows preamble the
        # committed fold silently lost prose.
        path = ROOT / "docs" / "specs" / "005-prioritization-and-pickup.md"
        self.assertEqual(fold.slice_section(path, "preamble"), "")

    def test_scaffold_pastes_a_placed_preamble_verbatim(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold_scaffold(
                tmp, fold=CHECK_FOLD_PREAMBLE_PLACED,
                specs={"900-alpha.md": ALPHA_PREAMBLE_SPEC},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            text = (repo / "docs" / "specs2" / "950-alpha-folded.md").read_text()
            self.assertIn("<!-- from: 900-alpha.md#preamble -->", text)
            self.assertIn("Read `ls:governs` below as `wl:governs`.", text)
            self.assertIn("Alpha's scope, stated before the first numbered section.", text)


# Provenance fixtures (task-7 review, Important 2): a whole-document `amendedBy`
# leaves no inline note anywhere in the source's section bodies, so a rewriter
# reading only the scaffold cannot know the amendment exists. --scaffold already
# parses each source's frontmatter for `requires:`; the amendment and
# supersession keys come along for free.
AMENDED_SPEC = """\
---
status: accepted
issued: 2026-01-01
amends:
  "#sec-1":
    - 902-gamma.md#sec-1
amendedBy:
  ".":
    - 902-gamma.md
  "#sec-2":
    - 903-external.md
replaces:
  ".":
    - 899-old.md
isReplacedBy:
  ".":
    - 904-newer.md
---
# Alpha

## 1. One {#sec-1}

One.
"""

PROVENANCE_FOLD = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 950-alpha-folded.md
    title: Alpha folded
    sources: [900-alpha.md]
    sections:
      - {new: "1", heading: "One", from: ["900-alpha.md#sec-1"]}
    dropped: []
"""


class ScaffoldProvenanceTest(unittest.TestCase):
    def scaffold_text(self, tmp, fold_yaml, specs):
        repo, result = run_fold_scaffold(tmp, fold=fold_yaml, specs=specs)
        self.assertEqual(result.returncode, 0, result.stderr)
        return (repo / "docs" / "specs2" / "950-alpha-folded.md").read_text()

    def test_all_four_amendment_and_supersession_keys_are_emitted(self):
        with tempfile.TemporaryDirectory() as tmp:
            text = self.scaffold_text(
                tmp, PROVENANCE_FOLD, {"900-alpha.md": AMENDED_SPEC}
            )
            self.assertIn("provenance", text)
            self.assertIn("amendedBy", text)
            self.assertIn("902-gamma.md", text)
            self.assertIn("amends", text)
            self.assertIn("replaces", text)
            self.assertIn("899-old.md", text)
            self.assertIn("isReplacedBy", text)
            self.assertIn("904-newer.md", text)
            # Per-section subjects survive alongside the doc-wide ones.
            self.assertIn("#sec-2", text)
            self.assertIn("903-external.md", text)

    def test_no_provenance_block_when_the_source_declares_none(self):
        with tempfile.TemporaryDirectory() as tmp:
            text = self.scaffold_text(
                tmp, PROVENANCE_FOLD, {"900-alpha.md": ALPHA_SPEC}
            )
            self.assertNotIn("provenance", text)

    def test_doc_wide_amendedby_of_004_and_011_reaches_their_scaffold_header(self):
        # The review's named cases: both are amended doc-wide by 018 with zero
        # inline marker, exactly as 005 was, and 004 is a part-3 fold.
        specs_dir = ROOT / "docs" / "specs"
        for source, to in (("004-execution-backbone.md", "004-execution-backbone.md"),
                           ("011-delivery-lifecycle.md", "011-delivery-lifecycle.md")):
            doc = fold.Document(to=to, title="T", sources=[source], sections=[])
            f = fold.Fold(version=1, corpus={"from": "docs/specs", "to": "docs/specs2"},
                          documents=[doc])
            text = fold.scaffold_text(doc, f, specs_dir)
            header = text.split("\n## ")[0]
            self.assertIn("amendedBy", header, source)
            self.assertIn('"."', header, source)
            self.assertIn("018-task-hierarchy.md", header, source)

    def test_provenance_block_does_not_break_the_house_checks(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, result = run_fold_scaffold(
                tmp, fold=PROVENANCE_FOLD, specs={"900-alpha.md": AMENDED_SPEC},
                extra_scripts=("secmeta.py",),
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            fmt = subprocess.run(
                [sys.executable, "scripts/secfmt.py", "-d", "docs/specs2"],
                cwd=repo, capture_output=True, text=True, check=False,
            )
            self.assertEqual(fmt.returncode, 0, fmt.stdout + fmt.stderr)
            self.assertEqual(fmt.stdout, "")


if __name__ == "__main__":
    unittest.main()
