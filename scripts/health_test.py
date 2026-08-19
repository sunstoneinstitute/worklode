#!/usr/bin/env python3
"""Hermetic tests for scripts/health.py.

Everything computable is a pure function over loaded data, so the tests build
synthetic specs, plans and task rows and never touch a server or a real repo.
The subprocess wrappers (fetch_tasks, fetch_timelines, git_dates) are the only
untested surface, deliberately: they are three lines each and a fake `lode`
would test the fake.
"""

import sys
import tempfile
import textwrap
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import health  # noqa: E402

NOW = datetime(2026, 8, 19, 12, 0, 0, tzinfo=timezone.utc)


def ago(days):
    return (NOW - timedelta(days=days)).isoformat().replace("+00:00", "Z")


def task(tid, state, kind="feature", created=None, body=""):
    return {
        "id": tid,
        "state": state,
        "kind": kind,
        "title": f"task {tid}",
        "body": body,
        "created_at": created or ago(10),
        "updated_at": ago(1),
    }


def state_entry(at, new, old="ready"):
    return {"at": at, "type": "state", "change": {"field": "state", "new": new, "old": old}}


class RepoFixture:
    """A throwaway docs/ tree; specs and plans are written as real files
    because loading them is half of what is under test."""

    def __init__(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        (self.root / "docs" / "specs").mkdir(parents=True)
        (self.root / "docs" / "plans").mkdir(parents=True)

    def spec(self, name, status="draft", sections=("sec-0", "sec-1")):
        body = "\n".join(
            f"## {i}. Heading {i} {{#{anchor}}}\n\nprose\n"
            for i, anchor in enumerate(sections)
        )
        (self.root / "docs" / "specs" / name).write_text(
            f"---\nstatus: {status}\n---\n# Spec — t\n\n{body}"
        )

    def plan(self, name, frontmatter):
        (self.root / "docs" / "plans" / name).write_text(
            f"---\n{textwrap.dedent(frontmatter).strip()}\n---\n# Plan\n\nprose\n"
        )

    def close(self):
        self.dir.cleanup()


class CoverageTest(unittest.TestCase):
    def setUp(self):
        self.repo = RepoFixture()
        self.addCleanup(self.repo.close)

    def load(self):
        return health.load_specs(self.repo.root), health.load_plans(self.repo.root)

    def test_bare_reference_covers_every_section(self):
        self.repo.spec("001-a.md", sections=("sec-0", "sec-1", "sec-2"))
        self.repo.plan("2026-01-01-p.md", "status: accepted\ncovers: docs/specs/001-a.md")
        specs, plans = self.load()
        self.assertEqual(
            health.covered_sections(specs, plans)["docs/specs/001-a.md"],
            {"sec-0", "sec-1", "sec-2"},
        )

    def test_anchored_reference_covers_only_that_section(self):
        self.repo.spec("001-a.md", sections=("sec-0", "sec-1"))
        self.repo.plan("2026-01-01-p.md", """
            status: accepted
            covers:
              - docs/specs/001-a.md#sec-1
        """)
        specs, plans = self.load()
        self.assertEqual(
            health.covered_sections(specs, plans)["docs/specs/001-a.md"], {"sec-1"}
        )

    def test_coverage_none_is_declared_debt_not_coverage(self):
        """The divergence from find_gaps.py: `none` means a plan looked and
        decided not to cover, which is exactly the gap this report reports."""
        self.repo.spec("001-a.md", sections=("sec-0", "sec-1"))
        self.repo.plan("2026-01-01-p.md", """
            status: accepted
            covers:
              - spec: docs/specs/001-a.md#sec-0
                coverage: full
              - spec: docs/specs/001-a.md#sec-1
                coverage: none
        """)
        specs, plans = self.load()
        self.assertEqual(
            health.covered_sections(specs, plans)["docs/specs/001-a.md"], {"sec-0"}
        )

    def test_partial_coverage_counts_as_covered(self):
        self.repo.spec("001-a.md", sections=("sec-0",))
        self.repo.plan("2026-01-01-p.md", """
            status: accepted
            covers:
              - spec: docs/specs/001-a.md#sec-0
                coverage: partial
        """)
        specs, plans = self.load()
        self.assertEqual(
            health.covered_sections(specs, plans)["docs/specs/001-a.md"], {"sec-0"}
        )

    def test_superseded_plan_covers_nothing(self):
        self.repo.spec("001-a.md", sections=("sec-0",))
        self.repo.plan("2026-01-01-p.md", "status: superseded\ncovers: docs/specs/001-a.md")
        specs, plans = self.load()
        self.assertEqual(health.covered_sections(specs, plans), {})

    def test_superseded_spec_is_not_in_the_denominator(self):
        self.repo.spec("001-a.md", status="superseded", sections=("sec-0", "sec-1"))
        self.repo.spec("002-b.md", sections=("sec-0",))
        specs, _ = self.load()
        self.assertEqual(list(specs), ["docs/specs/002-b.md"])

    def test_no_spec_sentinel_is_not_a_reference(self):
        self.repo.spec("001-a.md", sections=("sec-0",))
        self.repo.plan("2026-01-01-p.md", "status: accepted\ncovers: NO-SPEC")
        specs, plans = self.load()
        self.assertEqual(health.covered_sections(specs, plans), {})

    def test_malformed_frontmatter_does_not_crash_the_load(self):
        (self.repo.root / "docs" / "plans" / "2026-01-01-bad.md").write_text(
            "---\nstatus: {status}\ncovers: [\n---\n# Plan\n"
        )
        _, plans = self.load()
        self.assertEqual(plans["docs/plans/2026-01-01-bad.md"]["entries"], [])


class PlanLinkageTest(unittest.TestCase):
    def test_plan_is_linked_by_a_path_in_a_task_body(self):
        plans = {"docs/plans/2026-01-01-p.md": {}, "docs/plans/2026-01-02-q.md": {}}
        tasks = [
            task("WL-1", "ready", body="see docs/plans/2026-01-01-p.md for detail"),
            task("WL-2", "ready", body="no reference here"),
        ]
        by_plan, linked = health.plan_task_counts(plans, tasks)
        self.assertEqual(linked, 1)
        self.assertEqual([t["id"] for t in by_plan["docs/plans/2026-01-01-p.md"]], ["WL-1"])
        self.assertNotIn("docs/plans/2026-01-02-q.md", by_plan)


class FrontsTest(unittest.TestCase):
    """An open front is a spec begun and not closed. The distinction that
    matters is started-vs-untouched, not done-vs-not: a spec with 5 queued
    tasks and nothing begun costs nothing yet."""

    def plans(self, mapping):
        """{plan path: [spec refs]} -> the loaded-plan shape fronts() takes."""
        return {
            path: {"status": "accepted",
                   "entries": [(ref, True, "full", []) for ref in refs]}
            for path, refs in mapping.items()
        }

    def test_spec_of_plan_takes_the_most_referenced_spec(self):
        plan = {"entries": [(r, True, "full", []) for r in (
            "docs/specs/001-a.md#sec-1", "docs/specs/001-a.md#sec-2",
            "docs/specs/002-b.md#sec-1")]}
        self.assertEqual(health.spec_of_plan(plan), "docs/specs/001-a.md")

    def test_started_and_unfinished_is_an_open_front(self):
        plans = self.plans({"docs/plans/p.md": ["docs/specs/001-a.md"]})
        tasks = [task("WL-1", "merged", body="docs/plans/p.md"),
                 task("WL-2", "ready", body="docs/plans/p.md")]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"],
                         [{"spec": "docs/specs/001-a.md", "done": 1, "left": 1}])
        self.assertEqual(got["untouched_count"], 0)

    def test_in_flight_alone_opens_a_front(self):
        """Work in progress counts as begun even with nothing finished."""
        plans = self.plans({"docs/plans/p.md": ["docs/specs/001-a.md"]})
        tasks = [task("WL-1", "in_progress", body="docs/plans/p.md"),
                 task("WL-2", "ready", body="docs/plans/p.md")]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(len(got["open"]), 1)
        self.assertEqual(got["open"][0]["left"], 2)  # in-flight is still unfinished

    def test_queued_only_spec_is_untouched_not_a_front(self):
        plans = self.plans({"docs/plans/p.md": ["docs/specs/001-a.md"]})
        tasks = [task("WL-1", "ready", body="docs/plans/p.md"),
                 task("WL-2", "ready", body="docs/plans/p.md")]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"], [])
        self.assertEqual(got["untouched_count"], 1)

    def test_fully_done_spec_is_finished_not_a_front(self):
        plans = self.plans({"docs/plans/p.md": ["docs/specs/001-a.md"]})
        tasks = [task("WL-1", "merged", body="docs/plans/p.md")]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"], [])
        self.assertEqual(got["finished_count"], 1)

    def test_plans_in_a_series_share_one_front(self):
        """Two plans covering the same spec are one front, not two -- the
        whole point of grouping by spec rather than by plan."""
        plans = self.plans({
            "docs/plans/p1.md": ["docs/specs/001-a.md"],
            "docs/plans/p2.md": ["docs/specs/001-a.md"],
        })
        tasks = [task("WL-1", "merged", body="docs/plans/p1.md"),
                 task("WL-2", "ready", body="docs/plans/p2.md")]
        got = health.classify_fronts(health.fronts(plans, tasks))
        self.assertEqual(got["open"],
                         [{"spec": "docs/specs/001-a.md", "done": 1, "left": 1}])

    def test_abandoned_tasks_neither_open_nor_close_a_front(self):
        plans = self.plans({"docs/plans/p.md": ["docs/specs/001-a.md"]})
        tasks = [task("WL-1", "abandoned", body="docs/plans/p.md")]
        self.assertEqual(health.fronts(plans, tasks), {})

    def test_tasks_with_no_plan_are_excluded(self):
        plans = self.plans({"docs/plans/p.md": ["docs/specs/001-a.md"]})
        self.assertEqual(health.fronts(plans, [task("WL-1", "ready")]), {})

    def test_worst_case_is_open_plus_untouched(self):
        plans = self.plans({
            "docs/plans/p1.md": ["docs/specs/001-a.md"],
            "docs/plans/p2.md": ["docs/specs/002-b.md"],
        })
        tasks = [task("WL-1", "merged", body="docs/plans/p1.md"),
                 task("WL-2", "ready", body="docs/plans/p1.md"),
                 task("WL-3", "ready", body="docs/plans/p2.md")]
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
        tasks = [task("WL-1", "merged")]
        done_at, fallback = health.completion_times(tasks, {})
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
    def setUp(self):
        self.repo = RepoFixture()
        self.addCleanup(self.repo.close)
        self.repo.spec("001-a.md", sections=("sec-0", "sec-1", "sec-2", "sec-3"))
        self.repo.plan("2026-01-01-live.md", """
            status: accepted
            covers:
              - docs/specs/001-a.md#sec-0
        """)
        self.repo.plan("2026-01-02-dormant.md", """
            status: accepted
            covers:
              - docs/specs/001-a.md#sec-1
        """)
        self.repo.plan("2026-01-03-draft.md", "status: draft\ncovers: NO-SPEC")

    def report(self, tasks, timelines=None, weeks=2):
        return health.build_report(
            self.repo.root,
            health.load_specs(self.repo.root),
            health.load_plans(self.repo.root),
            tasks, timelines or {}, NOW, weeks,
        )

    def test_stage_counts_and_conversion(self):
        tasks = [
            task("WL-1", "merged", body="docs/plans/2026-01-01-live.md"),
            task("WL-2", "ready"),
            task("WL-3", "in_review"),
            task("WL-4", "abandoned"),
        ]
        r = self.report(tasks, {"WL-1": [state_entry(ago(3), "merged")]})

        self.assertEqual(r["stages"]["unplanned_sections"], 2)  # sec-2, sec-3
        self.assertEqual(r["stages"]["total_sections"], 4)
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
        r = self.report([task("WL-1", "ready")])
        self.assertIsNone(r["flow"]["runway_weeks"])

    def test_lead_time_measures_created_to_done(self):
        tasks = [task("WL-1", "merged", created=ago(10))]
        r = self.report(tasks, {"WL-1": [state_entry(ago(4), "merged")]})
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
        self.assertEqual(bug["created_28d"], 3)
        self.assertEqual(bug["completed_28d"], 1)
        self.assertEqual(bug["net_28d"], 2)

    def test_unknown_kinds_are_read_from_the_data(self):
        """The `spec` -> `design` rename must not need a code change."""
        r = self.report([task("WL-1", "ready", kind="design"),
                         task("WL-2", "ready", kind="spec")])
        self.assertEqual({row["kind"] for row in r["kinds"]}, {"design", "spec"})
        self.assertAlmostEqual(r["upkeep_share_pct"]["unfinished"], 0.0)

    def test_weekly_trend_buckets_by_week(self):
        tasks = [
            task("WL-1", "ready", created=ago(2)),
            task("WL-2", "merged", created=ago(9)),
        ]
        r = self.report(tasks, {"WL-2": [state_entry(ago(2), "merged")]}, weeks=2)
        self.assertEqual([row["created"] for row in r["trend"]], [1, 1])
        self.assertEqual([row["completed"] for row in r["trend"]], [0, 1])

    def test_stalls_rank_specs_by_unplanned_section_count(self):
        r = self.report([task("WL-1", "ready")])
        self.assertEqual(r["stalls"]["unplanned_specs"][0],
                         {"file": "docs/specs/001-a.md", "unplanned_sections": 2})

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


if __name__ == "__main__":
    unittest.main()
