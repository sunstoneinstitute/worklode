// Inbox import (spec 020): backfill a repo's existing GitHub issues and pull
// requests through the same store functions the webhook handler uses.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// importMaxPages caps each list at 20 pages of 100. Beyond it the response
// reports truncation and the caller narrows with --since, rather than the
// request running unbounded.
const importMaxPages = 20

// importTimeout bounds the GitHub round trips. They happen before the
// transaction opens, so a slow GitHub never holds a database lock.
const importTimeout = 60 * time.Second

var validImportStates = map[string]bool{"open": true, "closed": true, "all": true}

type importRequest struct {
	Repo       string     `json:"repo"`
	State      string     `json:"state"`
	IncludePRs bool       `json:"include_prs"`
	Since      *time.Time `json:"since"`
	DryRun     bool       `json:"dry_run"`
}

// importInbox handles POST /api/v1/inbox/import. It fetches outside any
// transaction, then applies every upsert inside one RecordEvent, so an import
// is one event and one transaction — and re-running it is safe, because
// UpsertIssue and UpsertPR never touch triage or correlation state that
// triage already set.
func (s *server) importInbox(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if s.appAuth == nil {
		writeErr(w, http.StatusServiceUnavailable, "github app not configured")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	if req.State == "" {
		req.State = "open"
	}
	if !validImportStates[req.State] {
		writeErr(w, http.StatusUnprocessableEntity, `invalid state: must be open, closed, or all`)
		return
	}
	if _, err := s.st.ProjectForRepo(r.Context(), req.Repo); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	fctx, cancel := context.WithTimeout(r.Context(), importTimeout)
	defer cancel()

	var since time.Time
	if req.Since != nil {
		since = *req.Since
	}
	issues, issuesTruncated, err := s.appAuth.ListIssues(fctx, req.Repo, req.State, since, importMaxPages)
	if err != nil {
		s.log.Warn("import: list issues", "repo", req.Repo, "err", err)
		writeErr(w, http.StatusBadGateway, "github list issues failed")
		return
	}
	var pulls []githubauth.PullRequest
	var prsTruncated bool
	if req.IncludePRs {
		prs, truncated, err := s.appAuth.ListPulls(fctx, req.Repo, req.State, importMaxPages)
		if err != nil {
			s.log.Warn("import: list pulls", "repo", req.Repo, "err", err)
			writeErr(w, http.StatusBadGateway, "github list pulls failed")
			return
		}
		prsTruncated = truncated
		// The pulls endpoint has no since parameter, so filter here. This can
		// only narrow the pages already fetched — it cannot reach further
		// pages, which is why prsTruncated (not this filtering) is what gets
		// reported.
		for _, pr := range prs {
			if since.IsZero() || !pr.UpdatedAt.Before(since) {
				pulls = append(pulls, pr)
			}
		}
	}

	resp := model.ImportResult{Repo: req.Repo, DryRun: req.DryRun}
	resp.Issues.Truncated = issuesTruncated
	resp.PRs.Truncated = prsTruncated
	resp.Truncated = issuesTruncated || prsTruncated
	if issuesTruncated {
		newest := newestIssueUpdatedAt(issues)
		if !newest.IsZero() {
			resp.NewestUpdatedAt = &newest
		}
	}

	// Safe to mutate resp here: RecordEvent runs apply exactly once, no retry.
	count := func(tx *sql.Tx) error {
		haveIssues, err := store.ExistingIssueNumbers(tx, req.Repo)
		if err != nil {
			return err
		}
		havePRs, err := store.ExistingPRNumbers(tx, req.Repo)
		if err != nil {
			return err
		}
		for _, is := range issues {
			if haveIssues[is.Number] {
				resp.Issues.Updated++
			} else {
				resp.Issues.New++
			}
		}
		for _, pr := range pulls {
			if havePRs[pr.Number] {
				resp.PRs.Updated++
			} else {
				resp.PRs.New++
			}
		}
		return nil
	}

	if req.DryRun {
		// Counting needs a transaction but no event: a dry run must leave the
		// events table untouched too, not just the typed tables.
		if err := s.st.Tx(r.Context(), count); err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "inbox.imported", payload,
		func(tx *sql.Tx, _ int64) error {
			if err := count(tx); err != nil {
				return err
			}
			for _, is := range issues {
				if err := store.UpsertIssue(tx, model.Issue{
					Repo:   req.Repo,
					Number: is.Number,
					Title:  is.Title,
					State:  is.State,
					URL:    is.HTMLURL,
				}); err != nil {
					return err
				}
			}
			for _, pr := range pulls {
				// Inventory only: no Transition, CloseActiveLease,
				// InsertTaskCommit, or ResolveDelivery. Those encode "this just
				// happened" and would rewrite lifecycle and roll-up state from
				// history. UpsertPR still correlates by head_ref/body.
				state := "open"
				if pr.State == "closed" {
					state = "closed"
					if pr.Merged {
						state = "merged"
					}
				}
				// list.go leaves MergedAt raw (it's a transport type); match
				// the webhook path's guard (internal/hooks/github.go) so a
				// closed-unmerged PR never gets a non-nil merged_at.
				var mergedAt *time.Time
				if pr.MergedAt != nil && !pr.MergedAt.IsZero() {
					mergedAt = pr.MergedAt
				}
				if _, err := store.UpsertPR(tx, store.PullRequest{
					Repo:     req.Repo,
					Number:   pr.Number,
					Title:    pr.Title,
					State:    state,
					HeadRef:  pr.HeadRef,
					HeadSHA:  pr.HeadSHA,
					MergeSHA: pr.MergeCommitSHA,
					URL:      pr.HTMLURL,
					OpenedAt: pr.CreatedAt,
					MergedAt: mergedAt,
				}, pr.Body); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// newestIssueUpdatedAt returns the maximum UpdatedAt across issues, or the
// zero Time if empty. Issues-only: only /issues accepts since server-side
// (see listQuery), so a cursor built from pulls too would point resume
// requests at a stream that cannot skip ahead, silently dropping issues
// between the two streams' timestamps.
func newestIssueUpdatedAt(issues []githubauth.Issue) time.Time {
	var newest time.Time
	for _, is := range issues {
		if is.UpdatedAt.After(newest) {
			newest = is.UpdatedAt
		}
	}
	return newest
}
