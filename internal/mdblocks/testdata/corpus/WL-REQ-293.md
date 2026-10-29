
Pi is integrated through a native, distributable extension rather than a shell-hook configuration. The adapter owns no task, lease, worktree, or authentication state; `lode` remains the sole interface and Pi owns its sign-in and project-trust decision.

The package lives at `plugins/pi/lode/` (`package.json` with a `pi` manifest, `extensions/worklode.ts`, `skills/`, `README.md`), imports only Pi's declared peer packages, and is valid both as a local package and as a future npm package. `lode install --agent pi` merges the relative source `./plugins/pi/lode` into the repo root's `.pi/settings.json`, preserving every other entry and recognizing its own resolved path on rerun. `lode uninstall --agent pi` removes only that entry. Pi loads project-local packages only after the user trusts the project; the installer neither pre-approves trust nor invokes login. If Pi is unavailable, an explicit install reports a prerequisite and auto-detection skips it.

Acceptance: install and uninstall preserve foreign settings and converge; a trusted project loads the package from the matching checkout. Go tests cover the settings merge (foreign package, duplicate local path, invalid JSON, file emptied by uninstall).

