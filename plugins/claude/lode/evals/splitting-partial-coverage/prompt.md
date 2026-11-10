---
max_turns: 6
allowed_tools: [Read, Glob, Grep, Skill]
---

I'm splitting a Worklode spec into a plan series. Plan 1 builds only the read
path of requirement WL-REQ-500; plan 2 will build the write path. How do I
express in plan 1's `covers:` that it only partly covers WL-REQ-500? Don't run
any commands.
