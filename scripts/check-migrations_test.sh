#!/usr/bin/env bash
# Tests scripts/check-migrations.sh's --no-fix checks: the base-max rule
# added for WL-847 (a branch-added migration numbered at or below the base
# ref's own highest number is skipped forever by golang-migrate) and a
# regression guard for the pre-existing collision rule.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CHECK="$SCRIPT_DIR/check-migrations.sh"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fails=0

# Builds a repo at $1 with a `main` branch carrying the migration keys
# passed as the remaining args, each with a trivial up/down pair listed in
# kustomization.yaml.
new_repo() {
	local dir=$1
	shift
	mkdir -p "$dir/deploy/base/migrations" "$dir/scripts"
	(
		cd "$dir"
		git init -q -b main
		git config user.email test@example.com
		git config user.name test
		cp "$CHECK" scripts/check-migrations.sh
		chmod +x scripts/check-migrations.sh
		{
			echo "apiVersion: kustomize.config.k8s.io/v1beta1"
			echo "kind: Kustomization"
			echo "configMapGenerator:"
			echo "  - name: migrations"
			echo "    files:"
		} >deploy/base/kustomization.yaml
		for key in "$@"; do
			add_files "$dir" "$key"
		done
		git add -A
		git commit -q -m baseline
	)
}

# Writes a migration's up/down pair and lists it in kustomization.yaml,
# without committing — this is what a branch "adds" on top of main.
add_files() {
	local dir=$1 key=$2
	printf -- '-- %s up\n' "$key" >"$dir/deploy/base/migrations/$key.up.sql"
	printf -- '-- %s down\n' "$key" >"$dir/deploy/base/migrations/$key.down.sql"
	echo "      - migrations/$key.up.sql" >>"$dir/deploy/base/kustomization.yaml"
	echo "      - migrations/$key.down.sql" >>"$dir/deploy/base/kustomization.yaml"
}

# Builds a repo at $1 with no `main` ref and no `origin` remote — a shallow
# CI checkout that never fetched main looks like this (WL-847).
new_repo_no_base() {
	local dir=$1
	shift
	mkdir -p "$dir/deploy/base/migrations" "$dir/scripts"
	(
		cd "$dir"
		git init -q -b work
		git config user.email test@example.com
		git config user.name test
		cp "$CHECK" scripts/check-migrations.sh
		chmod +x scripts/check-migrations.sh
		{
			echo "apiVersion: kustomize.config.k8s.io/v1beta1"
			echo "kind: Kustomization"
			echo "configMapGenerator:"
			echo "  - name: migrations"
			echo "    files:"
		} >deploy/base/kustomization.yaml
		for key in "$@"; do
			add_files "$dir" "$key"
		done
		git add -A
		git commit -q -m work
	)
}

# Runs the check in $dir and fails the test if the exit status or stderr
# don't match what's expected.
check() {
	local name=$1 dir=$2 want=$3 grep_for=${4:-} out status=0
	out=$(cd "$dir" && ./scripts/check-migrations.sh --no-fix 2>&1) || status=$?
	if [ "$want" = "pass" ] && [ "$status" -ne 0 ]; then
		echo "FAIL: $name — expected exit 0, got $status:"
		echo "$out"
		fails=$((fails + 1))
		return
	fi
	if [ "$want" = "fail" ] && [ "$status" -eq 0 ]; then
		echo "FAIL: $name — expected a nonzero exit, got 0"
		fails=$((fails + 1))
		return
	fi
	if [ -n "$grep_for" ] && ! printf '%s\n' "$out" | grep -qF "$grep_for"; then
		echo "FAIL: $name — expected output to mention '$grep_for':"
		echo "$out"
		fails=$((fails + 1))
	fi
}

# Case 1: main has up to 0071; the branch adds 0070 — below main's own max,
# no number collides, but golang-migrate would skip it forever (WL-847).
d1="$WORK/case1"
new_repo "$d1" 0071_entity_edges
add_files "$d1" 0070_label_declarations
check "adds a number below base max" "$d1" fail "0070_label_declarations"

# Case 2: main has up to 0071; the branch adds 0072 — the happy path.
d2="$WORK/case2"
new_repo "$d2" 0071_entity_edges
add_files "$d2" 0072_next_thing
check "adds the next free number" "$d2" pass

# Case 3: regression guard — two branch-added keys collide on one number.
d3="$WORK/case3"
new_repo "$d3" 0071_entity_edges
add_files "$d3" 0080_alpha
add_files "$d3" 0080_beta
check "collision between two new keys" "$d3" fail "number 80 is used by"

# Case 4: no origin/main or main ref at all (a depth-1 CI checkout that
# never fetched main) — the below-base rule can't be checked, so --no-fix
# must fail and say so rather than silently pass.
d4="$WORK/case4"
new_repo_no_base "$d4" 0071_entity_edges
check "no base ref resolvable" "$d4" fail "no base ref"

if [ "$fails" -ne 0 ]; then
	echo "$fails case(s) failed"
	exit 1
fi
echo "check-migrations: all cases pass"
