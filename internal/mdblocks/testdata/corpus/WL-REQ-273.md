
- Hooks are the compiled `lode-hook` executable. It takes an event and its arguments, `--harness <id>`, `--next <cmd> [argv...]`, or `--list`. It does not import Cobra or the command tree and parses arguments with the standard library.
- Daisy-chain: after its own action, a hook runs the `--next` command as a child, replays the original payload on its stdin, and exits with the child's exit code. Without `--next` it exits 0. Words between the event and `--next` are the hook's own; everything after `--next` goes to the chained hook verbatim. `commit-msg` names `"$@"` on both sides so neither half loses git's message-file argument.
- `lode-hook otel-headers` is a credential helper, not a lifecycle event: it reads no stdin and prints the bearer header for Claude Code's log exporter (WL-SPEC-80#sec-5.5a).

