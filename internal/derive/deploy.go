package derive

import (
	"context"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// DeployTriples derives the observed/deploy document (spec 007 deriver 4,
// vocabulary and guards per spec 006 §2.1-§6): a projection of already-
// ingested artifacts, deployments, environments, commit links and release
// frontiers. Projection, not new build (D6) — every triple comes from a row.
func DeployTriples(ctx context.Context, s *store.Store) ([]byte, error) {
	var ts []graphproj.Triple
	ts = append(ts, graphproj.EnvironmentTriples()...)

	artifacts, err := s.AllArtifactsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}

	// The commit guard is prefetched, not queried per artifact. Two reasons:
	// graphproj.CommitKnown cannot report an error, so a per-lookup failure
	// could only be swallowed as "unknown" — and this deriver replaces the
	// whole graph, so swallowing one would silently drop every commit edge
	// and still return success. Prefetching also collapses 2N round trips
	// (ArtifactTriples asks, then the CommitTriples guard below asks again)
	// into one query.
	known, err := knownCommits(ctx, s, artifacts)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}

	for _, a := range artifacts {
		ts = append(ts, graphproj.ArtifactTriples(a, known)...)
		if a.Repo != "" && a.SourceSHA != "" && known(a.Repo, a.SourceSHA) {
			ts = append(ts, graphproj.CommitTriples(graphproj.GitHubHost, a.Repo, a.SourceSHA)...)
		}
	}

	deployments, err := s.ListDeployments(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, d := range deployments {
		var artifact *store.Artifact
		if d.ArtifactID != nil {
			if a, ok := artifacts[*d.ArtifactID]; ok {
				artifact = &a
			}
		}
		ts = append(ts, graphproj.DeploymentTriples(d, artifact)...)
	}

	frontiers, err := s.AllReleaseFrontiers(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, f := range frontiers {
		ts = append(ts, graphproj.ReleaseCutFromTriples(f.Repo, f.Tag, f.SHA)...)
	}

	return graphproj.Document(ts), nil
}

// knownCommits resolves the commit guard for every artifact in one query and
// returns it as a pure map lookup, so no triple-building step can touch the
// database (and therefore no triple-building step can fail silently).
func knownCommits(ctx context.Context, s *store.Store, artifacts map[int64]store.Artifact) (graphproj.CommitKnown, error) {
	seen := map[store.RepoSHA]bool{}
	var keys []store.RepoSHA
	for _, a := range artifacts {
		if a.Repo == "" || a.SourceSHA == "" {
			continue
		}
		k := store.RepoSHA{Repo: a.Repo, SHA: a.SourceSHA}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	found, err := s.KnownMainCommits(ctx, keys)
	if err != nil {
		return nil, err
	}
	return func(repo, sha string) bool {
		return found[store.RepoSHA{Repo: repo, SHA: sha}]
	}, nil
}
