---
name: status
description: Read-only report of the current Worklode task, lease, and heartbeat state
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

## Status
!`lode work status --json`

Report the task, lease state (held/expired/none, expiry, renewal freshness),
and session-marker/heartbeat state from the JSON. This is read-only — never
mark done, block, claim, or release from here.
