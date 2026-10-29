
First hit wins:

1. `--project X` or `--repo owner/name` on the command line. An explicitly empty `--project=` means all projects and stops here. `--project` and `--repo` together is an error.
2. `current_project` in the repo-local `.worklode/config.toml`, found by walking up from the working directory.
3. `current_project` in `~/.config/worklode/config.toml`.
4. The git remote: `git remote get-url origin`, resolved against `project_repos` by the server.
5. Nothing resolved: unscoped, all projects.

Step 4 never fails a command. Not a git repo, no `origin`, server unreachable, unmapped repo: each falls through to step 5. A command that cannot proceed unscoped (`task add`) reports the missing project itself. `.worklode/config.toml` may also carry `project_key`, which lets a commit hook know the key without the server or the cache. It is not part of the chain.

