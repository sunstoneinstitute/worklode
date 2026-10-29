
Two engines behind one command, cheapest first: replay of stored events (WL-SPEC-80#sec-10.3a), then a poll of GitHub (WL-SPEC-80#sec-10.3b). Facts are repaired, findings reported, `--dry-run` suppresses writes for both. `--since` accepts RFC 3339 or a Go duration, resolved against the server clock; it bounds `events.received_at` for replay and `tasks.updated_at` for the poll. `--task` skips replay, because an ignored event's task is unknown before its apply runs. The poll is skipped when the server has no GitHub App. Each engine reports its own section. Leaving all filters off is the scheduled-caller case.

Output is one report per run, per engine, with `--json` for scheduled callers. The poll's `repaired` list reports what the run observed on GitHub, so a task whose PRs were already recorded appears every run.

