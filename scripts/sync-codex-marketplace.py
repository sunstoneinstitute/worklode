#!/usr/bin/env python3
"""Generate Codex plugin metadata from the Claude marketplace metadata.

The Claude JSON is the source of truth. Never hand-edit
`.agents/plugins/marketplace.json` or any `.codex-plugin/plugin.json`: edit
`.claude-plugin/marketplace.json`, then run this script. Pre-commit and CI run
it with --check, so the two cannot drift apart silently.

Ported from sunstoneinstitute/claude-plugins (489e112). Stdlib only, so it
runs as a plain script like secfmt.py rather than needing a Python
environment.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
CLAUDE_MARKETPLACE = ROOT / ".claude-plugin" / "marketplace.json"
CODEX_MARKETPLACE = ROOT / ".agents" / "plugins" / "marketplace.json"
SURFACE_TAG = re.compile(r"^\[(?:code|cowork|desktop|web)(?: (?:code|cowork|desktop|web))*\]\s*")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate Codex manifests from .claude-plugin/marketplace.json."
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="report generated-file drift without writing files",
    )
    return parser.parse_args()


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read {path.relative_to(ROOT)}: {error}") from error
    if not isinstance(value, dict):
        raise ValueError(f"{path.relative_to(ROOT)} must contain a JSON object")
    return value


def clean_description(description: str) -> str:
    """Remove Claude surface availability from a shared description."""
    return SURFACE_TAG.sub("", description, count=1)


def display_name(name: str) -> str:
    return " ".join(part.capitalize() for part in name.split("-"))


def require_string(
    value: dict[str, Any], key: str, *, context: str = "marketplace entry"
) -> str:
    result = value.get(key)
    if not isinstance(result, str) or not result.strip():
        raise ValueError(f"{context} field {key!r} must be a non-empty string")
    return result


def codex_manifest(entry: dict[str, Any], plugin_root: Path) -> dict[str, Any]:
    name = require_string(entry, "name")
    description = clean_description(require_string(entry, "description"))
    author = entry.get("author")
    if not isinstance(author, dict):
        raise ValueError(f"plugin {name!r} has no author object")
    author_name = require_string(author, "name", context=f"plugin {name!r} author")

    manifest: dict[str, Any] = {
        "name": name,
        "version": require_string(entry, "version", context=f"plugin {name!r}"),
        "description": description,
        "author": author,
        "keywords": entry.get("keywords", []),
    }
    if (plugin_root / "skills").is_dir():
        manifest["skills"] = "./skills/"
    manifest["interface"] = {
        "displayName": display_name(name),
        "shortDescription": description.split(".", 1)[0].strip(),
        "longDescription": description,
        "developerName": author_name,
        "category": "Productivity",
        "capabilities": ["Instructions"],
        "defaultPrompt": [f"Help me use the {display_name(name)} plugin."],
    }
    return manifest


def marketplace_display_name(source: dict[str, Any]) -> str:
    """The Codex storefront name, taken from the Claude file rather than hardcoded."""
    interface = source.get("interface")
    if isinstance(interface, dict):
        value = interface.get("displayName")
        if isinstance(value, str) and value.strip():
            return value
    return display_name(require_string(source, "name", context="Claude marketplace"))


def generated_files() -> dict[Path, str]:
    source = load_json(CLAUDE_MARKETPLACE)
    entries = source.get("plugins")
    if not isinstance(entries, list):
        raise ValueError(".claude-plugin/marketplace.json field 'plugins' must be an array")

    files: dict[Path, str] = {}
    codex_entries: list[dict[str, Any]] = []
    seen: set[str] = set()
    for raw_entry in entries:
        if not isinstance(raw_entry, dict):
            raise ValueError("every Claude marketplace plugin must be an object")
        name = require_string(raw_entry, "name")
        if name in seen:
            raise ValueError(f"duplicate plugin name {name!r}")
        seen.add(name)
        plugin_root = ROOT / "plugins" / name
        if not plugin_root.is_dir():
            raise ValueError(f"plugin directory does not exist: plugins/{name}")

        manifest = codex_manifest(raw_entry, plugin_root)
        files[plugin_root / ".codex-plugin" / "plugin.json"] = encode(manifest)
        codex_entries.append(
            {
                "name": name,
                "source": {"source": "local", "path": f"./plugins/{name}"},
                "policy": {
                    "installation": "AVAILABLE",
                    "authentication": "ON_INSTALL",
                },
                "category": "Productivity",
            }
        )

    marketplace = {
        "name": require_string(source, "name", context="Claude marketplace"),
        "interface": {"displayName": marketplace_display_name(source)},
        "plugins": codex_entries,
    }
    files[CODEX_MARKETPLACE] = encode(marketplace)
    return files


def encode(value: Any) -> str:
    return json.dumps(value, indent=2, ensure_ascii=False) + "\n"


def main() -> int:
    args = parse_args()
    try:
        files = generated_files()
    except ValueError as error:
        print(f"error: {error}", file=sys.stderr)
        return 2

    expected_paths = set(files)
    stale = sorted(
        path
        for path in ROOT.glob("plugins/*/.codex-plugin/plugin.json")
        if path not in expected_paths
    )
    drift: list[Path] = []
    for path, expected in files.items():
        actual = path.read_text(encoding="utf-8") if path.is_file() else None
        if actual == expected:
            continue
        drift.append(path)
        if not args.check:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(expected, encoding="utf-8")

    drift.extend(stale)
    if not args.check:
        for path in stale:
            path.unlink()

    if args.check and drift:
        print("Codex marketplace files are out of date:", file=sys.stderr)
        for path in drift:
            print(f"- {path.relative_to(ROOT)}", file=sys.stderr)
        print("Run: python3 scripts/sync-codex-marketplace.py", file=sys.stderr)
        return 1

    action = "Checked" if args.check else "Generated"
    print(f"{action} {len(files)} Codex marketplace files.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
