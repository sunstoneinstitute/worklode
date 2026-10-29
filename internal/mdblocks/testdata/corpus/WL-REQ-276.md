
The skill store (WL-SPEC-81 §7) is canonical at `~/.worklode/store/<hash>/`, with `~/.worklode/skills/<name>` symlinking to the current version. The store sits outside the linked skills directory so no harness that recurses lists a hash directory as a skill.

| Scope | Link | Serves |
|---|---|---|
| Personal (`--skills`) | `~/.agents/skills` -> `~/.worklode/skills` | Codex, Copilot CLI, Amp |
| Personal | `~/.claude/skills/<name>` -> `~/.worklode/skills/<name>`, per skill | Claude Code, which does not read `.agents/` and whose skills directory the user already populates |
| Project (inside a worktree) | `<worktree>/.agents/skills/<name>` per skill the brief lists, with `.agents/` added to `.git/info/exclude` | Any sandbox that never ran `lode install` |

`lode skill install <name> --link <harness>|all` performs the same publication standalone. In this repo `.agents/skills` symlinks to `.claude/skills`.

