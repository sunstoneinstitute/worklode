package derive

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// wlAffects is the wl:affects predicate (006 ontology), resolved through
// iri.Term rather than hardcoded, matching internal/graphproj's convention
// for wl: terms.
var wlAffects = iri.Term("affects")

// ErrNotFound is the RepoReader contract's sentinel for a missing file,
// deliberately independent of githubauth.ErrContentNotFound so this
// interface carries no forge dependency.
var ErrNotFound = errors.New("not found")

// RepoReader is the slice of the forge API the pr-affects deriver needs.
// Spec 007 deriver 3's inputs are pulled fresh on each run — derivers are
// cheap to re-run and hold no state.
type RepoReader interface {
	// FileAt fetches a file at the repo's default branch head.
	FileAt(ctx context.Context, repo, path string) ([]byte, error)
	// PRFiles lists a pull request's changed file paths.
	PRFiles(ctx context.Context, repo string, number int64) ([]string, error)
}

// PRAffectsTriples derives the observed/pr-affects document: for every
// task-bound PR, each changed path is mapped to a component through the
// repo's manifest and emitted as <task> wl:affects <component>. Repos
// without a manifest are skipped and reported, never fatal. The manifest is
// fetched at most once per repo — a hit or a miss is cached before moving
// to the next PR, however many PRs share that repo.
func PRAffectsTriples(ctx context.Context, prs []store.PRRef, rr RepoReader) (doc []byte, skippedRepos []string, err error) {
	manifests := map[string]*manifest.Manifest{}
	skipped := map[string]bool{}
	var ts []graphproj.Triple
	for _, pr := range prs {
		m, ok := manifests[pr.Repo]
		if !ok && !skipped[pr.Repo] {
			data, ferr := rr.FileAt(ctx, pr.Repo, ".worklode/components.yaml")
			switch {
			case errors.Is(ferr, ErrNotFound):
				skipped[pr.Repo] = true
			case ferr != nil:
				return nil, nil, fmt.Errorf("manifest for %s: %w", pr.Repo, ferr)
			default:
				if m, ferr = manifest.Parse(data); ferr != nil {
					return nil, nil, fmt.Errorf("manifest for %s: %w", pr.Repo, ferr)
				}
				manifests[pr.Repo] = m
			}
		}
		m = manifests[pr.Repo]
		if m == nil {
			continue
		}
		files, ferr := rr.PRFiles(ctx, pr.Repo, pr.Number)
		if ferr != nil {
			return nil, nil, fmt.Errorf("files of %s#%d: %w", pr.Repo, pr.Number, ferr)
		}
		for _, f := range files {
			if c, ok := m.Match(f); ok {
				ts = append(ts, graphproj.Triple{S: iri.Task(pr.TaskID), P: wlAffects, O: graphproj.IRIRef(c.IRI)})
			}
		}
	}
	for r := range skipped {
		skippedRepos = append(skippedRepos, r)
	}
	sort.Strings(skippedRepos)
	return graphproj.Document(ts), skippedRepos, nil
}
