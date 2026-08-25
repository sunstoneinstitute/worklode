#!/usr/bin/env bash
# supervisor.sh — the command a gha-agent tmux window runs.
#
# Starts an interactive Claude Code session that begins the Worklode agent
# loop. Claude Code has no flag that auto-runs a slash command in an
# interactive session, and -p is one-shot, so the loop is started by passing
# the skill's own body as the initial prompt.
#
# The prompt text is READ FROM THE SKILL FILE at launch rather than copied
# here, so there is no second version to drift. The babysitter keeps the
# checkout fast-forwarded to main, which means an edit to the skill reaches
# every worker on its next respawn.
#
# Usage: supervisor.sh [lode next filter flags...]
#   e.g. supervisor.sh --project worklode --kind chore
#
# Anything passed here is substituted for $ARGUMENTS in the skill body, so
# only flags the skill accepts belong on this command line: --project,
# --kind, --strict-focus.
set -euo pipefail

AGENT_ROOT=${AGENT_ROOT:-/home/ghrunner/gha-agent}
SKILL_PATH=plugins/claude/lode/skills/start-agent-loop/SKILL.md

# The env file carries CLAUDE_CODE_OAUTH_TOKEN, GH_TOKEN, HOME and PATH. It
# is sourced here, inside the window's own shell, rather than passed through
# `tmux -e`: a secret on a tmux command line is visible in `ps` to every user
# on the box.
if [ -r "$AGENT_ROOT/env" ]; then
  set -a
  # shellcheck disable=SC1091  # written by the workflow, absent at lint time
  . "$AGENT_ROOT/env"
  set +a
else
  echo "supervisor: $AGENT_ROOT/env missing or unreadable" >&2
  exit 1
fi

# ANTHROPIC_API_KEY outranks CLAUDE_CODE_OAUTH_TOKEN in Claude Code's auth
# precedence, so a stray one on the box would silently take over and
# authenticate as the wrong principal.
unset ANTHROPIC_API_KEY

cd "$AGENT_ROOT/repo"

if [ ! -r "$SKILL_PATH" ]; then
  echo "supervisor: $SKILL_PATH not found under $PWD" >&2
  exit 1
fi

# Strip the YAML frontmatter: everything through the second '---'. The
# frontmatter is harness metadata (model, allowed-tools), not instructions,
# and feeding it to the model as prose would just be noise.
prompt=$(awk 'BEGIN{n=0} /^---$/ && n<2 {n++; next} n>=2' "$SKILL_PATH")
if [ -z "${prompt//[[:space:]]/}" ]; then
  echo "supervisor: extracted an empty prompt from $SKILL_PATH" >&2
  exit 1
fi
prompt=${prompt//\$ARGUMENTS/$*}

exec claude --model sonnet --permission-mode auto "$prompt"
