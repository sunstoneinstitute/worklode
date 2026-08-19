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

Sources:
  docs/specs/*.md   section inventory and status (superseded specs excluded)
  docs/plans/*.md   status and `covers:`, parsed by secmeta.coverage_of
  lode task list    the live task rows
  lode timeline     per-task state_log transitions -- real completion times,
                    so throughput and lead time are measured, not guessed
  git log           last-touch date per doc, for the stall list

Asymmetry worth knowing about: tasks carry real history, docs do not. The
task half of this report has a weekly trend; the doc half is a snapshot with
git dates for age. Faking a doc-side trend would mean replaying git over the
whole corpus, which is slow and still wrong for anything predating the file.

Usage: health.py [--repo-root DIR] [--project ID] [--weeks N] [--json]
"""

import argparse
import concurrent.futures
import json
import re
import statistics
import subprocess
import sys
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.dont_write_bytecode = True  # importing secmeta must not litter scripts/
sys.path.insert(0, str(Path(__file__).resolve().parent))
from secfmt import generated, split_front_matter  # noqa: E402
from secindex import sections_of  # noqa: E402
from secmeta import coverage_of  # noqa: E402

try:
    import yaml
except ImportError:  # pragma: no cover - same guard secmeta.py uses
    sys.exit("health: needs PyYAML (pip install pyyaml)")

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

ANCHOR_KEY = re.compile(r"^sec-[\w.]+$")
SPEC_FILE_RE = re.compile(r"^\d+-.*\.md$")
PLAN_REF_RE = re.compile(r"docs/plans/([\w.\-]+\.md)")


# --- loading -----------------------------------------------------------


def frontmatter(path):
    """(data, body) for one document; data is {} when absent or malformed."""
    text = path.read_text(encoding="utf-8")
    fm, body = split_front_matter(text)
    if not fm:
        return {}, body
    try:
        data = yaml.safe_load(fm[4:-5]) or {}
    except yaml.YAMLError:
        return {}, body
    return (data if isinstance(data, dict) else {}), body


def load_specs(root):
    """{repo-relative path: {"status", "sections"}} for every live spec."""
    out = {}
    for path in sorted((root / "docs" / "specs").glob("*.md")):
        if generated(path) or not SPEC_FILE_RE.match(path.name):
            continue
        data, _ = frontmatter(path)
        if data.get("status") == "superseded":
            continue
        rel = str(path.relative_to(root))
        out[rel] = {
            "status": data.get("status", "unknown"),
            # secindex.sections_of, not a regex over the text: it skips fenced
            # code, so a spec quoting `{#sec-N}` in an example does not inflate
            # the denominator of the headline coverage metric.
            "sections": {key for key, _ in sections_of(path) if ANCHOR_KEY.match(key)},
        }
    return out


def load_plans(root):
    """{repo-relative path: {"status", "entries"}} for every plan."""
    out = {}
    for path in sorted((root / "docs" / "plans").glob("*.md")):
        if generated(path) or path.name == "index.yaml":
            continue
        rel = str(path.relative_to(root))
        data, _ = frontmatter(path)
        out[rel] = {
            "status": data.get("status", "unknown"),
            "entries": coverage_of(rel, data),
        }
    return out


# --- coverage ----------------------------------------------------------


def covered_sections(specs, plans):
    """Section anchors a live plan claims, as {"spec path": {anchor}}.

    A bare `docs/specs/NNN.md` reference covers every section of that spec; an
    anchored one covers just that section. `coverage: none` is a plan stating
    it does *not* cover the section -- declared debt, so it does not count as
    covered here. That is a deliberate divergence from find_gaps.py, which
    counts `none` because it asks a different question ("has a human looked at
    this spec at all?").
    """
    hit = defaultdict(set)
    for plan in plans.values():
        if plan["status"] == "superseded":
            continue
        for ref, _bare, level, _ in plan["entries"]:
            if not ref:
                continue  # NO-SPEC, cross-project shorthand, or prose
            path, _, anchor = ref.partition("#")
            if path not in specs:
                continue  # points at a superseded spec, or another repo
            # Only the object form can say `none`; coverage_of reports every
            # string entry as "full", whether or not it carries an anchor.
            if level == "none":
                continue
            if anchor:
                hit[path].add(anchor)
            else:
                hit[path] |= specs[path]["sections"]
    return hit


def spec_of_plan(plan):
    """The spec a plan mostly covers, or None when it covers none.

    A plan may name sections of several specs; the one it references most is
    the series it belongs to. Ties break on path so the grouping is stable.
    """
    counts = Counter()
    for ref, _bare, _level, _ in plan["entries"]:
        if ref:
            counts[ref.partition("#")[0]] += 1
    if not counts:
        return None
    return min(counts, key=lambda path: (-counts[path], path))


def fronts(plans, tasks):
    """Per-spec progress, as {spec: {done, left, started}}.

    An *open front* is a spec with work both finished and unfinished -- begun
    and not closed. Each one costs an agent context to re-enter, and unlike a
    queued task it represents effort already spent that returns nothing until
    the spec closes. Untouched specs are cheap by comparison: nothing is
    invested in them yet.

    Tasks with no recoverable plan are excluded rather than counted as fronts
    of one; the report prints the link rate so the omission is visible.
    """
    by_plan, _ = plan_task_counts(plans, tasks)
    spec_of_task = {}
    for plan_path, plan_tasks in by_plan.items():
        spec = spec_of_plan(plans[plan_path])
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


def classify_fronts(per_spec):
    """Split specs into open / untouched / finished, open ones listed."""
    open_, untouched, finished = [], [], []
    for spec, rec in sorted(per_spec.items()):
        if rec["left"] and rec["started"]:
            open_.append({"spec": spec, "done": rec["done"], "left": rec["left"]})
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


def plan_task_counts(plans, tasks):
    """{plan path: [task]} from plan paths named in task bodies.

    Tasks reference their plan in prose, not through a column, so this is a
    lower bound on linkage and therefore an upper bound on "dormant plans".
    The report prints the link rate so the number can be discounted.
    """
    by_name = {Path(p).name: p for p in plans}
    out = defaultdict(list)
    linked = 0
    for task in tasks:
        names = set(PLAN_REF_RE.findall(task.get("body") or ""))
        matched = [by_name[n] for n in names if n in by_name]
        if matched:
            linked += 1
        for plan in matched:
            out[plan].append(task)
    return out, linked


# --- task history ------------------------------------------------------


def run_lode(args, cwd):
    proc = subprocess.run(
        ["lode", *args], capture_output=True, text=True, cwd=cwd
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"lode {' '.join(args)} failed")
    return json.loads(proc.stdout)


def fetch_tasks(cwd, project):
    args = ["task", "list", "--status", "all", "--json"]
    if project:
        args += ["--project", project]
    return run_lode(args, cwd)["tasks"]


def fetch_timelines(cwd, ids, workers=8):
    """{task id: [entry]}. A task whose timeline fails is simply absent."""
    def one(task_id):
        try:
            return task_id, run_lode(["timeline", task_id, "--json"], cwd)["timeline"]
        except Exception:
            return task_id, None

    out = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for task_id, timeline in pool.map(one, ids):
            if timeline is not None:
                out[task_id] = timeline
    return out


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


def build_report(root, specs, plans, tasks, timelines, now, weeks):
    hit = covered_sections(specs, plans)
    total_sections = sum(len(s["sections"]) for s in specs.values())
    covered = sum(len(hit.get(p, set()) & s["sections"]) for p, s in specs.items())

    live_plans = {p: v for p, v in plans.items() if v["status"] != "superseded"}
    accepted = {p for p, v in live_plans.items() if v["status"] == "accepted"}
    draft_plans = {p for p, v in live_plans.items() if v["status"] == "draft"}
    by_plan, linked = plan_task_counts(live_plans, tasks)
    dormant = sorted(p for p in accepted if not by_plan.get(p))

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
            "unplanned_sections": total_sections - covered,
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
        "fronts": classify_fronts(fronts(live_plans, tasks)),
        "kinds": kind_rows,
        "upkeep_share_pct": {
            "unfinished": upkeep_share("unfinished"),
            "completed_28d": upkeep_share("completed_28d"),
            "all_time": upkeep_share("all_time"),
        },
        "trend": trend,
        "stalls": stalls(root, specs, hit, draft_plans, dormant, tasks, now),
        "caveats": {
            "tasks_linked_to_a_plan": [linked, len(tasks)],
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


def git_dates(root):
    """{repo-relative path: last-touch ISO date}, in one git pass."""
    try:
        out = subprocess.run(
            ["git", "-C", str(root), "log", "--format=%x00%aI", "--name-only",
             "--", "docs/specs", "docs/plans"],
            capture_output=True, text=True, check=True,
        ).stdout
    except (subprocess.CalledProcessError, FileNotFoundError):
        return {}
    dates, current = {}, None
    for line in out.splitlines():
        if line.startswith("\x00"):
            current = line[1:]
        elif line.strip() and current and line not in dates:
            dates[line] = current  # first sighting is the most recent commit
    return dates


def stalls(root, specs, hit, draft_plans, dormant, tasks, now, top=3):
    """The oldest or largest unfinished item at each stage.

    Docs are ranked by git last-touch, which is the closest thing they have to
    an age -- a plan sitting in `draft` since June is a different problem from
    one written yesterday. Unplanned specs rank by how many sections are
    unplanned instead, because every spec's file date moves whenever any
    section of it is edited.
    """
    dates = git_dates(root)
    oldest = lambda paths: sorted(paths, key=lambda p: (dates.get(p, "9999"), p))

    return {
        "unplanned_specs": sorted(
            ({"file": p, "unplanned_sections": len(s["sections"] - hit.get(p, set()))}
             for p, s in specs.items() if s["sections"] - hit.get(p, set())),
            key=lambda r: -r["unplanned_sections"],
        )[:top],
        "draft_plans": [{"file": p, "touched": dates.get(p, "unknown")[:10]}
                        for p in oldest(draft_plans)[:top]],
        "dormant_plans": [{"file": p, "touched": dates.get(p, "unknown")[:10]}
                          for p in oldest(dormant)[:top]],
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
        w(f"    {row['done']:2d} done /{row['left']:3d} left  {Path(row['spec']).name}\n")
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
        w(f"  unplanned  {row['unplanned_sections']:3d} sections  {row['file']}\n")
    for row in st["dormant_plans"]:
        w(f"  dormant     {row['touched']}  {row['file']}\n")
    for row in st["draft_plans"]:
        w(f"  unaccepted  {row['touched']}  {row['file']}\n")
    for row in st["oldest_queued"]:
        w(f"  queued     {row['age_days']:5.1f}d  {row['id']:<7} {row['title']}\n")
    w("\n")

    cav = report["caveats"]
    linked, total = cav["tasks_linked_to_a_plan"]
    w("NOTES\n")
    w(f"  {linked}/{total} tasks name a plan in their body, so \"accepted plans,\n"
      "  no tasks\" is an upper bound.\n")
    if cav["completion_time_fallbacks"]:
        w(f"  {cav['completion_time_fallbacks']} done tasks had no state_log transition;\n"
          "  their completion time fell back to updated_at.\n")
    w(f"  Task history spans {cav['task_history_days']} days -- weekly rows before that\n"
      "  are empty because the data does not exist, not because nothing happened.\n")
    w("  Specs and plans have no history here: their half of the report is a\n"
      "  snapshot, only the task half is a trend.\n")


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--repo-root", default=".", type=Path)
    ap.add_argument("--project", default=None, help="project id (default: this repo's)")
    ap.add_argument("--weeks", default=8, type=int)
    ap.add_argument("--json", action="store_true", dest="as_json")
    args = ap.parse_args()

    root = args.repo_root.resolve()
    specs = load_specs(root)
    plans = load_plans(root)

    try:
        tasks = fetch_tasks(root, args.project)
        timelines = fetch_timelines(root, [t["id"] for t in tasks])
    except Exception as e:
        print(f"health: no task data ({e}); reporting the doc side only", file=sys.stderr)
        tasks, timelines = [], {}

    report = build_report(root, specs, plans, tasks, timelines,
                          datetime.now(timezone.utc), args.weeks)
    if args.as_json:
        json.dump(report, sys.stdout, indent=2, default=str)
        sys.stdout.write("\n")
    else:
        render(report, sys.stdout)


if __name__ == "__main__":
    main()
