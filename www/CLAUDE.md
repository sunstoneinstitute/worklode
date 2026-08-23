# www/CLAUDE.md

The marketing site. `AGENTS.md` in this directory symlinks here, so both
names load the same instructions when working on anything under `www/`.

## Language style

Copy on this site states what Worklode does and is. It never defines a
feature by what it isn't or what it replaced.

- **No antithesis.** Don't write "X, not Y", "X rather than Y", or "X
  instead of Y" to describe a feature by contrast with an alternative. Say
  what it does. ("Skills delivered on demand," not "Skills delivered, not
  registered.") A contrast is fine only when it carries information the
  reader needs to tell two real cases apart, not as a rhetorical device.
- **No em dashes.** Use a period, comma, or colon instead. (The
  box-drawing section dividers in HTML comments, `<!-- ── Hero ── -->`,
  aren't visible copy and are exempt.)
- Prefer short, direct sentences. Cut filler that only pads a sentence to
  sound more confident.

Before landing a copy change, check the diff for `—`, `rather than`, and
`instead of`, and fix any hit that isn't load-bearing.

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
