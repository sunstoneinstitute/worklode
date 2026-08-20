#!/usr/bin/env python3
"""Tests for secfrozen.py, run against throwaway fixture git repos."""
import os
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


class TestPermanence(unittest.TestCase):
    def test_delete_published_anchor_fails(self):
        r = Repo()
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
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        f.write_text(f.read_text().replace("{#sec-2}", "{#sec-9}"))
        p = r.run()
        self.assertEqual(p.returncode, 2)
        self.assertIn("sec-2", p.stderr)

    def test_renumber_with_anchor_fails(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text = text.replace("## 2. Second {#sec-2}", "## 3. Second {#sec-3}")
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_letter_suffix_insert_passes(self):
        r = Repo()
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
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        f.write_text(f.read_text().replace("Body two.", "Body two, edited."))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_status_flip_does_not_unfreeze(self):
        r = Repo()
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
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="draft"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        f.write_text(f.read_text().replace("{#sec-2}", "{#sec-9}"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_delete_frozen_document_fails(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        os.remove(r.root / "docs/specs/001-fixture.md")
        p = r.run()
        self.assertEqual(p.returncode, 2)
        self.assertIn("001-fixture.md", p.stderr)
        self.assertIn("document", p.stderr)

    def test_new_document_is_unfrozen(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        r.write("docs/specs/002-new.md", SPEC.format(status="accepted"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_duplicate_anchor_fails(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        f = r.root / "docs/specs/001-fixture.md"
        text = f.read_text()
        text += "\n## 3. Duplicate {#sec-2}\n\nMore body.\n"
        f.write_text(text)
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_no_head_at_all(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        p = r.run()
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_superseded_is_frozen(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="superseded"))
        r.commit()
        text = (r.root / "docs/specs/001-fixture.md").read_text()
        lines = text.splitlines(keepends=True)
        idx = next(i for i, l in enumerate(lines) if l.startswith("## 2."))
        (r.root / "docs/specs/001-fixture.md").write_text("".join(lines[:idx]))
        p = r.run()
        self.assertEqual(p.returncode, 2)

    def test_runs_without_lode(self):
        r = Repo()
        r.write("docs/specs/001-fixture.md", SPEC.format(status="accepted"))
        r.commit()
        bindir = r.root / "bin"
        bindir.mkdir()
        (bindir / "python3").symlink_to(sys.executable)
        git = subprocess.run(["which", "git"], capture_output=True,
                             text=True).stdout.strip()
        (bindir / "git").symlink_to(git)
        env = {"PATH": str(bindir), "HOME": os.environ.get("HOME", "/tmp")}
        p = r.run(env=env)
        self.assertEqual(p.returncode, 0, p.stderr)


if __name__ == "__main__":
    unittest.main()
