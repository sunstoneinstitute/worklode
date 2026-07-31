#!/usr/bin/env bash
# Enforce one migration per number in deploy/base/migrations.
#
# A migration is the pair <number>_<name>.up.sql / <number>_<name>.down.sql.
# Numbers are compared numerically, so 06 and 0006 count as the same one.
#
# Two branches that each add the next number collide once they meet in a merge
# or rebase. When that happens the migration that is not yet on the base branch
# yields: it is renumbered to the next free number (files renamed, the
# kustomization file list rewritten, both staged) and the commit is stopped so
# the rename can be reviewed. Re-running the commit then succeeds.
#
# Usage: check-migrations.sh [--no-fix]
#   --no-fix  report collisions instead of renumbering (for CI)

set -euo pipefail

MIG_DIR="deploy/base/migrations"
KUSTOMIZATION="deploy/base/kustomization.yaml"

fix=1
case "${1:-}" in
	--no-fix) fix=0 ;;
	"") ;;
	*)
		echo "usage: $0 [--no-fix]" >&2
		exit 2
		;;
esac

cd "$(git rev-parse --show-toplevel)"
[ -d "$MIG_DIR" ] || exit 0

fail=0
err() {
	echo "migrations: $*" >&2
	fail=1
}

list_files() { ls -1 "$MIG_DIR" 2>/dev/null || true; }
strip_suffix() { sed -nE 's/\.(up|down)\.sql$//p'; }

files=$(list_files)

bad=$(printf '%s\n' "$files" | grep -v '^$' | grep -Ev '^[0-9]+_[A-Za-z0-9_-]+\.(up|down)\.sql$' || true)
if [ -n "$bad" ]; then
	err "not named <number>_<name>.(up|down).sql:"
	printf '  %s\n' $bad >&2
fi

keys=$(printf '%s\n' "$files" | strip_suffix | sort -u)
if [ -z "$keys" ]; then
	exit "$fail"
fi

for key in $keys; do
	for suffix in up down; do
		[ -f "$MIG_DIR/$key.$suffix.sql" ] || err "$key is missing $key.$suffix.sql"
	done
done

# One "<numeric value>\t<key>" line per migration, e.g. "6\t0006_task_hierarchy".
pairs=$(for key in $keys; do
	num=${key%%_*}
	printf '%d\t%s\n' "$((10#$num))" "$key"
done)

# The base branch decides who yields: whoever is already there keeps the number.
base=""
for ref in origin/main main; do
	if git rev-parse --verify --quiet "$ref^{commit}" >/dev/null; then
		base=$ref
		break
	fi
done
base_keys=""
if [ -n "$base" ]; then
	base_keys=$(git ls-tree -r --name-only "$base" -- "$MIG_DIR" | sed 's|.*/||' | strip_suffix | sort -u)
fi

max=$(printf '%s\n' "$pairs" | cut -f1 | sort -n | tail -1)
dups=$(printf '%s\n' "$pairs" | cut -f1 | sort -n | uniq -d)
renamed=0

for dup in $dups; do
	dup_keys=$(printf '%s\n' "$pairs" | awk -F'\t' -v n="$dup" '$1 == n {print $2}' | sort)
	if [ "$fix" -eq 0 ]; then
		err "number $dup is used by $(echo $dup_keys)"
		continue
	fi

	keep=""
	for key in $dup_keys; do
		if printf '%s\n' "$base_keys" | grep -Fxq "$key"; then
			keep=$key
			break
		fi
	done
	[ -n "$keep" ] || keep=$(printf '%s\n' "$dup_keys" | head -1)

	prefix=${keep%%_*}
	width=${#prefix}
	for key in $(printf '%s\n' "$dup_keys" | grep -Fxv "$keep"); do
		max=$((max + 1))
		new_key=$(printf "%0${width}d_%s" "$max" "${key#*_}")
		for suffix in up down; do
			old="$MIG_DIR/$key.$suffix.sql"
			new="$MIG_DIR/$new_key.$suffix.sql"
			[ -f "$old" ] || continue
			mv -- "$old" "$new"
			git add -- "$new"
			# Stage the disappearance of the old path only when git knows
			# it; an uncommitted migration has no index entry to update.
			if git ls-files --error-unmatch -- "$old" >/dev/null 2>&1; then
				git add -A -- "$old"
			fi
			if [ -f "$KUSTOMIZATION" ]; then
				sed -i.bak "s|migrations/$key.$suffix.sql|migrations/$new_key.$suffix.sql|" "$KUSTOMIZATION"
				rm -f "$KUSTOMIZATION.bak"
			fi
		done
		echo "migrations: $key yielded $dup to $keep, renumbered to $new_key" >&2
		renamed=1
	done
done

if [ "$renamed" -eq 1 ] && [ -f "$KUSTOMIZATION" ]; then
	git add -- "$KUSTOMIZATION"
fi

# Every migration must be in the configMapGenerator, or the deploy ships a
# schema the code does not expect.
if [ -f "$KUSTOMIZATION" ]; then
	listed=$(sed -n 's|^[[:space:]]*-[[:space:]]*migrations/\(.*\)$|\1|p' "$KUSTOMIZATION" | sort)
	present=$(list_files | grep -v '^$' | sort)
	missing=$(comm -13 <(printf '%s\n' "$listed") <(printf '%s\n' "$present"))
	stale=$(comm -23 <(printf '%s\n' "$listed") <(printf '%s\n' "$present"))
	[ -z "$missing" ] || err "not listed in $KUSTOMIZATION: $(echo $missing)"
	[ -z "$stale" ] || err "listed in $KUSTOMIZATION but absent: $(echo $stale)"
fi

if [ "$renamed" -eq 1 ]; then
	echo "migrations: renumbered and staged the renames — review, then commit again" >&2
	exit 1
fi
exit "$fail"
