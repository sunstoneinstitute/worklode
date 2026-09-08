#!/usr/bin/env python3
"""List tasks that look like the execution record of an accepted plan.

Early worklode tasks predate `plan_doc`, so plans executed by hand show up on
the Progress page under "No execution record". This finds the likely pairs: for
every accepted plan with no task linked to it, every task whose body names that
plan by slug or by `<KEY>-PLAN-<n>`. One line per candidate:

    WL-PLAN-43  WL-34  deployed_prod  <title>

The output is a proposal for a human to review, not a backfill. A task listed
twice matched two plans and needs a decision. Apply the kept lines yourself with
`lode task edit <task> --plan <plan>`.

Read only: it runs two `lode` list commands and prints. It never writes.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys


def lode_json(*args: str) -> dict:
    """Run one read-only `lode` list command and decode its JSON."""
    cmd = ["lode", *args, "--json"]
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError) as error:
        stderr = getattr(error, "stderr", "") or ""
        sys.exit(f"{' '.join(cmd)}: {error}\n{stderr.strip()}")
    return json.loads(out)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="List candidate tasks for the plans that have no execution record."
    )
    parser.add_argument(
        "project",
        nargs="?",
        default="",
        help="project id (default: the project of the current repo)",
    )
    args = parser.parse_args()
    scope = ["--project", args.project] if args.project else []

    docs = lode_json("doc", "list", "--kind", "plan", "--status", "accepted", *scope)["docs"]
    tasks = lode_json("task", "list", "--status", "all", *scope)["tasks"]

    linked = {t.get("plan_doc") for t in tasks}
    unlinked = [d for d in docs if d["id"] not in linked]

    count = 0
    for plan in sorted(unlinked, key=lambda d: d["number"]):
        ref = re.compile(rf"\b{re.escape(plan['ref'])}\b")
        for task in tasks:
            body = task.get("body", "")
            if plan["slug"] not in body and not ref.search(body):
                continue
            print(f"{plan['ref']:<12} {task['id']:<8} {task['state']:<14} {task['title']}")
            count += 1
    print(f"{count} candidate(s) over {len(unlinked)} unlinked plan(s)", file=sys.stderr)


if __name__ == "__main__":
    main()
