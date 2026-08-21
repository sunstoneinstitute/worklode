// Package reconcile implements engine 2 of lode reconcile (spec 013): ask
// GitHub the current truth about candidate tasks, write the missing facts
// through the existing upserts, and let store.ResolveDelivery advance the
// state. Because ResolveDelivery derives delivery state from recorded facts,
// repairing facts is sufficient — no event ordering to replay.
//
// Two phases per run: gather (network reads, no writes) then apply (one
// store.RecordEvent transaction under a single source='system' event of type
// "reconcile.poll", external_id = run id). Facts and transitions attribute
// to that event: the task advanced because reconcile observed it.
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Options bound one poll run. RunID is the system event's external_id and
// must be unique per run.
type Options struct {
	Repo   string
	Task   string
	Since  *time.Time
	DryRun bool
	RunID  string

	// Log and Metrics mirror what engine 1 takes (hooks.ReplayOptions); both
	// are optional. A nil Log falls back to slog.Default(); a nil Metrics
	// records nothing.
	Log     *slog.Logger
	Metrics *Metrics
}

// repoFacts is everything gathered for one repo before the apply phase.
type repoFacts struct {
	repo     string
	prs      []store.PullRequest // fresh facts, ready for UpsertPR
	prBodies map[int64]string
	// landedSHAs are the shas GitHub confirms are on the default branch, in
	// the order they landed — main_commits.id is the per-repo ordering every
	// frontier comparison depends on and is permanent, so appending in any
	// other order can over-advance a frontier.
	landedSHAs    []string
	landed        map[string]bool      // membership view of landedSHAs
	landedAt      map[string]time.Time // sha -> GitHub's committer date (may be zero)
	taskSHAs      map[string][]string  // task id -> the shas checked on its behalf, sorted
	mergedCommits []store.TaskCommit
	releases      []githubauth.ReleaseFacts
	tasks         []store.PollCandidate
}

// Poll runs engine 2. app must be non-nil; the API layer skips polling (with
// an explanation) when the GitHub App is not configured.
func Poll(ctx context.Context, st *store.Store, app *githubauth.AppAuth, opts Options) (*model.PollResult, error) {
	// The run id is the system event's external_id, and RecordEvent skips
	// apply entirely on conflict. An empty one would collide with the last
	// empty-id run and report repairs that never happened.
	if opts.RunID == "" {
		return nil, fmt.Errorf("reconcile poll: run id is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	candidates, err := st.PollCandidates(ctx, opts.Repo, opts.Task, opts.Since)
	if err != nil {
		return nil, err
	}
	res := &model.PollResult{RunID: opts.RunID, DryRun: opts.DryRun, Candidates: len(candidates)}
	if len(candidates) == 0 {
		return res, nil
	}

	byRepo := map[string][]store.PollCandidate{}
	var repos []string
	for _, c := range candidates {
		if _, seen := byRepo[c.Repo]; !seen {
			repos = append(repos, c.Repo)
		}
		byRepo[c.Repo] = append(byRepo[c.Repo], c)
	}
	// Map iteration is unordered; the run report and the --json contract are
	// not. PollCandidates already returns a sorted set, so first-seen order
	// over it is deterministic.

	var gathered []*repoFacts
	for _, repo := range repos {
		facts, err := gatherRepo(ctx, st, app, repo, byRepo[repo])
		if err != nil {
			// One repo failing (App not installed there, rate limit) must not
			// abort the run for every other repo.
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", repo, err))
			opts.Metrics.repoError()
			log.Warn("reconcile poll could not gather repo",
				"run_id", opts.RunID, "repo", repo, "candidates", len(byRepo[repo]), "err", err)
			continue
		}
		gathered = append(gathered, facts)
	}

	for _, f := range gathered {
		for _, c := range f.tasks {
			repair := model.TaskRepair{TaskID: c.TaskID, Repo: f.repo, State: c.State}
			for _, pr := range f.prs {
				if pr.TaskID != nil && *pr.TaskID == c.TaskID {
					repair.PRsUpdated = append(repair.PRsUpdated, pr.Number)
				}
			}
			// Only the shas gathered for this task, not the repo-wide set:
			// two candidates in one repo must not claim each other's commits.
			for _, sha := range f.taskSHAs[c.TaskID] {
				if f.landed[sha] {
					repair.CommitsLanded = append(repair.CommitsLanded, sha)
				}
			}
			if len(repair.PRsUpdated) > 0 || len(repair.CommitsLanded) > 0 {
				res.Repaired = append(res.Repaired, repair)
			}
		}
	}
	// Gate on what was gathered, not on res.Repaired: Repaired only fills
	// from task-level facts (PRs, commits), but a repo's releases are a
	// repo-level fact (013 §2.2). A candidate correlated solely through an
	// already-landed task_commits row produces no repair, yet a release
	// published during the outage still has to move it to released.
	// Every candidate belongs to exactly one repo, so the ones whose repo
	// never gathered are exactly those the gather loop skipped.
	polled := 0
	for _, f := range gathered {
		polled += len(f.tasks)
	}
	opts.Metrics.candidateOutcome("gather_error", len(candidates)-polled)

	if opts.DryRun || len(gathered) == 0 {
		opts.Metrics.candidateOutcome("dry_run", polled)
		return res, nil
	}

	summary, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encode run summary: %w", err)
	}
	_, inserted, err := st.RecordEvent(ctx, "system", opts.RunID, "reconcile.poll", summary,
		func(tx *sql.Tx, eventID int64) error {
			return applyFacts(tx, st.Now(), eventID, gathered)
		})
	if err != nil {
		opts.Metrics.candidateOutcome("error", polled)
		return nil, err
	}
	if !inserted {
		// RecordEvent skipped apply: nothing in res was written. Reporting
		// success here would describe repairs that did not happen.
		opts.Metrics.candidateOutcome("error", polled)
		return nil, fmt.Errorf("reconcile poll: run id %q already recorded; no facts were applied", opts.RunID)
	}

	// Counted only past the apply: every other exit wrote nothing, so
	// crediting a repair there would report facts that never landed.
	opts.Metrics.candidateOutcome("repaired", len(res.Repaired))
	opts.Metrics.candidateOutcome("clean", polled-len(res.Repaired))
	prs, commits := 0, 0
	for _, r := range res.Repaired {
		prs += len(r.PRsUpdated)
		commits += len(r.CommitsLanded)
	}
	opts.Metrics.repaired("pr", prs)
	opts.Metrics.repaired("commit", commits)
	log.Info("reconcile poll applied",
		"run_id", opts.RunID, "candidates", res.Candidates, "polled", polled,
		"repaired", len(res.Repaired), "prs", prs, "commits", commits,
		"repo_errors", len(res.Errors))
	return res, nil
}

// gatherRepo reads GitHub once per repo: one installation token, then the
// PRs, default-branch membership, and releases for that repo's candidate
// tasks. Read-only.
func gatherRepo(ctx context.Context, st *store.Store, app *githubauth.AppAuth, repo string, tasks []store.PollCandidate) (*repoFacts, error) {
	rc, err := app.NewRepoClient(ctx, repo)
	if err != nil {
		return nil, err
	}
	defaultBranch, err := rc.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	f := &repoFacts{
		repo:     repo,
		prBodies: map[int64]string{},
		landed:   map[string]bool{},
		landedAt: map[string]time.Time{},
		taskSHAs: map[string][]string{},
		tasks:    tasks,
	}
	now := st.Now()
	shasToCheck := map[string]bool{}
	// check records a sha to ask GitHub about and the task it was gathered
	// for, so the report can attribute each landing to the right task.
	seenForTask := map[string]map[string]bool{}
	check := func(taskID, sha string) {
		if sha == "" {
			return
		}
		shasToCheck[sha] = true
		if seenForTask[taskID] == nil {
			seenForTask[taskID] = map[string]bool{}
		}
		if seenForTask[taskID][sha] {
			return
		}
		seenForTask[taskID][sha] = true
		f.taskSHAs[taskID] = append(f.taskSHAs[taskID], sha)
	}

	for _, c := range tasks {
		prs, err := st.PRsForTask(ctx, c.TaskID)
		if err != nil {
			return nil, err
		}
		for _, known := range prs {
			if known.Repo != repo {
				continue
			}
			gh, err := rc.PR(ctx, known.Number)
			if err != nil {
				return nil, err
			}
			state := "open"
			if gh.State == "closed" {
				state = "closed"
				if gh.Merged {
					state = "merged"
				}
			}
			openedAt := gh.CreatedAt
			if openedAt.IsZero() {
				openedAt = now
			}
			taskID := c.TaskID
			f.prs = append(f.prs, store.PullRequest{
				Repo: repo, Number: gh.Number, Title: gh.Title, State: state,
				TaskID: &taskID, HeadRef: gh.HeadRef(), HeadSHA: gh.HeadSHA(),
				MergeSHA: gh.MergeCommitSHA, URL: gh.HTMLURL,
				OpenedAt: openedAt, MergedAt: gh.MergedAt,
				// UpdatedAt drives UpsertPR's non-regressing guard; a zero
				// value sorts as '-infinity' and the write is dropped for any
				// PR a webhook already timestamped.
				UpdatedAt: gh.UpdatedAt,
				Author:    gh.User.Login,
			})
			f.prBodies[gh.Number] = gh.Body
			if gh.Merged {
				if sha := gh.HeadSHA(); sha != "" {
					f.mergedCommits = append(f.mergedCommits, store.TaskCommit{
						TaskID: c.TaskID, Repo: repo, SHA: sha, Source: "pr", SeenAt: now,
					})
				}
				if gh.MergeCommitSHA != nil && *gh.MergeCommitSHA != "" {
					f.mergedCommits = append(f.mergedCommits, store.TaskCommit{
						TaskID: c.TaskID, Repo: repo, SHA: *gh.MergeCommitSHA, Source: "pr", SeenAt: now,
					})
					check(c.TaskID, *gh.MergeCommitSHA)
				}
			}
		}
		// Commits the backbone recorded that never showed up on main.
		unlanded, err := st.UnlandedTaskCommits(ctx, c.TaskID, repo)
		if err != nil {
			return nil, err
		}
		for _, sha := range unlanded {
			check(c.TaskID, sha)
		}
	}

	for _, shas := range f.taskSHAs {
		sort.Strings(shas)
	}

	ordered := make([]string, 0, len(shasToCheck))
	for sha := range shasToCheck {
		ordered = append(ordered, sha)
	}
	sort.Strings(ordered)
	for _, sha := range ordered {
		on, committed, err := rc.CommitOnBranch(ctx, defaultBranch, sha)
		if err != nil {
			return nil, err
		}
		if on {
			f.landed[sha] = true
			f.landedAt[sha] = committed
			f.landedSHAs = append(f.landedSHAs, sha)
		}
	}
	// Append order is commit date, not the sha order the requests were made
	// in: main_commits ids are permanent and a frontier read against them
	// must not place a later commit below an earlier one. The sha tiebreak
	// keeps equal (or missing) dates deterministic for --json.
	sort.SliceStable(f.landedSHAs, func(i, j int) bool {
		a, b := f.landedSHAs[i], f.landedSHAs[j]
		if !f.landedAt[a].Equal(f.landedAt[b]) {
			return f.landedAt[a].Before(f.landedAt[b])
		}
		return a < b
	})

	// Releases only matter for release-terminated repos; asking costs one
	// request and applyFacts ignores unresolvable ones, so ask uniformly.
	rels, err := rc.Releases(ctx)
	if err != nil {
		return nil, err
	}
	f.releases = rels
	return f, nil
}

// applyFacts writes one run's gathered facts inside the reconcile.poll
// event's transaction: PR upserts, task commits, main-branch appends,
// release frontiers, then ResolveDelivery per candidate. Every write is an
// upsert or a from-state-guarded transition, so a re-run converges.
func applyFacts(tx *sql.Tx, now time.Time, eventID int64, gathered []*repoFacts) error {
	for _, f := range gathered {
		for _, pr := range f.prs {
			if _, err := store.UpsertPR(tx, pr, f.prBodies[pr.Number]); err != nil {
				return err
			}
		}
		for _, tc := range f.mergedCommits {
			if err := store.InsertTaskCommit(tx, tc); err != nil {
				return err
			}
		}
		for _, sha := range f.landedSHAs {
			// Guarded: only append shas main_commits does not already know,
			// so re-running never duplicates the frontier.
			known, err := store.MainIDForSHA(tx, f.repo, sha)
			if err != nil {
				return err
			}
			if known == nil {
				// pushed_at is when the commit landed, not when reconcile
				// noticed; fall back to now when GitHub gave no date.
				pushedAt := f.landedAt[sha]
				if pushedAt.IsZero() {
					pushedAt = now
				}
				if _, err := store.AppendMainCommit(tx, f.repo, sha, pushedAt); err != nil {
					return err
				}
			}
		}
		for _, rel := range f.releases {
			mainID, err := store.MainIDForSHA(tx, f.repo, rel.TargetCommitish)
			if err != nil {
				return err
			}
			if mainID == nil {
				// target_commitish is often a branch name; without a
				// resolvable sha there is no frontier to record. Conservative:
				// skip rather than guess (the webhook path's LatestMainID
				// fallback is only correct at delivery time).
				continue
			}
			publishedAt := rel.PublishedAt
			if publishedAt.IsZero() {
				publishedAt = now
			}
			if err := store.SetReleaseFrontier(tx, f.repo, rel.TagName, *mainID, publishedAt); err != nil {
				return err
			}
		}
		for _, c := range f.tasks {
			if err := store.ResolveDelivery(tx, now, c.TaskID, f.repo, eventID); err != nil {
				return err
			}
		}
	}
	return nil
}
