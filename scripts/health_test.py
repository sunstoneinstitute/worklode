#!/usr/bin/env python3
"""Hermetic tests for scripts/health.py.

Every source is now the worklode backbone, so `health.run_lode` -- the one
place the script shells out -- is stubbed with recorded JSON in the exact wire
shapes of `internal/model`. Nothing here starts a server or reads a file.

FakeLode records its calls, because which documents the script fetches in full
is part of the contract: `lode doc show` is one round trip per document, and a
--needs-planning row already carries the section count a spec would otherwise
be fetched for.
"""

import sys
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import health  # noqa: E402

NOW = datetime(2026, 8, 19, 12, 0, 0, tzinfo=timezone.utc)


def ago(days):
    return (NOW - timedelta(days=days)).isoformat().replace("+00:00", "Z")


# --- wire fixtures -----------------------------------------------------


def doc(doc_id, kind, slug, status="accepted", updated=None):
    return {
        "id": doc_id, "project": "worklode", "kind": kind,
        "number": doc_id if kind == "spec" else 0, "slug": slug, "title": slug,
        "body": "",  # blanked on every list response, as the server does
        "status": status, "version": 1, "issued": "", "owner": "",
        "created_by": "stig", "created_at": ago(30),
        "updated_at": updated or ago(5),
    }


def spec_doc(doc_id, slug, **kw):
    return doc(doc_id, "spec", slug, **kw)


def plan_doc(doc_id, slug, **kw):
    return doc(doc_id, "plan", slug, **kw)


def detail(doc_id, sections=0, covers=()):
    """A `lode doc show` response.

    `covers` is one entry per coverage edge: an int is a resolved document, a
    string is the unresolvable reference the backbone puts in `to_external`.
    """
    edges = []
    for target in covers:
        external = isinstance(target, str)
        edges.append({
            "type": "covers", "from_anchor": "",
            "to_doc": 0 if external else target, "to_anchor": "",
            "to_external": target if external else "",
        })
    return {
        "id": doc_id,
        "sections": [
            {"anchor": f"sec-{i}", "number": str(i), "heading": f"h{i}",
             "depth": 2, "position": i, "last_revised_in": 1, "published": True}
            for i in range(sections)
        ],
        "edges": edges, "edges_in": [], "revision": None,
    }


def gap(doc_id, sections, *coverages):
    """A --needs-planning row: one `coverages` entry per undischarged section."""
    return {
        "doc": doc_id, "sections": sections,
        "gaps": [{"anchor": f"sec-{i}", "coverage": c}
                 for i, c in enumerate(coverages)],
    }


def task(tid, state, kind="feature", created=None, plan=0):
    row = {
        "id": tid, "project": "worklode", "state": state, "kind": kind,
        "title": f"task {tid}", "body": "", "priority": "medium",
        "created_at": created or ago(10), "updated_at": ago(1),
    }
    if plan:
        row["plan_doc"] = plan  # omitempty on the wire, absent when unminted
    return row


def state_entry(at, new, old="ready"):
    return {"at": at, "type": "state", "change": {"field": "state", "new": new, "old": old}}


class FakeLode:
    """A recorded `lode` standing in for health.run_lode."""

    def __init__(self, specs=(), plans=(), gaps=(), details=(), tasks=(), timelines=None):
        self.specs, self.plans, self.gaps = list(specs), list(plans), list(gaps)
        self.details = {d["id"]: d for d in details}
        self.tasks, self.timelines = list(tasks), timelines or {}
        self.calls = []

    def __call__(self, args):
        self.calls.append(list(args))
        if args[:2] == ["doc", "list"]:
            if "--needs-planning" in args:
                return {"docs": [], "planning_gaps": self.gaps}
            kind = args[args.index("--kind") + 1]
            return {"docs": self.specs if kind == "spec" else self.plans}
        if args[:2] == ["doc", "show"]:
            return self.details.get(int(args[2])) or detail(int(args[2]))
        if args[:2] == ["task", "list"]:
            return {"tasks": self.tasks}
        if args[:2] == ["task", "timeline"]:
            return {"timeline": self.timelines.get(args[2], [])}
        raise AssertionError(f"unexpected lode call: {args}")

    def fetched(self):
        """The document ids pulled in full, which is the cost this script pays."""
        return sorted(int(a[2]) for a in self.calls if a[:2] == ["doc", "show"])


def collect(fake, project=None):
    with mock.patch.object(health, "run_lode", fake):
        return health.collect(project)


# --- tests -------------------------------------------------------------


class LoadTest(unittest.TestCase):
    def test_accepted_spec_with_no_gap_row_is_fully_planned(self):
        fake = FakeLode(specs=[spec_doc(1, "001-a")], details=[detail(1, sections=3)])
        specs, _, _, _ = collect(fake)
        self.assertEqual((specs[1]["sections"], specs[1]["unplanned"]), (3, 0))

    def test_gap_row_supplies_both_the_total_and_the_gaps(self):
        fake = FakeLode(specs=[spec_doc(1, "001-a")],
                        gaps=[gap(1, 4, "unplanned", "partial")])
        specs, _, _, _ = collect(fake)
        self.assertEqual((specs[1]["sections"], specs[1]["unplanned"]), (4, 2))
        self.assertEqual(dict(specs[1]["reasons"]), {"unplanned": 1, "partial": 1})

    def test_bound_only_is_a_gap_not_coverage(self):
        """The backbone's replacement for parsing `coverage: none`: a plan that
        declared it does not discharge the section leaves the section open."""
        fake = FakeLode(specs=[spec_doc(1, "001-a")], gaps=[gap(1, 2, "bound-only")])
        specs, _, _, _ = collect(fake)
        self.assertEqual(specs[1]["unplanned"], 1)
        self.assertEqual(dict(specs[1]["reasons"]), {"bound-only": 1})

    def test_a_gap_row_spares_the_doc_get(self):
        fake = FakeLode(specs=[spec_doc(1, "001-a"), spec_doc(2, "002-b")],
                        gaps=[gap(1, 4, "unplanned")])
        collect(fake)
        self.assertEqual(fake.fetched(), [2])  # 1's count came with its gap row

    def test_draft_spec_counts_every_section_unplanned(self):
        """--needs-planning is defined over accepted specs (026 §2.1), so a
        draft's sections are unplanned by construction; dropping them from the
        denominator would flatter a corpus as it accumulates unaccepted design."""
        fake = FakeLode(specs=[spec_doc(1, "001-a", status="draft")],
                        details=[detail(1, sections=2)])
        specs, _, _, _ = collect(fake)
        self.assertEqual((specs[1]["sections"], specs[1]["unplanned"]), (2, 2))

    def test_superseded_spec_is_not_in_the_denominator(self):
        fake = FakeLode(
            specs=[spec_doc(1, "001-a", status="superseded"), spec_doc(2, "002-b")],
            details=[detail(2, sections=1)],
        )
        specs, _, _, _ = collect(fake)
        self.assertEqual(list(specs), [2])
        self.assertEqual(fake.fetched(), [2])  # nor is it fetched

    def test_superseded_plan_is_listed_but_not_fetched(self):
        fake = FakeLode(plans=[plan_doc(10, "p", status="superseded")])
        _, plans, _, _ = collect(fake)
        self.assertEqual(plans[10]["covers"], [])
        self.assertEqual(fake.fetched(), [])

    def test_covers_edges_become_the_plan_grouping(self):
        fake = FakeLode(plans=[plan_doc(10, "p")], details=[detail(10, covers=(1, 1, 2))])
        _, plans, _, _ = collect(fake)
        self.assertEqual(plans[10]["covers"], [1, 1, 2])

    def test_unresolvable_reference_is_not_a_coverage_target(self):
        """`covers: NO-SPEC` arrives as to_external with no to_doc: it names no
        document, so it groups no front."""
        fake = FakeLode(plans=[plan_doc(10, "p")], details=[detail(10, covers=("NO-SPEC",))])
        _, plans, _, _ = collect(fake)
        self.assertEqual(plans[10]["covers"], [])
        self.assertIsNone(health.spec_of_plan(plans[10]))

    def test_project_scoping_is_passed_through(self):
        fake = FakeLode()
        collect(fake, project="worklode")
        for args in fake.calls:
            if args[:2] in (["doc", "list"], ["task", "list"]):
                self.assertEqual(args[-2:], ["--project", "worklode"], args)


class SpecOfPlanTest(unittest.TestCase):
    def test_plan_belongs_to_the_document_it_references_most(self):
        self.assertEqual(health.spec_of_plan({"covers": [1, 1, 2]}), 1)

    def test_ties_break_on_doc_id_so_grouping_is_stable(self):
        self.assertEqual(health.spec_of_plan({"covers": [2, 1]}), 1)

    def test_plan_covering_nothing_has_no_group(self):
        self.assertIsNone(health.spec_of_plan({"covers": []}))


class PlanLinkageTest(unittest.TestCase):
    def test_plan_is_linked_by_the_task_it_minted(self):
        plans = {10: {"covers": [1]}, 11: {"covers": [1]}}
        tasks = [task("WL-1", "ready", plan=10), task("WL-2", "ready")]
        by_plan, linked = health.plan_task_counts(plans, tasks)
        self.assertEqual(linked, 1)
        self.assertEqual([t["id"] for t in by_plan[10]], ["WL-1"])
        self.assertNotIn(11, by_plan)

    def test_a_task_minted_by_a_filtered_out_plan_still_counts_as_linked(self):
        """Superseded plans are dropped before this runs; the task they minted
        is not unlinked work, so the link rate must not pretend it is."""
        by_plan, linked = health.plan_task_counts({}, [task("WL-1", "ready", plan=99)])
        self.assertEqual((dict(by_plan), linked), ({}, 1))


class FrontsTest(unittest.TestCase):
    """An open front is a spec begun and not closed. The distinction that
    matters is started-vs-untouched, not done-vs-not: a spec with 5 queued
    tasks and nothing begun costs nothing yet."""

    def plans(self, mapping):
        """{plan doc id: [covered doc ids]} -> the loaded-plan shape fronts() takes."""
        return {pid: {"status": "accepted", "covers": covers}
                for pid, covers in mapping.items()}

    def test_started_and_unfinished_is_an_open_front(self):
        plans = self.plans({10: [1]})
        tasks = [task("WL-1", "merged", plan=10), task("WL-2", "ready", plan=10)]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"], [{"spec": 1, "done": 1, "left": 1}])
        self.assertEqual(got["untouched_count"], 0)

    def test_open_fronts_are_named_when_names_are_given(self):
        plans = self.plans({10: [1]})
        tasks = [task("WL-1", "merged", plan=10), task("WL-2", "ready", plan=10)]
        got = health.classify_fronts(health.fronts(plans, tasks), {1: "001-a"})
        self.assertEqual(got["open"][0]["spec"], "001-a")

    def test_in_flight_alone_opens_a_front(self):
        """Work in progress counts as begun even with nothing finished."""
        plans = self.plans({10: [1]})
        tasks = [task("WL-1", "in_progress", plan=10), task("WL-2", "ready", plan=10)]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(len(got["open"]), 1)
        self.assertEqual(got["open"][0]["left"], 2)  # in-flight is still unfinished

    def test_queued_only_spec_is_untouched_not_a_front(self):
        plans = self.plans({10: [1]})
        tasks = [task("WL-1", "ready", plan=10), task("WL-2", "ready", plan=10)]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"], [])
        self.assertEqual(got["untouched_count"], 1)

    def test_fully_done_spec_is_finished_not_a_front(self):
        got = health.classify_fronts(
            health.fronts(self.plans({10: [1]}), [task("WL-1", "merged", plan=10)]))
        self.assertEqual(got["open"], [])
        self.assertEqual(got["finished_count"], 1)

    def test_plans_in_a_series_share_one_front(self):
        """Two plans covering the same spec are one front, not two -- the
        whole point of grouping by spec rather than by plan."""
        plans = self.plans({10: [1], 11: [1]})
        tasks = [task("WL-1", "merged", plan=10), task("WL-2", "ready", plan=11)]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"], [{"spec": 1, "done": 1, "left": 1}])

    def test_abandoned_tasks_neither_open_nor_close_a_front(self):
        plans = self.plans({10: [1]})
        self.assertEqual(health.fronts(plans, [task("WL-1", "abandoned", plan=10)]), {})

    def test_tasks_with_no_plan_are_excluded(self):
        self.assertEqual(health.fronts(self.plans({10: [1]}), [task("WL-1", "ready")]), {})

    def test_worst_case_is_open_plus_untouched(self):
        plans = self.plans({10: [1], 11: [2]})
        tasks = [task("WL-1", "merged", plan=10), task("WL-2", "ready", plan=10),
                 task("WL-3", "ready", plan=11)]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual((len(got["open"]), got["untouched_count"]), (1, 1))
        self.assertEqual(got["worst_case"], 2)


class CompletionTimeTest(unittest.TestCase):
    def test_state_log_wins_over_updated_at(self):
        tasks = [task("WL-1", "merged")]
        timelines = {"WL-1": [
            state_entry(ago(9), "in_progress"),
            state_entry(ago(5), "merged", "in_review"),
        ]}
        done_at, fallback = health.completion_times(tasks, timelines)
        self.assertEqual(fallback, 0)
        self.assertEqual(done_at["WL-1"], health.parse_time(ago(5)))

    def test_first_done_transition_wins_over_later_ones(self):
        tasks = [task("WL-1", "deployed_prod")]
        timelines = {"WL-1": [
            state_entry(ago(6), "merged"),
            state_entry(ago(2), "deployed_prod", "merged"),
        ]}
        done_at, _ = health.completion_times(tasks, timelines)
        self.assertEqual(done_at["WL-1"], health.parse_time(ago(6)))

    def test_missing_timeline_falls_back_and_is_counted(self):
        done_at, fallback = health.completion_times([task("WL-1", "merged")], {})
        self.assertEqual(fallback, 1)
        self.assertEqual(done_at["WL-1"], health.parse_time(ago(1)))

    def test_unfinished_tasks_have_no_completion_time(self):
        done_at, _ = health.completion_times([task("WL-1", "ready")], {})
        self.assertEqual(done_at, {})


class StageTest(unittest.TestCase):
    def test_states_map_to_stages(self):
        for state, stage in [
            ("ready", "queued"), ("draft", "queued"),
            ("in_progress", "in_flight"), ("in_review", "in_flight"),
            ("merged", "done"), ("deployed_dev", "done"), ("released", "done"),
            ("abandoned", "abandoned"),
        ]:
            self.assertEqual(health.stage_of(state), stage, state)


class MetricsTest(unittest.TestCase):
    """One accepted spec with 2 of 4 sections unplanned, one live plan, one
    dormant plan and one draft plan -- enough corpus for every stage to have a
    non-trivial value."""

    SPECS = [spec_doc(1, "001-a")]
    GAPS = [gap(1, 4, "unplanned", "unplanned")]
    PLANS = [plan_doc(10, "2026-01-01-live", updated=ago(4)),
             plan_doc(11, "2026-01-02-dormant", updated=ago(9)),
             plan_doc(12, "2026-01-03-draft", status="draft", updated=ago(2))]
    DETAILS = [detail(10, covers=(1,)), detail(11, covers=(1,)),
               detail(12, covers=("NO-SPEC",))]

    def report(self, tasks, timelines=None, weeks=2):
        fake = FakeLode(specs=self.SPECS, plans=self.PLANS, gaps=self.GAPS,
                        details=self.DETAILS, tasks=tasks, timelines=timelines or {})
        specs, plans, tasks, timelines = collect(fake)
        return health.build_report(specs, plans, tasks, timelines, NOW, weeks)

    def test_stage_counts_and_conversion(self):
        tasks = [
            task("WL-1", "merged", plan=10),
            task("WL-2", "ready"),
            task("WL-3", "in_review"),
            task("WL-4", "abandoned"),
        ]
        r = self.report(tasks, {"WL-1": [state_entry(ago(3), "merged")]})

        self.assertEqual(r["stages"]["unplanned_sections"], 2)
        self.assertEqual(r["stages"]["total_sections"], 4)
        self.assertEqual(r["stages"]["unplanned_sections_by_coverage"], {"unplanned": 2})
        self.assertEqual(r["stages"]["plans_awaiting_acceptance"], 1)
        self.assertEqual(r["stages"]["accepted_plans_without_tasks"], 1)
        self.assertEqual(r["stages"]["tasks_queued"], 1)
        self.assertEqual(r["stages"]["tasks_in_flight"], 1)

        self.assertAlmostEqual(r["conversion"]["sections_planned_pct"], 50.0)
        self.assertAlmostEqual(r["conversion"]["plans_activated_pct"], 50.0)
        self.assertAlmostEqual(r["conversion"]["tasks_completed_pct"], 100 / 3)

    def test_abandoned_is_excluded_from_completion_but_has_its_own_rate(self):
        tasks = [task("WL-1", "merged"), task("WL-2", "abandoned"), task("WL-3", "ready")]
        r = self.report(tasks, {"WL-1": [state_entry(ago(3), "merged")]})
        # completion is done / (done + unfinished): abandoned is in neither.
        self.assertAlmostEqual(r["conversion"]["tasks_completed_pct"], 50.0)
        self.assertAlmostEqual(r["flow"]["abandonment_pct"], 50.0)

    def test_throughput_counts_only_the_trailing_window(self):
        tasks = [task("WL-1", "merged"), task("WL-2", "merged")]
        timelines = {
            "WL-1": [state_entry(ago(3), "merged")],
            "WL-2": [state_entry(ago(40), "merged")],  # outside 28d
        }
        r = self.report(tasks, timelines)
        self.assertAlmostEqual(r["flow"]["throughput_per_week"], 0.25)  # 1 / 4 weeks

    def test_runway_is_unfinished_over_throughput(self):
        tasks = [task("WL-1", "merged")] + [task(f"WL-{i}", "ready") for i in range(2, 6)]
        r = self.report(tasks, {"WL-1": [state_entry(ago(3), "merged")]})
        self.assertAlmostEqual(r["flow"]["throughput_per_week"], 0.25)
        self.assertAlmostEqual(r["flow"]["runway_weeks"], 16.0)  # 4 queued / 0.25

    def test_runway_is_none_when_nothing_ships(self):
        self.assertIsNone(self.report([task("WL-1", "ready")])["flow"]["runway_weeks"])

    def test_lead_time_measures_created_to_done(self):
        r = self.report([task("WL-1", "merged", created=ago(10))],
                        {"WL-1": [state_entry(ago(4), "merged")]})
        self.assertAlmostEqual(r["flow"]["lead_time_days_median"], 6.0, places=3)

    def test_kind_mix_and_upkeep_share(self):
        tasks = [
            task("WL-1", "ready", kind="feature"),
            task("WL-2", "ready", kind="bug"),
            task("WL-3", "ready", kind="chore"),
            task("WL-4", "merged", kind="feature"),
        ]
        r = self.report(tasks, {"WL-4": [state_entry(ago(2), "merged")]})
        rows = {row["kind"]: row for row in r["kinds"]}
        self.assertEqual(rows["bug"]["unfinished"], 1)
        self.assertEqual(rows["feature"]["unfinished"], 1)
        self.assertEqual(rows["feature"]["completed_28d"], 1)
        # 2 of 3 unfinished are upkeep; 0 of 1 completed is.
        self.assertAlmostEqual(r["upkeep_share_pct"]["unfinished"], 200 / 3)
        self.assertAlmostEqual(r["upkeep_share_pct"]["completed_28d"], 0.0)

    def test_net_flow_is_positive_when_a_kind_grows(self):
        tasks = [
            task("WL-1", "ready", kind="bug", created=ago(3)),
            task("WL-2", "ready", kind="bug", created=ago(2)),
            task("WL-3", "merged", kind="bug", created=ago(20)),
        ]
        r = self.report(tasks, {"WL-3": [state_entry(ago(1), "merged")]})
        bug = next(row for row in r["kinds"] if row["kind"] == "bug")
        self.assertEqual((bug["created_28d"], bug["completed_28d"], bug["net_28d"]), (3, 1, 2))

    def test_unknown_kinds_are_read_from_the_data(self):
        """The `spec` -> `design` rename must not need a code change."""
        r = self.report([task("WL-1", "ready", kind="design"),
                         task("WL-2", "ready", kind="spec")])
        self.assertEqual({row["kind"] for row in r["kinds"]}, {"design", "spec"})
        self.assertAlmostEqual(r["upkeep_share_pct"]["unfinished"], 0.0)

    def test_weekly_trend_buckets_by_week(self):
        tasks = [task("WL-1", "ready", created=ago(2)),
                 task("WL-2", "merged", created=ago(9))]
        r = self.report(tasks, {"WL-2": [state_entry(ago(2), "merged")]}, weeks=2)
        self.assertEqual([row["created"] for row in r["trend"]], [1, 1])
        self.assertEqual([row["completed"] for row in r["trend"]], [0, 1])

    def test_stalls_rank_specs_by_unplanned_section_count(self):
        r = self.report([task("WL-1", "ready")])
        self.assertEqual(r["stalls"]["unplanned_specs"][0],
                         {"doc": "001-a", "unplanned_sections": 2,
                          "by_coverage": {"unplanned": 2}})

    def test_stalls_age_documents_by_updated_at(self):
        r = self.report([task("WL-1", "merged", plan=10)],
                        {"WL-1": [state_entry(ago(1), "merged")]})
        self.assertEqual([row["doc"] for row in r["stalls"]["dormant_plans"]],
                         ["2026-01-02-dormant"])
        self.assertEqual(r["stalls"]["dormant_plans"][0]["touched"], ago(9)[:10])
        self.assertEqual([row["doc"] for row in r["stalls"]["draft_plans"]],
                         ["2026-01-03-draft"])

    def test_report_survives_having_no_tasks_at_all(self):
        r = self.report([])
        self.assertEqual(r["stages"]["tasks_queued"], 0)
        self.assertIsNone(r["flow"]["runway_weeks"])
        self.assertIsNone(r["conversion"]["tasks_completed_pct"])
        self.assertEqual(r["kinds"], [])

    def test_renders_without_error_including_none_valued_metrics(self):
        import io
        out = io.StringIO()
        health.render(self.report([]), out)
        self.assertIn("UNFINISHED WORK BY STAGE", out.getvalue())
        self.assertIn("n/a", out.getvalue())

    def test_report_is_json_serialisable(self):
        import json
        json.dumps(self.report([task("WL-1", "merged", plan=10)]), default=str)


if __name__ == "__main__":
    unittest.main()
