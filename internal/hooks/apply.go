// The transport-independent apply router. The webhook handler and the
// replayer (replay.go) both route events through an applier, so a replayed
// event produces exactly the typed-table effect a live delivery would have.

package hooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// applier routes a mapped-repo event to its per-event apply callback,
// independent of how the event arrived. It carries everything the apply
// methods need that used to come from the HTTP handler: the store, a logger,
// the optional GitHub branch resolver, and the webhook metrics.
type applier struct {
	st            *store.Store
	log           *slog.Logger
	resolveBranch func(ctx context.Context, repo, branch string) (string, error)
	metrics       *Metrics
}

// applyForType routes a *stored* event type the way applyFunc routes a live
// delivery. The GitHub event name is the type's first dot-separated segment
// — event names themselves never contain dots — and a trailing ".ignored"
// only recorded the unmapped-at-arrival classification, so it is stripped
// first. Release target_commitish resolution runs here, before the apply's
// transaction opens, exactly as ServeHTTP does it for a live delivery.
func (a *applier) applyForType(ctx context.Context, typ string, env envelope, body []byte) func(tx *sql.Tx, eventID int64) error {
	// A skill push was handled by the sync, not by an apply; routing it here
	// would run applyPush, which the skill path deliberately skips. Marked
	// applied at ingestion today, but a row written by an older binary during
	// a rolling deploy can still reach the replayer.
	if typ == "push.skills" {
		return nil
	}
	base := strings.TrimSuffix(typ, ".ignored")
	event, _, _ := strings.Cut(base, ".")
	resolvedCommitish := ""
	if event == "release" && env.Action == "published" {
		rctx, cancel := context.WithTimeout(ctx, branchResolveTimeout)
		resolvedCommitish = a.resolveReleaseCommitish(rctx, env.Repository.FullName, body)
		cancel()
	}
	return a.applyFunc(event, env, body, resolvedCommitish)
}

// applyFunc routes a mapped-repo event to its per-event apply callback.
// Unknown events (and unhandled actions) get a nil apply: the event is still
// recorded, with no typed-table effect.
func (a *applier) applyFunc(event string, env envelope, body []byte, resolvedCommitish string) func(tx *sql.Tx, eventID int64) error {
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
			return a.applyPush(tx, eventID, repo, env.Repository.DefaultBranch, body)
		}
	case "pull_request":
		return func(tx *sql.Tx, eventID int64) error {
			return a.applyPullRequest(tx, eventID, repo, env.Action, body)
		}
	case "deployment_status":
		return func(tx *sql.Tx, eventID int64) error {
			return a.applyDeploymentStatus(tx, eventID, repo, body)
		}
	case "pull_request_review":
		if env.Action != "submitted" {
			return nil
		}
		return func(tx *sql.Tx, _ int64) error {
			return a.applyReview(tx, repo, body)
		}
	case "workflow_run":
		return func(tx *sql.Tx, _ int64) error {
			return a.applyWorkflowRun(tx, repo, body)
		}
	case "release":
		if env.Action != "published" {
			return nil
		}
		return func(tx *sql.Tx, eventID int64) error {
			return a.applyRelease(tx, eventID, repo, body, resolvedCommitish)
		}
	case "registry_package":
		if env.Action != "published" && env.Action != "updated" {
			return nil
		}
		return func(tx *sql.Tx, _ int64) error {
			return a.applyRegistryPackage(tx, repo, body)
		}
	default:
		return nil
	}
}

// resolveReleaseCommitish turns a release's target_commitish into a commit
// sha when it names a branch. Returns "" when there is nothing to resolve, no
// App is configured, or the lookup fails — every one of which leaves
// applyRelease on its existing fallback.
func (a *applier) resolveReleaseCommitish(ctx context.Context, repo string, body []byte) string {
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
	if a.resolveBranch == nil {
		a.metrics.branchResolved("skipped")
		return ""
	}
	sha, err := a.resolveBranch(ctx, repo, commitish)
	switch {
	case err != nil:
		a.log.Warn("release target_commitish resolution failed",
			"repo", repo, "branch", commitish, "err", err)
		a.metrics.branchResolved("error")
		return ""
	case sha == "":
		a.metrics.branchResolved("unknown")
		return ""
	default:
		a.metrics.branchResolved("resolved")
		return sha
	}
}

// markApplied wraps an apply (possibly nil) so the event's applied_at is set
// in the same transaction, by the webhook path and replayer alike. Only
// *.ignored deliveries are left unmarked: they are the replay candidates.
func markApplied(st *store.Store, inner func(tx *sql.Tx, eventID int64) error) func(tx *sql.Tx, eventID int64) error {
	return func(tx *sql.Tx, eventID int64) error {
		if inner != nil {
			if err := inner(tx, eventID); err != nil {
				return err
			}
		}
		return store.MarkEventApplied(tx, eventID, st.Now())
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
