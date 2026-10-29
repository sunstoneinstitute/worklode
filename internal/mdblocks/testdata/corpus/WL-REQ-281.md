
`lode-statusline` reads the status-line JSON on stdin and prints one line: the model name, the in-progress todo from Claude Code's todo store, the location, and usage fields (`[Ctx 12% 101k]`, plus `[Sess …]` and `[Week …]` with reset countdowns when the payload carries rate limits). It makes no network call, because it re-runs on every assistant message, and every segment degrades to empty rather than failing the line.

The command takes no harness argument. Claude Code defined the contract (command, payload on stdin, one line on stdout) and Cursor CLI adopted it verbatim; Codex and Gemini CLI accept no command at all, so there is nothing to dispatch on.

The location is the task id alone when the workspace is stamped with `worklode.task-id`, hyperlinked to the task when the terminal renders OSC 8 links and a server URL is configured. Otherwise it is the project name (from the remote URL), the branch, and a linked-worktree marker. The stamp is read with `git config --worktree`; a repo without `extensions.worktreeConfig` falls back to the branch rendering.

