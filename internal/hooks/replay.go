// Engine 1 of lode task reconcile (spec 013): re-apply stored events whose apply
// never ran — GitHub *.ignored deliveries recorded before their repo was
// mapped, and catalog deliveries that matched no declaration when they
// arrived (029 §3.2, WL-256). Offline: the payload is intact in
// events.payload, so no GitHub call is needed. Re-running is harmless because
// the applies are order-safe, not merely idempotent: a replayed event may be
// older than facts that already landed, so the fact upserts are guarded to be
// non-regressing (see store.UpsertPR) and transitions are guarded on the
// from-state.

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

// Replay applies every unapplied event whose route now exists — a github
// delivery whose repo is mapped, a catalog delivery whose artifact is now
// declared — in arrival order, each in its own transaction. The apply
// receives the ORIGINAL event's id, so any resulting state_log transition or
// evidence row points at the real delivery — the timeline reads "applied
// late". Events still without a route (unmapped repo, undeclared artifact)
// are left untouched for a later run and counted in StillUnmapped. A single
// event whose apply fails is reported and skipped; a store failure aborts the
// run.
//
// One run covers at most a batch of candidates (opts.Limit, default
// defaultReplayBatch); a full batch is reported as truncated so the caller
// knows to run again.
func Replay(ctx context.Context, st *store.Store, opts ReplayOptions) (*model.ReplayResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultReplayBatch
	}
	evs, err := st.UnappliedEvents(ctx, store.UnappliedFilter{
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
	ca := &catalogApplier{st: st, log: log}

	for _, ev := range evs {
		if ev.Source == "catalog" {
			replayCatalog(ctx, st, ca, ev, opts, res, addErr)
			continue
		}
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

// replayCatalog re-applies one stored catalog delivery: it files the recorded
// fact against whatever declares the artifact now. A delivery still matching
// no declaration is left unapplied — counted in StillUnmapped, the same
// "nothing routes it yet" outcome an unmapped repo gets — so a declaration
// added later still finds it.
func replayCatalog(ctx context.Context, st *store.Store, ca *catalogApplier,
	ev store.Event, opts ReplayOptions, res *model.ReplayResult, addErr func(string, ...any)) {
	var applied catalogResult
	apply, err := ca.applyStored(ev.Payload, &applied)
	if err != nil {
		addErr("event %d: %v", ev.ID, err)
		opts.Metrics.replayOutcome("error")
		return
	}
	if opts.DryRun {
		// Whether a stored delivery routes is only knowable from inside the
		// apply's transaction (the declaration lookup runs there), so a dry
		// run reports what it would attempt, not what would route.
		res.Replayed++
		opts.Metrics.replayOutcome("dry_run")
		return
	}
	if txErr := st.Tx(ctx, func(tx *sql.Tx) error { return apply(tx, ev.ID) }); txErr != nil {
		addErr("event %d (%s): %v", ev.ID, ev.Type, txErr)
		opts.Metrics.replayOutcome("error")
		return
	}
	if !applied.Routed() {
		res.StillUnmapped++
		opts.Metrics.replayOutcome("still_unmapped")
		return
	}
	// Counted after the transaction committed, as the webhook path does it.
	opts.Metrics.catalogEvidenceFiled(applied)
	res.Replayed++
	opts.Metrics.replayOutcome("replayed")
}
