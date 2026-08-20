#!/usr/bin/env python3
"""Hermetic tests for scripts/next_sim.py.

The part worth testing is the dynamic behaviour: claiming from an untouched
spec must turn it into an open front so the next pick stays there. A static
check of the sort key would miss exactly that.
"""

import io
import sys
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import next_sim  # noqa: E402


def task(tid, state="ready", priority="medium", created="2026-08-01T00:00:00Z", plan=0):
    row = {
        "id": tid, "state": state, "priority": priority, "kind": "feature",
        "title": f"task {tid}", "body": "", "created_at": created,
        "updated_at": created,
    }
    if plan:
        row["plan_doc"] = plan
    return row


def plan(covers, status="accepted"):
    """The loaded-plan shape health.build_plans produces: one `covers` entry
    per coverage edge, each the doc id the edge resolved to."""
    return {"status": status, "covers": list(covers)}


class SpecOfPlanTest(unittest.TestCase):
    def test_plan_belongs_to_the_spec_it_references_most(self):
        self.assertEqual(next_sim.spec_of_plan(plan([1, 1, 2])), 1)

    def test_ties_break_on_doc_id_so_grouping_is_stable(self):
        self.assertEqual(next_sim.spec_of_plan(plan([2, 1])), 1)

    def test_plan_covering_no_spec_has_no_group(self):
        self.assertIsNone(next_sim.spec_of_plan(plan([])))


class GroupTasksTest(unittest.TestCase):
    def test_a_minted_task_is_grouped_by_the_spec_its_plan_covers(self):
        group = next_sim.group_tasks({10: plan([1])}, [task("WL-1", plan=10)])
        self.assertEqual(group, {"WL-1": 1})

    def test_a_plan_covering_no_spec_groups_its_tasks_under_the_plan(self):
        group = next_sim.group_tasks({10: plan([])}, [task("WL-1", plan=10)])
        self.assertEqual(group, {"WL-1": "plan:10"})

    def test_a_task_no_plan_minted_is_a_front_of_one(self):
        group = next_sim.group_tasks({10: plan([1])}, [task("WL-1")])
        self.assertEqual(group, {"WL-1": "task:WL-1"})


class GroupStateTest(unittest.TestCase):
    def test_a_done_task_makes_its_spec_an_open_front(self):
        tasks = [task("WL-1", "merged"), task("WL-2")]
        group = {"WL-1": "S", "WL-2": "S"}
        started, remaining = next_sim.group_state(tasks, group, claimed=set())
        self.assertEqual(started, {"S"})
        self.assertEqual(remaining["S"], 1)  # the merged one is not remaining

    def test_an_untouched_spec_is_not_an_open_front(self):
        tasks = [task("WL-1"), task("WL-2")]
        group = {"WL-1": "S", "WL-2": "S"}
        started, remaining = next_sim.group_state(tasks, group, claimed=set())
        self.assertEqual(started, set())
        self.assertEqual(remaining["S"], 2)

    def test_claiming_opens_the_front_for_the_next_pick(self):
        """The dynamic that makes the loop converge on one spec."""
        tasks = [task("WL-1"), task("WL-2")]
        group = {"WL-1": "S", "WL-2": "S"}
        started, remaining = next_sim.group_state(tasks, group, claimed={"WL-1"})
        self.assertEqual(started, {"S"})
        self.assertEqual(remaining["S"], 1)


class RankingTest(unittest.TestCase):
    def test_open_front_beats_higher_priority_elsewhere(self):
        """The designed subordination of priority -- a medium task finishing a
        started spec outranks a high task starting a new one."""
        tasks = [task("WL-1", "merged"), task("WL-2", priority="medium"),
                 task("WL-3", priority="high")]
        group = {"WL-1": "started", "WL-2": "started", "WL-3": "fresh"}
        order = next_sim.replay(tasks, group, picks=1, proposed=True)
        self.assertEqual(order[0]["id"], "WL-2")

    def test_current_key_prefers_the_higher_priority_task(self):
        tasks = [task("WL-1", "merged"), task("WL-2", priority="medium"),
                 task("WL-3", priority="high")]
        group = {"WL-1": "started", "WL-2": "started", "WL-3": "fresh"}
        order = next_sim.replay(tasks, group, picks=1, proposed=False)
        self.assertEqual(order[0]["id"], "WL-3")

    def test_critical_still_preempts_the_open_front(self):
        """is_critical stays the first term: the new term must not shadow it."""
        tasks = [task("WL-1", "merged"), task("WL-2"),
                 task("WL-3", priority="critical")]
        group = {"WL-1": "started", "WL-2": "started", "WL-3": "fresh"}
        order = next_sim.replay(tasks, group, picks=1, proposed=True)
        self.assertEqual(order[0]["id"], "WL-3")

    def test_proposal_clusters_picks_where_the_current_key_scatters(self):
        """Two specs, interleaved created_at. The current key alternates
        between them; the proposal drains one, then the other."""
        tasks = [
            task("WL-1", "merged"), task("WL-5", "merged"),  # both specs started
            task("WL-2", created="2026-08-01T00:00:00Z"),
            task("WL-6", created="2026-08-01T00:00:01Z"),
            task("WL-3", created="2026-08-01T00:00:02Z"),
            task("WL-7", created="2026-08-01T00:00:03Z"),
        ]
        group = {"WL-1": "A", "WL-2": "A", "WL-3": "A",
                 "WL-5": "B", "WL-6": "B", "WL-7": "B"}

        current = [group[t["id"]] for t in next_sim.replay(tasks, group, 4, False)]
        proposed = [group[t["id"]] for t in next_sim.replay(tasks, group, 4, True)]

        self.assertEqual(current, ["A", "B", "A", "B"])
        self.assertEqual(proposed, ["A", "A", "B", "B"])
        self.assertEqual(next_sim.switches(current), 3)
        self.assertEqual(next_sim.switches(proposed), 1)

    def test_remaining_does_not_rank_untouched_specs(self):
        """Regression: ranking untouched specs by fewest-remaining prefers the
        smallest group, so a queue of one-task groups becomes pure context
        switching -- worse than the current key. Nothing is started here, so
        the one-task group must not automatically win; priority decides."""
        tasks = [
            task("WL-1", priority="medium"),                    # group "solo", 1 left
            task("WL-2", priority="high"),                      # group "big", 2 left
            task("WL-3", priority="high"),
        ]
        group = {"WL-1": "solo", "WL-2": "big", "WL-3": "big"}
        order = next_sim.replay(tasks, group, picks=1, proposed=True)
        self.assertEqual(order[0]["id"], "WL-2")

    def test_remaining_still_ranks_inside_an_open_front(self):
        tasks = [task("WL-1", "merged"), task("WL-2"),
                 task("WL-3", "merged"), task("WL-4"), task("WL-5")]
        group = {"WL-1": "small", "WL-2": "small",
                 "WL-3": "big", "WL-4": "big", "WL-5": "big"}
        order = next_sim.replay(tasks, group, picks=1, proposed=True)
        self.assertEqual(order[0]["id"], "WL-2")

    def test_agents_drift_rather_than_idle_when_no_front_is_open(self):
        """005 §2: the term sorts, it never removes rows."""
        tasks = [task("WL-1"), task("WL-2")]
        group = {"WL-1": "fresh-a", "WL-2": "fresh-b"}
        order = next_sim.replay(tasks, group, picks=2, proposed=True)
        self.assertEqual(len(order), 2)

    def test_replay_stops_at_the_ready_set_size(self):
        order = next_sim.replay([task("WL-1")], {"WL-1": "S"}, picks=10, proposed=True)
        self.assertEqual(len(order), 1)

    def test_unfinished_non_ready_states_are_not_claimable(self):
        tasks = [task("WL-1", "in_review"), task("WL-2", "abandoned")]
        group = {"WL-1": "S", "WL-2": "S"}
        self.assertEqual(next_sim.replay(tasks, group, 5, True), [])


class SwitchesTest(unittest.TestCase):
    def test_counts_only_transitions(self):
        self.assertEqual(next_sim.switches(["A", "A", "B", "B", "A"]), 2)
        self.assertEqual(next_sim.switches(["A", "A", "A"]), 0)
        self.assertEqual(next_sim.switches([]), 0)


class RenderTest(unittest.TestCase):
    def test_renders_both_keys_with_stats(self):
        tasks = [task("WL-1", "merged"), task("WL-2"), task("WL-3")]
        group = {"WL-1": "A", "WL-2": "A", "WL-3": "B"}
        out = io.StringIO()
        stats = next_sim.render(tasks, group, 2, out)
        self.assertIn("current", out.getvalue())
        self.assertIn("proposed", out.getvalue())
        self.assertEqual(len(stats), 2)

    def test_doc_ids_are_printed_as_slugs_when_names_are_given(self):
        tasks = [task("WL-1", "merged"), task("WL-2")]
        out = io.StringIO()
        next_sim.render(tasks, {"WL-1": 1, "WL-2": 1}, 1, out, {1: "001-a"})
        self.assertIn("001-a", out.getvalue())


if __name__ == "__main__":
    unittest.main()
