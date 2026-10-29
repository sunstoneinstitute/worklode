
| Condition | Behavior |
|---|---|
| Harness not detected | `--agent auto` skips it silently |
| Harness has no session hooks | Skills and the git heartbeat still install; the report names what was skipped |
| Event map lacks `SessionStart` | No auto-resume for that harness |
| Skill-target directory is a real directory | Per-skill links inside it; nothing Worklode did not create is deleted |
| Symlinks unavailable | Skill delivery copies the resolved store dir and reports the copy |
| Edge Agent unreachable | Exporter drops the batch; sessions and leases unaffected |
| Two harnesses on one worktree | Both bind hooks; both produce `agent_sessions` rows on the same lease |
| Harness config cannot be round-tripped | Left untouched and reported |

