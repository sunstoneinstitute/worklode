#!/usr/bin/env bash
# Check that every reference to the embedding image agrees with
# deploy/embeddings.Dockerfile. The Dockerfile's MODEL_ID and its TEI base
# versions define the image tag, <model>-tei<version>; the deployments,
# compose and the build workflow's default tag must all name that tag.
#
# Usage: check-embeddings-version.sh [repo-root]
set -euo pipefail
cd "${1:-$(git rev-parse --show-toplevel)}"

dockerfile=deploy/embeddings.Dockerfile
model=$(sed -n 's|^ARG MODEL_ID=.*/||p' "$dockerfile")
versions=$(grep -oE 'text-embeddings-inference:cpu-(arm64-)?[0-9][^ ]*' "$dockerfile" | sed -E 's/.*cpu-(arm64-)?//' | sort -u)

fail=0
if [ "$(printf '%s\n' "$versions" | grep -c .)" -ne 1 ]; then
	echo "$dockerfile: TEI base versions disagree: $(echo $versions)" >&2
	exit 1
fi
want="$model-tei$versions"

check() { # file, extracted tag
	if [ "$2" != "$want" ]; then
		echo "$1: embedding image tag is $2, want $want (from $dockerfile)" >&2
		fail=1
	fi
}

for f in deploy/base/embeddings.yaml deploy/base/embeddings-query.yaml docker-compose.yml; do
	tags=$(grep -oE 'worklode-embeddings:[^[:space:]"]+' "$f" | sed 's/.*://' || true)
	[ -n "$tags" ] || { echo "$f: no worklode-embeddings image found" >&2; fail=1; }
	for t in $tags; do check "$f" "$t"; done
done

wf=.github/workflows/build-embeddings-image.yml
check "$wf" "$(sed -n 's/^ *default: *//p' "$wf")"

exit $fail
