#!/usr/bin/env python3
"""Hermetic tests for scripts/fold.py -- fold.yaml parsing, derived
mapping.yaml, and the --check completeness gate. Follows
scripts/secmeta_test.py's isolated-repo pattern: copy the script(s) under
test into a throwaway repo and run fold.py as a subprocess, so REPO
(script-relative) resolves inside the temp dir and nothing touches
docs/specs/ or docs/specs2/ in this repo.

--check also imports currentspec.py (for the --with-drafts live view) and
secindex.py (to prove docs/specs/index.yaml is current), so its fixtures
copy those scripts too and build a small live docs/specs/ corpus alongside
docs/specs2/fold.yaml. secindex.render() is used here only to author a
byte-matching index.yaml fixture -- not to test secindex.py itself, which is
covered by its own suite.
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


def write_check_repo(tmp, fold, specs):
    """A throwaway repo for --check: scripts/fold.py plus the currentspec.py
    (+secfmt.py) it imports for the live view and secindex.py (+secfmt.py) it
    imports to prove docs/specs/index.yaml is current; docs/specs2/fold.yaml;
    and a docs/specs/ mini-corpus (`specs` is {filename: markdown}) with a
    real, byte-matching index.yaml built the same way secindex.py would."""
    repo = Path(tmp)
    scripts = repo / "scripts"
    scripts.mkdir(parents=True)
    for name in FOLD_SCRIPTS:
        shutil.copy2(ROOT / "scripts" / name, scripts / name)
    specs2 = repo / "docs" / "specs2"
    specs2.mkdir(parents=True)
    (specs2 / "fold.yaml").write_text(textwrap.dedent(fold))
    specs_dir = repo / "docs" / "specs"
    specs_dir.mkdir(parents=True)
    for name, content in specs.items():
        (specs_dir / name).write_text(textwrap.dedent(content))
    (specs_dir / "index.yaml").write_text(secindex.render(specs_dir, repo))
    return repo


def run_fold_check(tmp, *args, fold=CHECK_FOLD_CLEAN, specs=None):
    """Run the real fold.py --check in an isolated, minimal repository."""
    repo = write_check_repo(tmp, fold, specs if specs is not None else {"900-alpha.md": ALPHA_SPEC})
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


if __name__ == "__main__":
    unittest.main()
