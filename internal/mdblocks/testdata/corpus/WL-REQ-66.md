
| Condition | Response |
|---|---|
| Login with no `github_username` | No link attempt; the login completes |
| Link with mismatched login | No row; login completes; the server log names both logins |
| Consent denied, code exchange or `GET /user` failure | No row; login completes; retried at the next login |
| Expired or forged link `state` | 400; no row |
| Refresh failure | Row `broken`; `lode actor show` and the web UI say "log in again"; the caller gets a typed error |
| Token requested by an unlinked or broken actor | 409 naming the fix: log in again |
| Token requested with a task-scoped token | 403 |
| Actor id conflict with a non-human actor | 409 (`errActorKindConflict`) |

