#!/usr/bin/env python3
"""Report specs under docs/specs/ that no plan under docs/plans/ covers.

A spec counts as covered the moment any plan's `covers:` frontmatter
references it, at any section, at any coverage level (full/partial/none) --
that a plan claims `none` on a section still means a human has looked at it.
Section-level partial-coverage gaps are a plan's own declared debt (see
docs/authoring-design-docs.md and splitting-specs-into-plans); this script
only reports specs nobody has planned against at all.

Prints one JSON object per line (JSONL) to stdout, one per uncovered spec:
  {"number": 40, "file": "docs/specs/040-....md", "title": "...", "status": "draft"}

Usage: find_gaps.py [repo-root]
"""

import glob
import json
import os
import re
import sys

SPEC_RE = re.compile(r"^(\d+)-.*\.md$")
FRONTMATTER_RE = re.compile(r"\A---\n(.*?)\n---\n", re.S)
H1_RE = re.compile(r"^#\s*Spec\s+\d+\s*—\s*(.+)$", re.M)


def frontmatter(path):
    text = open(path, encoding="utf-8").read()
    m = FRONTMATTER_RE.match(text)
    return text, (m.group(1) if m else "")


def covered_spec_files(plans_dir):
    covered = set()
    for path in sorted(glob.glob(os.path.join(plans_dir, "*.md"))):
        _, fm = frontmatter(path)
        if not fm:
            continue
        refs = []
        # object form, one per line: "  - spec: docs/specs/NNN-....md#sec-N"
        refs += re.findall(r"(?:^|\n)\s*-\s*spec:\s*([^\n]+)", fm)
        # scalar or list form: "covers: docs/specs/NNN-....md" or "covers:\n  - ..."
        m = re.search(r"^covers:\s*([^\n]+)$", fm, re.M)
        if m and m.group(1).strip():
            refs.append(m.group(1).strip())
        m = re.search(r"^covers:\s*\n((?:\s+-\s+[^\n]+\n)+)", fm, re.M)
        if m:
            for line in m.group(1).splitlines():
                v = line.strip().lstrip("-").strip()
                if v and not v.startswith("spec:"):
                    refs.append(v)
        for ref in refs:
            ref = ref.split("#", 1)[0].strip()
            if ref and ref != "NO-SPEC":
                covered.add(os.path.basename(ref))
    return covered


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    specs_dir = os.path.join(root, "docs", "specs")
    plans_dir = os.path.join(root, "docs", "plans")

    covered = covered_spec_files(plans_dir)

    for path in sorted(glob.glob(os.path.join(specs_dir, "*.md"))):
        base = os.path.basename(path)
        m = SPEC_RE.match(base)
        if not m:
            continue  # index.yaml, fold.yaml, mapping.yaml, inlined/
        number = int(m.group(1))
        if base in covered:
            continue
        text, fm = frontmatter(path)
        status_m = re.search(r"^status:\s*(\S+)", fm, re.M)
        status = status_m.group(1) if status_m else "unknown"
        if status == "superseded":
            continue
        title_m = H1_RE.search(text)
        title = title_m.group(1).strip() if title_m else base
        print(json.dumps({
            "number": number,
            "file": os.path.relpath(path, root),
            "title": title,
            "status": status,
        }))


if __name__ == "__main__":
    main()
