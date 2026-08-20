#!/usr/bin/env python3
"""Tests for scripts/nsgen.py's Turtle subset parser.

WL-70: the generator replaced three hand-kept copies of the task-kind enum
with one derived list, so the parser is now the thing standing between
ns/concept.ttl and a CHECK constraint. The property that matters is not "it
parses the file today" — the CI drift check proves that — but that every way
of getting the Turtle wrong is an *error* rather than a shorter list. A
silently dropped concept narrows `validKinds` and the API starts rejecting
valid input with nothing failing.

Run: python3 scripts/nsgen_test.py
"""

import importlib.util
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONCEPT_TTL = ROOT / "ns" / "concept.ttl"


def load_nsgen():
    spec = importlib.util.spec_from_file_location("nsgen", ROOT / "scripts" / "nsgen.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


nsgen = load_nsgen()

PREAMBLE = """\
@prefix wlc:  <https://worklode.io/ns/concept/> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .

wlc:TaskKind a skos:ConceptScheme ; skos:prefLabel "Task kind" .
wlc:DesignDocStatus a skos:ConceptScheme ; skos:prefLabel "DesignDoc lifecycle" .

wlc:draft a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "draft" .
wlc:accepted a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "accepted" .
wlc:superseded a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "superseded" .

wlc:DesignDocStatusOrder a skos:OrderedCollection ;
    skos:memberList ( wlc:draft wlc:accepted wlc:superseded ) .
"""

TWO_KINDS = """\
wlc:bug a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "bug" .
wlc:feature a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "feature" .
"""


def ttl(body):
    return PREAMBLE + body


class TestHappyPath(unittest.TestCase):
    def test_extracts_both_schemes(self):
        kinds, statuses = nsgen.extract(ttl(TWO_KINDS))
        self.assertEqual(kinds, ["bug", "feature"])
        # Lifecycle order from skos:memberList, not alphabetical.
        self.assertEqual(statuses, ["draft", "accepted", "superseded"])

    def test_kinds_are_sorted_regardless_of_file_order(self):
        reversed_body = """\
wlc:feature a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "feature" .
wlc:bug a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "bug" .
"""
        kinds, _ = nsgen.extract(ttl(reversed_body))
        self.assertEqual(kinds, ["bug", "feature"])

    def test_prefixed_name_before_an_unspaced_terminator(self):
        # A local part may contain '.' but not end in one, so `wlc:TaskKind.`
        # is a name plus the statement terminator. Lexing it as one name would
        # drop the concept with no error, which is why the rule is explicit.
        kinds, _ = nsgen.extract(
            ttl(TWO_KINDS + "wlc:chore a skos:Concept ; skos:inScheme wlc:TaskKind.\n")
        )
        self.assertEqual(kinds, ["bug", "chore", "feature"])

    def test_dot_inside_a_local_name_is_kept(self):
        kinds, _ = nsgen.extract(
            ttl(TWO_KINDS + "wlc:a.b a skos:Concept ; skos:inScheme wlc:TaskKind .\n")
        )
        self.assertEqual(kinds, ["a.b", "bug", "feature"])

    def test_real_corpus_parses(self):
        kinds, statuses = nsgen.extract(CONCEPT_TTL.read_text(encoding="utf-8"))
        self.assertIn("design", kinds)
        self.assertEqual(statuses[0], "draft")


class TestSilentDropsAreErrors(unittest.TestCase):
    """Each of these once produced a shorter list and exit 0."""

    def assertRaisesTurtle(self, body, needle=""):
        with self.assertRaises(nsgen.TurtleError) as cm:
            nsgen.extract(ttl(body))
        if needle:
            self.assertIn(needle, str(cm.exception))

    def test_misspelled_inscheme(self):
        self.assertRaisesTurtle(
            TWO_KINDS + 'wlc:chore a skos:Concept ; skos:inscheme wlc:TaskKind .\n',
            "no `skos:inScheme",
        )

    def test_scheme_name_quoted_by_mistake(self):
        self.assertRaisesTurtle(
            TWO_KINDS + 'wlc:chore a skos:Concept ; skos:inScheme "TaskKind" .\n',
            "no `skos:inScheme",
        )

    def test_concept_attached_only_by_has_top_concept(self):
        self.assertRaisesTurtle(
            TWO_KINDS
            + "wlc:TaskKind skos:hasTopConcept wlc:chore .\n"
            + "wlc:chore a skos:Concept ; skos:prefLabel \"chore\" .\n",
            "no `skos:inScheme",
        )

    def test_member_declared_without_a_concept_type(self):
        self.assertRaisesTurtle(
            TWO_KINDS + "wlc:chore skos:inScheme wlc:TaskKind .\n",
            "not declared `a skos:Concept`",
        )

    def test_duplicate_in_member_list(self):
        dup = PREAMBLE.replace(
            "( wlc:draft wlc:accepted wlc:superseded )",
            "( wlc:draft wlc:draft wlc:accepted wlc:superseded )",
        )
        with self.assertRaises(nsgen.TurtleError) as cm:
            nsgen.extract(dup + TWO_KINDS)
        self.assertIn("once each", str(cm.exception))

    def test_member_list_missing_a_status(self):
        short = PREAMBLE.replace(" wlc:superseded )", " )")
        with self.assertRaises(nsgen.TurtleError):
            nsgen.extract(short + TWO_KINDS)

    def test_empty_scheme(self):
        with self.assertRaises(nsgen.TurtleError) as cm:
            nsgen.extract(ttl(""))
        self.assertIn("no members", str(cm.exception))


class TestUnsupportedConstructsRaise(unittest.TestCase):
    """The parser must never guess at Turtle it does not implement."""

    def assertRaisesTurtle(self, body):
        with self.assertRaises(nsgen.TurtleError):
            nsgen.extract(ttl(body))

    def test_blank_node_property_list(self):
        self.assertRaisesTurtle(TWO_KINDS + "wlc:chore skos:note [ skos:prefLabel \"x\" ] .\n")

    def test_bare_boolean_literal(self):
        self.assertRaisesTurtle(TWO_KINDS + "wlc:bug skos:note true .\n")

    def test_sparql_style_prefix(self):
        self.assertRaisesTurtle(TWO_KINDS + "PREFIX ex: <http://example.org/>\n")

    def test_missing_wlc_prefix(self):
        with self.assertRaises(nsgen.TurtleError) as cm:
            nsgen.extract("@prefix skos: <http://www.w3.org/2004/02/skos/core#> .\n")
        self.assertIn("@prefix wlc:", str(cm.exception))


class TestCli(unittest.TestCase):
    def run_nsgen(self, *args):
        return subprocess.run(
            [sys.executable, str(ROOT / "scripts" / "nsgen.py"), *args],
            capture_output=True, text=True,
        )

    def test_check_is_clean_against_the_committed_file(self):
        r = self.run_nsgen("--check")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_generated_file_is_byte_identical_to_a_regenerate(self):
        want = nsgen.render(*nsgen.extract(CONCEPT_TTL.read_text(encoding="utf-8")))
        self.assertEqual((ROOT / "internal" / "ns" / "gen.go").read_text(encoding="utf-8"), want)


if __name__ == "__main__":
    unittest.main()
