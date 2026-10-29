
`internal/githubauth/list.go`:

```go
func (a *AppAuth) ListIssues(ctx context.Context, repo, state string, since time.Time, maxPages int) ([]Issue, bool, error)
func (a *AppAuth) ListPulls(ctx context.Context, repo, state string, maxPages int) ([]PullRequest, bool, error)
```

Both page `?sort=updated&direction=asc&per_page=100&page=N` until a short page or `maxPages` (20), returning `truncated`. Ascending update order puts the truncated tail at the newest end, so `--since` is a resume cursor for issues (`/issues` accepts `since`). `/pulls` has no `since`, so PRs filter on `updated_at` client-side and a truncated PR list cannot be resumed; the CLI tells the caller to narrow with `--state open`. `issues.truncated` and `prs.truncated` are reported independently and `newest_updated_at` comes from the issues stream only. `ListIssues` skips every entry with a `pull_request` key. The structs carry exactly the fields the webhook appliers read; the API layer assembles `store.Issue` / `store.PullRequest`.

