package eventbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Outcome classifies one handled event for metrics (spec 025 §15.7).
type Outcome string

const (
	OutcomeApplied    Outcome = "applied"
	OutcomeSuppressed Outcome = "suppressed"
)

// Handler processes one event. Returning an error stops the batch: the
// prefix already handled is acked, the failed event is redelivered on the
// next poll, and in-order delivery means it blocks everything behind it —
// deliberate (at-least-once, no DLQ; 025 §22 keeps retention/partitioning
// out of scope). The error surfaces in the outcome="error" counter and in
// the lag gauge.
type Handler func(ctx context.Context, ev store.Event) (Outcome, error)

// Options configures Run.
type Options struct {
	Store     *store.Store
	Name      string // subscriber name; the row must exist (EnsureEventSubscriber)
	Handler   Handler
	Poll      time.Duration // default 1s (025 §15.1: polling, deliberately no LISTEN/NOTIFY)
	LockRetry time.Duration // default 15s: how often a standby retries the lock
	BatchSize int           // default 100
	Metrics   *Metrics      // nil-safe
	Log       *slog.Logger  // nil = slog.Default()
}

const (
	defaultPoll      = time.Second
	defaultLockRetry = 15 * time.Second
	defaultBatchSize = 100
	// releaseTimeout bounds the unlock round trip on shutdown, which runs on
	// a context detached from the cancelled one.
	releaseTimeout = 5 * time.Second
)

// Run consumes until ctx is cancelled. Lifecycle per iteration:
//  1. no lock → TryLockSubscriber; on failure sleep LockRetry.
//  2. on acquiring the lock: ResetEventRead (redeliver read-but-unacked).
//  3. ReadEventBatch; empty → sleep Poll.
//  4. handle events in order; on first error ack the successful prefix,
//     rewind the read offset onto it (ResetEventRead) so the failed event
//     comes back, count outcome=error, sleep Poll (head-of-line retry).
//  5. all applied → AckEvents(last id).
//
// Any store error on the lock connection path drops the lock (Release)
// and returns to 1 — a broken session must not be treated as held.
// On ctx.Done the lock is Released.
//
// An unknown subscriber name is a wiring bug, not a transient fault: it
// ends the loop with ErrNotFound rather than spinning on a row that will
// never appear.
func Run(ctx context.Context, o Options) error {
	if o.Store == nil {
		return fmt.Errorf("event loop: Store is required")
	}
	if o.Name == "" {
		return fmt.Errorf("event loop: Name is required")
	}
	if o.Handler == nil {
		return fmt.Errorf("event loop %s: Handler is required", o.Name)
	}
	poll := o.Poll
	if poll <= 0 {
		poll = defaultPoll
	}
	lockRetry := o.LockRetry
	if lockRetry <= 0 {
		lockRetry = defaultLockRetry
	}
	batchSize := o.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("subscriber", o.Name)

	// One lock handle, released exactly once by dropLock (which clears it),
	// on every path out of the loop as well as on every error inside it.
	var lock *store.SubscriberLock
	dropLock := func() {
		if lock == nil {
			return
		}
		if err := releaseLock(ctx, lock); err != nil {
			log.Warn("event loop: release subscriber lock", "err", err)
		}
		lock = nil
	}
	defer dropLock()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 1. Acquire, and 2. redeliver whatever the previous holder read
		// but never acked.
		if lock == nil {
			l, ok, err := o.Store.TryLockSubscriber(ctx, o.Name)
			switch {
			case err != nil:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Error("event loop: acquire subscriber lock", "err", err)
			case !ok:
				// The steady state for every replica but one: another
				// consumer holds the stream (025 §15.1).
				log.Debug("event loop: subscriber held elsewhere, standing by")
			default:
				lock = l
			}
			if lock == nil {
				if !wait(ctx, lockRetry) {
					return ctx.Err()
				}
				continue
			}
			log.Debug("event loop: holding the subscriber")
			if err := o.Store.ResetEventRead(ctx, o.Name); err != nil {
				dropLock()
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("event loop: %w", err)
				}
				log.Error("event loop: reset read offset", "err", err)
				if !wait(ctx, poll) {
					return ctx.Err()
				}
				continue
			}
		}

		// 3. Read the next batch below the commit horizon.
		start := time.Now()
		events, err := o.Store.ReadEventBatch(ctx, o.Name, batchSize)
		if err != nil {
			dropLock()
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("event loop: %w", err)
			}
			log.Error("event loop: read batch", "err", err)
			if !wait(ctx, poll) {
				return ctx.Err()
			}
			continue
		}
		if len(events) == 0 {
			if !wait(ctx, poll) {
				return ctx.Err()
			}
			continue
		}

		// 4. Handle in order, stopping at the first failure.
		var handledUpTo int64
		failed := false
		for _, ev := range events {
			outcome, herr := o.Handler(ctx, ev)
			if herr != nil {
				o.Metrics.event(o.Name, ev.Type, "error")
				log.Error("event loop: handler failed, retrying head of line",
					"event", ev.ID, "type", ev.Type, "err", herr)
				failed = true
				break
			}
			o.Metrics.event(o.Name, ev.Type, outcomeLabel(outcome))
			handledUpTo = ev.ID
		}

		// A cancelled context mid-batch skips the ack rather than writing
		// through a dead context: the handled prefix is simply redelivered
		// to whoever takes the stream next (at-least-once).
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 5. Ack the handled prefix, then — only if the batch broke — pull
		// last_read_offset back onto it so the failed event is redelivered.
		// The order is load-bearing: ResetEventRead lowers last_read_offset
		// to last_acked_offset, so acking after it would ack past the read
		// offset and trip the CHECK.
		if handledUpTo > 0 {
			if err := o.Store.AckEvents(ctx, o.Name, handledUpTo); err != nil {
				dropLock()
				log.Error("event loop: ack events", "err", err, "up_to", handledUpTo)
				if !wait(ctx, poll) {
					return ctx.Err()
				}
				continue
			}
		}
		if failed {
			if err := o.Store.ResetEventRead(ctx, o.Name); err != nil {
				dropLock()
				log.Error("event loop: rewind read offset after handler error", "err", err)
				if !wait(ctx, poll) {
					return ctx.Err()
				}
				continue
			}
		}
		o.Metrics.batch(o.Name, time.Since(start).Seconds())
		if failed && !wait(ctx, poll) {
			return ctx.Err()
		}
	}
}

// outcomeLabel bounds the outcome label to the set 025 §15.7 fixes: a
// handler that returns anything but "suppressed" applied its event.
func outcomeLabel(o Outcome) string {
	if o == OutcomeSuppressed {
		return string(OutcomeSuppressed)
	}
	return string(OutcomeApplied)
}

// releaseLock unlocks on a context detached from ctx, so a shutdown still
// gets the unlock statement out. Release discards the session either way, so
// Postgres drops the lock even if the round trip fails.
func releaseLock(ctx context.Context, l *store.SubscriberLock) error {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	return l.Release(rctx)
}

// wait sleeps for d, reporting false if ctx was cancelled first.
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
