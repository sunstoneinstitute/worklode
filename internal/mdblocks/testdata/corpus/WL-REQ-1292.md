
Agent sessions run `gh` and `git` as the human who started them, through the human's stored token, so their GitHub actions carry the App attribution. Commit authorship still comes from `git config`; the attribution covers API actions such as pull requests, comments, reviews and merges, and pushes over HTTPS.

| Surface | Behaviour |
|---|---|
| `GET /api/v1/actor/github-token` | Bearer-authed. Returns `{token, expires_at, login}` from `store.UserToken` for the calling human actor. |
| `lode github token` | Prints the access token. Caches it in the OS keychain until 5 minutes before `expires_at`. |
| `lode github credential` | A git credential helper: answers `get` for `github.com` with `x-access-token` and the access token. |
| `lode-hook` SessionStart | Appends `export GH_TOKEN="$(lode github token)"` to `$CLAUDE_ENV_FILE`. The file is sourced before every Bash command, so `gh` picks up a refreshed token after the 8-hour expiry. |
| `lode install` | Sets `credential.https://github.com.helper` to `!lode github credential` in the repo's local git config. |

When the actor has no usable link, `lode github token` fails with the 409's guidance and `GH_TOKEN` stays unset, so `gh` falls back to the operator's own login.

