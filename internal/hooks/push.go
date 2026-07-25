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
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
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
func (h *githubHandler) applyPush(tx *sql.Tx, eventID int64, repo, defaultBranch string, body []byte) error {
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse push payload: %w", err)
	}
	branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
	if !ok {
		return nil // tag pushes etc.
	}
	now := h.st.Now()

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
		for _, taskID := range taskIDsFromMessage(c.Message) {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: c.ID,
				Source: sourceForMessage(c.Message), SeenAt: now,
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
// named in a merge-commit subject, plus any "WL-Task: <id>" marker line.
// "WL-Task" is the fixed marker label — the id after it carries its own
// project key (SW-3, ...), matched by store.TaskIDFromBody.
func taskIDsFromMessage(msg string) []string {
	var out []string
	for _, pat := range mergeMessagePatterns {
		if m := pat.FindStringSubmatch(msg); m != nil {
			if id := store.TaskIDFromRef(m[1]); id != "" {
				out = append(out, id)
			}
		}
	}
	if id := store.TaskIDFromBody(msg); id != "" {
		out = append(out, id)
	}
	return out
}

func sourceForMessage(msg string) string {
	if store.TaskIDFromBody(msg) != "" {
		return "marker"
	}
	return "merge_message"
}
