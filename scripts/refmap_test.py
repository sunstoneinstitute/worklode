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


def git(root, *args):
    subprocess.run(["git", *args], cwd=root, capture_output=True, text=True, check=True)


def write_tree(tmp, mapping, files, commit=True):
    """A throwaway --root: docs/specs2/mapping.yaml (mapping, a YAML string)
    plus `files` ({relpath: content}). `commit` (default True) also makes
    `root` a git repo with everything committed -- -w's idempotency gate
    (C3: shorthand/prose substitutions are not self-inverse, since old and
    new numbering both start at 1) requires a clean git worktree, so every
    -w test needs a real, committed repo unless it is specifically testing
    that gate's "not a repo at all" branch (commit=False)."""
    root = Path(tmp)
    specs2 = root / "docs" / "specs2"
    specs2.mkdir(parents=True)
    (specs2 / "mapping.yaml").write_text(textwrap.dedent(mapping))
    for rel, content in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(content))
    if commit:
        git(root, "init", "-q")
        git(root, "config", "user.email", "refmap-test@example.com")
        git(root, "config", "user.name", "refmap-test")
        git(root, "config", "commit.gpgsign", "false")
        git(root, "add", "-A")
        git(root, "commit", "-q", "-m", "fixture")
    return root


def commit_all(root, message="post-write"):
    """What a human does between two -w runs: stage and commit everything,
    including whatever -w just wrote (the rewritten files and, once it
    exists, the durable .refmap-applied marker)."""
    git(root, "add", "-A")
    git(root, "commit", "-q", "-m", message)


def invoke_refmap(root, *args):
    """Run the real refmap.py against an already-built `root`, without
    rebuilding or re-committing it -- what a second -w run in a row needs."""
    return subprocess.run(
        [sys.executable, str(REFMAP), "--root", str(root), *args],
        capture_output=True, text=True, check=False,
    )


def run_refmap(tmp, mapping, files, *args, commit=True):
    root = write_tree(tmp, mapping, files, commit=commit)
    return root, invoke_refmap(root, *args)


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
                {"internal/cmd/show.go":
                    '\t{"WL-SPEC-14#sec-5", targetDoc, ""},\n'},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cmd/show.go").read_text()
            self.assertIn('"WL-SPEC-6#sec-9"', got)

    def test_wl_spec_shorthand_without_fragment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cmd/show.go": '\t{"WL-SPEC-2", targetDoc, ""},\n'},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cmd/show.go").read_text()
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
            # A name that cannot collide with a real file `git init`/`commit`
            # writes into .git/ (write_tree now git-inits every fixture, for
            # the -w idempotency gate -- COMMIT_EDITMSG used to work when
            # .git/ was just a fixture directory, but a real git commit
            # actually writes that file, which made this test pass for the
            # wrong reason).
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {".git/refmap-fixture-marker.txt": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / ".git/refmap-fixture-marker.txt").read_text(), original
            )

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


class IgnoredPathTest(unittest.TestCase):
    """Files that carry synthetic spec identifiers as *data* must not be read
    as prose. A test fixture corpus (`001-alpha.md`, `WL-SPEC-900`), a
    table-driven Go test case, or a quoted review diff resolves to nothing --
    and one unmapped id is enough to make -w refuse the whole run, so
    scanning them would block cutover outright. Reordering cutover cannot
    help: the Go fixtures are never deleted."""

    UNMAPPED = "// see docs/specs/900-not-in-the-mapping.md#sec-1\n"
    MAPPED = "// see docs/specs/002-github-app-auth.md#sec-3.5\n"

    def assert_skipped(self, rel):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {rel: self.UNMAPPED + self.MAPPED}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            # Unchanged, not merely tolerated: a skipped file is never read.
            self.assertEqual((root / rel).read_text(), self.UNMAPPED + self.MAPPED)
            self.assertNotIn(rel, result.stdout)

    def test_python_test_fixtures_are_not_scanned(self):
        self.assert_skipped("scripts/fold_test.py")
        self.assert_skipped("scripts/refmap_test.py")

    def test_go_test_fixtures_are_not_scanned(self):
        self.assert_skipped("internal/cmd/show_test.go")
        self.assert_skipped("e2e/docsync_test.go")

    def test_agent_session_artifacts_are_not_scanned(self):
        self.assert_skipped(".superpowers/sdd/some-plan/review-a..b.diff")

    def test_an_ordinary_go_source_is_still_scanned(self):
        # The exclusion is by filename suffix, not by living under internal/:
        # production code beside a skipped test file must still be rewritten.
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cmd/show.go": self.MAPPED,
                 "internal/cmd/show_test.go": self.MAPPED},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("docs/specs2/001-identity-and-authentication.md#sec-6.2",
                          (root / "internal/cmd/show.go").read_text())
            self.assertEqual((root / "internal/cmd/show_test.go").read_text(), self.MAPPED)

    def test_ignore_glob_flag_skips_an_extra_path(self):
        # Cutover's escape hatch for a file the built-in list cannot know
        # about -- a plan that quotes a fixture corpus verbatim, say.
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/plans/quotes-a-fixture.md": self.UNMAPPED},
                "-w", "--ignore-glob", "docs/plans/quotes-a-fixture.md",
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual((root / "docs/plans/quotes-a-fixture.md").read_text(), self.UNMAPPED)

    def test_without_the_flag_the_same_path_still_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/plans/quotes-a-fixture.md": self.UNMAPPED}, "-w",
            )
            self.assertNotEqual(result.returncode, 0)


class SpecZeroTest(unittest.TestCase):
    """`WL-SPEC-0` is the reserved "no governing spec" sentinel (026 §4.2a):
    "There is no spec 0 and there will not be one, so it is the only
    reference that resolves to nothing without being a defect." It appears in
    docs/authoring-design-docs.md, spec 025 and spec 026 -- all shipping
    prose that also carries real references -- so it has to be recognised and
    left alone rather than excluded by the file."""

    def test_spec_zero_shorthand_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "so `WL-SPEC-0` is recognised but reported: write `NO-SPEC`.\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/authoring-design-docs.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual((root / "docs/authoring-design-docs.md").read_text(), original)

    def test_spec_zero_shorthand_with_a_fragment_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "The `WL-SPEC-0#sec-1` form is never valid.\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/specs/025-documents-in-the-backbone.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(
                (root / "docs/specs/025-documents-in-the-backbone.md").read_text(), original)

    def test_spec_zero_prose_form_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "The authority sentence in 000 §1 updates accordingly.\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/specs/025-documents-in-the-backbone.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(
                (root / "docs/specs/025-documents-in-the-backbone.md").read_text(), original)

    def test_a_real_reference_in_the_same_file_is_still_rewritten(self):
        # The point of handling the sentinel per-reference rather than by
        # excluding the file: docs/authoring-design-docs.md carries one
        # WL-SPEC-0 and about fifteen real references.
        with tempfile.TemporaryDirectory() as tmp:
            original = ("write `NO-SPEC`, never `WL-SPEC-0`.\n"
                        "covers: docs/specs/002-github-app-auth.md\n")
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/authoring-design-docs.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            got = (root / "docs/authoring-design-docs.md").read_text()
            self.assertIn("`WL-SPEC-0`", got)
            self.assertIn("covers: docs/specs2/001-identity-and-authentication.md\n", got)


class ShorthandScopeTest(unittest.TestCase):
    def test_wl_adr_shorthand_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            # No leading whitespace: write_tree runs content through
            # textwrap.dedent, which strips a lone leading tab from a
            # single-line string entirely, so a tab here would not
            # round-trip even on an untouched file.
            original = '{"WL-ADR-7", targetDoc, ""},\n'
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"internal/cmd/show.go": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cmd/show.go").read_text(), original)

    def test_cross_project_shorthand_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "amends: [rdf-registry:ADR-0006]\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/specs/006-knowledge-graph.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "docs/specs/006-knowledge-graph.md").read_text(), original)

    def test_cross_project_shorthand_with_a_section_sign_is_left_alone(self):
        # C1 regression: extends the test above one character further -- the
        # review noted the un-extended version stops one character short of
        # the bug (a "§" after a cross-project id must still not trip the
        # *prose* branch).
        with tempfile.TemporaryDirectory() as tmp:
            original = "amends: [rdf-registry:ADR-0006 §3]\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"docs/specs/006-knowledge-graph.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "docs/specs/006-knowledge-graph.md").read_text(), original)


# C1: the prose branch's leading lookbehind excluded \w but not "-" or ".",
# so a digit run immediately after either -- an ADR number, a version
# string, a date -- was captured as a spec "number" and silently rewritten
# using the target spec's mapping. Real sites: docs/specs/006-knowledge-
# graph.md lines 94, 583, 754, 790 and docs/plans/2026-07-30-data-platform-
# kg-requirements.md:48 (all "ADR-NNNN §N"), used verbatim below.
class ProseFormBoundaryCorruptionTest(unittest.TestCase):
    # A mapping where old spec 6 (whose id the corrupted "0006" collapses to
    # via int()) and old spec 1/3/11 are all real, mapped documents -- so a
    # silent corruption would produce a *plausible*, not obviously-broken,
    # wrong id, exactly like the review's proof (WL-SPEC-6 -> WL-SPEC-11).
    OVERLAP_MAPPING = """\
        version: 1
        corpus: {from: docs/specs, to: docs/specs2}
        documents:
          - {from: 001-old-one.md, to: 900-new-one.md, from_id: WL-SPEC-1, to_id: WL-SPEC-3}
          - {from: 003-old-three.md, to: 901-new-three.md, from_id: WL-SPEC-3, to_id: WL-SPEC-9}
          - {from: 006-knowledge-graph.md, to: 902-new-six.md, from_id: WL-SPEC-6, to_id: WL-SPEC-11}
          - {from: 011-old-eleven.md, to: 903-new-eleven.md, from_id: WL-SPEC-11, to_id: WL-SPEC-20}
        sections: {}
        dropped: []
        """

    def test_adr_number_before_section_sign_is_not_a_spec_reference(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = (
                "Branch-free, version-free term & instance IRIs (ADR-0006 §3). "
                "This is the **host/namespace\n"
            )
            root, result = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"docs/specs/006-knowledge-graph.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "docs/specs/006-knowledge-graph.md").read_text(), original)

    def test_adr_number_real_repo_lines(self):
        # docs/specs/006-knowledge-graph.md:94,583,754,790, verbatim.
        original = (
            "so per ADR-0006 §1 it sits directly under `rdf/`, not under `rdf/domain/`.\n"
            "  add `ls` to the `/rdf/` DCAT/VoID index (ADR-0006 §5).\n"
            "   Deployment / Environment is documented and branch-free (ADR-0006 §3); "
            "spec 009 can host it.\n"
        )
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"docs/specs/006-knowledge-graph.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "docs/specs/006-knowledge-graph.md").read_text(), original)

    def test_adr_number_in_plan_real_repo_line(self):
        # docs/plans/2026-07-30-data-platform-kg-requirements.md:48, verbatim.
        original = (
            "| 5 | Writable fixed branch | data-platform | **Done.** "
            "`PUT /branches/main/graphs?graph=<iri>` "
            "(`crates/graph-server/src/app.rs:53-59`); ADR-0003 §2: "
            '"every project writes to the one fixed writable branch `main`". |\n'
        )
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.OVERLAP_MAPPING,
                {"docs/plans/2026-07-30-data-platform-kg-requirements.md": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / "docs/plans/2026-07-30-data-platform-kg-requirements.md").read_text(),
                original,
            )

    def test_version_string_is_not_a_spec_reference(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "Requires v1.3 §3 of the client.\n"
            root, result = run_refmap(tmp, self.OVERLAP_MAPPING, {"README.md": original}, "-w")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "README.md").read_text(), original)

    def test_date_is_not_a_spec_reference(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "Filed on 2026-08-11 §3 of the ledger.\n"
            root, result = run_refmap(tmp, self.OVERLAP_MAPPING, {"README.md": original}, "-w")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "README.md").read_text(), original)

    def test_zero_padded_number_longer_than_three_digits_is_rejected_even_after_spec_prefix(self):
        # The lookbehind fix alone does not stop this one -- "spec " is a
        # legitimate leading boundary -- so an explicit digit-length cap is
        # the second half of the C1 fix.
        with tempfile.TemporaryDirectory() as tmp:
            original = "See spec 0006 §3 for background.\n"
            root, result = run_refmap(tmp, self.OVERLAP_MAPPING, {"README.md": original}, "-w")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "README.md").read_text(), original)

    def test_ordinary_three_digit_prose_reference_still_rewrites(self):
        # The fix must not overcorrect into rejecting legitimate references.
        # (Prose output uses the new *filename's* own leading number, per
        # fold.py's H1-title convention -- not to_id -- so the fixture's `to`
        # must itself start with "006" for "spec 006 §9" to be the right
        # expectation.)
        with tempfile.TemporaryDirectory() as tmp:
            mapping = """\
                version: 1
                corpus: {from: docs/specs, to: docs/specs2}
                documents:
                  - {from: 014-old.md, to: 006-new.md, from_id: WL-SPEC-14, to_id: WL-SPEC-6}
                sections:
                  "014-old.md#sec-5": "006-new.md#sec-9"
                dropped: []
                """
            root, result = run_refmap(tmp, mapping, {"ns/shapes.ttl": "# spec 014 §5\n"}, "-w")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "ns/shapes.ttl").read_text(), "# spec 006 §9\n")


# C2: the path form's trailing lookahead excluded "." outright, so a path
# reference ending a sentence ("....md.") never matched at all -- no
# substitution and no unmapped entry, the "too narrow, silently stale"
# failure the brief warns about. The fragment form had the mirror bug: the
# anchor's own character class included ".", so "#sec-3." swallowed the
# period into the anchor and failed loudly (a false unmapped) instead of
# resolving. Real sites (verbatim): internal/api/cliauth.go:6, deploy/base/
# migrations/0004_agent_sessions.up.sql:2, 0007_skills.up.sql:3, docs/plans/
# 2026-07-27-org-wide-skills.md:56, docs/plans/2026-07-20-provider-neutral-
# cli-login.md:119.
class PathFormSentenceEndTest(unittest.TestCase):
    MAPPING = """\
        version: 1
        corpus: {from: docs/specs, to: docs/specs2}
        documents:
          - {from: 031-provider-neutral-cli-login.md, to: 901-new.md,
             from_id: WL-SPEC-31, to_id: WL-SPEC-901}
          - {from: 012-agent-sessions.md, to: 902-new.md,
             from_id: WL-SPEC-12, to_id: WL-SPEC-902}
          - {from: 016-org-wide-skills.md, to: 903-new.md,
             from_id: WL-SPEC-16, to_id: WL-SPEC-903}
        sections: {}
        dropped: []
        """

    def test_cliauth_go_real_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.MAPPING,
                {"internal/api/cliauth.go":
                    "// docs/specs/031-provider-neutral-cli-login.md.\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / "internal/api/cliauth.go").read_text(), "// docs/specs2/901-new.md.\n"
            )

    def test_migration_comment_real_lines(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.MAPPING,
                {"deploy/base/migrations/0004_agent_sessions.up.sql":
                    "-- docs/specs/012-agent-sessions.md.\n",
                 "deploy/base/migrations/0007_skills.up.sql":
                    "-- See docs/specs/016-org-wide-skills.md.\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / "deploy/base/migrations/0004_agent_sessions.up.sql").read_text(),
                "-- docs/specs2/902-new.md.\n",
            )
            self.assertEqual(
                (root / "deploy/base/migrations/0007_skills.up.sql").read_text(),
                "-- See docs/specs2/903-new.md.\n",
            )

    def test_plan_real_lines(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.MAPPING,
                {"docs/plans/2026-07-27-org-wide-skills.md":
                    "-- See docs/specs/016-org-wide-skills.md.\n",
                 "docs/plans/2026-07-20-provider-neutral-cli-login.md":
                    "// docs/specs/031-provider-neutral-cli-login.md.\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (root / "docs/plans/2026-07-27-org-wide-skills.md").read_text(),
                "-- See docs/specs2/903-new.md.\n",
            )
            self.assertEqual(
                (root / "docs/plans/2026-07-20-provider-neutral-cli-login.md").read_text(),
                "// docs/specs2/901-new.md.\n",
            )

    def test_path_followed_by_an_extension_is_still_rejected(self):
        # Must not regress: ".md.bak" names a different file, never a match.
        with tempfile.TemporaryDirectory() as tmp:
            original = "// docs/specs/031-provider-neutral-cli-login.md.bak\n"
            root, result = run_refmap(
                tmp, self.MAPPING, {"internal/api/cliauth.go": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/api/cliauth.go").read_text(), original)

    def test_path_followed_by_a_hyphenated_continuation_is_still_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// docs/specs/031-provider-neutral-cli-login.md-ish\n"
            root, result = run_refmap(
                tmp, self.MAPPING, {"internal/api/cliauth.go": original}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/api/cliauth.go").read_text(), original)

    def test_anchor_ending_a_sentence_does_not_swallow_the_period(self):
        mapping = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - {from: 014-old.md, to: 900-new.md, from_id: WL-SPEC-14, to_id: WL-SPEC-900}
            sections:
              "014-old.md#sec-3": "900-new.md#sec-9"
            dropped: []
            """
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, mapping, {"docs/specs/014-old.md": "See 014-old.md#sec-3.\n"}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "docs/specs/014-old.md").read_text(), "See 900-new.md#sec-9.\n")


# C3: old and new spec numbering both start at 1, so a shorthand/prose
# substitution's *output* can coincidentally be a valid *input* for another
# entry (WL-SPEC-4 -> WL-SPEC-6, and WL-SPEC-6 is itself a real from_id
# mapping to WL-SPEC-11) -- unlike the path form, which is self-blocking
# because its own output carries the new corpus_to prefix. A second -w
# before the first is committed must refuse, not rewrite again.
class IdempotencyTest(unittest.TestCase):
    OVERLAP_MAPPING = """\
        version: 1
        corpus: {from: docs/specs, to: docs/specs2}
        documents:
          - {from: 004-old-four.md, to: 900-new-four.md, from_id: WL-SPEC-4, to_id: WL-SPEC-6}
          - {from: 006-old-six.md, to: 901-new-six.md, from_id: WL-SPEC-6, to_id: WL-SPEC-11}
        sections: {}
        dropped: []
        """

    def test_first_write_on_a_clean_worktree_succeeds(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": "// see WL-SPEC-4\n"}, "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cmd/show.go").read_text(), "// see WL-SPEC-6\n")

    def test_second_write_before_committing_refuses_and_does_not_double_rewrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, first = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": "// see WL-SPEC-4\n"}, "-w",
            )
            self.assertEqual(first.returncode, 0, first.stderr)
            after_first = (root / "internal/cmd/show.go").read_text()
            self.assertEqual(after_first, "// see WL-SPEC-6\n")

            second = invoke_refmap(root, "-w")
            self.assertNotEqual(second.returncode, 0)
            after_second = (root / "internal/cmd/show.go").read_text()
            self.assertEqual(after_second, after_first)  # not rewritten a second time
            self.assertNotIn("WL-SPEC-11", after_second)

    def test_write_refuses_when_root_is_not_a_git_worktree(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see WL-SPEC-4\n"
            root, result = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": original}, "-w", commit=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((root / "internal/cmd/show.go").read_text(), original)

    def test_dry_run_is_unaffected_by_a_dirty_worktree(self):
        # --dry-run never writes, so it needs no clean-tree guard at all.
        with tempfile.TemporaryDirectory() as tmp:
            root, first = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": "// see WL-SPEC-4\n"}, "-w",
            )
            self.assertEqual(first.returncode, 0, first.stderr)
            dry = invoke_refmap(root, "--dry-run")
            self.assertEqual(dry.returncode, 0, dry.stderr)


# C3 round 2: the git-dirty check only ever caught a second -w run *before*
# a commit -- committing the first run's output (files + marker) makes the
# tree clean again, and round 1's gate had nothing left to say. These are
# the full sequences the round-2 review demanded: -w, commit, -w again, and
# the same sequence under --corpus-to docs/specs, which is the mode where
# the path form's own self-blocking property (its output carries corpus_to,
# which the input pattern requires) stops holding, because corpus_to *is*
# corpus_from there.
class DurableIdempotencyMarkerTest(unittest.TestCase):
    OVERLAP_MAPPING = IdempotencyTest.OVERLAP_MAPPING

    def test_second_write_after_committing_the_first_still_refuses(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, first = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": "// see WL-SPEC-4\n"}, "-w",
            )
            self.assertEqual(first.returncode, 0, first.stderr)
            after_first = (root / "internal/cmd/show.go").read_text()
            self.assertEqual(after_first, "// see WL-SPEC-6\n")
            self.assertTrue((root / "docs/specs2/.refmap-applied").is_file())

            commit_all(root)  # the tree is clean again -- round 1's gate alone would pass
            second = invoke_refmap(root, "-w")
            self.assertNotEqual(second.returncode, 0)
            after_second = (root / "internal/cmd/show.go").read_text()
            self.assertEqual(after_second, after_first)
            self.assertNotIn("WL-SPEC-11", after_second)

    def test_second_write_after_committing_the_first_still_refuses_under_corpus_to_override(self):
        # Real repro shape from the review: a path reference whose *target*
        # filename is itself a real `from` elsewhere in the mapping, rewritten
        # under --corpus-to equal to corpus_from -- the output is
        # indistinguishable from a fresh old-corpus reference on a second pass.
        mapping = """\
            version: 1
            corpus: {from: docs/specs, to: docs/specs2}
            documents:
              - {from: 025-design-object-model.md, to: 006-knowledge-graph.md,
                 from_id: WL-SPEC-25, to_id: WL-SPEC-6}
              - {from: 006-knowledge-graph.md, to: 011-kg.md,
                 from_id: WL-SPEC-6, to_id: WL-SPEC-11}
            sections: {}
            dropped: []
            """
        with tempfile.TemporaryDirectory() as tmp:
            root, first = run_refmap(
                tmp, mapping,
                {"README.md": "See docs/specs/025-design-object-model.md.\n"},
                "-w", "--corpus-to", "docs/specs",
            )
            self.assertEqual(first.returncode, 0, first.stderr)
            after_first = (root / "README.md").read_text()
            self.assertEqual(after_first, "See docs/specs/006-knowledge-graph.md.\n")

            commit_all(root)
            second = invoke_refmap(root, "-w", "--corpus-to", "docs/specs")
            self.assertNotEqual(second.returncode, 0)
            after_second = (root / "README.md").read_text()
            self.assertEqual(after_second, after_first)
            self.assertNotIn("011-kg.md", after_second)

    def test_force_bypasses_the_marker_and_rewrites_again(self):
        # The explicit escape hatch for "I amended mapping.yaml and really do
        # need to re-run" -- deliberately reaches the WL-SPEC-11 collision
        # this whole mechanism exists to block by default, to prove --force
        # actually removes the block rather than something else.
        with tempfile.TemporaryDirectory() as tmp:
            root, first = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": "// see WL-SPEC-4\n"}, "-w",
            )
            self.assertEqual(first.returncode, 0, first.stderr)
            commit_all(root)

            second = invoke_refmap(root, "-w", "--force")
            self.assertEqual(second.returncode, 0, second.stderr)
            self.assertEqual(
                (root / "internal/cmd/show.go").read_text(), "// see WL-SPEC-11\n"
            )

    def test_marker_is_not_written_when_dry_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, self.OVERLAP_MAPPING, {"internal/cmd/show.go": "// see WL-SPEC-4\n"},
                "--dry-run",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertFalse((root / "docs/specs2/.refmap-applied").exists())


class CorpusToOverrideTest(unittest.TestCase):
    def test_corpus_to_flag_overrides_mapping_yaml_for_output_paths_only(self):
        # Part 5 runs `refmap.py -w --corpus-to docs/specs` *before* `git mv`
        # (see the module docstring) -- output paths must use the override,
        # while input recognition still keys off mapping.yaml's own
        # corpus.from (unaffected by this flag).
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "// see docs/specs/002-github-app-auth.md#sec-3.5 for the rule\n"},
                "-w", "--corpus-to", "docs/specs",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cli/scope.go").read_text()
            self.assertEqual(
                got,
                "// see docs/specs/001-identity-and-authentication.md#sec-6.2 for the rule\n",
            )

    def test_without_the_flag_mapping_yamls_own_corpus_to_is_used(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = run_refmap(
                tmp, MAIN_MAPPING,
                {"internal/cli/scope.go":
                    "// see docs/specs/002-github-app-auth.md#sec-3.5 for the rule\n"},
                "-w",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            got = (root / "internal/cli/scope.go").read_text()
            self.assertIn("docs/specs2/001-identity-and-authentication.md#sec-6.2", got)


class AllowDroppedTest(unittest.TestCase):
    def test_allow_dropped_passes_through_a_recorded_drop_unchanged(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-2 (dropped)\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"internal/cli/scope.go": original}, "-w", "--allow-dropped",
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "internal/cli/scope.go").read_text(), original)

    def test_allow_dropped_still_fails_a_never_mapped_reference(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// docs/specs/002-github-app-auth.md#sec-99 was never placed or dropped\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"internal/cli/scope.go": original}, "-w", "--allow-dropped",
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((root / "internal/cli/scope.go").read_text(), original)

    def test_without_the_flag_a_dropped_reference_still_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = "// see docs/specs/002-github-app-auth.md#sec-2 (dropped)\n"
            root, result = run_refmap(
                tmp, MAIN_MAPPING, {"internal/cli/scope.go": original}, "--dry-run",
            )
            self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
