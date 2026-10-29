
One coordination model, N harnesses. Every harness reads the same `SKILL.md`, fires the same lifecycle moments, and differs only in which directory it reads skills from and which config file names the hook. The `internal/harness` package holds one adapter per harness plus a registry keyed by id: `amp`, `claude-code`, `codex`, `copilot`. `--agent` with any other id is refused with the supported list. Cursor and opencode have no adapter and none is planned.

```go
type Harness interface {
    ID() string
    Detect(repoDir string) (bool, error)
    SkillTargets(repoDir, scope string) ([]SkillTarget, error)
    InstallHooks(repoDir, scope string) (HookInstall, error)
    UninstallHooks(repoDir, scope string) (HookUninstall, error)
    Events() map[Event][]string
}
```

`Event` is Worklode's vocabulary: `session-start`, `subagent-start`, `session-end`, `heartbeat`, `worktree-enter`. Pre-commit is not an adapter event: the git hook covers it for every harness. An unmapped event degrades that harness and never fails install. Claude Code also implements the optional status-line interface (§6). Adapters add no hook semantics; `lode-hook` behaves the same behind every name. `--harness <id>` tells `internal/hookrun` which payload shape to normalize before the guard runs; a payload missing a field Worklode needs produces a NOP.

