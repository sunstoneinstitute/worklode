#!/usr/bin/env python3
"""Simulate the open-front term proposed for spec 005 §3's ranking key.

The problem: `(is_critical, concern_rank, priority, blocking_fan_out)` has no
term that prefers finishing a spec over starting one, so an agent loop spreads
across every plan at once and nothing completes.

The proposal adds one term after `concern_rank`:

    (is_critical, concern_rank, open_front, remaining, priority, fan_out)

`open_front` is 0 when the task's spec already has work done or in flight, 1
otherwise. `remaining` is that spec's unfinished task count, fewest first, and
applies **only inside an open front** -- see proposed_key for why extending it
to untouched specs backfires. The pair picks *which spec* to work on;
`priority` then orders within it. Like `concern_rank`, it only sorts -- it
never removes rows, so an agent still drifts to an untouched spec rather than
idle (005 §2).

What makes this worth simulating rather than reasoning about: the term is
*dynamic*. Claiming a task from an untouched spec turns that spec into an open
front, which changes the ranking for the next claim. A greedy replay is the
only way to see whether the loop actually converges on one spec at a time.

Two limits, both about what the task list can say:

  - A task names the plan whose acceptance minted it (`plan_doc`, 025 §9.2)
    and the plan's `covers` edges name its spec, so grouping is exact for
    minted tasks. Tasks filed by hand carry no plan and become singleton
    groups here; the header prints how many.
  - `blocking_fan_out` needs the `blocks` graph, which the task list endpoint
    does not return. It sits *below* the new term in both keys, so it cannot
    change which spec is picked -- only the order inside one. Ties that would
    fall to fan_out fall to created_at here instead.

Usage: next_sim.py [--project ID] [--picks N]
"""

import argparse
import sys
from collections import defaultdict
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import health  # noqa: E402
from health import spec_of_plan  # noqa: E402  (one definition, health owns it)

PRIORITY = {"critical": 0, "high": 1, "medium": 2, "low": 3}


def group_tasks(plans, tasks):
    """{task id: group key}. The group is the spec its plan covers where one is
    recoverable, else a singleton keyed by the task itself -- an unlinked task
    is a front of one, which is what it behaves like."""
    by_plan, _ = health.plan_task_counts(plans, tasks)
    group = {}
    for plan_id, plan_tasks in by_plan.items():
        spec = spec_of_plan(plans[plan_id]) or f"plan:{plan_id}"
        for task in plan_tasks:
            group.setdefault(task["id"], spec)
    for task in tasks:
        group.setdefault(task["id"], f"task:{task['id']}")
    return group


def group_state(tasks, group, claimed):
    """(open fronts, remaining per group) given the tasks already claimed in
    this replay. A claimed task counts as in flight, which is what makes a
    freshly-started spec an open front for the next pick."""
    started, remaining = set(), defaultdict(int)
    for task in tasks:
        key = group[task["id"]]
        stage = health.stage_of(task["state"])
        if stage == "done" or stage == "in_flight" or task["id"] in claimed:
            started.add(key)
        if stage in ("queued", "in_flight") and task["id"] not in claimed:
            remaining[key] += 1
    return started, remaining


def current_key(task):
    """spec 005 §3 today. fan_out is unavailable; created_at stands in."""
    return (
        task["priority"] != "critical",
        0,  # concern_rank: every task's concern is null today
        PRIORITY.get(task["priority"], 9),
        task["created_at"],
        task["id"],
    )


def proposed_key(task, group, started, remaining):
    key = group[task["id"]]
    front = key in started
    return (
        task["priority"] != "critical",
        0,  # concern_rank, unchanged
        0 if front else 1,  # the new term
        # `remaining` ranks only *inside* an open front, where it means
        # "closest to done". Applying it to untouched specs too made the
        # simulation worse than the current key: nothing there has progress to
        # be close to, so fewest-remaining just prefers the smallest group, and
        # a queue with many one-task groups becomes pure context switching.
        # Let priority and fan_out choose where to open a front; once open,
        # drain it.
        remaining[key] if front else 0,
        PRIORITY.get(task["priority"], 9),
        task["created_at"],
        task["id"],
    )


def replay(tasks, group, picks, proposed):
    """The next `picks` claims, re-ranking after each one.

    Only the proposed key actually changes between rounds; the current key is
    replayed the same way so the two are compared under identical mechanics.
    """
    ready = [t for t in tasks if health.stage_of(t["state"]) == "queued"]
    claimed, order = set(), []
    for _ in range(min(picks, len(ready))):
        pool = [t for t in ready if t["id"] not in claimed]
        if proposed:
            started, remaining = group_state(tasks, group, claimed)
            pick = min(pool, key=lambda t: proposed_key(t, group, started, remaining))
        else:
            pick = min(pool, key=current_key)
        claimed.add(pick["id"])
        order.append(pick)
    return order


def switches(fronts):
    """Times consecutive picks change spec -- what an agent pays in reloaded
    context, and the number the distinct-spec count hides: six picks scattered
    across three specs cost more than six picks in three blocks."""
    return sum(1 for a, b in zip(fronts, fronts[1:]) if a != b)


def render(tasks, group, picks, out, names=None):
    """`names` maps a group key to the slug to print; keys it does not cover
    (the `plan:`/`task:` singletons) print as-is."""
    w = out.write
    rows = {
        "current  (is_critical, concern, priority, fan_out)": replay(tasks, group, picks, False),
        "proposed (…, open_front, remaining, priority, fan_out)": replay(tasks, group, picks, True),
    }
    out_stats = {}
    for label, order in rows.items():
        fronts = [group[t["id"]] for t in order]
        w(f"{label}\n")
        for i, task in enumerate(order, 1):
            key = group[task["id"]]
            name = str((names or {}).get(key, key))
            w(f"  {i:2d}. {task['id']:<7} {task['priority']:<8} {name[:44]:<44}"
              f" {task['title'][:30]}\n")
        stats = {"distinct": len(set(fronts)), "switches": switches(fronts)}
        out_stats[label] = stats
        w(f"  → {len(order)} picks, {stats['distinct']} distinct specs, "
          f"{stats['switches']} context switches\n\n")
    return out_stats


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--project", default=None, help="project id (default: this repo's)")
    ap.add_argument("--picks", default=10, type=int)
    args = ap.parse_args()

    # Only plans need their detail fetched: the grouping runs off `covers`
    # edges, and spec slugs are just labels, so the section counts health.py
    # pays for are not needed here.
    spec_docs = health.fetch_docs(args.project, "spec")
    plan_docs = health.fetch_docs(args.project, "plan")
    live = [d["id"] for d in plan_docs if d["status"] != "superseded"]
    plans = health.build_plans(plan_docs, health.fetch_details(live))
    tasks = health.fetch_tasks(args.project)

    group = group_tasks(plans, tasks)
    names = {d["id"]: d["slug"] for d in spec_docs}

    # A group key is a spec's doc id, or the "plan:"/"task:" string a
    # singleton falls back to.
    linked = sum(1 for t in tasks if not str(group[t["id"]]).startswith("task:"))
    print(f"{len(tasks)} tasks, {linked} grouped by spec, "
          f"{len(tasks) - linked} unlinked (each its own front)\n")
    render(tasks, group, args.picks, sys.stdout, names)
    print("fan_out is omitted: it ranks below the new term in both keys, so it\n"
          "cannot change which spec is picked, only the order within one.")


if __name__ == "__main__":
    main()
