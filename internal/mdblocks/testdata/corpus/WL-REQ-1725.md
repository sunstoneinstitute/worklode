
The session-start brief reaches each harness in the shape it consumes: Claude Code and Codex get the `hookSpecificOutput.additionalContext` envelope with exactly those two fields, since Codex drops context whose JSON fails its schema; Amp gets plain text; Copilot gets nothing, because its envelope is unverified.

