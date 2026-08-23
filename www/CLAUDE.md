# www/CLAUDE.md

The marketing site. `AGENTS.md` in this directory symlinks here, so both
names load the same instructions when working on anything under `www/`.

For the language style this copy follows (no antithesis, no em dashes),
see the root `CLAUDE.md`'s "`www/` copy style" section.

## Accuracy

Copy must describe what's implemented, not what a spec proposes. Every
spec under `docs/specs/` carries `status: draft` regardless of whether the
feature shipped, so that field is not a signal of anything: check the
actual code (the `internal/cmd` command tree, `internal/watcher`, event
types in `internal/eventbus`) before writing a sentence in the present
tense. A designed-but-unbuilt feature gets a plain label saying so, in
future tense (see the Escalation section and `README.md`'s note on it),
not marketing present tense implying it already runs.

When the architecture changes materially, update this copy too. Nothing
derives it automatically.
