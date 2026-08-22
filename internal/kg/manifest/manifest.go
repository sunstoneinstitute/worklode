// Package manifest reads the per-repo component-boundary manifest
// .worklode/components.yaml (spec 007 §2.2 — the authoring burden the spec
// accepts). The manifest is the single place component boundaries are
// declared: it fixes each component's IRI-bearing slug, and its path globs
// are the path→component index the observed-layer derivers consume.
package manifest

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Component is one declared component and the path globs it owns.
type Component struct {
	IRI   string   `yaml:"iri"`
	Name  string   `yaml:"name"`
	Paths []string `yaml:"paths"`
}

// Manifest is a parsed .worklode/components.yaml.
type Manifest struct {
	Repo       string      `yaml:"repo"`
	Components []Component `yaml:"components"`
}

// Load reads and parses the manifest at p. A missing file surfaces as
// os.IsNotExist so callers can treat "no manifest" distinctly (a
// single-component repo may get a default instead — 007 §2.2).
func Load(p string) (*Manifest, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

// Parse parses and validates manifest YAML: repo and at least one component
// are required; every component needs an iri, a unique name, and at least one
// well-formed glob.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse component manifest: %w", err)
	}
	if strings.TrimSpace(m.Repo) == "" {
		return nil, fmt.Errorf("component manifest: repo is required")
	}
	if len(m.Components) == 0 {
		return nil, fmt.Errorf("component manifest: at least one component is required")
	}
	seen := map[string]bool{}
	for i, c := range m.Components {
		if strings.TrimSpace(c.IRI) == "" || strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("component manifest: component %d needs iri and name", i)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("component manifest: duplicate component name %q", c.Name)
		}
		seen[c.Name] = true
		if len(c.Paths) == 0 {
			return nil, fmt.Errorf("component manifest: component %q needs at least one path glob", c.Name)
		}
		for _, g := range c.Paths {
			if err := checkGlob(g); err != nil {
				return nil, fmt.Errorf("component manifest: component %q: %w", c.Name, err)
			}
		}
	}
	return &m, nil
}

// Match maps a repo-relative, slash-separated path to its owning component.
// First match wins (007 §2.2); ok=false means the path belongs to no
// component — a gap the caller reports, never an error.
func (m *Manifest) Match(p string) (*Component, bool) {
	p = strings.TrimPrefix(p, "./")
	for i := range m.Components {
		for _, g := range m.Components[i].Paths {
			if matchGlob(g, p) {
				return &m.Components[i], true
			}
		}
	}
	return nil, false
}

// checkGlob rejects patterns path.Match cannot evaluate, and patterns that
// are structurally dead against repo-relative paths (which never start or
// end with "/", and never contain "//"): a leading "/", a trailing "/", or a
// "//" all produce an empty path segment, which no repo-relative path
// segment can ever equal, so the whole pattern could never match. Rejecting
// at parse time turns a silent stray-slash typo into an authoring error.
func checkGlob(pattern string) error {
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "" {
			return fmt.Errorf("bad glob %q: empty path segment (leading/trailing/doubled slash)", pattern)
		}
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, "probe"); err != nil {
			return fmt.Errorf("bad glob %q: %w", pattern, err)
		}
	}
	return nil
}

// matchGlob matches a slash-separated path against a slash-separated pattern:
// "**" spans zero or more whole segments; any other segment follows
// path.Match, so "*" never crosses a "/".
func matchGlob(pattern, p string) bool {
	pat := strings.Split(pattern, "/")
	segs := strings.Split(p, "/")
	// memo[i][j] caches matchSegs(pat[i:], segs[j:]); a "**" segment tries
	// every skip length, and adjacent "**" segments can revisit the same
	// (i, j) many times without it, which is what makes the unmemoized
	// recursion exponential in the number of "**" segments.
	memo := make([][]int8, len(pat)+1)
	for i := range memo {
		memo[i] = make([]int8, len(segs)+1)
	}
	return matchSegs(pat, segs, 0, 0, memo)
}

const (
	memoUnknown int8 = iota
	memoTrue
	memoFalse
)

// matchSegs matches pat[i:] against segs[j:], memoizing on (i, j) so that
// overlapping "**" backtracking explores each state at most once.
func matchSegs(pat, segs []string, i, j int, memo [][]int8) bool {
	if v := memo[i][j]; v != memoUnknown {
		return v == memoTrue
	}
	result := matchSegsCompute(pat, segs, i, j, memo)
	if result {
		memo[i][j] = memoTrue
	} else {
		memo[i][j] = memoFalse
	}
	return result
}

func matchSegsCompute(pat, segs []string, i, j int, memo [][]int8) bool {
	if i == len(pat) {
		return j == len(segs)
	}
	if pat[i] == "**" {
		for skip := j; skip <= len(segs); skip++ {
			if matchSegs(pat, segs, i+1, skip, memo) {
				return true
			}
		}
		return false
	}
	if j == len(segs) {
		return false
	}
	if ok, err := path.Match(pat[i], segs[j]); err != nil || !ok {
		return false
	}
	return matchSegs(pat, segs, i+1, j+1, memo)
}
