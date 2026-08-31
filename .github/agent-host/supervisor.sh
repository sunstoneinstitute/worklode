#!/usr/bin/env bash
# supervisor.sh — the command a gha-agent tmux window runs.
#
# Starts an interactive Claude Code session that begins the Worklode agent
# loop. Claude Code has no flag that auto-runs a slash command in an
# interactive session, and -p is one-shot, so the loop is started by passing
# the skill's own body as the initial prompt.
#
# The prompt text is READ FROM THE SKILL FILE at launch rather than copied
# here, so there is no second version to drift. The babysitter keeps each
# worker's tree fast-forwarded to origin/main, which means an edit to the
# skill reaches every worker on its next respawn.
#
# Usage: supervisor.sh <window> [focus...]
#   e.g. supervisor.sh gha-chore1 --project worklode --kind chore
#        supervisor.sh gha-spec1 --project worklode 032
#        supervisor.sh gha-unblock1 --project worklode unblock
#
# The window names the worker, and its working tree is derived from it rather
# than configured separately: every worker gets its own linked worktree at
# $AGENT_ROOT/agents/<window>. They share one .git, but never a working tree —
# a working tree is one HEAD, one index and one set of untracked files, so
# two agents in the same one would contend over all three.
#
# Everything after the window is substituted for $ARGUMENTS in the skill body,
# so it takes whatever focus that skill resolves: the scope and filter flags
# (--project, --repo, --kind, --strict-focus, --max), a spec or plan ref, a
# task id, or one of its focus words (bugs, chores, unblock).
set -euo pipefail

AGENT_ROOT=${AGENT_ROOT:-/home/ghrunner/gha-agent}
SKILL_PATH=plugins/claude/lode/skills/start-agent-loop/SKILL.md

if [ "$#" -lt 1 ]; then
  echo "supervisor: usage: supervisor.sh <window> [filter flags...]" >&2
  exit 2
fi
WINDOW=$1
shift
WORKTREE="$AGENT_ROOT/agents/$WINDOW"

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

# reconcile.sh creates this before starting the window. Failing loudly beats
# falling back to the primary clone: that is the shared tree this split exists
# to keep agents out of.
if [ ! -d "$WORKTREE/.git" ] && [ ! -f "$WORKTREE/.git" ]; then
  echo "supervisor: $WORKTREE is not a git worktree; run reconcile.sh first" >&2
  exit 1
fi
cd "$WORKTREE"

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
