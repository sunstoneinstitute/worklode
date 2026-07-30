package skillsync

import (
	"fmt"
	"path"
	"strings"
)

// Source is one configured skill source: a repo tree at a ref, filtered by a
// dir glob that names skill directories (each containing a SKILL.md).
// Wire format (LODE_SKILL_SOURCES): "owner/repo@ref:glob[,owner/repo@ref:glob...]".
type Source struct {
	Repo string // owner/name
	Ref  string // branch or tag
	Glob string // path.Match pattern for skill dirs, e.g. plugins/*/skills/*
}

// ParseSources parses the LODE_SKILL_SOURCES value. Empty means no sources.
func ParseSources(s string) ([]Source, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []Source
	seen := map[string]bool{}
	for _, entry := range strings.Split(s, ",") {
		repo, rest, ok := strings.Cut(entry, "@")
		if !ok || !strings.Contains(repo, "/") {
			return nil, fmt.Errorf("skill source %q: want owner/repo@ref:glob", entry)
		}
		ref, glob, ok := strings.Cut(rest, ":")
		if !ok || ref == "" || glob == "" {
			return nil, fmt.Errorf("skill source %q: want owner/repo@ref:glob", entry)
		}
		if err := validateGlob(glob); err != nil {
			return nil, fmt.Errorf("skill source %q: %w", entry, err)
		}
		repo = strings.TrimSpace(repo)
		// Deletion is scoped by repo, so two entries naming one repo would each
		// soft-delete the other's skills on every sync. Differing refs or globs
		// do not help: both entries still claim the same repo.
		if seen[repo] {
			return nil, fmt.Errorf("skill source %q: repo listed more than once", repo)
		}
		seen[repo] = true
		out = append(out, Source{Repo: repo, Ref: ref, Glob: glob})
	}
	return out, nil
}

// validateGlob rejects patterns that can never match a skill dir. A glob that
// matches nothing soft-deletes every skill from its repo, so a typo caught
// here is worth more than one caught at sync time.
func validateGlob(glob string) error {
	if _, err := path.Match(glob, "x"); err != nil {
		return fmt.Errorf("bad glob %q: %w", glob, err)
	}
	// Dirs are matched without a trailing slash and relative to the repo root.
	if strings.HasSuffix(glob, "/") {
		return fmt.Errorf("glob %q: drop the trailing /", glob)
	}
	if strings.HasPrefix(glob, "./") {
		return fmt.Errorf("glob %q: drop the leading ./", glob)
	}
	return nil
}

// MatchesPush reports whether a push to repo's branch should trigger a sync.
func MatchesPush(sources []Source, repo, branch string) bool {
	for _, src := range sources {
		if src.Repo == repo && src.Ref == branch {
			return true
		}
	}
	return false
}
