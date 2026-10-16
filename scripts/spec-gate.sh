#!/bin/sh
# CI's spec gate (WL-REQ-7, WL-REQ-1791), run before a push. pre-commit sets
# PRE_COMMIT_TO_REF to the commit being pushed; the base is where it forked
# from origin/main, as a pull request's base would be.
set -e
head=${PRE_COMMIT_TO_REF:-HEAD}
base=$(git merge-base origin/main "$head")
exec go run -trimpath ./cmd/lode-gate-check --base "$base" --head "$head"
