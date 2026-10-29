
Git is the source of truth. Server config lists skill source repos (`owner/repo@ref:glob`, for example `plugins/*/skills/*/SKILL.md`). Authoring and review stay PR-based; Worklode indexes and distributes. Format is the existing SKILL.md convention: frontmatter `name`/`description` plus body, plus optional sibling files (`references/`, scripts).

Sync: a webhook push to a source repo triggers a resync; `lode skill sync` (admin) triggers a full resync via `POST /api/v1/skills/sync`, which answers 409 while another sync runs. A skill removed from git is soft-deleted.

Git is the version. Latest on the source ref is what gets recommended and installed; the content hash pins exactness. No semver.

