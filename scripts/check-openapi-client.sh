#!/usr/bin/env bash
# Fail if web/app/src/api/schema.d.ts is stale relative to the server's
# OpenAPI document (WL-REQ-1309). Regenerates and diffs, like
# scripts/check-generated.sh does for templ.
set -euo pipefail
cd "$(dirname "$0")/.."
make openapi
(cd web/app && pnpm gen:api)
dirty=$(git status --porcelain -uall -- web/app/src/api/schema.d.ts)
if [ -n "$dirty" ]; then
  git diff -- web/app/src/api/schema.d.ts >&2
  echo "web/app/src/api/schema.d.ts is stale: run make web-gen and commit" >&2
  exit 1
fi
