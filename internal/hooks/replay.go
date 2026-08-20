// Engine 1 of lode reconcile (spec 013): re-apply stored events whose apply
// never ran — *.ignored deliveries recorded before their repo was mapped.
// Offline: the payload is intact in events.payload, so no GitHub call is
// needed. Re-running is harmless because the applies are order-safe, not
// merely idempotent: a replayed event may be older than facts that already
// landed, so the fact upserts are guarded to be non-regressing (see
// store.UpsertPR) and transitions are guarded on the from-state.

package hooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ReplayOptions bounds the candidate set and supplies the wiring the applies
// need. Zero values disable each bound.
type ReplayOptions struct {
	Repo   string
	Since  *time.Time
	DryRun bool

	// Log, ResolveBranch and Metrics mirror what the webhook handler gives
	// its applier; all are optional. A nil Log falls back to slog.Default().
	// A nil ResolveBranch leaves a replayed release on applyRelease's
	// existing fallback — the same degradation a server with no GitHub App
	// configured already has. A nil Metrics records nothing.
	Log           *slog.Logger
	ResolveBranch func(ctx context.Context, repo, branch string) (string, error)
	Metrics       *Metrics
}

// Replay applies every unapplied github event whose repo is now mapped, in
// arrival order, each in its own transaction. The apply receives the
// ORIGINAL event's id, so any resulting state_log transition points at the
// real GitHub event — the timeline reads "applied late". Events whose repo
// is still unmapped are left untouched for a later run. A single event whose
// apply fails is reported and skipped; a store failure aborts the run.
func Replay(ctx context.Context, st *store.Store, opts ReplayOptions) (*model.ReplayResult, error) {
	evs, err := st.UnappliedGitHubEvents(ctx, store.UnappliedFilter{Repo: opts.Repo, Since: opts.Since})
	if err != nil {
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	res := &model.ReplayResult{DryRun: opts.DryRun, Candidates: len(evs)}
	a := &applier{st: st, log: log, resolveBranch: opts.ResolveBranch, metrics: opts.Metrics}

	for _, ev := range evs {
		var env envelope
		if err := json.Unmarshal(ev.Payload, &env); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("event %d: parse payload: %v", ev.ID, err))
			opts.Metrics.replayOutcome("error")
			continue
		}
		repo := env.Repository.FullName
		if repo == "" {
			// No repository in the payload: nothing to map it by; leave it.
			res.StillUnmapped++
			opts.Metrics.replayOutcome("still_unmapped")
			continue
		}
		if _, err := st.ProjectForRepo(ctx, repo); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				res.StillUnmapped++
				opts.Metrics.replayOutcome("still_unmapped")
				continue
			}
			return nil, err
		}
		if opts.DryRun {
			res.Replayed++
			opts.Metrics.replayOutcome("dry_run")
			continue
		}

		apply := markApplied(st, a.applyForType(ctx, ev.Type, env, ev.Payload))
		txErr := st.Tx(ctx, func(tx *sql.Tx) error { return apply(tx, ev.ID) })
		if txErr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("event %d (%s): %v", ev.ID, ev.Type, txErr))
			opts.Metrics.replayOutcome("error")
			continue
		}
		res.Replayed++
		opts.Metrics.replayOutcome("replayed")
	}
	return res, nil
}
