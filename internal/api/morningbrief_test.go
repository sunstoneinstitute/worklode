package api

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestMorningBriefTierOf(t *testing.T) {
	cases := map[string]morningBriefTier{
		// tier 3: stopped or reached a bound
		"task.stopped":         briefTierStopped,
		"task.abandoned":       briefTierStopped,
		"lease.expired":        briefTierStopped,
		"runtime.crashloop":    briefTierStopped,
		"runtime.oom":          briefTierStopped,
		"runtime.flux_failure": briefTierStopped,
		// tier 2: material outcomes and changes
		"wl:DocumentSubmitted":  briefTierOutcome,
		"wl:DocumentAccepted":   briefTierOutcome,
		"task.done":             briefTierOutcome,
		"task.reopened":         briefTierOutcome,
		"deliverable.created":   briefTierOutcome,
		"approval.decided":      briefTierOutcome,
		"crew.member_added":     briefTierOutcome,
		"crew.member_removed":   briefTierOutcome,
		"issue.promoted":        briefTierOutcome,
		"runtime.flux_recovery": briefTierOutcome,
		// tier 4: everything else, routine, default
		"task.created":      briefTierRoutine,
		"push":              briefTierRoutine,
		"never.seen.before": briefTierRoutine,
	}

	for eventType, want := range cases {
		t.Run(eventType, func(t *testing.T) {
			got := morningBriefTierOf(eventType)
			if got != want {
				t.Errorf("morningBriefTierOf(%q) = %v, want %v", eventType, got, want)
			}
		})
	}
}

func TestMorningBriefProject(t *testing.T) {
	keyToProject := map[string]string{"WL": "worklode"}
	repoToProject := map[string]string{"sunstoneinstitute/worklode": "worklode"}

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "payload project key wins",
			payload: `{"project": "explicit-project", "task": "WL-7"}`,
			want:    "explicit-project",
		},
		{
			name:    "task key resolves via keyToProject",
			payload: `{"task": "WL-7"}`,
			want:    "worklode",
		},
		{
			name:    "repository.full_name resolves via repoToProject",
			payload: `{"repository": {"full_name": "sunstoneinstitute/worklode"}}`,
			want:    "worklode",
		},
		{
			name:    "no attribution present",
			payload: `{"foo": "bar"}`,
			want:    "",
		},
		{
			name:    "non-object payload",
			payload: `"not an object"`,
			want:    "",
		},
		{
			name:    "unknown task prefix",
			payload: `{"task": "ZZ-1"}`,
			want:    "",
		},
		{
			name:    "unknown repository",
			payload: `{"repository": {"full_name": "someone/else"}}`,
			want:    "",
		},
		{
			name:    "task key without dash",
			payload: `{"task": "notaslug"}`,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := store.Event{Payload: []byte(tc.payload)}
			got := morningBriefProject(ev, keyToProject, repoToProject)
			if got != tc.want {
				t.Errorf("morningBriefProject(%s) = %q, want %q", tc.payload, got, tc.want)
			}
		})
	}
}
