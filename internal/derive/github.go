package derive

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// repoClientTTL bounds how long one cached RepoClient is reused. GitHub
// installation tokens expire an hour after minting, and NewRepoClient bakes
// the token into the client's Authorization header (repoclient.go) rather
// than refreshing it per call — so an unexpiring cache 401s forever once the
// owning process outlives the first hour. 45 minutes keeps a quarter-hour of
// slack for a deriver pass that picked a client up just before the deadline
// and is still reading with it when the hour runs out.
const repoClientTTL = 45 * time.Minute

// GitHubReader implements RepoReader over internal/githubauth's per-repo
// installation-token client (RepoClient). It mints one RepoClient per repo
// and reuses it while the token is fresh, so a run over many PRs in one repo
// mints one token rather than one per read.
//
// Safe to share: the cache is mutex-guarded and every entry carries its mint
// time, so a single GitHubReader built once at server boot and captured in a
// long-lived handler closure — the way `lode serve` uses it — stays correct
// under concurrent requests and across the token lifetime.
type GitHubReader struct {
	Auth *githubauth.AppAuth

	// now is the clock the freshness check reads; nil means time.Now.
	// export_test.go's SetClock drives an entry past repoClientTTL without
	// sleeping.
	now func() time.Time

	mu      sync.Mutex
	clients map[string]cachedRepoClient
}

// cachedRepoClient is one repo's client stamped with the moment its
// installation token was minted.
type cachedRepoClient struct {
	rc     *githubauth.RepoClient
	minted time.Time
}

func (g *GitHubReader) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// client mints-or-reuses the RepoClient for repo, re-minting once the cached
// entry's token is older than repoClientTTL. Minting holds the lock, so two
// concurrent first calls for the same repo cost one token, not two.
func (g *GitHubReader) client(ctx context.Context, repo string) (*githubauth.RepoClient, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	if c, ok := g.clients[repo]; ok && now.Sub(c.minted) < repoClientTTL {
		return c.rc, nil
	}
	rc, err := g.Auth.NewRepoClient(ctx, repo)
	if err != nil {
		return nil, err
	}
	if g.clients == nil {
		g.clients = map[string]cachedRepoClient{}
	}
	g.clients[repo] = cachedRepoClient{rc: rc, minted: now}
	return rc, nil
}

// notFound collapses githubauth's two "routine fact about this repo"
// sentinels onto the RepoReader contract's ErrNotFound. ErrContentNotFound is
// the obvious one. ErrAppNotInstalled joins it because the derivers run
// org-globally over every repo a PR was ever opened against: a repo the App
// was never installed on (or was uninstalled from) simply has nothing this
// process can read, exactly like a repo with no manifest, and must skip
// rather than abort the run for every other repo. A transport failure or a
// 5xx is neither and keeps propagating.
func notFound(err error) bool {
	return errors.Is(err, githubauth.ErrContentNotFound) ||
		errors.Is(err, githubauth.ErrAppNotInstalled)
}

// FileAt implements RepoReader, mapping githubauth's not-found sentinels to
// this package's forge-independent one (see notFound).
func (g *GitHubReader) FileAt(ctx context.Context, repo, path string) ([]byte, error) {
	rc, err := g.client(ctx, repo)
	if err != nil {
		if notFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	data, err := rc.FileAt(ctx, path)
	if notFound(err) {
		return nil, ErrNotFound
	}
	return data, err
}

// PRFiles implements RepoReader. Only the client-minting step can report
// ErrAppNotInstalled; the files endpoint itself has no not-found case the
// deriver may skip — it is called only for a PR the backbone already has a
// row for, so a 404 there is a real fault.
func (g *GitHubReader) PRFiles(ctx context.Context, repo string, number int64) ([]string, error) {
	rc, err := g.client(ctx, repo)
	if err != nil {
		if notFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return rc.PRFiles(ctx, number)
}
