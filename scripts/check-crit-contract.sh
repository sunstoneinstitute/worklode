#!/usr/bin/env bash
# check-crit-contract.sh <tag>: re-fetch the crit source files the golden copy
# in internal/review/critjson was read from (WL-REQ-1773) and diff them against
# testdata/<tag>/upstream/<path>.txt. Run by hand when moving the pin in PIN.
# Arguments after the tag are ignored. CRIT_RAW_BASE overrides the download
# root (the test points it at a file:// mirror).
set -euo pipefail
tag=${1:?usage: check-crit-contract.sh <tag>}
base=${CRIT_RAW_BASE:-https://raw.githubusercontent.com/tomasz-tomczyk/crit}
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
upstream="$root/internal/review/critjson/testdata/$tag/upstream"
[[ -d $upstream ]] || { echo "no golden copy for $tag at $upstream" >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

status=0
while IFS= read -r -d '' copy; do
	rel=${copy#"$upstream/"}
	rel=${rel%.txt}
	curl -fsSL "$base/$tag/$rel" -o "$tmp/fetched"
	if ! diff -u --label "golden/$rel" --label "$tag/$rel" "$copy" "$tmp/fetched"; then
		status=1
	fi
done < <(find "$upstream" -name '*.txt' -print0)
exit "$status"
