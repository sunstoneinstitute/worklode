package model_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestTimelineEntryKeysPerType pins the one thing a flat union gets wrong by
// accident: a field that should have been omitempty leaks into every other
// type's entry. The want lists are the exact key sets internal/api emitted
// when each entry was a hand-built map, so this also guards the wire against
// a future field being added to the struct without a type to belong to.
func TestTimelineEntryKeysPerType(t *testing.T) {
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	merged := at.Add(time.Hour)
	conclusion := "success"

	cases := []struct {
		entry model.TimelineEntry
		want  []string
	}{{
		entry: model.TimelineEntry{
			At: at, Type: "state", Change: json.RawMessage(`{"field":"state"}`), EventID: 7,
		},
		want: []string{"at", "change", "event_id", "type"},
	}, {
		entry: model.TimelineEntry{
			At: at, Type: "pr", Repo: "o/r", Number: 42, Title: "t",
			State: "merged", URL: "https://x", MergedAt: &merged,
		},
		want: []string{"at", "merged_at", "number", "repo", "state", "title", "type", "url"},
	}, {
		entry: model.TimelineEntry{
			At: at, Type: "ci", Repo: "o/r", Workflow: "CI", Status: "completed",
			Conclusion: &conclusion, URL: "https://x", CompletedAt: &merged,
		},
		want: []string{"at", "completed_at", "conclusion", "repo", "status", "type", "url", "workflow"},
	}, {
		entry: model.TimelineEntry{
			At: at, Type: "review", Repo: "o/r", Number: 42, Reviewer: "bob", State: "approved",
		},
		want: []string{"at", "number", "repo", "reviewer", "state", "type"},
	}, {
		entry: model.TimelineEntry{
			At: at, Type: "artifact", Kind: "git_tag", Name: "o/r", Version: "v1.0.0",
		},
		want: []string{"at", "kind", "name", "type", "version"},
	}, {
		entry: model.TimelineEntry{
			At: at, Type: "deployment", Environment: "prod", TargetName: "flux/demo", Status: "deployed",
		},
		want: []string{"at", "environment", "status", "target_name", "type"},
	}, {
		entry: model.TimelineEntry{
			At: at, Type: "runtime", Kind: "crashloop", Cluster: "c", Workload: "w", Message: "m",
		},
		want: []string{"at", "cluster", "kind", "message", "type", "workload"},
	}, {
		entry: model.TimelineEntry{At: at, Type: "landed", Repo: "o/r", SHA: "abc"},
		want:  []string{"at", "repo", "sha", "type"},
	}, {
		entry: model.TimelineEntry{At: at, Type: "deployed", Repo: "o/r", Environment: "dev"},
		want:  []string{"at", "environment", "repo", "type"},
	}, {
		entry: model.TimelineEntry{At: at, Type: "released", Repo: "o/r", Tag: "v1.0.0"},
		want:  []string{"at", "repo", "tag", "type"},
	}}

	for _, tc := range cases {
		t.Run(tc.entry.Type, func(t *testing.T) {
			b, err := json.Marshal(tc.entry)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			keys := make([]string, 0, len(got))
			for k := range got {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if !reflect.DeepEqual(keys, tc.want) {
				t.Errorf("keys = %v, want %v", keys, tc.want)
			}
		})
	}
}
