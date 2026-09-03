#!/usr/bin/env bash
# poke.sh — the sidecar for one supervisor window.
#
# The lode-worker loop stops when `lode work next` reports no ready work, which
# leaves the supervisor session idle forever. This waits for work to appear
# and nudges the supervisor back into the loop.
#
# Usage: poke.sh <window> [lode work next filter flags...]
#   e.g. poke.sh gha-chore1 --project worklode --kind chore
#
# The filter is passed through to `lode worker listen` unchanged, and is the
# same string supervisor.sh substitutes into the skill body. That is the point
# of the two sharing a vocabulary: the sidecar wakes on exactly the work its
# supervisor could claim.
set -euo pipefail

AGENT_ROOT=${AGENT_ROOT:-/home/ghrunner/gha-agent}
SOCKET=${LODE_TMUX_SOCKET:-gha-agent}
SESSION=${LODE_TMUX_SESSION:-agents}

# How long the pane must stay byte-identical to count as idle. A working
# Claude session animates a spinner, so a still pane means it is waiting.
IDLE_SETTLE=${IDLE_SETTLE:-4}
# After poking, wait before listening again: the supervisor needs a moment to
# claim, and re-poking the same task in the meantime is pure noise.
POKE_COOLDOWN=${POKE_COOLDOWN:-120}
# Backoff after `lode worker listen` exits non-zero (a rejected token, an
# unknown project). Long enough that a misconfigured sidecar does not hammer
# the backbone.
ERROR_BACKOFF=${ERROR_BACKOFF:-60}

if [ "$#" -lt 1 ]; then
  echo "poke: usage: poke.sh <window> [filter flags...]" >&2
  exit 2
fi
WINDOW=$1
shift

if [ -r "$AGENT_ROOT/env" ]; then
  set -a
  # shellcheck disable=SC1091  # written by the workflow, absent at lint time
  . "$AGENT_ROOT/env"
  set +a
fi

target="$SESSION:$WINDOW"

# pane_state is the pane's visible tail, hashed. Empty when the window is
# gone, which is how the loop learns to stand down.
pane_state() {
  tmux -L "$SOCKET" capture-pane -p -S -20 -t "$target" 2>/dev/null | cksum
}

# is_idle: two reads of the pane, IDLE_SETTLE apart, that agree.
#
# This is a heuristic, and the reason it is acceptable is its failure mode: a
# busy session mistaken for an idle one receives text into its input box,
# which costs a turn. It cannot corrupt state or claim anything.
is_idle() {
  local before after
  before=$(pane_state)
  [ -n "$before" ] || return 1
  sleep "$IDLE_SETTLE"
  after=$(pane_state)
  [ -n "$after" ] || return 1
  [ "$before" = "$after" ]
}

poke_text="Ready work is available. Start a lode-worker agent and run the loop again: \`lode work next --json $*\`, then work the claimed task from its brief as in your initial instructions."

while :; do
  # The window disappearing means the babysitter is rebuilding it, or the
  # host is being torn down. Either way this sidecar has nothing left to
  # nudge, so it exits rather than looping against a dead target.
  if ! tmux -L "$SOCKET" has-session -t "$target" 2>/dev/null; then
    echo "poke: $target is gone; exiting" >&2
    exit 0
  fi

  # --once so restart policy lives here rather than inside the command.
  if ! lode worker listen --once "$@"; then
    echo "poke: lode worker listen failed; retrying in ${ERROR_BACKOFF}s" >&2
    sleep "$ERROR_BACKOFF"
    continue
  fi

  if is_idle; then
    tmux -L "$SOCKET" send-keys -t "$target" "$poke_text" Enter
    echo "poke: nudged $target"
    sleep "$POKE_COOLDOWN"
  fi
done
