// Package repourl normalizes the git remote URL forms `git remote get-url`
// emits down to the "owner/name" form worklode stores in project_repos.
//
// Normalize deliberately discards the host: project_repos.repo is unique on
// owner/name, so a mirror of a mapped repo on another host resolves to the
// same project. Host recovers it separately for the callers that mint
// host-qualified IRIs (WL-269).
package repourl

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotRepoURL is returned for input that does not name a repository.
var ErrNotRepoURL = errors.New("not a repository URL")

// Normalize turns a git remote URL into "owner/name".
//
// Accepted: scheme URLs (https://, ssh://, git://, git+ssh://, with or
// without a user, port, or .git suffix), scp-style host:path remotes
// (git@github.com:owner/name.git), and a bare owner/name.
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty remote: %w", ErrNotRepoURL)
	}

	if i := strings.Index(s, "://"); i >= 0 {
		// scheme://[user@]host[:port]/owner/name — drop everything through
		// the authority's trailing slash.
		rest := s[i+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", fmt.Errorf("remote %q has no path: %w", raw, ErrNotRepoURL)
		}
		s = rest[slash+1:]
	} else if c := strings.Index(s, ":"); c >= 0 {
		// scp-style [user@]host:owner/name — only when the colon comes
		// before any slash, so a path containing a colon is left alone.
		if slash := strings.Index(s, "/"); slash < 0 || c < slash {
			s = s[c+1:]
		}
	}

	s = strings.Trim(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("remote %q is not owner/name: %w", raw, ErrNotRepoURL)
	}
	return owner + "/" + name, nil
}

// Host extracts the forge host from a git remote URL, for the callers that
// do need it — `lode derive` puts it in the repo instance IRI
// (iri.Repo(host, owner, name), WL-269) — while Normalize keeps discarding
// it. A remote that carries no host (a bare owner/name) returns "", and the
// caller chooses its default; input that Normalize would reject returns
// ErrNotRepoURL.
func Host(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty remote: %w", ErrNotRepoURL)
	}

	var authority string
	if i := strings.Index(s, "://"); i >= 0 {
		// scheme://[user@]host[:port]/owner/name
		rest := s[i+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", fmt.Errorf("remote %q has no path: %w", raw, ErrNotRepoURL)
		}
		authority = rest[:slash]
		// Strip an optional :port; the colon in the authority can only
		// introduce one here, unlike scp-style below.
		if c := strings.LastIndex(authority, ":"); c >= 0 {
			authority = authority[:c]
		}
	} else if c := strings.Index(s, ":"); c >= 0 {
		// scp-style [user@]host:owner/name — same guard as Normalize: only
		// when the colon comes before any slash.
		if slash := strings.Index(s, "/"); slash < 0 || c < slash {
			authority = s[:c]
		}
	}
	if authority == "" {
		return "", nil // bare owner/name: no host to report
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	if authority == "" {
		return "", fmt.Errorf("remote %q has no host: %w", raw, ErrNotRepoURL)
	}
	return authority, nil
}
