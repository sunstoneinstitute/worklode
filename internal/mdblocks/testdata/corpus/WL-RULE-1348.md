
- GitHub link flow at login, `GET /auth/github/link` and `/auth/github/callback`, and the `github_user_tokens` columns and lazy refresh of sections 9.2 and 9.3: WL-PLAN-52 (stale, no tasks minted).
- `lode login` linking GitHub, `lode actor show`, and the section 9.4 error handling: WL-PLAN-53 (stale, no tasks minted).
- Agent sessions acting on GitHub as their human (section 9.5): `GET /api/v1/actor/github-token`, `lode github token`, `lode github credential`, the `GH_TOKEN` SessionStart export and the credential helper set by `lode install`. No plan yet; a plan follows once WL-PLAN-52 lands, because it needs `store.UserToken`.
- GitHub App installation scope and its guards, and which spec needs each App permission (section 9.1): WL-PLAN-143.
- Worktree exit purges secrets on a definitely gone lease (section 10.6); today only removal, submit, block and `lode doctor` purge: WL-PLAN-3.
- `tokens.minted_by` (`REFERENCES actors ON DELETE SET NULL`), recording the actor that minted a token when it differs from the actor the token acts as, so "which human dispatched the agent that made this change" is a join: WL-PLAN-116.
- The `janitor` agent actor for unattended sweeps, which can claim `kind = 'chore'` and cannot accept a document: WL-PLAN-126.
