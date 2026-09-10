// Package staleness holds the pure 025 §8.7 threshold calculation, moved out
// of internal/watcher so internal/store's sweeper can call it directly:
// watcher pulls in internal/eventbus, which imports internal/store, so
// internal/store cannot import internal/watcher without an import cycle.
// This package has no such dependency — stdlib only — and internal/watcher
// re-exports StaleInput and StaleAt for its existing callers and tests.
package staleness

import "time"

// Input is everything the §8.7 clock consults for one document.
type Input struct {
	DocKind      string // spec | adr | plan
	Status       string
	UpdatedAt    time.Time // any revision or patch re-arms the clock
	ProjectDays  int       // projects.doc_staleness_days; 0 = no override
	DefaultDays  int       // instance default (30)
	HasExecution bool      // plan: a task ever leased; spec: an accepted covering plan
}

// At returns the instant the document crosses the staleness threshold, or
// the zero time when it never does: not accepted, an ADR (decisions already
// taken are never groomed), or execution exists. The threshold is
// inclusive — the document counts as stale at exactly UpdatedAt plus the
// threshold, not only strictly after it, so a sweeper comparing to "now"
// should test !now.Before(At(...)).
func At(in Input) time.Time {
	if in.DocKind == "adr" {
		return time.Time{}
	}
	if in.Status != "accepted" {
		return time.Time{}
	}
	if in.HasExecution {
		return time.Time{}
	}
	days := in.ProjectDays
	if days == 0 {
		days = in.DefaultDays
	}
	return in.UpdatedAt.AddDate(0, 0, days)
}
