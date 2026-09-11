#!/usr/bin/env python3
"""Tests for scripts/canon-refs.py: the resolution ladder and what it skips."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "canon_refs", Path(__file__).with_name("canon-refs.py")
)
cr = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cr)


def doc(key, kind, number, slug):
    return {"project_key": key, "kind": kind, "number": number, "slug": slug,
            "ref": f"{key}-{kind.upper()}-{number}"}


CORPUS = cr.Corpus([
    doc("WL", "spec", 4, "004-execution-backbone"),
    doc("WL", "spec", 1, "001-identity-and-authentication"),
    doc("WL", "spec", 42, "secret-templates"),          # minted, no number prefix
    doc("WL", "spec", 60, "reading-diffs"),             # renumbered from 029
    doc("WL", "adr", 43, "secrets-catalog-home"),
    doc("EA", "spec", 1, "001-zero-trust-gateway"),
    doc("WL", "plan", 74, "2026-08-03-design-doc-queries-1-corpus-and-list"),
    doc("WL", "plan", 75, "2026-08-03-design-doc-queries-3-permanence-gate"),
])


class Resolve(unittest.TestCase):
    def test_exact_slug(self):
        d, rule = CORPUS.resolve("WL", "004-execution-backbone")
        self.assertEqual((d["ref"], rule), ("WL-SPEC-4", "exact"))

    def test_stem_and_number_for_a_minted_slug(self):
        d, rule = CORPUS.resolve("WL", "042-secret-templates")
        self.assertEqual((d["ref"], rule), ("WL-SPEC-42", "stem+number"))

    def test_stem_alone_survives_a_renumbering(self):
        d, rule = CORPUS.resolve("WL", "029-reading-diffs")
        self.assertEqual((d["ref"], rule), ("WL-SPEC-60", "stem"))

    def test_home_project_wins_over_another_corpus(self):
        d, _ = CORPUS.resolve("WL", "001-identity-and-authentication")
        self.assertEqual(d["ref"], "WL-SPEC-1")

    def test_never_reaches_into_another_corpus(self):
        # The legacy spelling cannot cross a corpus (025 §14.3), and every
        # occurrence that looked cross-project was a grammar example.
        d, rule = CORPUS.resolve("WL", "001-zero-trust-gateway")
        self.assertEqual((d, rule), (None, "unresolved"))

    def test_a_shared_number_alone_resolves_to_nothing(self):
        # 001-alpha.md is a test fixture, not WL-SPEC-1.
        d, rule = CORPUS.resolve("WL", "001-alpha")
        self.assertEqual((d, rule), (None, "unresolved"))


    def test_a_plan_resolves_on_its_date_slug(self):
        d, rule = CORPUS.resolve("WL", "2026-08-03-design-doc-queries-1-corpus-and-list")
        self.assertEqual((d["ref"], rule), ("WL-PLAN-74", "plan-slug"))

    def test_an_unknown_date_slug_resolves_to_nothing(self):
        d, rule = CORPUS.resolve("WL", "2026-07-05-graph-server-auth-design")
        self.assertEqual((d, rule), (None, "unresolved"))


class Rewrite(unittest.TestCase):
    def test_rewrites_prose_frontmatter_and_inline_code(self):
        body = (
            "---\nrequires:\n  - 004-execution-backbone.md\n---\n"
            "See 004-execution-backbone.md#sec-3 and `043-secrets-catalog-home.md`.\n"
        )
        new, hits = cr.rewrite(CORPUS, "WL", body)
        self.assertIn("  - WL-SPEC-4\n", new)
        self.assertIn("See WL-SPEC-4#sec-3", new)
        self.assertIn("`WL-ADR-43`", new)
        self.assertEqual(sum(1 for h in hits if h["target"]), 3)

    def test_skips_fenced_blocks(self):
        body = "before 004-execution-backbone.md\n```go\n// 004-execution-backbone.md\n```\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertIn("before WL-SPEC-4\n", new)
        self.assertIn("// 004-execution-backbone.md", new)

    def test_leaves_prose_spellings_alone(self):
        body = "025 §7.3 and spec 025 and plan 14 stay as they are.\n"
        new, hits = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)
        self.assertEqual(hits, [])

    def test_leaves_line_ranges_and_compounds_alone(self):
        body = "lines 125-249, a 256-bit key, and 016-width vectors.\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_consumes_a_dead_corpus_path(self):
        # docs/specs/ was deleted when documents left the tree (055), so the
        # whole path goes, not just its filename.
        body = "Modify: `docs/specs/004-execution-backbone.md#sec-3`\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, "Modify: `WL-SPEC-4#sec-3`\n")

    def test_leaves_a_path_under_any_other_directory_alone(self):
        body = "internal/testdata/004-execution-backbone.md is a fixture.\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_rewrites_a_plan_reference_in_prose(self):
        body = ("Part 1 (2026-08-03-design-doc-queries-1-corpus-and-list.md) builds it; "
                "Part 3 (docs/plans/2026-08-03-design-doc-queries-3-permanence-gate.md) is independent.\n")
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, "Part 1 (WL-PLAN-74) builds it; Part 3 (WL-PLAN-75) is independent.\n")

    def test_leaves_a_markdown_link_alone(self):
        # Rewriting only the text half labels a dead link with a live ref.
        body = "See [`docs/plans/2026-08-03-design-doc-queries-1-corpus-and-list.md`](./x.md).\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_leaves_another_repos_tree_alone(self):
        # docs/superpowers/specs/ ends in "specs" but is not this corpus.
        body = "See docs/superpowers/specs/2026-08-03-design-doc-queries-1-corpus-and-list.md\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_leaves_the_fold_staging_directory_alone(self):
        body = "Create docs/specs2/004-execution-backbone.md from the source.\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_leaves_a_line_number_citation_alone(self):
        body = "`docs/specs/004-execution-backbone.md:706` names a line.\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_leaves_a_command_argument_alone(self):
        body = "- [ ] `./scripts/fold.py --scaffold --only 004-execution-backbone.md`\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)

    def test_leaves_the_generated_inline_view_alone(self):
        body = "Amend `docs/specs/004-execution-backbone.md`, never `docs/specs/inlined/004-execution-backbone.md`.\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, "Amend `WL-SPEC-4`, never `docs/specs/inlined/004-execution-backbone.md`.\n")

    def test_leaves_a_git_revspec_alone(self):
        body = "e4e2920:docs/specs/004-execution-backbone.md is the old text.\n"
        new, _ = cr.rewrite(CORPUS, "WL", body)
        self.assertEqual(new, body)


if __name__ == "__main__":
    unittest.main()
