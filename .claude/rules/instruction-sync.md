---
paths:
  - "**/AGENTS.md"
  - "**/CLAUDE.md"
  - ".claude/rules/**"
---

# Keep agent instructions synchronized

Claude Code and Codex must receive the same shared guidance. Update each
corresponding instruction surface in the same change.

- Root `AGENTS.md`, `internal/cmd/AGENTS.md`, and `www/AGENTS.md` symlink to
  their sibling `CLAUDE.md`. Edit the target and preserve the symlink.
- `.agents/skills` symlinks to `.claude/skills`. Edit skills at their source.
- Root instructions read `.worklode/agent-instructions.md` if it exists.
  `lode install` owns that file's marked block and adds missing root pointers.
  Keep the shared file and pointers tracked together; personal notes stay
  in `CLAUDE.local.md`.
- `.claude/rules/migrations.md` matches `deploy/base/AGENTS.md`.
- `.claude/rules/cockpit-ui.md` matches `internal/ui/AGENTS.md` and the
  `render.go` section of `internal/api/AGENTS.md`.
- `.claude/rules/store-error-mapping.md` matches `internal/store/AGENTS.md`.
- This maintenance rule is also referenced from root `CLAUDE.md` so Codex
  reads it when editing instruction files.

Keep each mirrored rule body identical, apart from YAML frontmatter and
the nested file's scope note. Preserve file-specific conditions: the API
copy applies only to `internal/api/render.go`; the migration copy applies
only to `deploy/base/migrations/**` and `deploy/base/kustomization.yaml`;
the store copy applies only to Go files.

When adding, moving, or removing a path-scoped rule, update the matching
nested instructions and this mapping. Update the root navigation paragraph
when scopes change. Check symlink targets and compare mirrored rule bodies
before finishing.

Refer to skills as "Use the `<plugin>:<skill>` skill" with the installed
name. Preserve slash syntax when describing an actual Claude Code command.
