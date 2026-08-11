#!/usr/bin/env python3
"""Hermetic tests for scripts/refmap.py -- the reference rewriter that
consumes docs/specs2/mapping.yaml (never fold.yaml) at cutover.

Unlike fold_test.py's isolated-repo pattern, refmap.py needs no sibling
imports and takes --root explicitly, so these tests run the real script by
its real path against a synthetic --root: a throwaway tree with its own
docs/specs2/mapping.yaml and whatever files a case needs, never docs/specs/
or docs/specs2/ in this repo.

Each fixture mapping.yaml is deliberately minimal -- just enough documents/
sections/dropped entries for the one case it backs -- following
secmeta_test.py's per-case-fixture style rather than fold_test.py's one
shared corpus, because refmap has no completeness invariant to hold a shared
fixture to (that is fold.py --check's job, not this tool's).
"""

import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REFMAP = ROOT / "scripts" / "refmap.py"


def write_tree(tmp, mapping, files):
    """A throwaway --root: docs/specs2/mapping.yaml (mapping, a YAML string)
    plus `files` ({relpath: content})."""
    root = Path(tmp)
    specs2 = root / "docs" / "specs2"
    specs2.mkdir(parents=True)
    (specs2 / "mapping.yaml").write_text(textwrap.dedent(mapping))
    for rel, content in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(content))
    return root


def run_refmap(tmp, mapping, files, *args):
    root = write_tree(tmp, mapping, files)
    result = subprocess.run(
        [sys.executable, str(REFMAP), "--root", str(root), *args],
        capture_output=True, text=True, check=False,
    )
    return root, result


# A three-document mapping exercising a straight 1:1 fold (002 -> 001), a
# many:1 merge (002 and 023 both fold into 001), and a second target (014 ->
# 006) so shorthand/prose tests have two distinct new ids to tell apart.
MAIN_MAPPING = """\
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - {from: 002-github-app-auth.md, to: 001-identity-and-authentication.md,
     from_id: WL-SPEC-2, to_id: WL-SPEC-1}
  - {from: 023-keycloak-primary-auth.md, to: 001-identity-and-authentication.md,
     from_id: WL-SPEC-23, to_id: WL-SPEC-1}
  - {from: 014-design-documents-as-graph-objects.md, to: 006-knowledge-graph.md,
     from_id: WL-SPEC-14, to_id: WL-SPEC-6}
sections:
  "002-github-app-auth.md#sec-3.5": "001-identity-and-authentication.md#sec-6.2"
  "014-design-documents-as-graph-objects.md#sec-5": "006-knowledge-graph.md#sec-9"
dropped:
  - {ref: "002-github-app-auth.md#sec-2", reason: "spent: current-state narrative, retired by 023"}
"""


class RoundTripTest(unittest.TestCase):
    """Each of the four reference spellings, old -> new, in both -w's written
    bytes and --dry-run's report."""

    def test_repo_relative_path_with_fragment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "// see docs/specs/002-github-app-auth.md#sec-3.5 for the rule\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cli/scope.go").read_text()
            self.assertEqual(
                got,
                "// see docs/specs2/001-identity-and-authentication.md#sec-6.2 for the rule\n",
            )

    def test_repo_relative_path_without_fragment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"docs/plans/some-plan.md":
                    "---\nstatus: draft\ncovers: docs/specs/002-github-app-auth.md\n---\n# Plan\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "docs/plans/some-plan.md").read_text()
            self.assertIn("covers: docs/specs2/001-identity-and-authentication.md\n", got)

    def test_bare_filename_within_corpus_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"docs/specs/023-keycloak-primary-auth.md":
                    "---\nstatus: accepted\nissued: 2026-01-01\nrequires:\n"
                    "  - 002-github-app-auth.md\n---\n# Spec\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "docs/specs/023-keycloak-primary-auth.md").read_text()
            self.assertIn("  - 001-identity-and-authentication.md\n", got)

    def test_bare_filename_outside_corpus_directory_is_not_a_reference(self):
        # The same bare filename, unprefixed, in a file that does not live
        # under docs/specs/ -- must be left alone, not treated as a reference.
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"docs/notes.md": "See 002-github-app-auth.md for background.\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "docs/notes.md").read_text()
            self.assertEqual(got, "See 002-github-app-auth.md for background.\n")

    def test_wl_spec_shorthand_with_fragment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cmd/show_test.go":
                    '\t{"WL-SPEC-14#sec-5", targetDoc, ""},\n'},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cmd/show_test.go").read_text()
            self.assertIn('"WL-SPEC-6#sec-9"', got)

    def test_wl_spec_shorthand_without_fragment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cmd/show_test.go": '\t{"WL-SPEC-2", targetDoc, ""},\n'},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cmd/show_test.go").read_text()
            self.assertIn('"WL-SPEC-1"', got)

    def test_prose_form_spec_prefixed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"ns/shapes.ttl": "# Extracted from spec 014 §5, see the schema.\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "ns/shapes.ttl").read_text()
            self.assertEqual(got, "# Extracted from spec 006 §9, see the schema.\n")

    def test_prose_form_bare_number(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cmd/task.go":
                    "// resolveBody returns the body (014 §5, see the note).\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cmd/task.go").read_text()
            self.assertIn("(006 §9, see the note).", got)


class WordBoundaryHazardTest(unittest.TestCase):
    """The three hazards named in the task brief: a short shorthand id must
    not match as a prefix of a longer one, a short filename must not match
    inside a longer one, and a short anchor must not match as a prefix of a
    longer one. Each fixture maps both the short and the long form to
    distinct, recognisable targets, so any cross-contamination shows up as a
    wrong-but-plausible id rather than a crash."""

    def test_wl_spec_1_does_not_match_inside_wl_spec_14(self):
        mapping = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - {from: 001-old-one.md, to: 900-new-one.md, from_id: WL-SPEC-1, to_id: WL-SPEC-900}
              - {from: 014-old-fourteen.md, to: 901-new-fourteen.md, from_id: WL-SPEC-14, to_id: WL-SPEC-901}
            sections: {}
            dropped: []
            """
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, mapping,
                {"internal/cmd/show.go": "// see WL-SPEC-1 and WL-SPEC-14 for the rule\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cmd/show.go").read_text()
            self.assertEqual(got, "// see WL-SPEC-900 and WL-SPEC-901 for the rule\n")

    def test_filename_does_not_match_inside_a_longer_filename(self):
        mapping = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - {from: 004-execution-backbone.md, to: 900-execution-and-delivery.md,
                 from_id: WL-SPEC-4, to_id: WL-SPEC-900}
            sections: {}
            dropped: []
            """
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, mapping,
                {"internal/cli/scope.go":
                    "// see docs/specs/004-execution-backbone.md and the unrelated\n"
                    "// docs/specs/1004-execution-backbone.md, which is not that file\n"},
                "--dry-run",
            )
            # 1004-execution-backbone.md is not in the mapping at all, so the
            # run reports it unmapped rather than silently leaving it -- but
            # the point of this test is what it must *not* produce: a
            # corrupted hybrid like "1900-execution-and-delivery.md" (the
            # short filename's replacement with the decoy's leading digit
            # still glued on).
            self.assertNotIn("1900-execution-and-delivery.md", result.stdout)
            self.assertIn(
                "docs/specs/004-execution-backbone.md -> "
                "docs/specs2/900-execution-and-delivery.md",
                result.stdout,
            )
            self.assertIn("1004-execution-backbone.md", result.stdout)  # named as unmapped
            self.assertNotEqual(result.returncode, 0)  # the decoy is a hard failure

    def test_anchor_does_not_match_as_prefix_of_a_longer_anchor(self):
        mapping = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - {from: 014-old.md, to: 900-new.md, from_id: WL-SPEC-14, to_id: WL-SPEC-900}
            sections:
              "014-old.md#sec-1": "900-new.md#sec-1"
              "014-old.md#sec-1.3": "900-new.md#sec-1.9"
            dropped: []
            """
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, mapping,
                {"docs/specs/014-old.md":
                    "See 014-old.md#sec-1 and 014-old.md#sec-1.3 for the two rules.\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "docs/specs/014-old.md").read_text()
            self.assertEqual(
                got, "See 900-new.md#sec-1 and 900-new.md#sec-1.9 for the two rules.\n"
            )
            # The corrupted-by-prefix-match result would reuse #sec-1's
            # target for the .3 anchor too.
            self.assertNotIn("900-new.md#sec-1.3", got)


class UnmappedReferenceTest(unittest.TestCase):
    def test_dropped_section_reference_is_unmapped_and_named_with_file_and_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "package cli\n\n// see docs/specs/002-github-app-auth.md#sec-2 (dropped)\n"},
                "--dry-run",
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("internal/cli/scope.go:3:", result.stdout)
            self.assertIn("002-github-app-auth.md#sec-2", result.stdout)

    def test_reference_with_no_sections_entry_at_all_is_unmapped(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "// docs/specs/002-github-app-auth.md#sec-99 was never placed or dropped\n"},
                "--dry-run",
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("002-github-app-auth.md#sec-99", result.stdout)

    def test_unmapped_reference_blocks_writing_the_whole_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "// docs/specs/002-github-app-auth.md#sec-3.5 is fine\n"
                    "// docs/specs/002-github-app-auth.md#sec-99 is not\n"},
                "-w",
            )
            self.assertNotEqual(result.returncode, 0)
            got = (root / "internal/cli/scope.go").read_text()
            self.assertIn("#sec-3.5", got)  # untouched -- the whole file was not rewritten
            self.assertNotIn("006.2", got)


class DryRunAndWriteTest(unittest.TestCase):
    def test_dry_run_writes_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-3.5\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"internal/cli/scope.go": original}, "--dry-run",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cli/scope.go").read_text(), original)
            self.assertIn(
                "docs/specs/002-github-app-auth.md#sec-3.5 -> "
                "docs/specs2/001-identity-and-authentication.md#sec-6.2",
                result.stdout,
            )

    def test_default_with_no_flags_is_dry_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-3.5\n"
            root, result = run_refmap(tmp, MAIN_MAPPING, {"internal/cli/scope.go": original})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cli/scope.go").read_text(), original)

    def test_write_produces_exactly_the_expected_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "package cli\n\n// see docs/specs/002-github-app-auth.md#sec-3.5\n"
                    "// unrelated line, untouched\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / "internal/cli/scope.go").read_bytes(),
                b"package cli\n\n"
                b"// see docs/specs2/001-identity-and-authentication.md#sec-6.2\n"
                b"// unrelated line, untouched\n",
            )

    def test_write_does_not_touch_a_file_with_no_matches(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "package cli\n\n// nothing to see here\n"
            root, result = run_refmap(tmp, MAIN_MAPPING, {"internal/cli/scope.go": original}, "-w")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cli/scope.go").read_text(), original)


class ExclusionTest(unittest.TestCase):
    def test_worktrees_directory_is_never_touched(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-3.5\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {".worktrees/WL-9-foo/internal/cli/scope.go": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / ".worktrees/WL-9-foo/internal/cli/scope.go").read_text(), original
            )
            self.assertNotIn(".worktrees", result.stdout)

    def test_claude_worktrees_directory_is_never_touched(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-3.5\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {".claude/worktrees/WL-9-foo/scope.go": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / ".claude/worktrees/WL-9-foo/scope.go").read_text(), original
            )

    def test_git_directory_is_never_touched(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-3.5\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {".git/COMMIT_EDITMSG": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / ".git/COMMIT_EDITMSG").read_text(), original)

    def test_an_ordinary_sibling_named_worktreesx_is_still_scanned(self):
        # Regression guard for an over-broad exclusion: only the exact
        # directory names are pruned, not anything containing them.
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"worktrees-notes/scope.go":
                    "// see docs/specs/002-github-app-auth.md#sec-3.5\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "worktrees-notes/scope.go").read_text()
            self.assertIn("docs/specs2/001-identity-and-authentication.md#sec-6.2", got)


class ShorthandScopeTest(unittest.TestCase):
    def test_wl_adr_shorthand_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            # No leading whitespace: write_tree runs content through
            # textwrap.dedent, which strips a lone leading tab from a
            # single-line string entirely, so a tab here would not
            # round-trip even on an untouched file.
            original = '{"WL-ADR-7", targetDoc, ""},\n'
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"internal/cmd/show_test.go": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cmd/show_test.go").read_text(), original)

    def test_cross_project_shorthand_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "amends: [rdf-registry:ADR-0006]\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/specs/006-knowledge-graph.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "docs/specs/006-knowledge-graph.md").read_text(), original)


if __name__ == "__main__":
    unittest.main()
