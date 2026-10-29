
| Command | Scoping |
|---|---|
| `task list`, `task claim --next`, `work next` | default scope plus remote fallback |
| `task add` | default scope; an error when nothing resolves or `--project=` is explicit |
| `task board` | `--project`, `--repo`, default scope; positional `[project]` is shorthand |
| `inbox list` | `--project`, `--repo`, default scope |
| every id-taking task command, `task timeline`, `task block --on` | accept a bare task number |
| `project list` | org-wide, unscoped |
| `work resume`, `work submit`, `install`, `actor`, `token`, `login` | act on a worktree, an explicit id, or the whole install |

Every command taking `--project` also takes `--repo`, and both carry help text naming the default source. Non-goals: multi-project selection, a task-to-repo association, scoping `project list`, writing `current_project` back into a tracked file, cross-project guardrails on id-taking commands.

