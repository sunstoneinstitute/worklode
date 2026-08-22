#!/usr/bin/env python3
"""Tests for secfrozen.py, run against throwaway fixture git repos."""
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "secfrozen.py"

SPEC = """\
---
status: {status}
---
# Spec 001 — Fixture

## 1. First {{#sec-1}}

Body one.

## 2. Second {{#sec-2}}

### 2.1 Nested {{#sec-2.1}}

Body two.
"""


class Repo:
    """A throwaway git repo holding a docs/specs + docs/plans corpus."""

    def __init__(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        (self.root / "docs/specs").mkdir(parents=True)
        (self.root / "docs/plans").mkdir(parents=True)
        self.git("init", "-q")
        self.git("config", "user.email", "t@t")
        self.git("config", "user.name", "t")

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True,
                       capture_output=True)

    def write(self, rel, text):
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def commit(self, msg="c"):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", msg, "--no-verify")

    def run(self, env=None):
        """Run secfrozen.py with cwd at the fixture root."""
        return subprocess.run(
            [sys.executable, str(SCRIPT)], cwd=self.root,
            capture_output=True, text=True, env=env,
        )

    def close(self):
        self._tmp.cleanup()


class RepoCase(unittest.TestCase):
    """Base for the cases that build fixture repos: `self.repo()` ties the
    temporary directory's lifetime to the test, so no case leaks one to the
    interpreter's exit-time finalizer (a ResourceWarning per test)."""

    def repo(self):
        r = Repo()
        self.addCleanup(r.close)
        return r


class TestPermanence(RepoCase):
    def test_delete_published_anchor_fails(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        text = (r.root / "docs/specs/001-fixture.md").read_text()
        lines = text.splitlines(keepends=True)
        # Drop the "## 2." section entirely (heading through end of file).
        idx = next(i for i, l in enumerate(lines) if l.startswith("## 2."))
        (r.root / "docs/specs/001-fixture.md").write_text("".join(lines[:idx]))
        p = r.run()
        self.assertEqual(p.returncode, 2)
        self.assertIn("sec-2", p.stderr)
        self.assertIn("001-fixture.md", p.stderr)

    def test_rename_anchor_fails(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        f.write_text(f.read_text().replace("{#sec-2}", "{#sec-9}"))
        p = r.run()
        self.assertEqual(p.returncode, 2)
        self.assertIn("sec-2", p.stderr)

    def test_renumber_with_anchor_fails(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text = text.replace("## 2. Second {#sec-2}", "## 3. Second {#sec-3}")
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_letter_suffix_insert_passes(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text = text.replace(
            "Body two.\n",
            "Body two.\n\n### 2.1a Inserted {#sec-2.1a}\n\nInserted body.\n",
        )
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_body_edit_passes(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        f.write_text(f.read_text().replace("Body two.", "Body two, edited."))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_status_flip_does_not_unfreeze(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text = text.replace("status: accepted", "status: draft")
        text = text.replace("{#sec-2}", "{#sec-9}")
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_draft_doc_may_renumber(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="draft"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        f.write_text(f.read_text().replace("{#sec-2}", "{#sec-9}"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_delete_frozen_document_fails(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        os.remove(r.root / "docs/specs/001-fixture.md")
        p = r.run()
        self.assertEqual(p.returncode, 2)
        self.assertIn("001-fixture.md", p.stderr)
        self.assertIn("document", p.stderr)

    def test_new_document_is_unfrozen(self):
        """A document with no baseline is not inspected at all, whatever its
        status says: the duplicate anchor that refuses a published document
        one test below is not this gate's finding on a file HEAD never saw."""
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        r.write("docs/specs/002-new.md", SPEC.format(status="accepted")
                + "\n## 3. Duplicate {#sec-2}\n\nMore body.\n")
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stderr, "")

    def test_generated_view_is_never_frozen(self):
        """head_files() drops generated views: inlinespec.py renumbers a view
        whenever the amendments folded into it change, and that is the
        generator's output, not a published anchor being broken."""
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        # Frontmatter a real view never carries — the point is that scanning
        # the file at all would refuse the renumber below.
        r.write("docs/specs/inlined/001-fixture.md",
                SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/inlined/001-fixture.md"
        f.write_text(f.read_text().replace("{#sec-2}", "{#sec-9}"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stderr, "")

    def test_duplicate_anchor_fails(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text += "\n## 3. Duplicate {#sec-2}\n\nMore body.\n"
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_no_head_at_all(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_superseded_is_frozen(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="superseded"))
        r.commit()
        text = (r.root / "docs/specs/001-fixture.md").read_text()
        lines = text.splitlines(keepends=True)
        idx = next(i for i, l in enumerate(lines) if l.startswith("## 2."))
        (r.root / "docs/specs/001-fixture.md").write_text("".join(lines[:idx]))
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_duplicate_anchor_reports_once_not_also_as_moved(self):
        """WL-210 defect 1: a duplicated anchor whose surviving occurrence
        also carries a different section number must produce exactly one
        finding -- the duplicate refusal -- not a second "moved" refusal for
        the same root cause."""
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text += "\n## 5. Cloned {#sec-2}\n\nCloned body.\n"
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 2)
        self.assertIn("appears on more than one heading", p.stderr)
        self.assertNotIn("moved from", p.stderr)

    def test_runs_without_lode(self):
        r = self.repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        bindir = r.root / "bin"
        bindir.mkdir()
        (bindir / "python3").symlink_to(sys.executable)
        git = shutil.which("git")
        if git is None:
            self.skipTest("no git on PATH to symlink into the stripped bindir")
        (bindir / "git").symlink_to(git)
        env = {"PATH": str(bindir), "HOME": os.environ.get("HOME", "/tmp")}
        p = r.run(env=env)
        self.assertEqual(p.returncode, 0, p.stderr)


DOC = """\
---
status: {status}
{front}---
# Spec {n} — Fixture

## 1. First {{#sec-1}}

Body one.

## 2. Second {{#sec-2}}

Body two.
"""


def doc(status="draft", front="", n="001"):
    """A minimal corpus document; `front` is extra frontmatter lines."""
    return DOC.format(status=status, front=front, n=n)


def edge(key, subject, *values, indent=4):
    """One map-shaped frontmatter edge key. `indent` is the list indent, which
    the live corpus writes both as 4 (nested under the subject) and as 2."""
    lines = [f"{key}:", f'  "{subject}":']
    lines += [f"{' ' * indent}- {v}" for v in values]
    return "\n".join(lines) + "\n"


class TestMirrorEdges(RepoCase):
    """026 §4: an amends/replaces edge must be recorded from both sides."""

    def pair(self, a_front="", b_front="", commit=True):
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(front=a_front, n="001"))
        r.write("docs/specs/002-b.md", doc(front=b_front, n="002"))
        if commit:
            r.commit()
        return r

    def test_missing_mirror_fails(self):
        r = self.pair(edge("amends", "#sec-1", "002-b.md#sec-2"))
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)
        self.assertIn("002-b.md", p.stderr)
        self.assertIn("amendedBy", p.stderr)
        self.assertIn("#sec-2", p.stderr)

    def test_missing_acting_half_fails(self):
        """Recorded only from the passive side is equally a disagreement."""
        r = self.pair(b_front=edge("amendedBy", "#sec-2", "001-a.md#sec-1"))
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)
        self.assertIn("002-b.md", p.stderr)
        self.assertIn("amends", p.stderr)

    def test_mirrored_pair_passes(self):
        r = self.pair(
            edge("amends", "#sec-1", "002-b.md#sec-2"),
            edge("amendedBy", "#sec-2", "001-a.md#sec-1"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stderr, "")

    def test_two_indent_list_style_pairs(self):
        """The corpus writes the list at both +4 and +2 under a subject."""
        r = self.pair(
            edge("amends", "#sec-1", "002-b.md#sec-2", indent=2),
            edge("amendedBy", "#sec-2", "001-a.md#sec-1"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_all_reference_forms_resolve(self):
        """The same edge written in different path forms still pairs."""
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(
            front=edge("amends", "#sec-1", "../specs/002-b.md#sec-2")))
        r.write("docs/specs/002-b.md", doc(n="002", front=(
            edge("amendedBy", "#sec-2", "/docs/specs/001-a.md#sec-1")
            + edge("isReplacedBy", ".", "../plans/2026-01-01-p.md"))))
        r.write("docs/plans/2026-01-01-p.md", doc(
            n="003", front=edge("replaces", ".", "docs/specs/002-b.md")))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_annotation_tolerated(self):
        """A trailing parenthetical is opaque and stripped before pairing."""
        r = self.pair(
            edge("amends", "#sec-1", "002-b.md#sec-2 (D1–D3)"),
            edge("amendedBy", "#sec-2", "001-a.md#sec-1"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_inline_comment_stripped(self):
        """A YAML inline comment is not part of the value; `#sec-N` is."""
        r = self.pair(
            edge("amends", "#sec-1", "002-b.md#sec-2  # the older half"),
            edge("amendedBy", "#sec-2", "001-a.md#sec-1\t# and back"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stderr, "")

    def test_colon_form_unresolved(self):
        r = self.pair(edge("amends", ".", "rdf-registry:ADR-0006"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("unresolved", p.stderr)
        self.assertIn("rdf-registry:ADR-0006", p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_foreign_shorthand_unresolved(self):
        r = self.pair(edge("amends", ".", "CMS-SPEC-4"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("unresolved", p.stderr)
        self.assertIn("CMS-SPEC-4", p.stderr)

    def test_missing_target_file_is_not_our_defect(self):
        """secmeta.py owns dangling references; the mirror gate skips them."""
        r = self.pair(edge("amends", "#sec-1", "009-nope.md#sec-1"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("unresolved", p.stderr)

    def test_unparseable_flow_map_fails(self):
        r = self.pair('amends: {".": ["002-b.md#sec-2"]}\n')
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_unparseable_tab_indent_fails(self):
        r = self.pair('amends:\n  "#sec-1":\n\t- 002-b.md#sec-2\n')
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_unparseable_flow_value_on_subject_line_fails(self):
        """WL-210 defect 2: a flow-style value on the *subject* line, not
        just the key line, must be a refusal -- normalise() would otherwise
        silently reject the bogus fragment it decodes to, and a real defect
        would degrade to an unresolved note."""
        r = self.pair('amends:\n  "#sec-1": [002-b.md#sec-2]\n')
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_unparseable_empty_flow_value_on_subject_line_fails(self):
        """WL-210 defect 2, the empty-flow-value variant."""
        r = self.pair('amends:\n  "#sec-1": []\n')
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_unquoted_subject_key_blames_the_key_line(self):
        """WL-210 defect 3: an unquoted subject key must not be swallowed by
        the comment-skip branch -- the resulting refusal must name the key
        line itself, not the list line that follows it."""
        r = self.pair('amends:\n  #sec-1:\n    - 002-b.md#sec-2\n')
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("#sec-1:", p.stderr)
        self.assertNotIn("002-b.md#sec-2", p.stderr)

    def test_self_edge_without_own_mirror_fails(self):
        """WL-210 defect 4: a self-edge (acting doc == target doc) must not
        satisfy the mirror test from a single recording -- it needs its own
        mirror entry the same as any two-document edge."""
        r = self.repo()
        r.write("docs/specs/001-a.md",
                doc(front=edge("amends", "#sec-1", "001-a.md#sec-2")))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_self_edge_with_own_mirror_passes(self):
        """The positive case for defect 4: a self-edge recorded from both
        of its own halves is a clean mirror, not a refusal."""
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(front=(
            edge("amends", "#sec-1", "001-a.md#sec-2")
            + edge("amendedBy", "#sec-2", "001-a.md#sec-1"))))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_list_directly_under_edge_key_fails(self):
        """The edge keys are maps; a bare list is an edge with no subject."""
        r = self.pair("amends:\n  - 002-b.md#sec-2\n")
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("001-a.md", p.stderr)

    def test_other_keys_ignored(self):
        """Only the edge keys are read; covers' nested objects must not
        be mistaken for edge content."""
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(front="requires:\n- 009-nope.md\n"))
        r.write("docs/plans/2026-01-01-p.md", doc(n="002", front=(
            "covers:\n"
            "  - spec: docs/specs/001-a.md#sec-1\n"
            "    coverage: partial\n"
            "    fullCoverageWith:\n"
            "      - docs/plans/2026-01-02-q.md\n"
            "wasDerivedFrom: 001-a.md (D1–D3)\n")))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stderr, "")

    def test_new_document_checked_for_edges(self):
        """No baseline means unfrozen, but the edges are still paired."""
        r = self.repo()
        r.write("docs/specs/002-b.md", doc(n="002"))
        r.commit()
        r.write("docs/specs/001-a.md",
                doc(front=edge("amends", "#sec-1", "002-b.md#sec-2")))
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn("002-b.md", p.stderr)

    def test_doc_scoped_replaces_pairs(self):
        r = self.pair(
            edge("replaces", ".", "002-b.md"),
            edge("isReplacedBy", ".", "001-a.md"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_generated_views_ignored(self):
        """The walk skips generated views, so their edges never enter the
        graph. The view here carries a half-recorded `amends` — a refusal if
        it were read as authored — precisely so the skip is what is proven."""
        r = self.pair(
            edge("amends", "#sec-1", "002-b.md#sec-2"),
            edge("amendedBy", "#sec-2", "001-a.md#sec-1"),
            commit=False,
        )
        r.write("docs/specs/inlined/001-a.md",
                doc(front=edge("amends", "#sec-1", "../002-b.md#sec-2")))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stderr, "")


class TestExtractEdges(unittest.TestCase):
    """extract_edges() directly -- the level WL-210 defect 5 must be fixed
    at, rather than relying on normalise() to reject the bogus value it
    would otherwise decode to."""

    def setUp(self):
        sys.path.insert(0, str(SCRIPT.parent))
        import secfrozen
        self.m = secfrozen

    def test_comment_only_item_has_no_value(self):
        front = 'amends:\n  "#sec-1":\n    - # why\n'
        self.assertEqual(list(self.m.extract_edges(front)), [])

    def test_comment_only_item_alongside_a_real_one(self):
        front = (
            'amends:\n'
            '  "#sec-1":\n'
            '    - # why\n'
            '    - 002-b.md#sec-2\n'
        )
        self.assertEqual(
            list(self.m.extract_edges(front)),
            [("amends", "#sec-1", "002-b.md#sec-2")])


class TestCanonical(unittest.TestCase):
    """canonical() is the edge set task 3's cycle check consumes."""

    def setUp(self):
        sys.path.insert(0, str(SCRIPT.parent))
        import secfrozen
        self.m = secfrozen

    def test_mirror_halves_canonicalise_alike(self):
        acting = self.m.canonical(
            "docs/specs/001-a.md", "amends", "#sec-1",
            "docs/specs/002-b.md", "sec-2")
        passive = self.m.canonical(
            "docs/specs/002-b.md", "amendedBy", "#sec-2",
            "docs/specs/001-a.md", "sec-1")
        self.assertEqual(acting, passive)
        self.assertEqual(acting, ("amends", "docs/specs/001-a.md", "sec-1",
                                  "docs/specs/002-b.md", "sec-2"))

    def test_doc_scoped_ends(self):
        self.assertEqual(
            self.m.canonical("docs/plans/p.md", "isReplacedBy", ".",
                             "docs/specs/001-a.md", ""),
            ("replaces", "docs/specs/001-a.md", ".", "docs/plans/p.md", "."))


class TestCycles(RepoCase):
    """026 §4.1: an amends/replaces cycle in the section-level graph is
    refused, even when task 2's mirror check finds each edge correctly
    recorded from both sides."""

    def pair(self, a_front="", b_front=""):
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(front=a_front, n="001"))
        r.write("docs/specs/002-b.md", doc(front=b_front, n="002"))
        r.commit()
        return r

    def cycle_pair(self, status="draft"):
        """A#sec-1 <-> B#sec-2 mutual amends, correctly mirrored both ways
        -- a 2-cycle that a naive per-recording check could mistake for a
        clean mirror pair."""
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(status=status, n="001", front=(
            edge("amends", "#sec-1", "002-b.md#sec-2")
            + edge("amendedBy", "#sec-1", "002-b.md#sec-2"))))
        r.write("docs/specs/002-b.md", doc(status=status, n="002", front=(
            edge("amends", "#sec-2", "001-a.md#sec-1")
            + edge("amendedBy", "#sec-2", "001-a.md#sec-1"))))
        r.commit()
        return r

    def test_two_document_loop_refused(self):
        r = self.cycle_pair(status="accepted")
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn(
            "docs/specs/001-a.md#sec-1 -> docs/specs/002-b.md#sec-2 -> "
            "docs/specs/001-a.md#sec-1", p.stderr)

    def test_mirrored_pair_is_not_a_cycle(self):
        """Task 2's mirrored-pair corpus: one edge, recorded from both
        sides. Direction canonicalisation, not edge counting, is what tells
        this apart from a loop."""
        r = self.pair(
            edge("amends", "#sec-1", "002-b.md#sec-2"),
            edge("amendedBy", "#sec-2", "001-a.md#sec-1"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_three_document_loop_refused(self):
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(n="001", front=(
            edge("replaces", "#sec-1", "002-b.md#sec-2")
            + edge("isReplacedBy", "#sec-1", "003-c.md#sec-3"))))
        r.write("docs/specs/002-b.md", doc(n="002", front=(
            edge("replaces", "#sec-2", "003-c.md#sec-3")
            + edge("isReplacedBy", "#sec-2", "001-a.md#sec-1"))))
        r.write("docs/specs/003-c.md", doc(n="003", front=(
            edge("replaces", "#sec-3", "001-a.md#sec-1")
            + edge("isReplacedBy", "#sec-3", "002-b.md#sec-2"))))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn(
            "amends/replaces cycle: docs/specs/001-a.md#sec-1 -> "
            "docs/specs/002-b.md#sec-2 -> docs/specs/003-c.md#sec-3 -> "
            "docs/specs/001-a.md#sec-1", p.stderr)

    def test_self_amendment_refused(self):
        """A loop within one document is the same error."""
        front = (
            'amends:\n'
            '  "#sec-1":\n'
            '    - 001-a.md#sec-2\n'
            '  "#sec-2":\n'
            '    - 001-a.md#sec-1\n'
            'amendedBy:\n'
            '  "#sec-1":\n'
            '    - 001-a.md#sec-2\n'
            '  "#sec-2":\n'
            '    - 001-a.md#sec-1\n'
        )
        r = self.repo()
        r.write("docs/specs/001-a.md", doc(front=front, n="001"))
        r.commit()
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn(
            "amends/replaces cycle: docs/specs/001-a.md#sec-1 -> "
            "docs/specs/001-a.md#sec-2 -> docs/specs/001-a.md#sec-1",
            p.stderr)

    def test_doc_scoped_edges_never_cycle(self):
        """§4.1 scopes acyclicity to the section-level graph; doc-scoped
        edges are §3.2 banners never inlined, so they cannot recurse."""
        r = self.pair(
            edge("amends", ".", "002-b.md")
            + edge("amendedBy", ".", "002-b.md"),
            edge("amends", ".", "001-a.md")
            + edge("amendedBy", ".", "001-a.md"),
        )
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_draft_cycle_still_refused(self):
        """Effectiveness (§3.1) is a read-time gate; acceptance is a status
        flip that would never re-present the edges here."""
        r = self.cycle_pair(status="draft")
        p = r.run()
        self.assertEqual(p.returncode, 2, p.stderr)
        self.assertIn(
            "amends/replaces cycle: docs/specs/001-a.md#sec-1 -> "
            "docs/specs/002-b.md#sec-2 -> docs/specs/001-a.md#sec-1",
            p.stderr)


if __name__ == "__main__":
    unittest.main()
