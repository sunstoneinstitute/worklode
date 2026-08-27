#!/usr/bin/env bash
# rules-session.sh — a plain Claude Code session in the agent's HOME, for
# running /auto-mode-setup by hand.
#
# Deliberately not supervisor.sh: this session must NOT start the worker loop,
# and must NOT run in auto mode — the whole point is to author the rules that
# auto mode will later enforce, with a human at the keyboard.
set -euo pipefail

AGENT_ROOT=${AGENT_ROOT:-/home/ghrunner/gha-agent}

if [ -r "$AGENT_ROOT/env" ]; then
  set -a
  # shellcheck disable=SC1091  # written by the workflow, absent at lint time
  . "$AGENT_ROOT/env"
  set +a
else
  echo "rules-session: $AGENT_ROOT/env missing or unreadable" >&2
  exit 1
fi
unset ANTHROPIC_API_KEY

cd "$AGENT_ROOT/repo"

cat <<'BANNER'
Auto-mode rule setup.

  1. Run: /auto-mode-setup
  2. Review the drafted autoMode block and accept it.
  3. Leave this window; re-run the "Agent host rules" workflow with
     action=capture to open a PR with the result.

The block is written to this session's user-scope settings:
BANNER
echo "  $HOME/.claude/settings.json"
echo

exec claude --model sonnet
