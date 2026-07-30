// Repo content downloads for skill sync, alongside the App authentication in
// app.go.

package githubauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// maxTarball caps a skill-source repo download. Source repos are docs-sized;
// anything bigger is a misconfiguration, not a skill collection. It is a var so
// tests can lower it instead of moving 64 MiB.
var maxTarball = 64 << 20

// tarballClient does not share httpClient's 10-second budget: that one covers
// small JSON calls, while this one must also transfer the archive body.
var tarballClient = &http.Client{Timeout: 2 * time.Minute}

// Tarball downloads the repo tarball at ref using an installation token. The
// result is a gzipped tar whose entries share a single "<owner>-<repo>-<sha>/"
// root directory (GitHub's tarball format).
func (a *AppAuth) Tarball(ctx context.Context, repo, ref string) ([]byte, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	tarURL := a.BaseURL + "/repos/" + path + "/tarball/" + url.PathEscape(ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build github request GET %s: %w", tarURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	// GitHub answers 302 to a signed codeload URL. The client follows it and
	// net/http drops Authorization on the cross-host hop, which is what we
	// want: the redirect URL carries its own credentials.
	resp, err := tarballClient.Do(req)
	if err != nil {
		// The URL in a *url.Error is the last hop attempted — for a private
		// repo that is the signed codeload link, so drop it before logging.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("tarball %s@%s: %w", repo, ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tarball %s@%s: status %d: %s", repo, ref, resp.StatusCode, msg)
	}
	// Read one byte past the cap so an over-size body is detected instead of
	// silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxTarball)+1))
	if err != nil {
		return nil, fmt.Errorf("tarball %s@%s: %w", repo, ref, err)
	}
	if len(data) > maxTarball {
		return nil, fmt.Errorf("tarball %s@%s: exceeds %d bytes", repo, ref, maxTarball)
	}
	return data, nil
}
