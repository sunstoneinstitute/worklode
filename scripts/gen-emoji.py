#!/usr/bin/env python3
"""Regenerate internal/ui/assets/emoji.json from goldmark-emoji's own data.

The editor's shortcode completion (mdinput.js) must offer exactly the names
mdrender will substitute, so the list is derived from the same module rather
than hand-kept: goldmark-emoji ships its GitHub emoji table as
_tools/github.json in the module zip. Output is {shortname: glyph}, sorted.
internal/mdrender/emojiasset_test.go fails if the two ever disagree.
"""
import json
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(__file__).resolve().parent.parent
gomod = (root / "go.mod").read_text()
m = re.search(r"github\.com/yuin/goldmark-emoji (v\S+)", gomod)
if not m:
    sys.exit("goldmark-emoji not found in go.mod")
cache = subprocess.check_output(["go", "env", "GOMODCACHE"], text=True).strip()
src = pathlib.Path(cache) / "github.com/yuin" / f"goldmark-emoji@{m.group(1)}" / "_tools/github.json"
data = json.loads(src.read_text())["data"]

out = {}
for e in data:
    glyph = "".join(chr(int(c)) for c in e["Unicode"])
    for name in e["ShortNames"]:
        out[name] = glyph

dst = root / "internal/ui/assets/emoji.json"
dst.write_text(json.dumps(dict(sorted(out.items())), ensure_ascii=False, separators=(",", ":")) + "\n")
print(f"{dst.relative_to(root)}: {len(out)} shortcodes")
