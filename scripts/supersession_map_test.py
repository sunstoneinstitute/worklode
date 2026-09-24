#!/usr/bin/env python3
"""Tests for scripts/supersession_map.py.

Run: python3 scripts/supersession_map_test.py
"""

import sys
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import supersession_map as sm  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
SECTION_MAP = ROOT / "docs" / "specs2" / "section-map.tsv"
RESIDUE = ROOT / "docs" / "specs2" / "ttl" / "residue.tsv"


def row(old_ref, old_anchor, new_file, new_section, disposition, old_title="t", note=""):
    return {
        "old_ref": old_ref, "old_anchor": old_anchor, "old_title": old_title,
        "new_file": new_file, "new_section": new_section,
        "disposition": disposition, "note": note,
    }


def residue_row(old_ref, old_anchor, new_file, new_anchor, why="because"):
    return {
        "old_ref": old_ref, "old_anchor": old_anchor, "new_file": new_file,
        "new_anchor": new_anchor, "why": why,
    }


REFS = {"02-identity-actors-and-secrets": "WL-SPEC-80", "01-a": "WL-SPEC-90", "02-b": "WL-SPEC-91"}
ANCHORS = {"02-identity-actors-and-secrets": {"preamble": "sec-0", "open-questions": "sec-11"}}


class BuildMapTest(unittest.TestCase):
    def test_a_moved_row(self):
        rows = [row("WL-SPEC-1", "sec-10", "02-identity-actors-and-secrets.md", "9.4", "moved")]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual(lines, ["WL-SPEC-1#sec-10 -> WL-SPEC-80#sec-9.4"])

    def test_two_merged_rows_from_one_old_section_collapse_to_one_line(self):
        rows = [
            row("WL-SPEC-4", "sec-2", "01-a.md", "3", "merged"),
            row("WL-SPEC-4", "sec-2", "02-b.md", "5", "merged"),
        ]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual(lines, ["WL-SPEC-4#sec-2 -> WL-SPEC-90#sec-3 WL-SPEC-91#sec-5"])

    def test_a_dropped_history_row_withdraws_with_no_successor(self):
        rows = [row("WL-SPEC-1", "sec-1", "02-identity-actors-and-secrets.md", "-", "dropped-history")]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual(lines, ["WL-SPEC-1#sec-1 ->"])

    def test_a_residue_row_takes_its_target_from_residue_tsv(self):
        rows = [row("WL-SPEC-1", "sec-13", "02-identity-actors-and-secrets.md", "Open questions", "merged")]
        residue = [residue_row("WL-SPEC-1", "sec-13", "02-identity-actors-and-secrets.md", "open-questions")]
        lines = sm.build_map(rows, residue, REFS, ANCHORS)
        self.assertEqual(lines, ["WL-SPEC-1#sec-13 -> WL-SPEC-80#sec-11"])

    def test_a_residue_row_overrides_a_numbered_new_section(self):
        # WL-SPEC-29 sec-6.2 shape: section-map names a section the target
        # document does not have; residue.tsv's ruling wins regardless.
        rows = [row("WL-SPEC-29", "sec-6.2", "02-identity-actors-and-secrets.md", "13", "pointer")]
        residue = [residue_row("WL-SPEC-29", "sec-6.2", "02-identity-actors-and-secrets.md", "10")]
        lines = sm.build_map(rows, residue, REFS, None)
        self.assertEqual(lines, ["WL-SPEC-29#sec-6.2 -> WL-SPEC-80#sec-10"])

    def test_a_new_file_missing_from_refs_is_an_error_naming_it(self):
        rows = [row("WL-SPEC-1", "sec-2", "09-missing.md", "3", "moved")]
        with self.assertRaises(ValueError) as cm:
            sm.build_map(rows, [], {}, None)
        self.assertIn("09-missing.md", str(cm.exception))

    def test_a_residue_target_with_no_residue_tsv_row_is_an_error(self):
        rows = [row("WL-SPEC-9", "sec-9", "01-a.md", "Open questions", "merged")]
        with self.assertRaises(ValueError) as cm:
            sm.build_map(rows, [], REFS, None)
        self.assertIn("WL-SPEC-9#sec-9", str(cm.exception))

    def test_a_preamble_target_with_no_anchors_file_is_an_error(self):
        rows = [row("WL-SPEC-1", "sec-13", "02-identity-actors-and-secrets.md", "Open questions", "merged")]
        residue = [residue_row("WL-SPEC-1", "sec-13", "02-identity-actors-and-secrets.md", "open-questions")]
        with self.assertRaises(ValueError) as cm:
            sm.build_map(rows, residue, REFS, None)
        self.assertIn("--anchors", str(cm.exception))

    def test_a_comma_list_new_section_becomes_several_successors(self):
        rows = [row("WL-SPEC-45", "sec-4", "01-a.md", "8.7, 8.8", "moved")]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual(lines, ["WL-SPEC-45#sec-4 -> WL-SPEC-90#sec-8.7 WL-SPEC-90#sec-8.8"])

    def test_a_same_major_range_new_section_expands_in_order(self):
        rows = [row("WL-SPEC-45", "sec-1", "01-a.md", "8.1-8.4", "moved")]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual(
            lines,
            ["WL-SPEC-45#sec-1 -> WL-SPEC-90#sec-8.1 WL-SPEC-90#sec-8.2 "
             "WL-SPEC-90#sec-8.3 WL-SPEC-90#sec-8.4"],
        )

    def test_a_range_crossing_a_major_section_is_an_error(self):
        rows = [row("WL-SPEC-1", "sec-1", "01-a.md", "8.4-9.1", "moved")]
        with self.assertRaises(ValueError) as cm:
            sm.build_map(rows, [], REFS, None)
        self.assertIn("8.4-9.1", str(cm.exception))

    def test_intro_resolves_to_the_preamble_rule(self):
        rows = [row("WL-SPEC-8", "sec-0", "02-identity-actors-and-secrets.md", "intro", "merged")]
        lines = sm.build_map(rows, [], REFS, ANCHORS)
        self.assertEqual(lines, ["WL-SPEC-8#sec-0 -> WL-SPEC-80#sec-0"])

    def test_an_invalid_old_anchor_becomes_a_skip_comment_not_an_entry(self):
        # The WL-SPEC-62 shape: old_anchor is a note ("§2 (line 106)"), not a
        # real "sec-N" anchor. lode rule supersede resolves the whole map
        # in one transaction, so this row must never become a map entry.
        rows = [row("WL-SPEC-62", "§2 (line 106)", "01-a.md", "-", "dropped-other")]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual(len(lines), 1)
        self.assertTrue(lines[0].startswith("# skipped:"), lines[0])
        self.assertIn("WL-SPEC-62", lines[0])
        self.assertIn('"§2 (line 106)"', lines[0])
        self.assertIn("row 2", lines[0])

    def test_a_stem_missing_entirely_from_anchors_is_an_error_naming_the_stem(self):
        rows = [row("WL-SPEC-1", "sec-13", "02-identity-actors-and-secrets.md", "Open questions", "merged")]
        residue = [residue_row("WL-SPEC-1", "sec-13", "02-identity-actors-and-secrets.md", "open-questions")]
        with self.assertRaises(ValueError) as cm:
            sm.build_map(rows, residue, REFS, {})  # anchors present, but no entry for the stem
        self.assertIn("'02-identity-actors-and-secrets' is not in --anchors", str(cm.exception))

    def test_output_is_sorted_numerically_by_old_ref(self):
        rows = [
            row("WL-SPEC-10", "sec-1", "01-a.md", "1", "moved"),
            row("WL-SPEC-2", "sec-1", "01-a.md", "1", "moved"),
        ]
        lines = sm.build_map(rows, [], REFS, None)
        self.assertEqual([l.split(" ")[0] for l in lines], ["WL-SPEC-2#sec-1", "WL-SPEC-10#sec-1"])


class RealSectionMapTest(unittest.TestCase):
    """The 23 residue rows R11 decided plus the two pointer rows the A2 run
    found, as they actually sit in the real files."""

    def test_finds_exactly_the_25_residue_rows(self):
        section_rows = sm.read_tsv(SECTION_MAP)
        residue_rows = sm.read_tsv(RESIDUE)
        matches = sm.matched_residue_keys(section_rows, sm.residue_index(residue_rows))
        self.assertEqual(len(matches), 25)
        distinct_old_sections = {(m[0], m[1]) for m in matches}
        self.assertEqual(len(distinct_old_sections), 24)
        self.assertEqual(sum(1 for m in matches if m[:2] == ("WL-SPEC-8", "sec-1")), 2)


if __name__ == "__main__":
    unittest.main()
