// Package hooks implements the webhook ingestion endpoints. Webhooks
// authenticate with HMAC signatures (not bearer tokens); every delivery is
// recorded as one idempotent event via store.RecordEvent, with the typed-table
// updates applied in the same transaction.
package hooks

import (
	"context"
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
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxGitHubBody caps webhook request bodies at 5 MiB (GitHub's own delivery
// payload limit is well under this); larger bodies get 413.
const maxGitHubBody = 5 << 20

// branchResolveTimeout bounds the GitHub App round trips (installation
// lookup, token mint, ref lookup) resolveReleaseCommitish makes before
// RecordEvent opens its transaction. GitHub's own webhook delivery budget is
// about 10s; this leaves room for the rest of the handler and still turns a
// hung upstream into an ordinary "error" outcome and fallback rather than a
// stalled delivery.
const branchResolveTimeout = 4 * time.Second

type githubHandler struct {
	st            *store.Store
	secret        string
	log           *slog.Logger
	onSkillPush   func(repo, branch string) bool
	resolveBranch func(ctx context.Context, repo, branch string) (string, error)
	metrics       *Metrics
}

// NewGitHubHandler returns the POST /hooks/github handler. It verifies the
// X-Hub-Signature-256 HMAC against secret, records each delivery exactly once
// (keyed by X-GitHub-Delivery), and applies the per-event effects. An empty
// secret makes the handler refuse all requests with 503 — a misconfigured
// server must not accept unauthenticated webhooks.
//
// onSkillPush, if non-nil, is consulted on every push event with the repo and
// target branch; a true result marks the event "push.skills" instead of
// running the normal apply path (see ServeHTTP). onSkillPush may be nil
// (tests); production always passes a closure that reports false when no
// skill sources are configured.
//
// appAuth, if non-nil, resolves a release's branch-name target_commitish to a
// commit sha (see resolveReleaseCommitish); nil disables resolution and the
// release falls back to main's head, as it did before this App integration.
func NewGitHubHandler(st *store.Store, secret string, log *slog.Logger, onSkillPush func(repo, branch string) bool, appAuth *githubauth.AppAuth, m *Metrics) http.Handler {
	var resolveBranch func(ctx context.Context, repo, branch string) (string, error)
	if appAuth != nil {
		resolveBranch = appAuth.BranchSHA
	}
	return newGitHubHandler(st, secret, log, onSkillPush, resolveBranch, m)
}

// newGitHubHandler is the common constructor: NewGitHubHandler derives
// resolveBranch from appAuth, and tests reach it directly (export_test.go) to
// stub branch resolution without a fake GitHub App server.
func newGitHubHandler(st *store.Store, secret string, log *slog.Logger, onSkillPush func(repo, branch string) bool, resolveBranch func(ctx context.Context, repo, branch string) (string, error), m *Metrics) *githubHandler {
	if log == nil {
		log = slog.Default()
	}
	return &githubHandler{st: st, secret: secret, log: log, onSkillPush: onSkillPush, resolveBranch: resolveBranch, metrics: m}
}

// envelope is the part of every GitHub webhook payload the router needs.
type envelope struct {
	Ref        string `json:"ref"`
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
	// Every exit records exactly one delivery; result stays "error" unless a
	// branch below sets it, so new early returns default to error, not silence.
	result := "error"
	defer func() {
		h.metrics.event("github", eventLabel(r.Header.Get("X-GitHub-Event")), result)
	}()

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
		result = "rejected"
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

	// A push to a configured skill source triggers a sync instead of the
	// normal apply path, whether or not its repo also maps to a project.
	skillPush := false
	if event == "push" && h.onSkillPush != nil {
		if branch, ok := strings.CutPrefix(env.Ref, "refs/heads/"); ok &&
			h.onSkillPush(env.Repository.FullName, branch) {
			skillPush = true
		}
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

	// A release's target_commitish is often a branch name. Resolving it needs
	// a GitHub API call, which must not happen inside the apply callback —
	// that runs in an open transaction. Resolve here and pass the sha in; a
	// failure (including a timeout, bounded well under GitHub's own webhook
	// delivery budget) degrades to the existing main-head fallback and never
	// fails the delivery.
	resolvedCommitish := ""
	if event == "release" && env.Action == "published" && !ignored {
		ctx, cancel := context.WithTimeout(r.Context(), branchResolveTimeout)
		resolvedCommitish = h.resolveReleaseCommitish(ctx, env.Repository.FullName, body)
		cancel()
	}

	var apply func(tx *sql.Tx, eventID int64) error
	switch {
	case skillPush:
		typ = "push.skills"
	case ignored:
		typ += ".ignored"
	default:
		apply = h.applyFunc(event, env, body, resolvedCommitish)
	}

	_, inserted, err := h.st.RecordEvent(r.Context(), "github", delivery, typ, body, apply)
	if err != nil {
		h.log.Error("github webhook: apply", "event", event, "delivery", delivery, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	switch {
	case !inserted:
		result = "ok"
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
	case skillPush:
		result = "ok"
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case ignored:
		result = "ignored"
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	default:
		result = "ok"
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handledEvents are the GitHub event names applyFunc routes. It is the single
// source of truth: applyFunc switches over these names, and the add-repo
// subscription check compares an installation's subscriptions against them, so
// adding a ninth event cannot leave the check behind.
var handledEvents = []string{
	"issues", "push", "pull_request", "deployment_status",
	"pull_request_review", "workflow_run", "release", "registry_package",
}

// HandledEvents returns the event names this handler routes.
func HandledEvents() []string {
	out := make([]string, len(handledEvents))
	copy(out, handledEvents)
	return out
}

// eventLabel bounds the metric's event label to the handled GitHub events;
// anything else (including an empty header) is "other".
func eventLabel(event string) string {
	if slices.Contains(handledEvents, event) {
		return event
	}
	return "other"
}

// applyFunc routes a mapped-repo event to its per-event apply callback.
// Unknown events (and unhandled actions) get a nil apply: the event is still
// recorded, with no typed-table effect.
func (h *githubHandler) applyFunc(event string, env envelope, body []byte, resolvedCommitish string) func(tx *sql.Tx, eventID int64) error {
	if !slices.Contains(handledEvents, event) {
		return nil
	}
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
			return h.applyRelease(tx, eventID, repo, body, resolvedCommitish)
		}
	case "registry_package":
		if env.Action != "published" && env.Action != "updated" {
			return nil
		}
		return func(tx *sql.Tx, _ int64) error {
			return h.applyRegistryPackage(tx, repo, body)
		}
	default:
		return nil
	}
}

// resolveReleaseCommitish turns a release's target_commitish into a commit
// sha when it names a branch. Returns "" when there is nothing to resolve, no
// App is configured, or the lookup fails — every one of which leaves
// applyRelease on its existing fallback.
func (h *githubHandler) resolveReleaseCommitish(ctx context.Context, repo string, body []byte) string {
	var p struct {
		Release struct {
			TargetCommitish string `json:"target_commitish"`
		} `json:"release"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	commitish := p.Release.TargetCommitish
	if commitish == "" || isCommitSHA(commitish) {
		return ""
	}
	if h.resolveBranch == nil {
		h.metrics.branchResolved("skipped")
		return ""
	}
	sha, err := h.resolveBranch(ctx, repo, commitish)
	switch {
	case err != nil:
		h.log.Warn("release target_commitish resolution failed",
			"repo", repo, "branch", commitish, "err", err)
		h.metrics.branchResolved("error")
		return ""
	case sha == "":
		h.metrics.branchResolved("unknown")
		return ""
	default:
		h.metrics.branchResolved("resolved")
		return sha
	}
}

// isCommitSHA reports whether s is a full 40-character hex commit sha, the
// form target_commitish takes when a release was cut from an explicit commit.
func isCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
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
	return store.UpsertIssue(tx, model.Issue{
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
		// The lease is deliberately left alone: it says a worktree is
		// occupied, which a merge does not change (spec 004 §3).
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

func (h *githubHandler) applyRelease(tx *sql.Tx, eventID int64, repo string, body []byte, resolvedCommitish string) error {
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
	now := h.st.Now()
	publishedAt := p.Release.PublishedAt
	if publishedAt.IsZero() {
		publishedAt = now
	}

	// Resolve the release frontier first, so the artifact can be attributed
	// to a real commit. Prefer the tagged commit itself, so a backport tag
	// covers only what it actually contains. A pre-resolved sha (ServeHTTP
	// turned a branch name into a commit) takes precedence; otherwise use the
	// payload's own commitish, which may itself already be a sha. Neither may
	// resolve, in which case the release covers main's head as of this
	// webhook's arrival, which is right for release-on-merge.
	commitish := resolvedCommitish
	if commitish == "" {
		commitish = p.Release.TargetCommitish
	}
	frontier, err := store.MainIDForSHA(tx, repo, commitish)
	if err != nil {
		return err
	}
	if frontier == nil {
		if frontier, err = store.LatestMainID(tx, repo); err != nil {
			return err
		}
	}

	// The artifact's source_sha must be a commit, never a branch name: a
	// branch name can never match a Flux revision, so the artifact would be
	// permanently uncorrelatable. A resolved commit is used directly even when
	// it has not landed on main — a release branch's tip commonly hasn't, and a
	// backport tag's commit may predate the repo's onboarding. That commit is
	// either the one ServeHTTP resolved from a branch name, or the payload's
	// own commitish when it already was a sha. Otherwise the sha comes from the
	// frontier's main commit, or is left empty when neither resolved.
	sourceSHA := resolvedCommitish
	if sourceSHA == "" && isCommitSHA(p.Release.TargetCommitish) {
		sourceSHA = p.Release.TargetCommitish
	}
	if sourceSHA == "" && frontier != nil {
		if sourceSHA, err = store.MainSHAForID(tx, *frontier); err != nil {
			return err
		}
	}
	if _, err := store.CreateArtifact(tx, store.Artifact{
		Kind:      "git_tag",
		Name:      repo,
		Version:   p.Release.TagName,
		Repo:      repo,
		SourceSHA: sourceSHA,
		BuiltAt:   p.Release.PublishedAt,
	}); err != nil {
		return err
	}

	if frontier == nil {
		return nil
	}
	if err := store.SetReleaseFrontier(tx, repo, p.Release.TagName, *frontier, publishedAt); err != nil {
		return err
	}
	tasks, err := store.TasksBelowFrontier(tx, repo, *frontier)
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

// applyRegistryPackage mints a docker_image artifact from a container push.
// The artifact is keyed by (image name, tag) so FindArtifactByImage and the
// Flux digest correlation can both reach it; a version with no container tag
// has no key to store under and is recorded as an event only.
func (h *githubHandler) applyRegistryPackage(tx *sql.Tx, repo string, body []byte) error {
	var p struct {
		RegistryPackage struct {
			Name           string `json:"name"`
			PackageType    string `json:"package_type"`
			PackageVersion struct {
				Version           string    `json:"version"`
				TargetCommitish   string    `json:"target_commitish"`
				CreatedAt         time.Time `json:"created_at"`
				PackageURL        string    `json:"package_url"`
				ContainerMetadata struct {
					Tag struct {
						Name   string `json:"name"`
						Digest string `json:"digest"`
					} `json:"tag"`
				} `json:"container_metadata"`
			} `json:"package_version"`
		} `json:"registry_package"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse registry_package payload: %w", err)
	}
	pkg := p.RegistryPackage
	if !strings.EqualFold(pkg.PackageType, "CONTAINER") &&
		!strings.EqualFold(pkg.PackageType, "docker") {
		return nil
	}
	tag := pkg.PackageVersion.ContainerMetadata.Tag.Name
	if tag == "" {
		// An untagged push (digest-only) has no artifact key. Recording the
		// event is the whole effect; a later tagged push carries the same
		// digest.
		return nil
	}

	// The image name must match what a Kubernetes image reference says, so
	// splitImage's (name, tag) split lines up: prefer package_url, which is
	// the registry-qualified name. Without it, reconstruct the GHCR name —
	// registry_package container deliveries come from GHCR — because the bare
	// package name never matches a "ghcr.io/owner/name" image reference.
	name := pkg.PackageVersion.PackageURL
	if name == "" {
		owner, _, _ := strings.Cut(repo, "/")
		name = "ghcr.io/" + owner + "/" + pkg.Name
	}
	digest := pkg.PackageVersion.ContainerMetadata.Tag.Digest
	if digest == "" {
		digest = pkg.PackageVersion.Version
	}
	var digestPtr *string
	if strings.HasPrefix(digest, "sha256:") {
		digestPtr = &digest
	}
	builtAt := pkg.PackageVersion.CreatedAt
	if builtAt.IsZero() {
		builtAt = h.st.Now()
	}

	_, err := store.CreateArtifact(tx, store.Artifact{
		Kind:      "docker_image",
		Name:      name,
		Version:   tag,
		Digest:    digestPtr,
		Repo:      repo,
		SourceSHA: pkg.PackageVersion.TargetCommitish,
		BuiltAt:   builtAt,
	})
	return err
}
