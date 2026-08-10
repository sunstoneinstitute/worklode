#!/usr/bin/env bash
# Fail if committed generated artifacts are stale relative to their sources.
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/gen-web.sh
if ! git diff --exit-code -- 'internal/api/*_templ.go' internal/api/assets/app.css; then
  echo "generated artifacts are stale — run ./scripts/gen-web.sh and commit" >&2
  exit 1
fi
