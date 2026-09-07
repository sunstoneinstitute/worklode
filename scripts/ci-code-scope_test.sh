#!/usr/bin/env bash
# Tests the gate's code-scoping decision in .github/workflows/pr-checks.yml:
# which changed-file sets let a PR skip lint, test and build-image (WL-718).
#
# The two patterns are read out of the workflow rather than copied, so this
# tests what CI actually runs. The case that matters most is the last group:
# deploy/base/migrations/ is the one thing under deploy/ that Go reads
# (store.MigrationsDirForTests, scripts/check-migrations.sh), so a migration
# must never skip the suite.
set -euo pipefail

WORKFLOW="$(dirname "$0")/../.github/workflows/pr-checks.yml"

pattern() {
    # The workflow assigns each pattern on its own line, single-quoted.
    sed -n "s/^ *$1='\(.*\)'$/\1/p" "$WORKFLOW"
}

CODE_INERT="$(pattern CODE_INERT)"
CODE_ANYWAY="$(pattern CODE_ANYWAY)"
[ -n "$CODE_INERT" ] || { echo "FAIL: CODE_INERT not found in $WORKFLOW"; exit 1; }
[ -n "$CODE_ANYWAY" ] || { echo "FAIL: CODE_ANYWAY not found in $WORKFLOW"; exit 1; }

# The gate's condition, verbatim. Prints what it would set `code` to.
scope() {
    local changed="$1"
    if [ -n "$changed" ] \
       && ! echo "$changed" | grep -qvE "$CODE_INERT" \
       && ! echo "$changed" | grep -qE "$CODE_ANYWAY"; then
        echo false
    else
        echo true
    fi
}

fails=0
check() {
    local want="$1" name="$2" changed="$3" got
    got="$(scope "$changed")"
    if [ "$got" != "$want" ]; then
        echo "FAIL: $name — want code=$want, got code=$got"
        fails=$((fails + 1))
    fi
}

# Skips the Go jobs: nothing here is read by the Go build.
check false "deploy manifest only"   'deploy/base/embeddings.yaml'
check false "several deploy files"   'deploy/base/embeddings.yaml
deploy/overlays/hzdev/kustomization.yaml'
check false "deploy plus docs"       'deploy/base/embeddings.yaml
docs/github-advanced-setup.md'
check false "docs only"              'docs/follow-ups.md'

# Runs the Go jobs.
check true  "migration only"         'deploy/base/migrations/0067_x.up.sql'
check true  "migration beside a manifest" 'deploy/base/embeddings.yaml
deploy/base/migrations/0067_x.up.sql'
check true  "go file"                'internal/indexer/indexer.go'
check true  "go file beside deploy"  'deploy/base/embeddings.yaml
internal/indexer/indexer.go'
check true  "workflow change"        '.github/workflows/pr-checks.yml'
check true  "agent surface"          'CLAUDE.md'
check true  "plugin skill"           'plugins/claude/lode/skills/done/SKILL.md'
check true  "empty list fails open"  ''

if [ "$fails" -ne 0 ]; then
    echo "$fails case(s) failed"
    exit 1
fi
echo "ci-code-scope: all cases pass"
