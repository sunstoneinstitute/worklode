#!/usr/bin/env bash
# check-crit-contract.sh passes against an identical mirror and fails on drift.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/check-crit-contract.sh"
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
pin=$(< "$root/internal/review/critjson/PIN")
tag=${pin%% *}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/$tag"
cp -R "$root/internal/review/critjson/testdata/$tag/upstream/." "$tmp/$tag/"
while IFS= read -r -d '' f; do mv "$f" "${f%.txt}"; done < <(find "$tmp/$tag" -name '*.txt' -print0)

CRIT_RAW_BASE="file://$tmp" "$script" "$tag" > /dev/null

echo '// drift' >> "$tmp/$tag/internal/hooks/env.go"
if CRIT_RAW_BASE="file://$tmp" "$script" "$tag" > /dev/null; then
	echo "FAIL: drift in env.go passed" >&2
	exit 1
fi
echo "check-crit-contract: ok"
