# Redirecting AskUserQuestion for headless sessions

Research note, 2026-08-24. Question: 038 §4.1 and §4.2 establish that a
sandbox session has no human watching a terminal and no statusline — the
exact audience Claude Code's built-in `AskUserQuestion` tool assumes. When a
headless worker hits a genuine judgment call, today it either stalls waiting
on a UI nobody can see, or the model guesses. Can Claude Code's own extension
points redirect an `AskUserQuestion` call to a different tool worklode
controls, so the question could in principle be captured and routed through
worklode instead of through Claude Code's own terminal-bound UI?

**Short answer: yes, the redirect itself is mechanically reliable — 280/280
successful reps observed, against a blunt and repeated instruction — but this
spike proves only the redirect, not a feature.** The hard remaining problem,
untouched here, is how a human's answer gets back to a worker process that is
blocked mid tool-call. See "What we did not establish" below before treating
this as buildable.

## Method

A stub MCP server was registered at Claude Code's `local` scope
(`claude mcp add <name> -- <command>`), exposing one tool with the same input
schema as `AskUserQuestion` (a `questions` array of
question/header/options/multiSelect), logging every call it received and
returning a canned answer instantly.

First attempt used LSP-style `Content-Length`-header framing on the server's
stdio transport. That is the wrong framing — MCP's stdio transport is
newline-delimited JSON, one message per line, no headers — and the failure
mode is silent: `claude mcp list` reports a 30-second connection timeout with
no other diagnostic. Worth remembering if this pattern is ever hand-rolled
again rather than built on an SDK.

Two conditions were tested, both against a real running Claude Code session
(a top-level agent and a fleet of subagents, not a mock):

- **Condition A — hook-enforced redirect.** A `PreToolUse` hook matched
  `AskUserQuestion` and returned
  `hookSpecificOutput: {permissionDecision: "deny", permissionDecisionReason:
  "<name the replacement tool>"}`. The deny reason reaches the model as
  ordinary tool-call error feedback — this is not a real substitution of the
  tool's result, it is advisory text the model has to act on itself. Nothing
  stops the model from ignoring it, retrying `AskUserQuestion`, or describing
  the question in prose instead.
- **Condition B — prompt instruction only, no hook.** The hook was removed
  entirely; the agent was told directly in its system prompt to call the MCP
  tool instead of `AskUserQuestion`, with no enforcement at all.

Both conditions ran 100 reps each, batched as 5 subagents × 20 self-looping
reps per batch (to bound the number of background dispatches rather than
spawning 100+ separate agents). Condition A ran once, on Claude Haiku 4.5.
Condition B ran on both Haiku 4.5 and Sonnet 5.

## Results

| Condition | Model | Result |
|---|---|---|
| B — prompt instruction only | Haiku 4.5 | 100/100 |
| B — prompt instruction only | Sonnet 5 | 100/100 |
| A — hook block + redirect | Haiku 4.5 | 80/80 clean (see below) |

Condition A's fifth batch (20 reps) reported 0/20, but its transcript shows
the subagent got confused about tool-calling mechanics generally — it claimed
`AskUserQuestion` was never available to it at all — rather than the redirect
failing once attempted. Those 20 reps are voided, not counted as failures of
the mechanism; the other 80 were clean.

A structural finding ties directly to 038 §4.2: MCP servers share the exact
"must arrive before the session starts, not during it" property that §4.2
already establishes for lifecycle hooks. Registering a server mid-session
(`claude mcp add`) does not make it callable in that same running session —
confirmed by `ToolSearch` returning no match until a manual `/mcp` reconnect —
while a **freshly started** process (a new subagent, or a new sandbox session)
picks up an already-registered server automatically at its own startup, no
reconnect needed. §4.2's answer for hooks — bake them into the image at build
time — is therefore also the answer for any MCP server this feature would
need; nothing here needs a new mechanism, only the same one applied twice.

## Recommendation

Name this as a verified-feasible seam for a future feature, not a shipped
one. Two gaps sit between this spike and a real feature:

1. **No answer-delivery channel.** The stub tool answered itself instantly
   with a canned response. A real version needs the MCP tool (or the CLI
   command it wraps) to actually block — write a pending-question row,
   surface it somewhere a human can see and answer (cockpit UI, CLI, chat),
   and hold the connection open or poll until a real answer lands. None of
   that was built or designed here.
2. **The redirect is a followed instruction, not a hard guarantee.** Both
   conditions hit 100%, but against a blunt, repeated instruction ("never
   call AskUserQuestion") and a modest N. A subtler instruction, or a moment
   where the built-in tool is the more natural completion, was not tested.

If 038 §5's dispatch seam or 032's agent pools reach the point where
unattended sessions routinely need to ask a human something, this is the
starting point — not before.

## What we did not establish

- **Reliability under harder prompting.** Only a blunt, repeated instruction
  was tested. An ambiguous instruction, or one competing with a strong pull
  toward the built-in tool, may not hold at 100%.
- **The answer-delivery design.** The single biggest gap — see
  Recommendation above. This spike is entirely about the outbound half of the
  interaction (capturing the question); the inbound half (getting a real
  human answer back to a blocked process) is unstarted.
- **Condition A on Sonnet 5.** Only Haiku 4.5 was run against the
  hook-enforced condition; whether a stronger model behaves differently when
  actually blocked (rather than merely instructed) is unknown.
- **Per-call latency at scale.** The hook shells out to `jq` on every
  `AskUserQuestion` attempt. Not measured, and likely negligible for a
  once-per-question interaction, but unverified.
- **Stability across Claude Code versions.** The mechanism depends on the
  current `PreToolUse` `hookSpecificOutput` contract and the current MCP
  stdio framing — both implementation details of Claude Code's harness, not
  a documented stable contract. A version bump could change either without
  notice.
