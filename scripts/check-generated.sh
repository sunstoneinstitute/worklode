#!/usr/bin/env bash
# Fail if committed generated artifacts are stale relative to their sources.
set -euo pipefail
cd "$(dirname "$0")/.."
paths=('internal/api/*_templ.go' 'internal/ui/*_templ.go' internal/ui/assets/app.css)
./scripts/gen-web.sh
# `git status`, not `git diff`: a generated file whose source was added but
# whose output was never committed is untracked, and `git diff` says nothing
# about untracked paths. -uall lists such a file individually rather than
# collapsing it into its parent directory.
dirty=$(git status --porcelain -uall -- "${paths[@]}")
if [ -n "$dirty" ]; then
  echo "$dirty" >&2
  git diff -- "${paths[@]}" >&2
  echo "generated artifacts are stale — run ./scripts/gen-web.sh and commit" >&2
  exit 1
fi
