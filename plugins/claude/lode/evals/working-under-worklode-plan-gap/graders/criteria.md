---
type: llm
weight: 1
---

The answer must reject quietly picking a value and editing the plan. It must
name the ladder: record the gap (`lode task gap <docref> --reason ...`), hand
the plan fix to a fixer at the planning tier (bracketed with `lode task fix`),
and treat setting a threshold or default as a substantive change that needs
review, so the agent stops (or escalates) rather than continuing on its own
edit.

Fail if it approves the edit as cosmetic or tells the user to just ask a
human and wait.
