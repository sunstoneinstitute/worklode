// RepoClient: authenticated reads against one repo for the reconcile poll
// engine (spec 013 engine 2). One installation token is minted per repo per
// run — the spec's batching unit for rate limits.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// RepoClient performs GitHub reads for one repo with an installation token.
type RepoClient struct {
	base string
	path string // escaped "owner/name"
	auth string
}

// NewRepoClient mints an installation token for repo and returns a client
// bound to it. Token minting failing IS the "App not installed" signal.
func (a *AppAuth) NewRepoClient(ctx context.Context, repo string) (*RepoClient, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	return &RepoClient{base: a.BaseURL, path: path, auth: "Bearer " + token}, nil
}

// PRFacts is the subset of a GitHub pull request the poll engine writes
// back through store.UpsertPR — the same fields the webhook payload carries.
type PRFacts struct {
	Number         int64      `json:"number"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	Merged         bool       `json:"merged"`
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
	// User.Login is the PR's author (store.PullRequest.Author). Leaving it
	// unread would let a PR the poller first observes sit with a NULL
	// author — and therefore an unrefusable self-approval (029 §7.1) —
	// until some later webhook delivery fills it in (WL-244).
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// HeadRef and HeadSHA give PRFacts the flat accessors the poller uses.
func (p *PRFacts) HeadRef() string { return p.Head.Ref }
func (p *PRFacts) HeadSHA() string { return p.Head.SHA }

// PR reads one pull request's current truth.
func (c *RepoClient) PR(ctx context.Context, number int64) (*PRFacts, error) {
	var pr PRFacts
	u := fmt.Sprintf("%s/repos/%s/pulls/%d", c.base, c.path, number)
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &pr)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("get PR %s#%d: status %d", c.path, number, code)
	}
	return &pr, nil
}

// DefaultBranch reads the repo's default branch name.
func (c *RepoClient) DefaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	code, err := githubJSON(ctx, http.MethodGet, c.base+"/repos/"+c.path, c.auth, &repo)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK || repo.DefaultBranch == "" {
		return "", fmt.Errorf("get repo %s: status %d", c.path, code)
	}
	return repo.DefaultBranch, nil
}

// CommitOnBranch reports whether sha is an ancestor of (i.e. contained in)
// branch, via the compare API: base=sha, head=branch — "ahead" or
// "identical" means the branch contains the sha. A 404 (unknown sha) is
// false, not an error.
//
// committed is the base commit's committer date, carried by the same
// response. The poll engine needs it to append main_commits in the order the
// commits actually landed; it is zero when GitHub omits it.
func (c *RepoClient) CommitOnBranch(ctx context.Context, branch, sha string) (on bool, committed time.Time, err error) {
	var cmp struct {
		Status     string `json:"status"`
		BaseCommit struct {
			Commit struct {
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		} `json:"base_commit"`
	}
	u := fmt.Sprintf("%s/repos/%s/compare/%s...%s",
		c.base, c.path, url.PathEscape(sha), url.PathEscape(branch))
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &cmp)
	if err != nil {
		return false, time.Time{}, err
	}
	switch code {
	case http.StatusOK:
		on = cmp.Status == "ahead" || cmp.Status == "identical"
		return on, cmp.BaseCommit.Commit.Committer.Date, nil
	case http.StatusNotFound:
		return false, time.Time{}, nil
	default:
		return false, time.Time{}, fmt.Errorf("compare %s %s...%s: status %d", c.path, sha, branch, code)
	}
}

// ReleaseFacts is one published release as the poll engine consumes it.
type ReleaseFacts struct {
	TagName         string    `json:"tag_name"`
	TargetCommitish string    `json:"target_commitish"`
	PublishedAt     time.Time `json:"published_at"`
}

// Releases lists the repo's releases, newest first (GitHub's order).
// per_page=100 matches DiscoverDoneState's pagination stance.
func (c *RepoClient) Releases(ctx context.Context) ([]ReleaseFacts, error) {
	var rels []ReleaseFacts
	u := c.base + "/repos/" + c.path + "/releases?per_page=100"
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &rels)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("list releases %s: status %d", c.path, code)
	}
	return rels, nil
}
