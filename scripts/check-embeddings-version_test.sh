#!/usr/bin/env bash
# check-embeddings-version.sh passes on the tree and fails on each kind of drift.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/check-embeddings-version.sh"
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

"$script" "$root"

files="deploy/embeddings.Dockerfile deploy/base/embeddings.yaml deploy/base/embeddings-query.yaml docker-compose.yml .github/workflows/build-embeddings-image.yml"
expect_fail() { # description, file, sed expression
	rm -rf "$tmp/r"
	for f in $files; do
		mkdir -p "$tmp/r/$(dirname "$f")" && cp "$root/$f" "$tmp/r/$f"
	done
	sed -i.bak -E "$3" "$tmp/r/$2"
	if "$script" "$tmp/r" 2>/dev/null; then
		echo "FAIL: $1 passed" >&2
		exit 1
	fi
}

expect_fail "deployment tag drift" deploy/base/embeddings-query.yaml 's/-tei[0-9.]+/-tei0.0.1/'
expect_fail "compose tag drift" docker-compose.yml 's/-tei[0-9.]+/-tei0.0.1/'
expect_fail "workflow default drift" .github/workflows/build-embeddings-image.yml 's/-tei[0-9.]+/-tei0.0.1/'
expect_fail "base versions disagree" deploy/embeddings.Dockerfile 's/cpu-arm64-[0-9.]+/cpu-arm64-0.0.1/'
expect_fail "base bumped alone" deploy/embeddings.Dockerfile 's/cpu-(arm64-)?[0-9.]+/cpu-\10.0.1/g'
echo "check-embeddings-version: ok"
