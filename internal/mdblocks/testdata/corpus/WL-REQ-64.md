
Linking happens during login. After provisioning (WL-REQ-53) and before `finishLogin` (WL-REQ-54), the web callback checks the actor: when `github_username` is set and the actor has no `active` row in `github_user_tokens`, it redirects through GitHub instead of finishing. A GitHub username alone yields no token; only the user's authorization of the App does. GitHub asks for consent the first time and skips it afterwards, so a returning user sees only a redirect.

1. The server redirects to GitHub's authorize endpoint as a confidential client without PKCE. `state` is signed with `LODE_SESSION_SECRET`, binds the actor id and expires in 10 minutes. The CLI-intent cookie (WL-REQ-56) is untouched, so the login resumes in the branch it started in.
2. `GET /auth/github/callback` exchanges the code, fetches `GET /user` (`id`, `login`), and strict-checks `login` against the actor's `github_username`, case-insensitively.
3. On a match, it upserts the row in `github_user_tokens` keyed by the Worklode actor id.
4. It calls `finishLogin` for the actor in `state`, whatever steps 2 and 3 returned.

A failed link never fails a login. A mismatch, a denied consent or a GitHub error writes no row, logs the reason server-side, and the login completes. The next login tries again, which also repairs a `broken` row. The attempt runs at most once per login. The direct token exchange (WL-REQ-55) cannot redirect a browser and never links. `lode login` (WL-REQ-56) links GitHub on the way through.

`GET /auth/github/link` (authenticated session) starts the same flow from the web UI, for a user who wants to reconnect without logging in again.

