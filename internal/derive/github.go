package derive

import (
	"context"
	"errors"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// GitHubReader implements RepoReader over internal/githubauth's per-repo
// installation-token client (RepoClient). It mints one RepoClient per repo
// on first use and reuses it for every subsequent call — RepoClient already
// holds the minted installation token, and re-minting one per read would
// cost an extra round trip per repo per call for no benefit. Not safe for
// concurrent use: the deriver that owns it runs single-goroutine, and the
// client cache below has no lock.
type GitHubReader struct {
	Auth    *githubauth.AppAuth
	clients map[string]*githubauth.RepoClient
}

// client mints-or-reuses the RepoClient for repo.
func (g *GitHubReader) client(ctx context.Context, repo string) (*githubauth.RepoClient, error) {
	if g.clients == nil {
		g.clients = map[string]*githubauth.RepoClient{}
	}
	if rc, ok := g.clients[repo]; ok {
		return rc, nil
	}
	rc, err := g.Auth.NewRepoClient(ctx, repo)
	if err != nil {
		return nil, err
	}
	g.clients[repo] = rc
	return rc, nil
}

// FileAt implements RepoReader, mapping githubauth's not-found sentinel to
// this package's forge-independent one.
func (g *GitHubReader) FileAt(ctx context.Context, repo, path string) ([]byte, error) {
	rc, err := g.client(ctx, repo)
	if err != nil {
		return nil, err
	}
	data, err := rc.FileAt(ctx, path)
	if errors.Is(err, githubauth.ErrContentNotFound) {
		return nil, ErrNotFound
	}
	return data, err
}

// PRFiles implements RepoReader.
func (g *GitHubReader) PRFiles(ctx context.Context, repo string, number int64) ([]string, error) {
	rc, err := g.client(ctx, repo)
	if err != nil {
		return nil, err
	}
	return rc.PRFiles(ctx, number)
}
