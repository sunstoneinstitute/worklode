#!/usr/bin/env bash
# reconcile.sh — bring the tmux worker fleet to the state workers.json describes.
#
# Idempotent by design: create what is missing, respawn what died, touch
# nothing that is healthy. Run from the repository root. Called by
# .github/workflows/agent-host.yml, and safe to run by hand on the box.
#
# Environment:
#   AGENT_ROOT        default /home/ghrunner/gha-agent
#   TMUX_SOCKET       default gha-agent
#   TMUX_SESSION      default agents
#   ONLY_WINDOW       reconcile just this window (empty: all of them)
#   DUMP_SCROLLBACK   "true" to print pane tails into the job log
#   RUN_ID            names the scrollback capture file
set -euo pipefail

AGENT_ROOT=${AGENT_ROOT:-/home/ghrunner/gha-agent}
SOCKET=${TMUX_SOCKET:-gha-agent}
SESSION=${TMUX_SESSION:-agents}
ONLY_WINDOW=${ONLY_WINDOW:-}
DUMP_SCROLLBACK=${DUMP_SCROLLBACK:-false}
RUN_ID=${RUN_ID:-manual}
WORKERS=${WORKERS:-.github/agent-host/workers.json}

BIN="$AGENT_ROOT/bin"
REPO="$AGENT_ROOT/repo"
LOGS="$AGENT_ROOT/logs"
# How long pane captures are kept. At the 15-minute cadence this is ~96 files
# per window per day, so an unbounded directory would quietly become the next
# disk-pressure incident on a host that has had one before (WL-188).
LOG_RETENTION_DAYS=${LOG_RETENTION_DAYS:-7}
install -d -m 0700 "$LOGS"
find "$LOGS" -type f -name '*.log' -mtime "+$LOG_RETENTION_DAYS" -delete 2>/dev/null || true

tm() { tmux -L "$SOCKET" "$@"; }

summary() { [ -n "${GITHUB_STEP_SUMMARY:-}" ] && printf '%s\n' "$*" >> "$GITHUB_STEP_SUMMARY"; return 0; }

# pane_command reports "<dead>:<command>" for a window, or nothing at all when
# the window does not exist.
#
# list-panes, not display-message: display-message silently falls back to the
# session's *current* window when its target is missing, so asking about a
# window that was never created cheerfully answers "0:bash" — which reads as a
# supervisor that crashed back to a prompt, and would have this script respawn
# a window it had just created.
#
# `|| true` is load-bearing under `set -e -o pipefail`: a missing window makes
# list-panes exit 1, which would otherwise abort the whole reconcile at the
# first worker that does not exist yet — precisely the case this script is
# for. Absence is an answer here, not a failure.
pane_command() {
  tm list-panes -t "$SESSION:$1" -F '#{pane_dead}:#{pane_current_command}' 2>/dev/null | head -n1 || true
}

# A supervisor is unhealthy when its pane is dead or has fallen back to a bare
# shell. Deliberately a denylist: claude runs as `node` today, but matching
# only that would turn any change in how it is launched — a wrapper, a
# different runtime — into an infinite respawn loop against a session that was
# working fine. Naming the shells asks the question that actually matters,
# which is whether the command we started is still the one running.
healthy() {
  case "$1" in
    "") return 1 ;;
    1:*) return 1 ;;
    0:bash|0:sh|0:zsh|0:dash|0:fish) return 1 ;;
    *) return 0 ;;
  esac
}

window_exists() {
  tm list-windows -t "$SESSION" -F '#{window_name}' 2>/dev/null | grep -qx "$1"
}

# start_window creates a window, or respawns its pane in place when it already
# exists. Respawn rather than kill-and-recreate keeps the window's identity and
# its scrollback, so a crash loop stays diagnosable.
start_window() {
  local name=$1 cwd=$2
  shift 2
  if window_exists "$name"; then
    tm respawn-pane -k -t "$SESSION:$name" -c "$cwd" "$@"
  else
    tm new-window -d -t "$SESSION" -n "$name" -c "$cwd" "$@"
  fi
}

if [ ! -r "$WORKERS" ]; then
  echo "reconcile: $WORKERS not found" >&2
  exit 1
fi

# setsid so the tmux server is not left in the runner service's cgroup, where
# a `systemctl restart` of the runner would take it down with it. tmux already
# double-forks; this is belt and braces.
if ! tm has-session -t "$SESSION" 2>/dev/null; then
  echo "reconcile: creating tmux session $SESSION on socket $SOCKET"
  setsid tmux -L "$SOCKET" new-session -d -s "$SESSION" -n bootstrap -c "$AGENT_ROOT"
fi

summary "### Agent host"
summary ""
summary "| window | before | after | action |"
summary "|---|---|---|---|"

count=0
while IFS=$'\t' read -r window filter; do
  [ -n "$window" ] || continue
  if [ -n "$ONLY_WINDOW" ] && [ "$ONLY_WINDOW" != "$window" ]; then
    continue
  fi
  count=$((count + 1))

  # shellcheck disable=SC2086  # the filter is a flag list and must word-split
  set -- $filter

  action=none
  state=$(pane_command "$window")
  if ! window_exists "$window"; then
    action=created
    start_window "$window" "$REPO" "$BIN/supervisor.sh" "$@"
  elif ! healthy "$state"; then
    action=respawned
    start_window "$window" "$REPO" "$BIN/supervisor.sh" "$@"
  fi

  # The sidecar is judged only on being alive. It IS a shell loop, so the
  # supervisor's "fell back to a shell" rule would condemn a healthy one; and
  # tmux closes a window when its command exits, so a poke.sh that died is
  # simply absent.
  poke="${window}-poke"
  poke_state=$(pane_command "$poke")
  case "$poke_state" in
    0:*) : ;;
    *) start_window "$poke" "$REPO" "$BIN/poke.sh" "$window" "$@" ;;
  esac

  # Scrollback goes to a 0600 file on the host, not into the job log: this
  # repository is public, so Actions logs and step summaries are readable by
  # anyone. --dump_scrollback is the deliberate opt-out for debugging.
  capture="$LOGS/${window}-${RUN_ID}.log"
  if tm capture-pane -p -S -2000 -t "$SESSION:$window" > "$capture" 2>/dev/null; then
    chmod 0600 "$capture"
  else
    rm -f "$capture"
    capture="(none)"
  fi
  if [ "$DUMP_SCROLLBACK" = "true" ] && [ "$capture" != "(none)" ]; then
    echo "--- $window scrollback (last 100 lines) ---"
    tail -n 100 "$capture"
  fi

  now=$(pane_command "$window")
  echo "reconcile: $window before=${state:-absent} after=${now:-absent} action=$action log=$capture"
  summary "| \`$window\` | ${state:-absent} | \`${now:-absent}\` | $action |"
done < <(python3 -c '
import json, sys
for w in json.load(open(sys.argv[1])):
    print(w["window"] + "\t" + w.get("filter", ""))
' "$WORKERS")

if [ "$count" -eq 0 ]; then
  echo "reconcile: no workers matched (ONLY_WINDOW=${ONLY_WINDOW:-<all>})" >&2
  exit 1
fi

# The placeholder window only exists because a tmux session must be created
# with one. Once real workers are up it is noise, and dropping it means the
# session dies with the last worker — which the next tick simply rebuilds.
if [ "$(tm list-windows -t "$SESSION" -F '#{window_name}' | wc -l)" -gt 1 ]; then
  tm kill-window -t "$SESSION:bootstrap" 2>/dev/null || true
fi

summary ""
summary "Scrollback is kept on hel01 under \`$LOGS\` for ${LOG_RETENTION_DAYS} days, not printed here."
