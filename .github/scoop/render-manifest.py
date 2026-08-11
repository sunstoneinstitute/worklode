# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
import json
import os
import sys

template_path, out_path = sys.argv[1], sys.argv[2]

replacements = {
    "__VERSION__": os.environ["VERSION"],
    "__URL__": os.environ["URL"],
    "__SHA256__": os.environ["SHA256"],
}

with open(template_path) as f:
    rendered = f.read()

for token, value in replacements.items():
    rendered = rendered.replace(token, value)

# A leftover placeholder means an env var was missing or the template drifted;
# fail loudly rather than push a manifest Scoop cannot install from.
if "__" in rendered:
    sys.exit("error: unresolved placeholder ('__') remains in the rendered manifest")

# The manifest is pushed as-is; a syntactically broken one bricks the bucket for
# every user, so prove it parses before writing it.
try:
    json.loads(rendered)
except json.JSONDecodeError as exc:
    sys.exit(f"error: rendered manifest is not valid JSON: {exc}")

with open(out_path, "w") as f:
    f.write(rendered)
