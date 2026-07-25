// Package hooks implements the webhook ingestion endpoints. Webhooks
// authenticate with HMAC signatures (not bearer tokens); every delivery is
// recorded as one idempotent event via store.RecordEvent, with the typed-table
// updates applied in the same transaction.
package hooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxGitHubBody caps webhook request bodies at 5 MiB (GitHub's own delivery
// payload limit is well under this); larger bodies get 413.
const maxGitHubBody = 5 << 20

type githubHandler struct {
	st     *store.Store
	secret string
	log    *slog.Logger
}

// NewGitHubHandler returns the POST /hooks/github handler. It verifies the
// X-Hub-Signature-256 HMAC against secret, records each delivery exactly once
// (keyed by X-GitHub-Delivery), and applies the per-event effects. An empty
// secret makes the handler refuse all requests with 503 — a misconfigured
// server must not accept unauthenticated webhooks.
func NewGitHubHandler(st *store.Store, secret string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return &githubHandler{st: st, secret: secret, log: log}
}

// envelope is the part of every GitHub webhook payload the router needs.
type envelope struct {
	Action     string `json:"action"`
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("encode webhook response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// validSignature reports whether header is a well-formed
// "sha256=<hex>" HMAC-SHA256 of body under secret (constant-time compare).
func validSignature(secret string, body []byte, header string) bool {
	sigHex, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

func (h *githubHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.secret == "" {
		writeErr(w, http.StatusServiceUnavailable, "github webhook secret not configured")
		return
	}

	// The signature covers the exact request bytes: read the raw body first
	// (capped at maxGitHubBody), verify, and only then parse.
	r.Body = http.MaxBytesReader(w, r.Body, maxGitHubBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "read body")
		return
	}
	if !validSignature(h.secret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")
	if event == "" || delivery == "" {
		writeErr(w, http.StatusBadRequest, "missing X-GitHub-Event or X-GitHub-Delivery header")
		return
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	typ := event
	if env.Action != "" {
		typ = event + "." + env.Action
	}

	// Resolve the repo → project mapping before recording, so an unmapped
	// repo's event is stored with a ".ignored" type and an empty apply. Only
	// the existence of the mapping matters here; the handlers work from the
	// repo name.
	ignored := false
	if repo := env.Repository.FullName; repo != "" {
		_, err = h.st.ProjectForRepo(r.Context(), repo)
		if errors.Is(err, store.ErrNotFound) {
			ignored = true
		} else if err != nil {
			h.log.Error("github webhook: project lookup", "repo", repo, "err", err)
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	var apply func(tx *sql.Tx, eventID int64) error
	if ignored {
		typ += ".ignored"
	} else {
		apply = h.applyFunc(event, env, body)
	}

	_, inserted, err := h.st.RecordEvent(r.Context(), "github", delivery, typ, body, apply)
	if err != nil {
		h.log.Error("github webhook: apply", "event", event, "delivery", delivery, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	switch {
	case !inserted:
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
	case ignored:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// applyFunc routes a mapped-repo event to its per-event apply callback.
// Unknown events (and unhandled actions) get a nil apply: the event is still
// recorded, with no typed-table effect.
func (h *githubHandler) applyFunc(event string, env envelope, body []byte) func(tx *sql.Tx, eventID int64) error {
	repo := env.Repository.FullName
	switch event {
	case "issues":
		return func(tx *sql.Tx, _ int64) error {
			return applyIssue(tx, repo, body)
		}
	case "push":
		return func(tx *sql.Tx, eventID int64) error {
			return h.applyPush(tx, eventID, repo, env.Repository.DefaultBranch, body)
		}
	case "pull_request":
		return func(tx *sql.Tx, eventID int64) error {
			return h.applyPullRequest(tx, eventID, repo, env.Action, body)
		}
	case "deployment_status":
		return func(tx *sql.Tx, eventID int64) error {
			return h.applyDeploymentStatus(tx, eventID, repo, body)
		}
	case "pull_request_review":
		if env.Action != "submitted" {
			return nil
		}
		return func(tx *sql.Tx, _ int64) error {
			return h.applyReview(tx, repo, body)
		}
	case "workflow_run":
		return func(tx *sql.Tx, _ int64) error {
			return h.applyWorkflowRun(tx, repo, body)
		}
	case "release":
		if env.Action != "published" {
			return nil
		}
		return func(tx *sql.Tx, eventID int64) error {
			return h.applyRelease(tx, eventID, repo, body)
		}
	default:
		return nil
	}
}

func applyIssue(tx *sql.Tx, repo string, body []byte) error {
	var p struct {
		Issue struct {
			Number  int64  `json:"number"`
			Title   string `json:"title"`
			State   string `json:"state"`
			HTMLURL string `json:"html_url"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse issues payload: %w", err)
	}
	return store.UpsertIssue(tx, store.Issue{
		Repo:   repo,
		Number: p.Issue.Number,
		Title:  p.Issue.Title,
		State:  p.Issue.State,
		URL:    p.Issue.HTMLURL,
	})
}

func (h *githubHandler) applyPullRequest(tx *sql.Tx, eventID int64, repo, action string, body []byte) error {
	var p struct {
		PullRequest struct {
			Number         int64      `json:"number"`
			Title          string     `json:"title"`
			State          string     `json:"state"`
			Merged         bool       `json:"merged"`
			Body           string     `json:"body"`
			HTMLURL        string     `json:"html_url"`
			CreatedAt      time.Time  `json:"created_at"`
			MergedAt       *time.Time `json:"merged_at"`
			MergeCommitSHA *string    `json:"merge_commit_sha"`
			Head           struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse pull_request payload: %w", err)
	}
	gh := p.PullRequest
	now := h.st.Now()

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
	var mergedAt *time.Time
	if gh.MergedAt != nil && !gh.MergedAt.IsZero() {
		mergedAt = gh.MergedAt
	}

	pr, err := store.UpsertPR(tx, store.PullRequest{
		Repo:     repo,
		Number:   gh.Number,
		Title:    gh.Title,
		State:    state,
		HeadRef:  gh.Head.Ref,
		HeadSHA:  gh.Head.SHA,
		MergeSHA: gh.MergeCommitSHA,
		URL:      gh.HTMLURL,
		OpenedAt: openedAt,
		MergedAt: mergedAt,
	}, gh.Body)
	if err != nil {
		return err
	}
	if pr.TaskID == nil {
		return nil
	}
	taskID := *pr.TaskID

	// in_review is not a delivery milestone, so it transitions here; every
	// delivery state is decided by store.ResolveDelivery from recorded facts.
	switch {
	case action == "opened" || action == "ready_for_review":
		// A PR on a claimed task means the work went to review. Any other
		// task state (ready, merged, ...) is left alone — a correlation must
		// never fail the delivery.
		taskState, err := store.TaskState(tx, taskID)
		if err != nil {
			return err
		}
		if taskState == "in_progress" {
			return store.Transition(tx, now, taskID, "in_progress", "in_review", eventID)
		}
	case action == "closed" && gh.Merged:
		if err := store.CloseActiveLease(tx, now, taskID); err != nil {
			return err
		}
		// Record the PR's shas as task commits; the resolver advances the
		// task once (and if) they appear on main via a push event.
		if gh.Head.SHA != "" {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: gh.Head.SHA, Source: "pr", SeenAt: now,
			}); err != nil {
				return err
			}
		}
		if gh.MergeCommitSHA != nil && *gh.MergeCommitSHA != "" {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: *gh.MergeCommitSHA, Source: "pr", SeenAt: now,
			}); err != nil {
				return err
			}
		}
		return store.ResolveDelivery(tx, now, taskID, repo, eventID)
	}
	return nil
}

func (h *githubHandler) applyReview(tx *sql.Tx, repo string, body []byte) error {
	var p struct {
		PullRequest struct {
			Number int64 `json:"number"`
		} `json:"pull_request"`
		Review struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submitted_at"`
		} `json:"review"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse pull_request_review payload: %w", err)
	}
	state := strings.ToLower(p.Review.State)
	switch state {
	case "approved", "changes_requested", "commented":
	default:
		state = "commented"
	}
	submittedAt := p.Review.SubmittedAt
	if submittedAt.IsZero() {
		submittedAt = h.st.Now()
	}
	return store.UpsertReview(tx, store.Review{
		Repo:        repo,
		PRNumber:    p.PullRequest.Number,
		Reviewer:    p.Review.User.Login,
		State:       state,
		SubmittedAt: submittedAt,
	})
}

func (h *githubHandler) applyWorkflowRun(tx *sql.Tx, repo string, body []byte) error {
	var p struct {
		WorkflowRun struct {
			Name         string    `json:"name"`
			HeadSHA      string    `json:"head_sha"`
			Status       string    `json:"status"`
			Conclusion   *string   `json:"conclusion"`
			HTMLURL      string    `json:"html_url"`
			RunStartedAt time.Time `json:"run_started_at"`
			UpdatedAt    time.Time `json:"updated_at"`
		} `json:"workflow_run"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse workflow_run payload: %w", err)
	}
	run := p.WorkflowRun
	startedAt := run.RunStartedAt
	if startedAt.IsZero() {
		startedAt = h.st.Now()
	}
	var completedAt *time.Time
	if run.Status == "completed" && !run.UpdatedAt.IsZero() {
		completedAt = &run.UpdatedAt
	}
	return store.UpsertCIRun(tx, store.CIRun{
		Repo:        repo,
		HeadSHA:     run.HeadSHA,
		Workflow:    run.Name,
		Status:      run.Status,
		Conclusion:  run.Conclusion,
		URL:         run.HTMLURL,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	})
}

func (h *githubHandler) applyRelease(tx *sql.Tx, eventID int64, repo string, body []byte) error {
	var p struct {
		Release struct {
			TagName         string    `json:"tag_name"`
			TargetCommitish string    `json:"target_commitish"`
			PublishedAt     time.Time `json:"published_at"`
		} `json:"release"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse release payload: %w", err)
	}
	if _, err := store.CreateArtifact(tx, store.Artifact{
		Kind:      "git_tag",
		Name:      repo,
		Version:   p.Release.TagName,
		Repo:      repo,
		SourceSHA: p.Release.TargetCommitish,
		BuiltAt:   p.Release.PublishedAt,
	}); err != nil {
		return err
	}

	now := h.st.Now()
	publishedAt := p.Release.PublishedAt
	if publishedAt.IsZero() {
		publishedAt = now
	}
	// Record the release frontier: releases tag main's head, so the newest
	// main commit we've seen is what the release covers.
	latest, err := store.LatestMainID(tx, repo)
	if err != nil {
		return err
	}
	if latest == nil {
		return nil
	}
	if err := store.SetReleaseFrontier(tx, repo, p.Release.TagName, *latest, publishedAt); err != nil {
		return err
	}
	tasks, err := store.TasksBelowFrontier(tx, repo, *latest)
	if err != nil {
		return err
	}
	for _, taskID := range tasks {
		if err := store.ResolveDelivery(tx, now, taskID, repo, eventID); err != nil {
			return err
		}
	}
	return nil
}
