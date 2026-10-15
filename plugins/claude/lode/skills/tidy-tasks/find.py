#!/usr/bin/env python3
"""Print the tidy-tasks worklist as JSON: open tasks that point at withdrawn
rules (by governs edge or in body text) or cite spec sections by number.

Usage: find.py [--project P]
"""
import collections, json, re, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

proj = sys.argv[1:3] if sys.argv[1:2] == ["--project"] else []

def lode(*args):
    return json.loads(subprocess.run(["lode", *args, *proj, "--json"], check=True,
                                     capture_output=True, text=True).stdout)

tasks = {t["id"]: t for t in lode("task", "list")["tasks"]}
refs = [r["ref"] for r in lode("rule", "list")]
# rule list can omit governed_tasks; rule show is authoritative.
with ThreadPoolExecutor(8) as ex:
    rules = {r["ref"]: r for r in ex.map(lambda ref: json.loads(subprocess.run(
        ["lode", "rule", "show", ref, "--json"], check=True, capture_output=True,
        text=True).stdout), refs)}

succ = collections.defaultdict(set)  # old ref -> refs that supersede it
for r in rules.values():
    for e in r["edges"]:
        if e["type"] == "supersedes":
            succ[e["to"]].add(e["from"])

def current(ref, seen=frozenset()):
    """Follow supersedes edges to the accepted rule(s); [] if none."""
    if rules.get(ref, {}).get("status") == "accepted":
        return {ref}
    out = set()
    for s in succ.get(ref, ()):
        if s not in seen:
            out |= current(s, seen | {ref})
    return out

stale = {ref for ref, r in rules.items() if r["status"] != "accepted"}
work = {}
for tid, t in tasks.items():
    text = t["title"] + "\n" + t["body"]
    edges = [r["ref"] for r in rules.values() if r["ref"] in stale
             for g in r["governed_tasks"] if g["id"] == tid]
    body = set()
    for m in re.finditer(r"\b([A-Z]+)-(REQ|RULE|DEF)-(\d+)(?:\s*(?:\.\.|–)\s*(\d+))?", text):
        for n in range(int(m[3]), int(m[4] or m[3]) + 1):
            if (ref := f"{m[1]}-{m[2]}-{n}") in stale:
                body.add(ref)
    sections = [m[0].strip() for m in re.finditer(r".{0,30}§[\w.–, §]{1,20}", text)]
    if edges or body or sections:
        work[tid] = {"title": t["title"],
                     "stale_edges": {e: sorted(current(e)) for e in edges},
                     "stale_body_refs": {b: sorted(current(b)) for b in sorted(body)},
                     "section_citations": sections}
json.dump(work, sys.stdout, indent=1)
