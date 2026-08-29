package hooks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// pushPayload is the part of a GitHub push event the handler needs.
type pushPayload struct {
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
}

// zeroSHA is git's all-zeros object id, sent as before or after when a ref
// is created or deleted.
const zeroSHA = "0000000000000000000000000000000000000000"

// truncated reports whether the commits array is missing the head of the
// push, meaning the attribution below saw only part of what landed.
//
// GitHub caps the array (2048 commits) and sends no truncation flag, so
// there is nothing to read directly. It does document the array as every
// commit between before and after — so an after that is absent from it
// proves we did not get them all. Testing for the head rather than for the
// cap keeps this correct if the number ever changes.
//
// A ref create or delete carries a zero before or after and no useful commit
// list; neither is truncation.
func (p pushPayload) truncated() bool {
	if p.After == "" || p.After == zeroSHA || len(p.Commits) == 0 {
		return false
	}
	for _, c := range p.Commits {
		if c.ID == p.After {
			return false
		}
	}
	return true
}

// mergeMessagePatterns extract the merged branch name from merge-commit
// messages ("Merge branch 'x'", "Merge pull request #1 from owner/x").
var mergeMessagePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^Merge branch '([^']+)'`),
	regexp.MustCompile(`^Merge pull request #\d+ from [^/\s]+/(\S+)`),
}

// mainSHATrailer matches the main-sha trailer the update-deploy-branch
// action stamps on last-deploy/* cherry-picks.
var mainSHATrailer = regexp.MustCompile(`(?mi)^main-sha:\s*([0-9a-f]{7,40})`)

// applyPush routes a push by ref: task-branch pushes attribute commits to
// the task, default-branch pushes append main_commits and advance landed
// tasks, last-deploy/* pushes map deploy shas back to main commits. Every
// other ref (feature branches, tags) is a no-op.
func (a *applier) applyPush(tx *sql.Tx, eventID int64, repo, defaultBranch string, body []byte) error {
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse push payload: %w", err)
	}
	branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
	if !ok {
		return nil // tag pushes etc.
	}
	// Report and carry on: a partial commit list still attributes what it
	// does contain, and dropping the delivery would lose that too. The
	// commits we never saw are recoverable only by reconciliation (spec 013).
	if p.truncated() {
		a.metrics.truncatedPushDelivery()
		a.log.Warn("push payload truncated; some commits were not attributed",
			"repo", repo, "ref", p.Ref, "before", p.Before, "after", p.After,
			"commits_received", len(p.Commits))
	}
	now := a.st.Now()

	if taskID := store.TaskIDFromRef(branch); taskID != "" {
		for _, c := range p.Commits {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: c.ID,
				Source: "branch_push", SeenAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	if defaultBranch != "" && branch == defaultBranch {
		return applyMainPush(tx, eventID, repo, now, p)
	}

	if env, ok := strings.CutPrefix(branch, "last-deploy/"); ok {
		if store.NormalizeEnvironment(env) == "" {
			return nil
		}
		for _, c := range p.Commits {
			m := mainSHATrailer.FindStringSubmatch(c.Message)
			if m == nil {
				continue
			}
			mainID, err := store.MainIDForSHA(tx, repo, m[1])
			if err != nil {
				return err
			}
			if mainID == nil {
				continue // main push not seen (yet); harmless
			}
			if err := store.MapDeploySHA(tx, repo, c.ID, *mainID); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// applyMainPush records the default-branch commits in order and resolves
// every task those commits land work for.
func applyMainPush(tx *sql.Tx, eventID int64, repo string, now time.Time, p pushPayload) error {
	affected := map[string]bool{}
	for _, c := range p.Commits {
		if _, err := store.AppendMainCommit(tx, repo, c.ID, now); err != nil {
			return err
		}
		// Attribute by message: merge-commit branch name or marker.
		ids, source := taskIDsFromMessage(c.Message)
		for _, taskID := range ids {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: c.ID,
				Source: source, SeenAt: now,
			}); err != nil {
				return err
			}
			affected[taskID] = true
		}
		// Attribute by prior branch-push tracking or PR correlation.
		ids, err := store.TaskIDsForSHA(tx, repo, c.ID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			affected[id] = true
		}
	}
	// ResolveDelivery is per-task and reads only repo-level frontiers it
	// never writes, so the outcome is order-independent; sorting only keeps
	// the resulting state_log ordering reproducible.
	for _, taskID := range slices.Sorted(maps.Keys(affected)) {
		if err := store.ResolveDelivery(tx, now, taskID, repo, eventID); err != nil {
			return err
		}
	}
	return nil
}

// taskIDsFromMessage extracts task ids from a commit message: the branch
// named in a merge-commit subject, plus every "Worklode-Task: <id>" trailer
// line. The label is fixed — the id after it carries its own project key
// (SW-3, ...), matched by store.TaskIDsFromBody. source is the attribution
// every id from this message is filed under: a trailer is the stronger
// signal, so its presence makes the whole message "marker".
func taskIDsFromMessage(msg string) (ids []string, source string) {
	for _, pat := range mergeMessagePatterns {
		if m := pat.FindStringSubmatch(msg); m != nil {
			if id := store.TaskIDFromRef(m[1]); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if trailers := store.TaskIDsFromBody(msg); len(trailers) > 0 {
		return append(ids, trailers...), "marker"
	}
	return ids, "merge_message"
}
