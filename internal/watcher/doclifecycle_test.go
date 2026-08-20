package watcher_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// TestEvaluate covers the §5 truth table of the plan: the two doc-lifecycle
// rules, each guard, and the "everything else mints nothing" cases (plan and
// ADR acceptance, a vendor event, an unrecognised wl: type).
func TestEvaluate(t *testing.T) {
	cases := []struct {
		name string
		in   watcher.Input
		want []watcher.Action
	}{
		{
			name: "submit mints review",
			in: watcher.Input{
				EventID:   42,
				EventType: eventbus.TypeDocumentSubmitted,
				DocIRI:    "wlid:doc/spec-025",
				DocKind:   "spec",
				DocTitle:  "Documents in the backbone",
				Version:   2,
			},
			want: []watcher.Action{{
				Rule:     "review-on-submit",
				TaskKind: "review",
				Title:    "Review: Documents in the backbone",
			}},
		},
		{
			name: "submit with open review task suppressed",
			in: watcher.Input{
				EventID:        43,
				EventType:      eventbus.TypeDocumentSubmitted,
				DocIRI:         "wlid:doc/spec-025",
				DocKind:        "spec",
				DocTitle:       "Documents in the backbone",
				Version:        2,
				OpenReviewTask: "WL-100",
			},
			want: []watcher.Action{{
				Rule:       "review-on-submit",
				Suppressed: true,
			}},
		},
		{
			name: "accept of spec mints design",
			in: watcher.Input{
				EventID:   44,
				EventType: eventbus.TypeDocumentAccepted,
				DocIRI:    "wlid:doc/spec-025",
				DocKind:   "spec",
				DocTitle:  "Documents in the backbone",
				Version:   2,
			},
			want: []watcher.Action{{
				Rule:     "plan-on-accept",
				TaskKind: "design",
				Title:    "Plan: decompose Documents in the backbone into plans",
			}},
		},
		{
			name: "accept of spec with open design task suppressed with note",
			in: watcher.Input{
				EventID:        45,
				EventType:      eventbus.TypeDocumentAccepted,
				DocIRI:         "wlid:doc/spec-025",
				DocKind:        "spec",
				DocTitle:       "Documents in the backbone",
				Version:        2,
				OpenDesignTask: "WL-101",
			},
			want: []watcher.Action{{
				Rule:       "plan-on-accept",
				Suppressed: true,
				NoteTask:   "WL-101",
			}},
		},
		{
			name: "accept of plan mints nothing",
			in: watcher.Input{
				EventID:   46,
				EventType: eventbus.TypeDocumentAccepted,
				DocIRI:    "wlid:doc/plan-2026-08-03-x",
				DocKind:   "plan",
				DocTitle:  "Some plan",
				Version:   1,
			},
			want: nil,
		},
		{
			name: "accept of adr mints nothing",
			in: watcher.Input{
				EventID:   47,
				EventType: eventbus.TypeDocumentAccepted,
				DocIRI:    "wlid:doc/adr-12",
				DocKind:   "adr",
				DocTitle:  "Some ADR",
				Version:   1,
			},
			want: nil,
		},
		{
			name: "vendor event ignored",
			in: watcher.Input{
				EventID:   48,
				EventType: "push",
			},
			want: nil,
		},
		{
			name: "unknown wl: type ignored",
			in: watcher.Input{
				EventID:   49,
				EventType: "wl:SomethingElse",
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := watcher.Evaluate(tc.in)

			if tc.want == nil {
				if got != nil {
					t.Fatalf("Evaluate() = %+v, want nil", got)
				}
				return
			}

			if len(got) != len(tc.want) {
				t.Fatalf("Evaluate() = %+v, want %+v", got, tc.want)
			}

			for i, wantAction := range tc.want {
				gotAction := got[i]

				if wantAction.Suppressed {
					// Suppressed actions carry no mint parameters: compare
					// whole, so a stray Title/Body/TaskKind fails loudly.
					if !reflect.DeepEqual(gotAction, wantAction) {
						t.Fatalf("Evaluate()[%d] = %+v, want %+v", i, gotAction, wantAction)
					}
					continue
				}

				if gotAction.Rule != wantAction.Rule {
					t.Fatalf("Evaluate()[%d].Rule = %q, want %q", i, gotAction.Rule, wantAction.Rule)
				}
				if gotAction.Suppressed {
					t.Fatalf("Evaluate()[%d].Suppressed = true, want false", i)
				}
				if gotAction.NoteTask != "" {
					t.Fatalf("Evaluate()[%d].NoteTask = %q, want \"\"", i, gotAction.NoteTask)
				}
				if gotAction.TaskKind != wantAction.TaskKind {
					t.Fatalf("Evaluate()[%d].TaskKind = %q, want %q", i, gotAction.TaskKind, wantAction.TaskKind)
				}
				if gotAction.Title != wantAction.Title {
					t.Fatalf("Evaluate()[%d].Title = %q, want %q", i, gotAction.Title, wantAction.Title)
				}

				// The failure this assertion exists to catch: a mint body
				// that lost its doc reference or its provenance line.
				if !strings.Contains(gotAction.Body, tc.in.DocIRI) {
					t.Fatalf("Evaluate()[%d].Body = %q, want it to contain doc IRI %q", i, gotAction.Body, tc.in.DocIRI)
				}
				wantProvenance := "wlid:event/" + strconv.FormatInt(tc.in.EventID, 10)
				if !strings.Contains(gotAction.Body, wantProvenance) {
					t.Fatalf("Evaluate()[%d].Body = %q, want it to contain provenance %q", i, gotAction.Body, wantProvenance)
				}
			}
		})
	}
}
