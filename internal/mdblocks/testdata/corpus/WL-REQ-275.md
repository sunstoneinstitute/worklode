
```
lode install   [--vcs git] [--no-vcs] [--agent <id>|auto|all]... [--no-agent]
               [--scope local|project] [--skills] [--no-statusline] [--json]
lode uninstall  (same flags)
```

| Flag | Rule |
|---|---|
| `--agent` | Repeatable, default `auto`: every adapter whose `Detect` returns true. `all` installs every adapter regardless, for image builds. An explicit undetected id still installs. |
| `--skills` | Opt-in, because it writes outside the hook config (WL-REQ-276). |
| `--scope` | `local` writes the personal config, `project` the committed one: Claude Code `settings.local.json` versus `settings.json`, Copilot `~/.copilot/hooks` versus `.github/hooks`. Codex and Amp have one user-level config and ignore it. |
| `--no-statusline` | Status line is on by default because its write is already conditional (WL-SPEC-80#sec-6a). |
| `--agent` with `--no-agent` | Contradiction, refused. |

Telemetry (WL-REQ-279) is configured on every install of Claude Code and Codex. Install also enables `extensions.worktreeConfig` in the repo's local git config (WL-SPEC-80#sec-3.4a).

