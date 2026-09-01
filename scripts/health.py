#!/usr/bin/env python3
"""Project health from the shape of unfinished work: specs vs plans vs tasks.

A project accumulates unfinished work at four points -- spec sections nobody
has planned against, plans nobody has accepted, accepted plans nobody has
minted tasks from, and tasks nobody has finished. Where the pile sits says
where time is under-invested. Specs growing without plans behind them is
design outrunning delivery; a bug pile growing faster than it drains is
quality debt; a queue with no throughput behind it is a backlog that will
never clear.

The report is deliberately not a single score. Which stage is stuck is the
only actionable part, and an average over the stages hides exactly that.

Every source is the worklode backbone, read through the `lode` CLI:

  lode doc list --kind spec       spec inventory and status
  lode doc list --kind plan       plan inventory and status
  lode doc list --needs-planning  the backbone's own verdict on which spec
                                  sections no accepted plan discharges
                                  (026 sec-2.1), each classified unplanned,
                                  partial or bound-only
  lode doc get <id>               a spec's section count and a plan's `covers`
                                  edges -- one round trip per document, so it
                                  is issued only where a list cannot answer
  lode task list                  the live task rows, each naming the plan it
                                  was minted from (plan_doc, 025 sec-9.2)
  lode task timeline               per-task state_log transitions -- real
                                  completion times, so throughput and lead
                                  time are measured, not guessed

Coverage comes from --needs-planning rather than from recomputing it over
`covers` edges. The wire form of an edge carries no coverage level, so a plan
declaring `coverage: none` -- stating that it deliberately does *not* discharge
a section -- would be indistinguishable from one that does. Asking the backbone
also means this report and the backbone cannot disagree about what is planned.

Asymmetry worth knowing about: tasks carry real history, documents carry only a
last touch. The task half of this report is a trend; the doc half is a
snapshot, aged by each document's updated_at.

Usage: health.py [--project ID] [--weeks N] [--json]
"""

import argparse
import concurrent.futures
import json
import statistics
import subprocess
import sys
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone

# Task states. Read as data where possible, but the done/queued/in-flight
# split is a judgement the report has to make, so it is named here.
DONE = {"merged", "deployed_dev", "deployed_prod", "released"}
QUEUED = {"ready", "draft"}
INFLIGHT = {"in_progress", "in_review"}
ABANDONED = {"abandoned"}

# Kinds are read from the data, not hardcoded -- the task kind `spec` is
# mid-rename to `design` (025 sec-10), and both spellings are live. Only the
# upkeep set is fixed, because that is the question being asked.
UPKEEP = {"bug", "chore"}


# --- backbone reads ----------------------------------------------------


def run_lode(args):
    """The one place this script shells out. Tests stub it."""
    proc = subprocess.run(["lode", *args], capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"lode {' '.join(args)} failed")
    return json.loads(proc.stdout)


def scoped(args, project):
    return [*args, "--project", project] if project else list(args)


def fetch_docs(project, kind):
    """The document rows of one kind. Bodies are blanked server-side."""
    return run_lode(scoped(["doc", "list", "--kind", kind, "--json"], project))["docs"]


def fetch_planning_gaps(project):
    """{spec doc id: planning gap} for accepted specs with an open section.

    A spec absent from the payload is the backbone saying "fully planned".
    """
    resp = run_lode(scoped(["doc", "list", "--needs-planning", "--json"], project))
    return {gap["doc"]: gap for gap in resp.get("planning_gaps") or []}


def fetch_details(ids, workers=8):
    """{doc id: detail}. A document whose fetch fails is simply absent."""
    def one(doc_id):
        try:
            return doc_id, run_lode(["doc", "get", str(doc_id), "--json"])
        except Exception:
            return doc_id, None

    out = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for doc_id, detail in pool.map(one, list(ids)):
            if detail is not None:
                out[doc_id] = detail
    return out


def fetch_tasks(project):
    return run_lode(scoped(["task", "list", "--status", "all", "--json"], project))["tasks"]


def fetch_timelines(ids, workers=8):
    """{task id: [entry]}. A task whose timeline fails is simply absent."""
    def one(task_id):
        try:
            return task_id, run_lode(["task", "timeline", task_id, "--json"])["timeline"]
        except Exception:
            return task_id, None

    out = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for task_id, timeline in pool.map(one, list(ids)):
            if timeline is not None:
                out[task_id] = timeline
    return out


def collect(project, workers=8):
    """Every backbone read, in one place: (specs, plans, tasks, timelines).

    Three list calls answer most of it. `lode doc get` is a round trip per
    document, so it is issued only where a list cannot answer: a plan's
    `covers` edges, and the section count of a live spec that --needs-planning
    did not already report one for.
    """
    spec_docs = fetch_docs(project, "spec")
    plan_docs = fetch_docs(project, "plan")
    gaps = fetch_planning_gaps(project)

    wanted = [d["id"] for d in spec_docs
              if d["status"] != "superseded" and d["id"] not in gaps]
    wanted += [d["id"] for d in plan_docs if d["status"] != "superseded"]
    details = fetch_details(wanted, workers)

    counts = {i: len(d.get("sections") or []) for i, d in details.items()}
    specs = build_specs(spec_docs, gaps, counts)
    plans = build_plans(plan_docs, details)

    tasks = fetch_tasks(project)
    timelines = fetch_timelines([t["id"] for t in tasks], workers)
    return specs, plans, tasks, timelines


# --- document model ----------------------------------------------------


def build_specs(docs, gaps, section_counts):
    """{doc id: {...}} for every spec still in force.

    Sections are counted, not enumerated: the backbone already decided which
    anchors are undischarged, so this only has to total them. An accepted spec
    absent from `gaps` is fully planned. A draft spec has every section
    unplanned -- 026 sec-2.1 defines the planning gap over accepted specs only,
    and dropping drafts from the denominator would flatter a corpus precisely
    as it accumulates unaccepted design.
    """
    out = {}
    for doc in docs:
        if doc["status"] == "superseded":
            continue
        gap = gaps.get(doc["id"])
        if gap:
            total = gap["sections"]
            reasons = Counter(g["coverage"] for g in gap.get("gaps") or [])
        else:
            total = section_counts.get(doc["id"], 0)
            reasons = Counter({"unplanned": total}) if total and doc["status"] != "accepted" else Counter()
        out[doc["id"]] = {
            "slug": doc["slug"],
            "status": doc["status"],
            "updated_at": doc.get("updated_at") or "",
            "sections": total,
            "unplanned": sum(reasons.values()),
            "reasons": reasons,
        }
    return out


def build_plans(docs, details):
    """{doc id: {...}} for every plan, with the documents its `covers` edges hit.

    A reference the backbone could not resolve -- the NO-SPEC sentinel, a
    cross-project path -- arrives as `to_external` with no `to_doc`, and is
    dropped: it names no document to group a front around. One edge per
    coverage entry, so a plan naming three sections of a spec counts three
    times, which is what makes spec_of_plan's "mostly covers" meaningful.
    """
    out = {}
    for doc in docs:
        detail = details.get(doc["id"]) or {}
        out[doc["id"]] = {
            "slug": doc["slug"],
            "status": doc["status"],
            "updated_at": doc.get("updated_at") or "",
            "covers": [e["to_doc"] for e in detail.get("edges") or []
                       if e.get("type") == "covers" and e.get("to_doc")],
        }
    return out


def spec_of_plan(plan):
    """The document a plan mostly covers, or None when it covers none.

    A plan may name sections of several specs; the one it references most is
    the series it belongs to. Ties break on document id so grouping is stable.
    """
    counts = Counter(plan["covers"])
    if not counts:
        return None
    return min(counts, key=lambda doc_id: (-counts[doc_id], doc_id))


def plan_task_counts(plans, tasks):
    """({plan doc id: [task]}, linked count) from each task's plan_doc.

    A plan's acceptance mints its tasks and stamps plan_doc on them (025
    sec-9.2). Tasks filed by hand carry none, so this is a lower bound on
    linkage and therefore an upper bound on "dormant plans"; the report prints
    the link rate so the number can be discounted.
    """
    out = defaultdict(list)
    linked = 0
    for task in tasks:
        plan = task.get("plan_doc")
        if not plan:
            continue
        linked += 1
        if plan in plans:
            out[plan].append(task)
    return out, linked


def fronts(plans, tasks):
    """Per-spec progress, as {spec doc id: {done, left, started}}.

    An *open front* is a spec with work both finished and unfinished -- begun
    and not closed. Each one costs an agent context to re-enter, and unlike a
    queued task it represents effort already spent that returns nothing until
    the spec closes. Untouched specs are cheap by comparison: nothing is
    invested in them yet.

    Tasks with no plan are excluded rather than counted as fronts of one; the
    report prints the link rate so the omission is visible.
    """
    by_plan, _ = plan_task_counts(plans, tasks)
    spec_of_task = {}
    for plan_id, plan_tasks in by_plan.items():
        spec = spec_of_plan(plans[plan_id])
        if not spec:
            continue
        for task in plan_tasks:
            spec_of_task.setdefault(task["id"], spec)

    out = defaultdict(lambda: {"done": 0, "left": 0, "started": False})
    for task in tasks:
        spec = spec_of_task.get(task["id"])
        if not spec:
            continue
        stage = stage_of(task["state"])
        if stage == "done":
            out[spec]["done"] += 1
            out[spec]["started"] = True
        elif stage == "in_flight":
            out[spec]["left"] += 1
            out[spec]["started"] = True
        elif stage == "queued":
            out[spec]["left"] += 1
    return dict(out)


def classify_fronts(per_spec, names=None):
    """Split specs into open / untouched / finished, open ones listed.

    `names` maps a doc id to the slug the report prints; an id it does not
    cover is printed as-is, so a plan covering something other than a live spec
    is still visible rather than silently renamed.
    """
    label = lambda key: (names or {}).get(key, key)  # noqa: E731
    open_, untouched, finished = [], [], []
    for spec, rec in sorted(per_spec.items(), key=lambda kv: str(label(kv[0]))):
        if rec["left"] and rec["started"]:
            open_.append({"spec": label(spec), "done": rec["done"], "left": rec["left"]})
        elif rec["left"]:
            untouched.append(spec)
        elif rec["done"]:
            finished.append(spec)
    return {
        "open": open_,
        "untouched_count": len(untouched),
        "finished_count": len(finished),
        # If every untouched spec were started before any front closed, this is
        # where the front count lands -- the cost of spreading, made concrete.
        "worst_case": len(open_) + len(untouched),
    }


# --- task history ------------------------------------------------------


def parse_time(value):
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def completion_times(tasks, timelines):
    """{task id: when it first entered a done state}, plus the fallback count.

    Preferring state_log over `updated_at` is the whole point of reading
    timelines: `updated_at` is last touch, which drifts every time a done task
    is edited. Tasks with no recorded transition fall back to `updated_at`,
    and the count is reported rather than buried.
    """
    out, fallback = {}, 0
    for task in tasks:
        if task["state"] not in DONE:
            continue
        at = None
        for entry in timelines.get(task["id"], []):
            change = entry.get("change") or {}
            if entry.get("type") == "state" and change.get("new") in DONE:
                at = parse_time(entry["at"])
                break
        if at is None:
            at = parse_time(task.get("updated_at"))
            fallback += 1
        out[task["id"]] = at
    return out, fallback


# --- metrics -----------------------------------------------------------


def stage_of(state):
    if state in DONE:
        return "done"
    if state in ABANDONED:
        return "abandoned"
    if state in INFLIGHT:
        return "in_flight"
    if state in QUEUED:
        return "queued"
    return "other"


def pct(num, den):
    return None if not den else 100.0 * num / den


def build_report(specs, plans, tasks, timelines, now, weeks):
    total_sections = sum(s["sections"] for s in specs.values())
    unplanned = sum(s["unplanned"] for s in specs.values())
    reasons = Counter()
    for spec in specs.values():
        reasons.update(spec["reasons"])
    covered = total_sections - unplanned

    live_plans = {i: v for i, v in plans.items() if v["status"] != "superseded"}
    accepted = {i for i, v in live_plans.items() if v["status"] == "accepted"}
    draft_plans = {i for i, v in live_plans.items() if v["status"] == "draft"}
    by_plan, linked = plan_task_counts(live_plans, tasks)
    dormant = sorted(i for i in accepted if not by_plan.get(i))

    stages = Counter(stage_of(t["state"]) for t in tasks)
    done_at, fallback = completion_times(tasks, timelines)

    window = now - timedelta(days=7 * 4)
    done_recent = [i for i, at in done_at.items() if at and at >= window]
    throughput = len(done_recent) / 4.0
    unfinished = stages["queued"] + stages["in_flight"]

    lead_times = []
    for task in tasks:
        at = done_at.get(task["id"])
        created = parse_time(task.get("created_at"))
        if at and created:
            lead_times.append((at - created).total_seconds() / 86400.0)

    queue_ages = [
        (now - parse_time(t["created_at"])).total_seconds() / 86400.0
        for t in tasks
        if stage_of(t["state"]) in ("queued", "in_flight") and t.get("created_at")
    ]

    by_id = {t["id"]: t for t in tasks}
    kinds = sorted({t["kind"] for t in tasks})
    kind_rows = []
    for kind in kinds:
        of_kind = [t for t in tasks if t["kind"] == kind]
        kind_rows.append({
            "kind": kind,
            "unfinished": sum(
                1 for t in of_kind if stage_of(t["state"]) in ("queued", "in_flight")
            ),
            "completed_28d": sum(1 for i in done_recent if by_id[i]["kind"] == kind),
            "all_time": len(of_kind),
            "created_28d": sum(
                1 for t in of_kind
                if parse_time(t.get("created_at")) and parse_time(t["created_at"]) >= window
            ),
        })
    for row in kind_rows:
        row["net_28d"] = row["created_28d"] - row["completed_28d"]

    def upkeep_share(field):
        total = sum(r[field] for r in kind_rows)
        up = sum(r[field] for r in kind_rows if r["kind"] in UPKEEP)
        return pct(up, total)

    trend = weekly_trend(tasks, done_at, now, weeks)

    resolved = stages["done"] + stages["abandoned"]
    return {
        "generated_at": now.isoformat(),
        "counts": {"specs": len(specs), "plans": len(plans), "tasks": len(tasks)},
        "stages": {
            "unplanned_sections": unplanned,
            "unplanned_sections_by_coverage": dict(reasons),
            "total_sections": total_sections,
            "plans_awaiting_acceptance": len(draft_plans),
            "accepted_plans_without_tasks": len(dormant),
            "tasks_queued": stages["queued"],
            "tasks_in_flight": stages["in_flight"],
        },
        "conversion": {
            "sections_planned_pct": pct(covered, total_sections),
            "sections_planned": [covered, total_sections],
            "plans_activated_pct": pct(len(accepted) - len(dormant), len(accepted)),
            "plans_activated": [len(accepted) - len(dormant), len(accepted)],
            "tasks_completed_pct": pct(stages["done"], stages["done"] + unfinished),
            "tasks_completed": [stages["done"], stages["done"] + unfinished],
        },
        "flow": {
            "throughput_per_week": throughput,
            "runway_weeks": (unfinished / throughput) if throughput else None,
            "lead_time_days_median": statistics.median(lead_times) if lead_times else None,
            "queue_age_days_median": statistics.median(queue_ages) if queue_ages else None,
            "abandonment_pct": pct(stages["abandoned"], resolved),
        },
        "fronts": classify_fronts(
            fronts(live_plans, tasks), {i: s["slug"] for i, s in specs.items()}
        ),
        "kinds": kind_rows,
        "upkeep_share_pct": {
            "unfinished": upkeep_share("unfinished"),
            "completed_28d": upkeep_share("completed_28d"),
            "all_time": upkeep_share("all_time"),
        },
        "trend": trend,
        "stalls": stalls(specs, plans, draft_plans, dormant, tasks, now),
        "caveats": {
            "tasks_minted_by_a_plan": [linked, len(tasks)],
            "completion_time_fallbacks": fallback,
            "task_history_days": history_span(tasks),
        },
    }


def weekly_trend(tasks, done_at, now, weeks):
    """Per ISO week: tasks created, tasks completed, upkeep share of completed."""
    by_id = {t["id"]: t for t in tasks}
    rows = []
    for back in range(weeks - 1, -1, -1):
        end = now - timedelta(days=7 * back)
        start = end - timedelta(days=7)
        created = [
            t for t in tasks
            if (c := parse_time(t.get("created_at"))) and start <= c < end
        ]
        completed = [i for i, at in done_at.items() if at and start <= at < end]
        up = sum(1 for i in completed if by_id[i]["kind"] in UPKEEP)
        rows.append({
            "week": end.strftime("%G-W%V"),
            "created": len(created),
            "completed": len(completed),
            "upkeep_share_pct": pct(up, len(completed)),
        })
    return rows


def history_span(tasks):
    stamps = [parse_time(t["created_at"]) for t in tasks if t.get("created_at")]
    if not stamps:
        return 0
    return round((max(stamps) - min(stamps)).total_seconds() / 86400.0, 1)


def stalls(specs, plans, draft_plans, dormant, tasks, now, top=3):
    """The oldest or largest unfinished item at each stage.

    Plans are ranked by the backbone's `updated_at`, the closest thing a
    document has to an age -- one sitting in `draft` since June is a different
    problem from one written yesterday. Specs rank by how many sections are
    unplanned instead, because a spec's updated_at moves whenever any section
    of it is edited.
    """
    touched = lambda i: (plans[i]["updated_at"] or "unknown")[:10]  # noqa: E731
    oldest = lambda ids: sorted(ids, key=lambda i: (plans[i]["updated_at"] or "9999", i))  # noqa: E731

    return {
        "unplanned_specs": sorted(
            ({"doc": s["slug"], "unplanned_sections": s["unplanned"],
              "by_coverage": dict(s["reasons"])}
             for s in specs.values() if s["unplanned"]),
            key=lambda r: (-r["unplanned_sections"], r["doc"]),
        )[:top],
        "draft_plans": [{"doc": plans[i]["slug"], "touched": touched(i)}
                        for i in oldest(draft_plans)[:top]],
        "dormant_plans": [{"doc": plans[i]["slug"], "touched": touched(i)}
                          for i in oldest(dormant)[:top]],
        "oldest_queued": sorted(
            ({"id": t["id"], "kind": t["kind"],
              "age_days": round((now - parse_time(t["created_at"])).total_seconds() / 86400.0, 1),
              "title": t["title"][:48]}
             for t in tasks
             if stage_of(t["state"]) == "queued" and t.get("created_at")),
            key=lambda r: -r["age_days"],
        )[:top],
    }


# --- rendering ---------------------------------------------------------


def fmt(value, suffix="", places=1):
    return "n/a" if value is None else f"{value:.{places}f}{suffix}"


def render(report, out):
    w = out.write
    c = report["counts"]
    w(f"Worklode project health  ·  {report['generated_at'][:19]}\n")
    w(f"{c['specs']} specs, {c['plans']} plans, {c['tasks']} tasks\n\n")

    s = report["stages"]
    w("UNFINISHED WORK BY STAGE\n")
    w(f"  design    unplanned spec sections     {s['unplanned_sections']:5d}"
      f"  of {s['total_sections']}\n")
    by = s["unplanned_sections_by_coverage"]
    if by:
        w("            " + ", ".join(f"{n} {reason}" for reason, n in sorted(by.items()))
          + "  (026 sec-2.1)\n")
    w(f"  design    plans awaiting acceptance   {s['plans_awaiting_acceptance']:5d}\n")
    w(f"  design    accepted plans, no tasks    {s['accepted_plans_without_tasks']:5d}\n")
    w(f"  delivery  tasks queued                {s['tasks_queued']:5d}\n")
    w(f"  delivery  tasks in flight             {s['tasks_in_flight']:5d}\n\n")

    v = report["conversion"]
    w("CONVERSION  (each stage's throughput to the next)\n")
    for label, key, pair in (
        ("spec sections planned", "sections_planned_pct", "sections_planned"),
        ("accepted plans activated", "plans_activated_pct", "plans_activated"),
        ("tasks completed", "tasks_completed_pct", "tasks_completed"),
    ):
        n, d = v[pair]
        w(f"  {label:<26}{fmt(v[key], '%'):>7}   ({n}/{d})\n")
    w("\n")

    f = report["flow"]
    w("FLOW  (trailing 28d, completion times from state_log)\n")
    w(f"  throughput                {fmt(f['throughput_per_week'])} tasks/week\n")
    w(f"  runway                    {fmt(f['runway_weeks'])} weeks of queued + in-flight\n")
    w(f"  lead time (median)        {fmt(f['lead_time_days_median'])} days created to done\n")
    w(f"  queue age (median)        {fmt(f['queue_age_days_median'])} days unfinished\n")
    w(f"  abandonment               {fmt(f['abandonment_pct'], '%')} of resolved tasks\n\n")

    fr = report["fronts"]
    w("OPEN FRONTS  (specs begun and not closed)\n")
    w(f"  open fronts                 {len(fr['open']):5d}\n")
    w(f"  untouched, work queued      {fr['untouched_count']:5d}\n")
    w(f"  finished                    {fr['finished_count']:5d}\n")
    for row in fr["open"]:
        w(f"    {row['done']:2d} done /{row['left']:3d} left  {row['spec']}\n")
    w(f"  Starting every untouched spec before closing one would put this at\n"
      f"  {fr['worst_case']} open fronts. Each is effort spent that returns nothing\n"
      "  until its spec closes.\n\n")

    w("KIND MIX\n")
    w(f"  {'kind':<12}{'unfinished':>11}{'done 28d':>10}{'all time':>10}{'net 28d':>9}\n")
    for row in report["kinds"]:
        w(f"  {row['kind']:<12}{row['unfinished']:>11}{row['completed_28d']:>10}"
          f"{row['all_time']:>10}{row['net_28d']:>+9}\n")
    u = report["upkeep_share_pct"]
    w(f"  {'upkeep share':<12}{fmt(u['unfinished'], '%'):>11}"
      f"{fmt(u['completed_28d'], '%'):>10}{fmt(u['all_time'], '%'):>10}\n")
    w("  net 28d is created minus completed: positive means the pile is growing\n\n")

    w("WEEKLY\n")
    w(f"  {'week':<10}{'created':>9}{'completed':>11}{'upkeep of done':>16}\n")
    for row in report["trend"]:
        w(f"  {row['week']:<10}{row['created']:>9}{row['completed']:>11}"
          f"{fmt(row['upkeep_share_pct'], '%'):>16}\n")
    w("\n")

    st = report["stalls"]
    w("STALLS  (oldest or largest unfinished, per stage)\n")
    for row in st["unplanned_specs"]:
        w(f"  unplanned  {row['unplanned_sections']:3d} sections  {row['doc']}\n")
    for row in st["dormant_plans"]:
        w(f"  dormant     {row['touched']}  {row['doc']}\n")
    for row in st["draft_plans"]:
        w(f"  unaccepted  {row['touched']}  {row['doc']}\n")
    for row in st["oldest_queued"]:
        w(f"  queued     {row['age_days']:5.1f}d  {row['id']:<7} {row['title']}\n")
    w("\n")

    cav = report["caveats"]
    linked, total = cav["tasks_minted_by_a_plan"]
    w("NOTES\n")
    w(f"  {linked}/{total} tasks were minted by a plan (plan_doc, 025 sec-9.2), so\n"
      "  \"accepted plans, no tasks\" is an upper bound.\n")
    if cav["completion_time_fallbacks"]:
        w(f"  {cav['completion_time_fallbacks']} done tasks had no state_log transition;\n"
          "  their completion time fell back to updated_at.\n")
    w(f"  Task history spans {cav['task_history_days']} days -- weekly rows before that\n"
      "  are empty because the data does not exist, not because nothing happened.\n")
    w("  Specs and plans have no trend here: their half of the report is a\n"
      "  snapshot, aged by each document's updated_at.\n")


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--project", default=None, help="project id (default: this repo's)")
    ap.add_argument("--weeks", default=8, type=int)
    ap.add_argument("--json", action="store_true", dest="as_json")
    args = ap.parse_args()

    # Documents and tasks now come from the same server, so there is no
    # doc-only degraded mode left to fall back to: if the backbone is
    # unreachable there is no report.
    try:
        specs, plans, tasks, timelines = collect(args.project)
    except Exception as e:
        sys.exit(f"health: {e}")

    report = build_report(specs, plans, tasks, timelines,
                          datetime.now(timezone.utc), args.weeks)
    if args.as_json:
        json.dump(report, sys.stdout, indent=2, default=str)
        sys.stdout.write("\n")
    else:
        render(report, sys.stdout)


if __name__ == "__main__":
    main()
