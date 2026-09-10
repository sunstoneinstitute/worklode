package watcher_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// TestStaleAt covers the §8.7 clock's truth table: the default threshold,
// a project override winning over it, the never-stale cases (execution
// exists, an ADR, a non-accepted status), and the boundary instant.
func TestStaleAt(t *testing.T) {
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		in   watcher.StaleInput
		want time.Time
	}{
		{
			name: "default 30 days honored",
			in: watcher.StaleInput{
				DocKind:     "spec",
				Status:      "accepted",
				UpdatedAt:   updated,
				DefaultDays: 30,
			},
			want: updated.AddDate(0, 0, 30),
		},
		{
			name: "project override wins over default",
			in: watcher.StaleInput{
				DocKind:     "spec",
				Status:      "accepted",
				UpdatedAt:   updated,
				ProjectDays: 14,
				DefaultDays: 30,
			},
			want: updated.AddDate(0, 0, 14),
		},
		{
			name: "boundary: threshold instant is exactly UpdatedAt plus days, no off-by-one",
			in: watcher.StaleInput{
				DocKind:     "spec",
				Status:      "accepted",
				UpdatedAt:   updated,
				DefaultDays: 1,
			},
			want: updated.AddDate(0, 0, 1),
		},
		{
			name: "execution disarms the clock",
			in: watcher.StaleInput{
				DocKind:      "plan",
				Status:       "accepted",
				UpdatedAt:    updated,
				DefaultDays:  30,
				HasExecution: true,
			},
			want: time.Time{},
		},
		{
			name: "adr never goes stale, even with a default and no execution",
			in: watcher.StaleInput{
				DocKind:     "adr",
				Status:      "accepted",
				UpdatedAt:   updated,
				DefaultDays: 30,
			},
			want: time.Time{},
		},
		{
			name: "draft is never stale",
			in: watcher.StaleInput{
				DocKind:     "spec",
				Status:      "draft",
				UpdatedAt:   updated,
				DefaultDays: 30,
			},
			want: time.Time{},
		},
		{
			name: "stale is never stale again",
			in: watcher.StaleInput{
				DocKind:     "spec",
				Status:      "stale",
				UpdatedAt:   updated,
				DefaultDays: 30,
			},
			want: time.Time{},
		},
		{
			name: "withdrawn is never stale",
			in: watcher.StaleInput{
				DocKind:     "spec",
				Status:      "withdrawn",
				UpdatedAt:   updated,
				DefaultDays: 30,
			},
			want: time.Time{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := watcher.StaleAt(tc.in)
			if !got.Equal(tc.want) {
				t.Fatalf("StaleAt() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEvaluateStale covers the groom-on-stale rule: the mint for a spec and
// for a plan, and the suppression when a design task is already open,
// mirroring TestEvaluate in doclifecycle_test.go.
func TestEvaluateStale(t *testing.T) {
	cases := []struct {
		name string
		in   watcher.Input
		want watcher.Action
	}{
		{
			name: "stale spec mints groom",
			in: watcher.Input{
				EventID:  60,
				DocIRI:   "wlid:doc/spec-025",
				DocKind:  "spec",
				DocTitle: "Documents in the backbone",
			},
			want: watcher.Action{
				Rule:     "groom-on-stale",
				TaskKind: "design",
				Title:    "Groom: Documents in the backbone",
			},
		},
		{
			name: "stale plan mints re-plan",
			in: watcher.Input{
				EventID:  61,
				DocIRI:   "wlid:doc/plan-2026-08-03-x",
				DocKind:  "plan",
				DocTitle: "Some plan",
			},
			want: watcher.Action{
				Rule:     "groom-on-stale",
				TaskKind: "design",
				Title:    "Re-plan: Some plan",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.EventType = watcher.TypeDocStale
			got := watcher.Evaluate(in)
			if len(got) != 1 {
				t.Fatalf("Evaluate() = %+v, want one action", got)
			}
			action := got[0]
			if action.Rule != tc.want.Rule {
				t.Fatalf("Rule = %q, want %q", action.Rule, tc.want.Rule)
			}
			if action.Suppressed {
				t.Fatalf("Suppressed = true, want false")
			}
			if action.TaskKind != tc.want.TaskKind {
				t.Fatalf("TaskKind = %q, want %q", action.TaskKind, tc.want.TaskKind)
			}
			if action.Title != tc.want.Title {
				t.Fatalf("Title = %q, want %q", action.Title, tc.want.Title)
			}
			if !strings.Contains(action.Body, tc.in.DocIRI) {
				t.Fatalf("Body = %q, want it to contain doc IRI %q", action.Body, tc.in.DocIRI)
			}
			wantProvenance := "wlid:event/60"
			if tc.in.EventID == 61 {
				wantProvenance = "wlid:event/61"
			}
			if !strings.Contains(action.Body, wantProvenance) {
				t.Fatalf("Body = %q, want it to contain provenance %q", action.Body, wantProvenance)
			}
			if !strings.Contains(action.Body, "re-evaluate, adjust, or close") {
				t.Fatalf("Body = %q, want it to quote the §8.7 charge", action.Body)
			}
			if !strings.Contains(action.Body, "lode doc withdraw") {
				t.Fatalf("Body = %q, want it to name the close verb", action.Body)
			}
		})
	}

	t.Run("open design task suppressed with note", func(t *testing.T) {
		got := watcher.Evaluate(watcher.Input{
			EventID:        62,
			EventType:      watcher.TypeDocStale,
			DocIRI:         "wlid:doc/spec-025",
			DocKind:        "spec",
			DocTitle:       "Documents in the backbone",
			OpenDesignTask: "WL-103",
		})
		want := []watcher.Action{{Rule: "groom-on-stale", Suppressed: true, NoteTask: "WL-103"}}
		if len(got) != 1 || got[0] != want[0] {
			t.Fatalf("Evaluate() = %+v, want %+v", got, want)
		}
	})
}
