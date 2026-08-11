#!/usr/bin/env python3
"""Hermetic tests for scripts/fold.py -- fold.yaml parsing and derived
mapping.yaml. Follows scripts/secmeta_test.py's isolated-repo pattern: copy
the script under test into a throwaway repo and run it as a subprocess, so
REPO (script-relative) resolves inside the temp dir and nothing touches
docs/specs2/ in this repo."""

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


def write_repo(tmp, fold=FOLD, config=CONFIG):
    """A throwaway repo: scripts/fold.py, docs/specs2/fold.yaml, and (unless
    config is None) .worklode/config.toml."""
    repo = Path(tmp)
    scripts = repo / "scripts"
    scripts.mkdir(parents=True)
    shutil.copy2(ROOT / "scripts" / "fold.py", scripts / "fold.py")
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


if __name__ == "__main__":
    unittest.main()
