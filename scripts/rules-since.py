#!/usr/bin/env python3
"""rules-since.py <commit> [--diff]: rules whose text changed since <commit>'s commit time.

Per rule: baseline version (newest before the commit), current version, and the
agent surfaces that cite the rule by ref. --diff adds a unified diff of the text.
"""
import difflib, json, subprocess, sys
from datetime import datetime

SURFACES = ["CLAUDE.md", ".claude/skills", ".claude/rules", "plugins/claude/lode",
            ".worklode/agent-instructions.md", "internal/cmd/CLAUDE.md"]

def run(*a):
    return subprocess.run(a, capture_output=True, text=True, check=True).stdout

def ts(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00"))

def text(ref, v):
    return run("lode", "show", ref, "--version", str(v)).splitlines(keepends=True)

commit, show_diff = sys.argv[1], "--diff" in sys.argv
cut = ts(run("git", "log", "-1", "--format=%cI", commit).strip())
for r in json.loads(run("lode", "rule", "list", "--json")):
    if ts(r["updated_at"]) < cut:
        continue
    vs = json.loads(run("lode", "rule", "versions", r["ref"], "--json"))
    new = [v for v in vs if ts(v["created_at"]) >= cut]
    if not new:  # status, link or tag change only
        continue
    old = [v["version"] for v in vs if ts(v["created_at"]) < cut]
    base = max(old) if old else None
    cur = max(v["version"] for v in vs)
    cites = subprocess.run(["grep", "-rlw", "--", r["ref"], *SURFACES], capture_output=True, text=True).stdout.split()
    print(f'{r["ref"]}\t{r["status"]}\tv{base or "-"}->v{cur}\t{r["heading"]}\t{" ".join(cites) or "-"}')
    if show_diff:
        a = text(r["ref"], base) if base else []
        sys.stdout.writelines(difflib.unified_diff(a, text(r["ref"], cur), f'{r["ref"]} v{base}', f'{r["ref"]} v{cur}'))
        print()
