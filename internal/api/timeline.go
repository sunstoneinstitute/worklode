package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// timelineEntry pairs an entry's sort key with its wire object. Entries are
// appended source-by-source (state, pr, ci, review, artifact, deployment,
// runtime) and stably sorted by time, so equal timestamps keep that order.
type timelineEntry struct {
	at  time.Time
	obj map[string]any
}

// taskTimeline handles GET /api/v1/tasks/{id}/timeline: one ascending
// time-ordered array merging the task's state changes, its linked PRs, CI
// runs and reviews on those PRs, artifacts built from the PRs' merge SHAs,
// and deployments and runtime events referencing those artifacts.
func (s *server) taskTimeline(w http.ResponseWriter, r *http.Request) {
	t, entries, err := s.assembleTimeline(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	timeline := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		timeline = append(timeline, e.obj)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task":     toTaskJSON(t),
		"timeline": timeline,
	})
}

// assembleTimeline returns a task and its full timeline — state changes,
// linked PRs, CI runs and reviews on those PRs, artifacts built from the
// PRs' merge SHAs, and deployments and runtime events referencing those
// artifacts — ascending by time. Shared by the JSON
// /api/v1/tasks/{id}/timeline handler and the GET /tasks/{id} web page.
func (s *server) assembleTimeline(ctx context.Context, id string) (*store.Task, []timelineEntry, error) {
	t, err := s.st.GetTask(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	var entries []timelineEntry

	se, err := s.stateEntries(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, se...)

	prs, err := s.st.PRsForTask(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, prEntries(prs)...)

	ce, err := s.ciEntries(ctx, prs)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, ce...)

	rve, err := s.reviewEntries(ctx, prs)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, rve...)

	artifacts, err := s.mergedArtifacts(ctx, prs)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, artifactEntries(artifacts)...)

	de, err := s.deploymentEntries(ctx, artifacts)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, de...)

	rte, err := s.runtimeEntries(ctx, artifacts)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, rte...)

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	return t, entries, nil
}

func (s *server) stateEntries(ctx context.Context, taskID string) ([]timelineEntry, error) {
	logs, err := s.st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		return nil, err
	}
	out := make([]timelineEntry, 0, len(logs))
	for _, l := range logs {
		out = append(out, timelineEntry{at: l.At, obj: map[string]any{
			"at":       l.At,
			"type":     "state",
			"change":   json.RawMessage(l.Change),
			"event_id": l.EventID,
		}})
	}
	return out, nil
}

func prEntries(prs []store.PullRequest) []timelineEntry {
	out := make([]timelineEntry, 0, len(prs))
	for _, pr := range prs {
		out = append(out, timelineEntry{at: pr.OpenedAt, obj: map[string]any{
			"at":        pr.OpenedAt,
			"type":      "pr",
			"repo":      pr.Repo,
			"number":    pr.Number,
			"title":     pr.Title,
			"state":     pr.State,
			"url":       pr.URL,
			"merged_at": pr.MergedAt,
		}})
	}
	return out
}

func (s *server) ciEntries(ctx context.Context, prs []store.PullRequest) ([]timelineEntry, error) {
	var out []timelineEntry
	for _, pr := range prs {
		runs, err := s.st.CIRunsForSHA(ctx, pr.Repo, pr.HeadSHA)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			out = append(out, timelineEntry{at: run.StartedAt, obj: map[string]any{
				"at":           run.StartedAt,
				"type":         "ci",
				"repo":         run.Repo,
				"workflow":     run.Workflow,
				"status":       run.Status,
				"conclusion":   run.Conclusion,
				"url":          run.URL,
				"completed_at": run.CompletedAt,
			}})
		}
	}
	return out, nil
}

func (s *server) reviewEntries(ctx context.Context, prs []store.PullRequest) ([]timelineEntry, error) {
	var out []timelineEntry
	for _, pr := range prs {
		reviews, err := s.st.ReviewsForPR(ctx, pr.Repo, pr.Number)
		if err != nil {
			return nil, err
		}
		for _, rv := range reviews {
			out = append(out, timelineEntry{at: rv.SubmittedAt, obj: map[string]any{
				"at":       rv.SubmittedAt,
				"type":     "review",
				"repo":     rv.Repo,
				"number":   rv.PRNumber,
				"reviewer": rv.Reviewer,
				"state":    rv.State,
			}})
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

func artifactEntries(artifacts []store.Artifact) []timelineEntry {
	out := make([]timelineEntry, 0, len(artifacts))
	for _, a := range artifacts {
		out = append(out, timelineEntry{at: a.BuiltAt, obj: map[string]any{
			"at":      a.BuiltAt,
			"type":    "artifact",
			"kind":    a.Kind,
			"name":    a.Name,
			"version": a.Version,
		}})
	}
	return out
}

func (s *server) deploymentEntries(ctx context.Context, artifacts []store.Artifact) ([]timelineEntry, error) {
	var out []timelineEntry
	for _, a := range artifacts {
		deployments, err := s.st.DeploymentsForArtifact(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range deployments {
			out = append(out, timelineEntry{at: d.LastUpdate, obj: map[string]any{
				"at":          d.LastUpdate,
				"type":        "deployment",
				"environment": d.Environment,
				"target_name": d.TargetName,
				"status":      d.Status,
			}})
		}
	}
	return out, nil
}

func (s *server) runtimeEntries(ctx context.Context, artifacts []store.Artifact) ([]timelineEntry, error) {
	var out []timelineEntry
	for _, a := range artifacts {
		events, err := s.st.RuntimeEventsForArtifact(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		for _, re := range events {
			out = append(out, timelineEntry{at: re.OccurredAt, obj: map[string]any{
				"at":       re.OccurredAt,
				"type":     "runtime",
				"kind":     re.Kind,
				"cluster":  re.Cluster,
				"workload": re.Workload,
				"message":  re.Message,
			}})
		}
	}
	return out, nil
}
