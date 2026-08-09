#!/usr/bin/env python3
"""Hermetic integration tests for scripts/secmeta.py plan coverage checks."""

import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
import json
import re
from collections import defaultdict
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
WL = "https://worklode.io/ns/ontology#"
WLC = "https://worklode.io/ns/concept/"
RDF = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
RDFS = "http://www.w3.org/2000/01/rdf-schema#"
OWL = "http://www.w3.org/2002/07/owl#"
SH = "http://www.w3.org/ns/shacl#"
SKOS = "http://www.w3.org/2004/02/skos/core#"

NTRIPLE = re.compile(
    r'^(?P<subject><[^>]*>|_:[^ ]+) (?P<predicate><[^>]*>) '
    r'(?P<object><[^>]*>|_:[^ ]+|"(?:[^"\\]|\\.)*"(?:@[A-Za-z-]+|\^\^<[^>]*>)?) \.$'
)
SPEC = """\
---
status: accepted
issued: 2026-08-09
---
# Spec

## 1. Covered section {#sec-1}

## 2. Another section {#sec-2}
"""


def plan(status, covers):
    return f"""\
---
status: {status}
covers:
  - {covers}
---
# Plan
"""


def run_secmeta(files):
    """Run the real validator in an isolated, minimal repository."""
    with tempfile.TemporaryDirectory() as tmp:
        repo = Path(tmp)
        scripts = repo / "scripts"
        scripts.mkdir()
        for name in ("secmeta.py", "secfmt.py"):
            shutil.copy2(ROOT / "scripts" / name, scripts / name)
        for rel, content in files.items():
            path = repo / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(textwrap.dedent(content))
        return subprocess.run(
            [sys.executable, "scripts/secmeta.py", "docs/specs", "docs/plans"],
            cwd=repo, capture_output=True, text=True, check=False,
        )


def parse_ntriples(text):
    """Parse Riot N-Triples into the small graph view this structural test needs."""
    graph = defaultdict(lambda: defaultdict(list))
    for line in text.splitlines():
        match = NTRIPLE.fullmatch(line)
        if not match:
            raise AssertionError(f"unexpected Riot N-Triples line: {line!r}")
        subject = ntriples_term(match.group("subject"))
        predicate = ntriples_term(match.group("predicate"))
        graph[subject][predicate].append(ntriples_term(match.group("object")))
    return graph


def ntriples_term(term):
    if term.startswith("<"):
        return term[1:-1]
    if term.startswith('"'):
        literal_end = term.rfind('"')
        return json.loads(term[:literal_end + 1])
    return term


def objects(graph, subject, predicate):
    return graph[subject][predicate]


def rdf_list(graph, head):
    members = []
    while head != RDF + "nil":
        members.append(objects(graph, head, RDF + "first")[0])
        head = objects(graph, head, RDF + "rest")[0]
    return members


def property_node(graph, shape, predicate, path):
    for node in objects(graph, shape, SH + "property"):
        if objects(graph, node, predicate) == [path]:
            return node
    raise AssertionError(f"{shape} has no property shape for {path}")


class PlanCoverageTest(unittest.TestCase):
    # Each case is a real CLI contract. It would fail if the corresponding
    # frontmatter rule were removed or resolved against the wrong document.
    def assert_error(self, covers, message):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", covers)})
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn(message, result.stdout)

    def test_qualified_coverage_requires_spec(self):
        self.assert_error("coverage: full", "no spec")

    def test_qualified_coverage_requires_level(self):
        self.assert_error("spec: docs/specs/s.md#sec-1", "no coverage")

    def test_qualified_coverage_rejects_unknown_entry_key(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: full\n    extra: no",
                          "unknown key(s) extra")

    def test_qualified_coverage_rejects_invalid_level(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: nearly",
                          "coverage 'nearly' is not one of full/partial/none")

    def test_completion_is_invalid_beside_full(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: full\n"
                          "    fullCoverageWith: [docs/plans/b.md]",
                          "fullCoverageWith is invalid beside coverage: full")

    def test_completion_is_invalid_beside_none(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: none\n"
                          "    fullCoverageWith: [docs/plans/b.md]",
                          "fullCoverageWith is invalid beside coverage: none")

    def test_qualified_coverage_requires_section_fragment(self):
        self.assert_error("spec: docs/specs/s.md\n    coverage: full", "names no section")

    def test_scalar_bare_whole_document_is_reported_without_breaking_legacy_plans(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": "---\nstatus: accepted\ncovers: docs/specs/s.md\n---\n# Plan\n"})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("covers: docs/specs/s.md names no section", result.stderr)

    def test_list_bare_whole_document_is_reported_without_breaking_legacy_plans(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "docs/specs/s.md")})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("covers: docs/specs/s.md names no section", result.stderr)

    def test_qualified_coverage_rejects_nonexistent_section(self):
        self.assert_error("spec: docs/specs/s.md#sec-9\n    coverage: full", "names no anchor")

    def test_retired_implements_is_reported_without_failure(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": "---\nstatus: accepted\nimplements: docs/specs/s.md#sec-1\n---\n# Plan\n"})
        self.assertEqual(result.returncode, 0)
        self.assertIn("implements is retired", result.stderr)

    def test_covers_and_implements_cannot_both_appear(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": "---\nstatus: accepted\ncovers: docs/specs/s.md#sec-1\nimplements: docs/specs/s.md#sec-1\n---\n# Plan\n"})
        self.assertEqual(result.returncode, 1)
        self.assertIn("covers and implements", result.stdout)

    def test_duplicate_accepted_plans_require_qualified_form_but_do_not_fail(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "docs/specs/s.md#sec-1"),
                              "docs/plans/b.md": plan("accepted", "docs/specs/s.md#sec-1")})
        self.assertEqual(result.returncode, 0)
        self.assertIn("more than one accepted plan", result.stderr)

    def test_draft_sibling_does_not_require_qualified_form(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "docs/specs/s.md#sec-1"),
                              "docs/plans/b.md": plan("draft", "docs/specs/s.md#sec-1")})
        self.assertEqual(result.returncode, 0)
        self.assertNotIn("more than one accepted plan", result.stderr)

    def test_completion_must_use_repo_relative_plan_path(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [b.md]"),
                              "docs/plans/b.md": plan("accepted", "docs/specs/s.md#sec-1")})
        self.assertEqual(result.returncode, 1)
        self.assertIn("fullCoverageWith must be a list of repo-relative plan paths", result.stdout)

    def test_completion_requires_a_list_of_strings(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: docs/plans/b.md",
                          "fullCoverageWith must be a list of repo-relative plan paths")

    def test_completion_rejects_non_string_list_member(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: [{path: docs/plans/b.md}]",
                          "fullCoverageWith must be a list of repo-relative plan paths")

    def test_completion_rejects_non_plan_paths_before_resolution(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: [docs/specs/s.md]",
                          "fullCoverageWith must be a list of repo-relative plan paths")

    def test_completion_rejects_plan_path_traversal_before_resolution(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: [docs/plans/../specs/s.md]",
                          "fullCoverageWith must be a list of repo-relative plan paths")

    def test_completion_rejects_fragment_even_when_target_covers_section(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md#sec-1]"),
                              "docs/plans/b.md": plan("accepted", "docs/specs/s.md#sec-1")})
        self.assertEqual(result.returncode, 1)
        self.assertIn("fullCoverageWith must be a list of repo-relative plan paths", result.stdout)

    def test_completion_rejects_noncanonical_double_slash_path(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: [docs/plans//b.md]",
                          "fullCoverageWith must be a list of repo-relative plan paths")

    def test_completion_reports_nonexistent_target(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: [docs/plans/missing.md]", "resolves to no file")

    def test_completion_target_must_cover_the_same_section(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md]"),
                              "docs/plans/b.md": plan("accepted", "spec: docs/specs/s.md#sec-2\n    coverage: partial")})
        self.assertEqual(result.returncode, 0)
        self.assertIn("does not itself cover docs/specs/s.md#sec-1", result.stderr)

    def test_completion_rejects_the_current_plan_as_its_own_target(self):
        result = run_secmeta({
            "docs/specs/s.md": SPEC,
            "docs/plans/a.md": plan(
                "accepted",
                "spec: docs/specs/s.md#sec-1\n"
                "    coverage: partial\n"
                "    fullCoverageWith: [docs/plans/a.md]",
            ),
        })
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("fullCoverageWith cannot name its own plan", result.stdout)

    def test_completion_target_without_coverage_is_reported(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md]"),
                              "docs/plans/b.md": "---\nstatus: accepted\n---\n# Plan\n"})
        self.assertEqual(result.returncode, 0)
        self.assertIn("does not itself cover docs/specs/s.md#sec-1", result.stderr)

    def test_completion_target_must_be_accepted(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md]"),
                              "docs/plans/b.md": plan("draft", "spec: docs/specs/s.md#sec-1\n    coverage: full")})
        self.assertEqual(result.returncode, 0)
        self.assertIn("names draft plan docs/plans/b.md", result.stderr)

    def test_completion_target_with_none_contributes_nothing(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md]"),
                              "docs/plans/b.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: none")})
        self.assertEqual(result.returncode, 0)
        self.assertIn("coverage: none contributes nothing", result.stderr)

    def test_completion_rejects_empty_target_list(self):
        self.assert_error("spec: docs/specs/s.md#sec-1\n    coverage: partial\n"
                          "    fullCoverageWith: []",
                          "fullCoverageWith must name at least one plan")

    def test_accepted_full_and_partial_targets_close_same_section_cleanly(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md, docs/plans/c.md]"),
                              "docs/plans/b.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: full"),
                              "docs/plans/c.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial")})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout + result.stderr, "")

    def test_accepted_canonical_same_section_target_closes_cleanly(self):
        result = run_secmeta({"docs/specs/s.md": SPEC,
                              "docs/plans/a.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: partial\n    fullCoverageWith: [docs/plans/b.md]"),
                              "docs/plans/b.md": plan("accepted", "spec: docs/specs/s.md#sec-1\n    coverage: full")})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout + result.stderr, "")


class CoverageShapeTest(unittest.TestCase):
    def test_plan_status_domain_shape_and_coverage_level_range(self):
        """Catches a Plan status contradiction and treating a SKOS scheme as a class."""
        ontology = subprocess.run(
            ["riot", "--output=ntriples", "ns/ontology.ttl"],
            cwd=ROOT, capture_output=True, text=True, check=True,
        )
        graph = parse_ntriples(ontology.stdout)
        status_domain = objects(graph, WL + "status", RDFS + "domain")[0]
        status_union = objects(graph, status_domain, OWL + "unionOf")[0]
        self.assertEqual(
            rdf_list(graph, status_union),
            [WL + "DesignDoc", WL + "Plan", WL + "Section"],
        )
        self.assertEqual(
            objects(graph, WL + "coverageLevel", RDFS + "range"),
            [SKOS + "Concept"],
        )

        shapes = subprocess.run(
            ["riot", "--output=ntriples", "ns/shapes.ttl"],
            cwd=ROOT, capture_output=True, text=True, check=True,
        )
        shape_graph = parse_ntriples(shapes.stdout)
        self.assertEqual(
            objects(shape_graph, WL + "PlanShape", SH + "targetClass"),
            [WL + "Plan"],
        )
        status = property_node(
            shape_graph, WL + "PlanShape", SH + "path", WL + "status"
        )
        self.assertEqual(objects(shape_graph, status, SH + "minCount"), ["1"])
        status_node = objects(shape_graph, status, SH + "node")[0]
        scheme = property_node(
            shape_graph, status_node, SH + "path", SKOS + "inScheme"
        )
        self.assertEqual(
            objects(shape_graph, scheme, SH + "hasValue"),
            [WLC + "DesignDocStatus"],
        )

    def test_shacl_cli_rejects_plan_without_status_and_accepts_seeded_status(self):
        """Runs the installed SHACL engine over a small Plan ABox, not an OWL reasoner."""
        prefix = """\
@prefix ex: <https://example.test/> .
@prefix wl: <https://worklode.io/ns/ontology#> .
@prefix wlc: <https://worklode.io/ns/concept/> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .
wlc:accepted a skos:Concept ; skos:inScheme wlc:DesignDocStatus .
"""
        with tempfile.TemporaryDirectory() as tmp:
            data = Path(tmp) / "plan.ttl"
            data.write_text(prefix + "ex:plan a wl:Plan .\n")
            invalid = subprocess.run(
                ["shacl", "validate", "--text", "--shapes", "ns/shapes.ttl",
                 "--data", str(data)],
                cwd=ROOT, capture_output=True, text=True, check=False,
            )
            invalid_output = (invalid.stdout + invalid.stderr).strip()
            self.assertNotEqual(invalid_output, "Conforms")
            self.assertIn("Plan carries exactly one", invalid_output)

            data.write_text(prefix + "ex:plan a wl:Plan ; wl:status wlc:accepted .\n")
            valid = subprocess.run(
                ["shacl", "validate", "--text", "--shapes", "ns/shapes.ttl",
                 "--data", str(data)],
                cwd=ROOT, capture_output=True, text=True, check=False,
            )
            self.assertEqual(valid.returncode, 0, valid.stdout + valid.stderr)
            self.assertEqual((valid.stdout + valid.stderr).strip(), "Conforms")

    def test_coverage_shape_constrains_qualified_coverage_graph(self):
        """Fails if qualified coverage is not constrained as one direct-edge-backed record."""
        result = subprocess.run(
            ["riot", "--output=ntriples", "ns/shapes.ttl"],
            cwd=ROOT, capture_output=True, text=True, check=True,
        )
        graph = parse_ntriples(result.stdout)
        shape = graph[WL + "CoverageShape"]

        self.assertEqual(shape[SH + "targetClass"], [WL + "Coverage"])
        for path, value_class in (
            (WL + "coveringPlan", WL + "Plan"),
            (WL + "coveredSection", WL + "Section"),
        ):
            node = property_node(graph, WL + "CoverageShape", SH + "path", path)
            self.assertEqual(objects(graph, node, SH + "class"), [value_class])
            self.assertEqual(objects(graph, node, SH + "minCount"), ["1"])
            self.assertEqual(objects(graph, node, SH + "maxCount"), ["1"])

        level = property_node(
            graph, WL + "CoverageShape", SH + "path", WL + "coverageLevel"
        )
        self.assertEqual(objects(graph, level, SH + "minCount"), ["1"])
        self.assertEqual(objects(graph, level, SH + "maxCount"), ["1"])
        self.assertEqual(
            rdf_list(graph, objects(graph, level, SH + "in")[0]),
            [WLC + "full", WLC + "partial", WLC + "none"],
        )
        completed_with = property_node(
            graph, WL + "CoverageShape", SH + "path", WL + "completedWith"
        )
        self.assertEqual(objects(graph, completed_with, SH + "class"), [WL + "Plan"])

        branches = rdf_list(graph, objects(graph, WL + "CoverageShape", SH + "or")[0])
        self.assertEqual(len(branches), 2)
        partial = next(
            branch for branch in branches
            if objects(
                graph,
                property_node(graph, branch, SH + "path", WL + "coverageLevel"),
                SH + "hasValue",
            ) == [WLC + "partial"]
        )
        self.assertEqual(objects(graph, partial, SH + "property").__len__(), 1)

        complete = next(
            branch for branch in branches
            if any(
                objects(graph, node, SH + "maxCount") == ["0"]
                for node in objects(graph, branch, SH + "property")
                if objects(graph, node, SH + "path") == [WL + "completedWith"]
            )
        )
        complete_level = property_node(graph, complete, SH + "path", WL + "coverageLevel")
        self.assertEqual(
            rdf_list(graph, objects(graph, complete_level, SH + "in")[0]),
            [WLC + "full", WLC + "none"],
        )

        sparql = objects(graph, WL + "CoverageShape", SH + "sparql")[0]
        select = objects(graph, sparql, SH + "select")[0]
        compact_select = " ".join(select.split())
        self.assertIn("PREFIX wl: <https://worklode.io/ns/ontology#>", compact_select)
        self.assertIn(
            "$this wl:coveringPlan ?plan ; wl:coveredSection ?section .",
            compact_select,
        )
        self.assertIn(
            "FILTER NOT EXISTS { ?plan wl:covers ?section . }",
            compact_select,
        )


if __name__ == "__main__":
    unittest.main()
