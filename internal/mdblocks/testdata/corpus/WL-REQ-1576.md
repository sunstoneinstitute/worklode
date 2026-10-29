
`store.UserToken(ctx, actorID)` returns a valid access token from the actor's `github_user_tokens` row (WL-REQ-65), refreshing lazily when expired or within a skew window. GitHub refresh tokens are single-use, so the row is locked `FOR UPDATE` during a refresh; concurrent callers wait and reuse the new pair. A failed refresh sets `status = broken`, and callers translate that into "log in again" guidance. There is no background refresher.

