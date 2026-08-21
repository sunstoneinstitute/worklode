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

	known := func(repo, sha string) bool {
		ok, err := s.HasMainCommit(ctx, repo, sha)
		return err == nil && ok
	}

	artifacts, err := s.AllArtifactsByID(ctx)
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
