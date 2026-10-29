
Each vendored skill carries its transformation as a prompt in a sibling `UPSTREAM.md`, addressed to an agent holding the upstream sources. Prompts compose in two layers: a plugin-level `plugins/claude/lode/skills/UPSTREAM-<plugin>.md` (path rewrites, house style, the standing instruction that task state belongs to `lode`) followed by the skill-level prompt. Re-vendoring is a re-run of the prompts against the new upstream, reviewed as a diff.

