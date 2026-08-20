package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// taskTimeline handles GET /api/v1/tasks/{id}/timeline: one ascending
// time-ordered array merging the task's state changes, its linked PRs, CI
// runs and reviews on those PRs, artifacts built from the PRs' merge SHAs,
// deployments and runtime events referencing those artifacts, and the
// delivery milestones its commits reached.
func (s *server) taskTimeline(w http.ResponseWriter, r *http.Request) {
	t, entries, err := s.assembleTimeline(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.TimelineResponse{Task: *t, Timeline: entries})
}

// assembleTimeline returns a task and its full timeline — state changes,
// linked PRs, CI runs and reviews on those PRs, artifacts built from the
// PRs' merge SHAs, deployments and runtime events referencing those
// artifacts, and delivery milestones — ascending by time. Entries are
// appended source-by-source and then stably sorted, so equal timestamps keep
// that order. Shared by the JSON /api/v1/tasks/{id}/timeline handler and the
// GET /tasks/{id} web page.
func (s *server) assembleTimeline(ctx context.Context, id string) (*model.Task, []model.TimelineEntry, error) {
	t, err := s.st.GetTask(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	// Non-nil, not `var entries []model.TimelineEntry`: this slice is the
	// response body's "timeline", and a nil one marshals to null rather than
	// the empty array a task with no history has to answer with.
	entries := []model.TimelineEntry{}
	// add appends one source's entries, or reports its error. Append order is
	// what the stable sort below tie-breaks on, so the sequence of calls is
	// significant: keep each source's entries contiguous.
	add := func(src []model.TimelineEntry, err error) error {
		if err != nil {
			return err
		}
		entries = append(entries, src...)
		return nil
	}

	if err := add(s.stateEntries(ctx, id)); err != nil {
		return nil, nil, err
	}

	prs, err := s.st.PRsForTask(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := add(prEntries(prs), nil); err != nil {
		return nil, nil, err
	}
	if err := add(s.ciEntries(ctx, prs)); err != nil {
		return nil, nil, err
	}
	if err := add(s.reviewEntries(ctx, prs)); err != nil {
		return nil, nil, err
	}

	artifacts, err := s.mergedArtifacts(ctx, prs)
	if err != nil {
		return nil, nil, err
	}
	if err := add(artifactEntries(artifacts), nil); err != nil {
		return nil, nil, err
	}
	if err := add(s.deploymentEntries(ctx, artifacts)); err != nil {
		return nil, nil, err
	}
	if err := add(s.runtimeEntries(ctx, artifacts)); err != nil {
		return nil, nil, err
	}
	if err := add(s.deliveryEntries(ctx, id)); err != nil {
		return nil, nil, err
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	return t, entries, nil
}

func (s *server) stateEntries(ctx context.Context, taskID string) ([]model.TimelineEntry, error) {
	logs, err := s.st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		return nil, err
	}
	out := make([]model.TimelineEntry, 0, len(logs))
	for _, l := range logs {
		out = append(out, model.TimelineEntry{
			At:      l.At,
			Type:    "state",
			Change:  json.RawMessage(l.Change),
			EventID: l.EventID,
		})
	}
	return out, nil
}

func prEntries(prs []store.PullRequest) []model.TimelineEntry {
	out := make([]model.TimelineEntry, 0, len(prs))
	for _, pr := range prs {
		out = append(out, model.TimelineEntry{
			At:       pr.OpenedAt,
			Type:     "pr",
			Repo:     pr.Repo,
			Number:   pr.Number,
			Title:    pr.Title,
			State:    pr.State,
			URL:      pr.URL,
			MergedAt: pr.MergedAt,
		})
	}
	return out
}

func (s *server) ciEntries(ctx context.Context, prs []store.PullRequest) ([]model.TimelineEntry, error) {
	var out []model.TimelineEntry
	for _, pr := range prs {
		runs, err := s.st.CIRunsForSHA(ctx, pr.Repo, pr.HeadSHA)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			out = append(out, model.TimelineEntry{
				At:          run.StartedAt,
				Type:        "ci",
				Repo:        run.Repo,
				Workflow:    run.Workflow,
				Status:      run.Status,
				Conclusion:  run.Conclusion,
				URL:         run.URL,
				CompletedAt: run.CompletedAt,
			})
		}
	}
	return out, nil
}

func (s *server) reviewEntries(ctx context.Context, prs []store.PullRequest) ([]model.TimelineEntry, error) {
	var out []model.TimelineEntry
	for _, pr := range prs {
		reviews, err := s.st.ReviewsForPR(ctx, pr.Repo, pr.Number)
		if err != nil {
			return nil, err
		}
		for _, rv := range reviews {
			out = append(out, model.TimelineEntry{
				At:       rv.SubmittedAt,
				Type:     "review",
				Repo:     rv.Repo,
				Number:   rv.PRNumber,
				Reviewer: rv.Reviewer,
				State:    rv.State,
			})
		}
	}
	return out, nil
}

// mergedArtifacts returns the artifacts built from the merge SHA of each
// merged PR, deduplicated by artifact id (two PRs can share a merge SHA).
func (s *server) mergedArtifacts(ctx context.Context, prs []store.PullRequest) ([]store.Artifact, error) {
	var out []store.Artifact
	seen := map[int64]bool{}
	for _, pr := range prs {
		if pr.MergeSHA == nil {
			continue
		}
		artifacts, err := s.st.ArtifactsBySourceSHA(ctx, *pr.MergeSHA)
		if err != nil {
			return nil, err
		}
		for _, a := range artifacts {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, a)
			}
		}
	}
	return out, nil
}

func artifactEntries(artifacts []store.Artifact) []model.TimelineEntry {
	out := make([]model.TimelineEntry, 0, len(artifacts))
	for _, a := range artifacts {
		out = append(out, model.TimelineEntry{
			At:      a.BuiltAt,
			Type:    "artifact",
			Kind:    a.Kind,
			Name:    a.Name,
			Version: a.Version,
		})
	}
	return out
}

func (s *server) deploymentEntries(ctx context.Context, artifacts []store.Artifact) ([]model.TimelineEntry, error) {
	var out []model.TimelineEntry
	for _, a := range artifacts {
		deployments, err := s.st.DeploymentsForArtifact(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range deployments {
			out = append(out, model.TimelineEntry{
				At:          d.LastUpdate,
				Type:        "deployment",
				Environment: d.Environment,
				TargetName:  d.TargetName,
				Status:      d.Status,
			})
		}
	}
	return out, nil
}

// deliveryEntries reports the task's delivery milestones: where its work
// landed, which environments have confirmably received it, and the release
// that shipped it — one set per repo the task landed commits in.
func (s *server) deliveryEntries(ctx context.Context, taskID string) ([]model.TimelineEntry, error) {
	facts, err := s.st.DeliveryFactsForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var out []model.TimelineEntry
	for _, f := range facts {
		out = append(out, model.TimelineEntry{
			At: f.LandedAt, Type: "landed", Repo: f.Repo, SHA: f.LandedSHA,
		})
		for _, d := range f.Deployed {
			out = append(out, model.TimelineEntry{
				At: d.At, Type: "deployed", Repo: f.Repo, Environment: d.Environment,
			})
		}
		if f.ReleaseTag != "" {
			out = append(out, model.TimelineEntry{
				At: f.ReleasedAt, Type: "released", Repo: f.Repo, Tag: f.ReleaseTag,
			})
		}
	}
	return out, nil
}

func (s *server) runtimeEntries(ctx context.Context, artifacts []store.Artifact) ([]model.TimelineEntry, error) {
	var out []model.TimelineEntry
	for _, a := range artifacts {
		events, err := s.st.RuntimeEventsForArtifact(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		for _, re := range events {
			out = append(out, model.TimelineEntry{
				At:       re.OccurredAt,
				Type:     "runtime",
				Kind:     re.Kind,
				Cluster:  re.Cluster,
				Workload: re.Workload,
				Message:  re.Message,
			})
		}
	}
	return out, nil
}
