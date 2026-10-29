
Recommendation is a caller of the corpus search, `store.Search` restricted to skills (WL-SPEC-79 §15): a dense arm over embeddings and a lexical arm, fused by reciprocal rank. Only the server holds embedding credentials. Only SKILL.md is indexed, so SKILL.md must carry the text that should match. Keeping the index current with skill content is the corpus indexer's job.

`POST /api/v1/skills/recommend` takes exactly one of `task_id` or `text`, plus `limit` (default 5, at most 20). For a task the query text is the task's title and body, and the task's pins are resolved. It returns `{pinned: [{name, description, hash, content}], matches: [{name, description, hash, score}], warnings, provider}`. `score` is the fused rank score: it orders matches and does not measure similarity. Pins are never duplicated into matches.

`lode skill recommend [--task <id> | --file <path> | --text <s>] --json` is the one generic surface. The task brief uses the same matching server-side; other stages call the CLI. Corpus-wide ranking over docs, tasks and skills together is `lode search`, described in WL-SPEC-79 §15.

