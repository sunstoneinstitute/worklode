// The Progress page's one outward write (WL-SPEC-66 §3.6): queue a task's
// pull request for merge, or merge it. Everything else this page writes goes
// to the backbone alone; this route also calls GitHub, so §4.2 rule 9
// applies — the request is in the event log before the call leaves.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// progressMerge handles POST /projects/{id}/progress/merge. The branch's
// stored rules decide the operation: a queue-protected branch is enqueued
// through GraphQL, since GitHub refuses a REST merge on one; anything else
// is merged through REST.
//
// Two events bracket the call. pr.merge_requested is written first and
// always: a crash, a timeout or a lost reply between it and the outcome
// still leaves "this actor asked for this" in the log, which is the point of
// the ordering. pr.merge_result follows with what GitHub said.
func (s *server) progressMerge(w http.ResponseWriter, r *http.Request) {
	var body model.ProgressMergeInput
	sub, project, ok := s.beginJSONPost(w, r, "merge", &body)
	if !ok {
		return
	}
	ctx := r.Context()

	// Acting on a PR needs the App's installation token; without an App
	// there is nothing to act with, and that is a deployment fact rather
	// than a bad request.
	if s.appAuth == nil {
		s.observeProgressWrite("merge", "refused")
		writeErr(w, http.StatusServiceUnavailable, "no GitHub App is configured")
		return
	}

	task, err := s.st.GetTask(ctx, body.Task)
	if err != nil {
		s.observeProgressWrite("merge", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	// A task of another project is not found on this project's route, as it
	// is for every other act on this page.
	if task.Project != project.ID {
		s.observeProgressWrite("merge", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	pr, err := s.st.GetPR(ctx, body.PR.Repo, body.PR.Number)
	if err != nil {
		s.observeProgressWrite("merge", progressWriteOutcome(err))
		s.mapStoreErr(w, err)
		return
	}

	// The page renders the button from facts that may have moved on. Each of
	// these is the backbone refusing the act, not the actor (§4.2 rule 4),
	// and repeating a request that already took effect lands here rather
	// than acting twice (rule 7).
	var conflict string
	switch {
	case pr.TaskID == nil || *pr.TaskID != task.ID:
		conflict = fmt.Sprintf("PR %s#%d does not carry %s", pr.Repo, pr.Number, task.ID)
	case pr.State == "merged" || pr.MergedAt != nil:
		conflict = fmt.Sprintf("PR %s#%d is already merged", pr.Repo, pr.Number)
	case pr.State != "open":
		conflict = fmt.Sprintf("PR %s#%d is %s", pr.Repo, pr.Number, pr.State)
	case pr.QueuedAt != nil:
		conflict = fmt.Sprintf("PR %s#%d is already queued for merge", pr.Repo, pr.Number)
	}
	if conflict != "" {
		s.observeProgressWrite("merge", "conflict")
		writeErr(w, http.StatusConflict, conflict)
		return
	}

	// One row per repo, for its default branch (§6.3), so the lookup needs
	// no branch. Unknown reads as no queue, which is the plain merge.
	queues, err := s.st.BranchRulesForRepos(ctx, []string{pr.Repo})
	if err != nil {
		s.observeProgressWrite("merge", "error")
		s.mapStoreErr(w, err)
		return
	}
	op := "merge"
	if queues[pr.Repo] {
		op = "enqueue"
	}

	if err := s.recordTaskEvent(ctx, "web", "pr.merge_requested", task.ID, map[string]any{
		"actor": sub.ActorID, "repo": pr.Repo, "number": pr.Number, "op": op,
	}, nil); err != nil {
		s.observeProgressWrite("merge", "error")
		s.mapStoreErr(w, err)
		return
	}

	callErr := s.callGitHubMerge(ctx, op, pr.Repo, pr.Number)
	outcome, message := "ok", ""
	if callErr != nil {
		outcome, message = "refused", githubMessage(callErr)
	}
	// The result is a record, not the reply: failing to write it must not
	// turn a merge GitHub already performed into an error the page retries.
	if err := s.recordTaskEvent(ctx, "web", "pr.merge_result", task.ID, map[string]any{
		"actor": sub.ActorID, "repo": pr.Repo, "number": pr.Number, "op": op,
		"outcome": outcome, "message": message,
	}, nil); err != nil {
		s.log.Error("progress merge: recording the result failed", "task", task.ID, "err", err)
	}

	var ghErr *githubauth.GitHubError
	switch {
	case errors.As(callErr, &ghErr) && ghErr.Status < http.StatusInternalServerError:
		// GitHub's own reason — checks failing, not mergeable, a draft —
		// passed back verbatim for the page to show inline.
		s.observeProgressWrite("merge", "conflict")
		writeErr(w, http.StatusConflict, ghErr.Message)
	case callErr != nil:
		s.observeProgressWrite("merge", "error")
		s.log.Error("progress merge: github call failed", "op", op,
			"repo", pr.Repo, "number", pr.Number, "err", callErr)
		writeErr(w, http.StatusBadGateway, "github could not be reached: "+message)
	default:
		s.observeProgressWrite("merge", "ok")
		writeJSON(w, http.StatusOK, model.ProgressMergeResponse{Op: op, Queued: op == "enqueue"})
	}
}

// callGitHubMerge performs the operation itself. Enqueuing costs one read
// more than merging: the mutation takes the PR's GraphQL node id, and REST
// is where that is published.
func (s *server) callGitHubMerge(ctx context.Context, op, repo string, number int64) error {
	if op == "merge" {
		s.observeGitHubCall("merge_pr")
		return s.appAuth.MergePR(ctx, repo, number, "")
	}
	s.observeGitHubCall("pr_node_id")
	nodeID, err := s.appAuth.PRNodeID(ctx, repo, number)
	if err != nil {
		return err
	}
	s.observeGitHubCall("enqueue_pr")
	return s.appAuth.EnqueuePR(ctx, repo, nodeID)
}

// githubMessage is what the result event records about a failure: GitHub's
// own words when it explained itself, the error text otherwise.
func githubMessage(err error) string {
	var ghErr *githubauth.GitHubError
	if errors.As(err, &ghErr) {
		return ghErr.Message
	}
	return err.Error()
}
