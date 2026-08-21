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

// defaultReplayBatch caps how many candidate events one run reads. Every
// candidate is materialised with its whole delivery payload (up to
// maxGitHubBody each), and the unscoped org-wide run is the scheduled case
// (spec 013 §2), so an unbounded read is a backlog-sized allocation. A run
// that fills its batch says so in ReplayResult.Truncated; re-running drains
// the rest, because an applied event leaves the candidate set.
const defaultReplayBatch = 500

// maxReplayErrors caps the reported error list. It is the reconcile
// response body's "replay" section, so a backlog where every apply fails
// must not turn into a megabyte of JSON; the overflow is counted in
// ReplayResult.ErrorsOmitted instead.
const maxReplayErrors = 100

// ReplayOptions bounds the candidate set and supplies the wiring the applies
// need. Zero values disable each bound.
type ReplayOptions struct {
	Repo   string
	Since  *time.Time
	DryRun bool

	// Limit caps the candidate batch; 0 means defaultReplayBatch. There is
	// no unbounded setting: the whole point of the batch is that no caller
	// reads an unbounded backlog into memory.
	Limit int

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
//
// One run covers at most a batch of candidates (opts.Limit, default
// defaultReplayBatch); a full batch is reported as truncated so the caller
// knows to run again.
func Replay(ctx context.Context, st *store.Store, opts ReplayOptions) (*model.ReplayResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultReplayBatch
	}
	evs, err := st.UnappliedGitHubEvents(ctx, store.UnappliedFilter{
		Repo: opts.Repo, Since: opts.Since, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	res := &model.ReplayResult{DryRun: opts.DryRun, Candidates: len(evs), Truncated: len(evs) == limit}
	addErr := func(format string, args ...any) {
		if len(res.Errors) >= maxReplayErrors {
			res.ErrorsOmitted++
			return
		}
		res.Errors = append(res.Errors, fmt.Sprintf(format, args...))
	}
	a := &applier{st: st, log: log, resolveBranch: opts.ResolveBranch, metrics: opts.Metrics}

	for _, ev := range evs {
		var env envelope
		if err := json.Unmarshal(ev.Payload, &env); err != nil {
			addErr("event %d: parse payload: %v", ev.ID, err)
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
			addErr("event %d (%s): %v", ev.ID, ev.Type, txErr)
			opts.Metrics.replayOutcome("error")
			continue
		}
		res.Replayed++
		opts.Metrics.replayOutcome("replayed")
	}
	return res, nil
}
