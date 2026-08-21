// Per-repo file reads for the pr-affects deriver (spec 007 deriver 3),
// alongside the other RepoClient reads in repoclient.go.

package githubauth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrContentNotFound reports that the contents API has no file at the
// requested path: a fact about the repo, matching ErrAppNotInstalled's
// stance in app.go, not a transport failure.
var ErrContentNotFound = errors.New("github content not found")

// escapeRepoPath escapes a multi-segment repo-relative path for use in a
// URL path, segment by segment — url.PathEscape on the whole string would
// also encode the "/" separators.
func escapeRepoPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// FileAt fetches path at the repo's default branch head via the contents
// API. GitHub wraps the base64 body at 60 characters; no pre-processing is
// needed, because encoding/base64's decoders ignore \r and \n by contract.
// A file over 1 MB comes back with encoding "none" and no content (GitHub's
// cutoff for this endpoint) — reported as an error rather than silently
// returned as empty bytes.
func (c *RepoClient) FileAt(ctx context.Context, path string) ([]byte, error) {
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	u := c.base + "/repos/" + c.path + "/contents/" + escapeRepoPath(path)
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &payload)
	if err != nil {
		return nil, err
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("get content %s %s: %w", c.path, path, ErrContentNotFound)
	default:
		return nil, fmt.Errorf("get content %s %s: status %d", c.path, path, code)
	}
	if payload.Encoding == "none" {
		return nil, fmt.Errorf("get content %s %s: file exceeds the contents API's size limit (encoding=none)", c.path, path)
	}
	data, err := base64.StdEncoding.DecodeString(payload.Content)
	if err != nil {
		return nil, fmt.Errorf("decode content %s %s: %w", c.path, path, err)
	}
	return data, nil
}

// maxPRFilesPages caps PRFiles' pagination. It sits one page past GitHub's
// own hard cap of 3000 changed files on this endpoint (maxPerPage*30)
// deliberately: a PR at exactly that cap returns a full page 30, so the
// short-page check never fires and a 30-page limit would error on GitHub's
// routine truncation rather than on anything wrong. Page 31 comes back empty
// and terminates the loop normally. The cap still exists — the loop must not
// be unbounded, and a silently truncated file list feeding wl:affects would
// omit edges with no trace of why — it just no longer collides with the
// boundary it was meant to sit outside.
const maxPRFilesPages = 31

// PRFiles lists a pull request's changed file paths, paging the same way
// ListIssues/ListPulls do (list.go): per_page=maxPerPage, page-number
// pagination, a short page means the list is done. The PR files endpoint
// has no Link-header alternative worth using here, since the rest of this
// package already standardizes on page-number paging.
func (c *RepoClient) PRFiles(ctx context.Context, number int64) ([]string, error) {
	var out []string
	for page := 1; page <= maxPRFilesPages; page++ {
		var raw []struct {
			Filename string `json:"filename"`
		}
		u := c.base + "/repos/" + c.path + "/pulls/" + strconv.FormatInt(number, 10) +
			"/files?per_page=" + strconv.Itoa(maxPerPage) + "&page=" + strconv.Itoa(page)
		code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &raw)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf("list pr files %s#%d: status %d", c.path, number, code)
		}
		for _, f := range raw {
			out = append(out, f.Filename)
		}
		if len(raw) < maxPerPage {
			return out, nil
		}
	}
	return nil, fmt.Errorf("list pr files %s#%d: %d full pages with no short page (%d changed files, past GitHub's 3000-file cap for this endpoint)",
		c.path, number, maxPRFilesPages, maxPerPage*maxPRFilesPages)
}
