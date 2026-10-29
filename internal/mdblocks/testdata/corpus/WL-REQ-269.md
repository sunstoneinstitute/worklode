
| Setting | Where | Default |
|---|---|---|
| `LODE_BRANCH_TEMPLATE` | server env | `{{ .id }}-{{ .slug }}` |
| `worktree_dir` | repo `.worklode/config.toml` | `.worktrees` |
| `LODE_WORKTREE_DIR` | client env, overrides `worktree_dir` | none |

A branch name is published to GitHub and CI, so the backbone owns it. A worktree path is local to one checkout, so the checkout owns it. `worktree_dir` is interpreted relative to the git root; absolute paths and `..` escapes are rejected. Because `.worklode/config.toml` is repo content it is checked out inside every worktree, so a hook resolves the base from its own cwd. `LODE_WORKTREE_DIR` does not persist; a worktree created under it is invisible to a later session started without it, and every hook NOPs silently.

The layout is flat: every worktree is exactly one directory below the base, named by the branch. A `/` in a rendered branch is flattened to `-` in the directory name (`team/WL-3-x` becomes `.worktrees/team-WL-3-x`). `.gitignore` carries `.worktrees/`.

