package otlp

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func boolPtr(b bool) *bool { return &b }

func TestDecodeLogs(t *testing.T) {
	body, err := os.ReadFile("testdata/logs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	records, err := DecodeLogs(body)
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	if len(records) != 6 {
		t.Fatalf("got %d records, want 6", len(records))
	}

	// A record whose timeUnixNano is absent or null is stamped at decode
	// time, so the page does not show it as 1970.
	decodedAfter := time.Now().Add(-time.Minute)

	at := func(ns int64) time.Time { return time.Unix(0, ns) }

	want := []Record{
		{
			Task: "WL-900", Project: "worklode", Session: "sess-rec-A",
			Service: "claude-code", Event: "claude_code.tool_result",
			At: at(1700000000000000000),
			Attrs: model.ActivityAttrs{
				ToolName:   "Bash",
				Success:    boolPtr(true),
				DurationMS: 1234,
			},
		},
		{
			// Falls back to the resource's session.id: no session.id on
			// this record.
			Task: "WL-900", Project: "worklode", Session: "sess-res-1",
			Service: "claude-code", Event: "claude_code.api_error",
			At: at(1700000001000000000),
			Attrs: model.ActivityAttrs{
				StatusCode: 500,
				Attempt:    2,
			},
		},
		{
			// "prompt" is not in the allowlist and must be dropped.
			Task: "WL-900", Project: "worklode", Session: "sess-rec-C",
			Service: "claude-code", Event: "claude_code.user_prompt",
			At:    at(1700000002000000000),
			Attrs: model.ActivityAttrs{PromptLength: 42},
		},
		{
			// No event.name anywhere and a body — both dropped, no crash.
			Task: "WL-900", Project: "worklode", Session: "sess-res-1",
			Service: "claude-code", Event: "",
			At: at(1700000003000000000),
			Attrs: model.ActivityAttrs{
				ToolUseID:   "abc123",
				CommandName: "ls",
			},
		},
		{
			// timeUnixNano is null: At is the decode time, checked below
			// rather than against a fixture value.
			Task: "WL-900", Project: "worklode", Session: "sess-res-1",
			Service: "claude-code", Event: "claude_code.tool_decision",
			Attrs: model.ActivityAttrs{
				DecisionType:   "accept",
				DecisionSource: "config",
			},
		},
		{
			// No worklode.task.id on this resource: unattributed.
			Task: "", Project: "", Session: "sess-res-2",
			Service: "codex", Event: "codex.turn_completed",
			At:    at(1700000004000000000),
			Attrs: model.ActivityAttrs{Model: "gpt-5"},
		},
	}

	for i, w := range want {
		got := records[i]
		if got.Task != w.Task || got.Project != w.Project || got.Session != w.Session ||
			got.Service != w.Service || got.Event != w.Event {
			t.Errorf("record %d: got %+v, want %+v", i, got, w)
		}
		switch {
		case !w.At.IsZero():
			if !got.At.Equal(w.At) {
				t.Errorf("record %d: At = %v, want %v", i, got.At, w.At)
			}
		case !got.At.After(decodedAfter):
			t.Errorf("record %d: At = %v, want the decode time", i, got.At)
		}
		if !reflect.DeepEqual(got.Attrs, w.Attrs) {
			t.Errorf("record %d: Attrs = %#v, want %#v", i, got.Attrs, w.Attrs)
		}
	}
}

func TestDecodeLogsInvalidJSON(t *testing.T) {
	if _, err := DecodeLogs([]byte("not json")); err == nil {
		t.Fatal("want an error for malformed JSON")
	}
}

// TestDecodeLogsDropsUnallowlistedResourceAttrs pins the allowlist as the
// only route into a Record: the fixture's resource carries a user.email that
// no field can hold, so nothing decoded may show it.
func TestDecodeLogsDropsUnallowlistedResourceAttrs(t *testing.T) {
	body, err := os.ReadFile("testdata/logs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !strings.Contains(string(body), "user.email") {
		t.Fatal("fixture no longer carries a user.email attribute; this test proves nothing")
	}

	records, err := DecodeLogs(body)
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	for i, rec := range records {
		attrs, err := json.Marshal(rec.Attrs)
		if err != nil {
			t.Fatalf("marshal attrs: %v", err)
		}
		if strings.Contains(string(attrs), `"user`) {
			t.Errorf("record %d: attrs carry a user key: %s", i, attrs)
		}
		whole, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if strings.Contains(string(whole), "someone@example.com") {
			t.Errorf("record %d: carries the resource's user.email: %s", i, whole)
		}
	}
}
