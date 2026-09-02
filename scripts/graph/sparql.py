#!/usr/bin/env python3
"""Run a .rq file against a HornDB endpoint and print the results as a table.

Usage: sparql.py <port> <query.rq>
"""
import json
import subprocess
import sys


def main(port: str, path: str) -> int:
    raw = subprocess.run(
        ["curl", "-s", "-G", f"http://127.0.0.1:{port}/query",
         "--data-urlencode", f"query@{path}",
         "-H", "Accept: application/sparql-results+json"],
        capture_output=True, text=True).stdout
    try:
        doc = json.loads(raw)
    except json.JSONDecodeError:
        print(raw[:1000], file=sys.stderr)
        return 1

    names = doc["head"]["vars"]
    rows = doc["results"]["bindings"]

    def cell(row, name):
        if name not in row:
            return ""
        value = row[name]["value"]
        # IRIs are long and their last segment is the readable part (task id,
        # concept name); literals print as-is.
        return value.rsplit("/", 1)[-1] if value.startswith("http") else value

    width = [max([len(n)] + [len(cell(r, n)) for r in rows]) for n in names]
    print(" | ".join(n.ljust(width[i]) for i, n in enumerate(names)))
    print("-+-".join("-" * w for w in width))
    for row in rows:
        print(" | ".join(cell(row, n).ljust(width[i]) for i, n in enumerate(names)))
    print(f"\n({len(rows)} rows)", file=sys.stderr)
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    sys.exit(main(sys.argv[1], sys.argv[2]))
