package otlp

import (
	"os"
	"reflect"
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
	if len(records) != 5 {
		t.Fatalf("got %d records, want 5", len(records))
	}

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
		if !got.At.Equal(w.At) {
			t.Errorf("record %d: At = %v, want %v", i, got.At, w.At)
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
