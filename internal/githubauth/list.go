// Paged REST reads of a repo's issues and pull requests, used by inbox import
// (spec 020), alongside the App authentication in app.go.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// maxPerPage is GitHub's maximum page size. The pager treats a short page as
// the end of the list, which is why the value must match what it requests.
const maxPerPage = 100

// Issue is one GitHub issue, carrying exactly the fields the inbox stores.
type Issue struct {
	Number    int64
	Title     string
	State     string
	HTMLURL   string
	UpdatedAt time.Time
}

// PullRequest is one GitHub pull request, carrying exactly the fields
// store.UpsertPR needs. Merged is derived: the list endpoint returns
// merged_at but no "merged" boolean, unlike the webhook payload.
type PullRequest struct {
	Number         int64
	Title          string
	State          string
	Merged         bool
	Body           string
	HTMLURL        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MergedAt       *time.Time
	MergeCommitSHA *string
	HeadRef        string
	HeadSHA        string
}

// listQuery builds the shared per-page query string. sort=updated with
// direction=asc is fixed, not caller-configurable: it makes the truncated
// tail the newest items, so --since (a lower bound on updated_at) can resume
// a capped import instead of only ever narrowing one.
func listQuery(state string, page int) url.Values {
	q := url.Values{}
	q.Set("state", state)
	q.Set("per_page", strconv.Itoa(maxPerPage))
	q.Set("page", strconv.Itoa(page))
	q.Set("sort", "updated")
	q.Set("direction", "asc")
	return q
}

// ListIssues pages a repo's issues under an installation token, oldest
// updated_at first (see listQuery) so a truncated run's tail is resumable via
// since. Entries carrying a pull_request key are skipped: GitHub's issues
// endpoint returns pull requests as issues, and without the filter every PR
// in the repo would land in the inbox as an issue. A zero since disables the
// filter. The bool reports truncation — maxPages exhausted without reaching a
// short page — so the caller can say so rather than silently importing a
// prefix.
func (a *AppAuth) ListIssues(ctx context.Context, repo, state string, since time.Time, maxPages int) ([]Issue, bool, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, false, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	auth := "Bearer " + token

	var out []Issue
	for page := 1; page <= maxPages; page++ {
		q := listQuery(state, page)
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}
		var raw []struct {
			Number      int64     `json:"number"`
			Title       string    `json:"title"`
			State       string    `json:"state"`
			HTMLURL     string    `json:"html_url"`
			UpdatedAt   time.Time `json:"updated_at"`
			PullRequest *struct{} `json:"pull_request"`
		}
		u := a.BaseURL + "/repos/" + path + "/issues?" + q.Encode()
		code, err := githubJSON(ctx, http.MethodGet, u, auth, &raw)
		if err != nil {
			return nil, false, err
		}
		if code != http.StatusOK {
			return nil, false, fmt.Errorf("list issues for %s: status %d", repo, code)
		}
		for _, it := range raw {
			if it.PullRequest != nil {
				continue
			}
			out = append(out, Issue{
				Number: it.Number, Title: it.Title, State: it.State,
				HTMLURL: it.HTMLURL, UpdatedAt: it.UpdatedAt,
			})
		}
		if len(raw) < maxPerPage {
			return out, false, nil
		}
	}
	return out, true, nil
}

// ListPulls pages a repo's pull requests under an installation token, oldest
// updated_at first (see listQuery), matching ListIssues so their truncation
// and resume behavior agree. The endpoint takes no since parameter, so
// callers filter on UpdatedAt.
func (a *AppAuth) ListPulls(ctx context.Context, repo, state string, maxPages int) ([]PullRequest, bool, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, false, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	auth := "Bearer " + token

	var out []PullRequest
	for page := 1; page <= maxPages; page++ {
		var raw []struct {
			Number         int64      `json:"number"`
			Title          string     `json:"title"`
			State          string     `json:"state"`
			Body           string     `json:"body"`
			HTMLURL        string     `json:"html_url"`
			CreatedAt      time.Time  `json:"created_at"`
			UpdatedAt      time.Time  `json:"updated_at"`
			MergedAt       *time.Time `json:"merged_at"`
			MergeCommitSHA *string    `json:"merge_commit_sha"`
			Head           struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
		}
		u := a.BaseURL + "/repos/" + path + "/pulls?" + listQuery(state, page).Encode()
		code, err := githubJSON(ctx, http.MethodGet, u, auth, &raw)
		if err != nil {
			return nil, false, err
		}
		if code != http.StatusOK {
			return nil, false, fmt.Errorf("list pulls for %s: status %d", repo, code)
		}
		for _, pr := range raw {
			merged := pr.MergedAt != nil && !pr.MergedAt.IsZero()
			out = append(out, PullRequest{
				Number: pr.Number, Title: pr.Title, State: pr.State, Merged: merged,
				Body: pr.Body, HTMLURL: pr.HTMLURL, CreatedAt: pr.CreatedAt,
				UpdatedAt: pr.UpdatedAt, MergedAt: pr.MergedAt,
				MergeCommitSHA: pr.MergeCommitSHA,
				HeadRef:        pr.Head.Ref, HeadSHA: pr.Head.SHA,
			})
		}
		if len(raw) < maxPerPage {
			return out, false, nil
		}
	}
	return out, true, nil
}
