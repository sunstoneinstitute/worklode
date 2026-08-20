package hookrun

import (
	"reflect"
	"testing"
)

func TestNormalizePayload(t *testing.T) {
	cases := []struct {
		name    string
		harness string
		raw     string
		want    Payload
	}{
		{"claude-code: existing shape, byte-identical behaviour", "claude-code",
			`{"cwd":"/w","session_id":"s1","transcript_path":"/t.jsonl"}`,
			Payload{Cwd: "/w", SessionID: "s1", TranscriptPath: "/t.jsonl"}},
		// The default (empty harness) is claude-code, for every binding
		// already installed (spec 008 §17.4).
		{"default harness is claude-code", "",
			`{"cwd":"/w","session_id":"s1"}`,
			Payload{Cwd: "/w", SessionID: "s1"}},
		{"codex: camelCase keys", "codex",
			`{"cwd":"/w","sessionId":"s2"}`,
			Payload{Cwd: "/w", SessionID: "s2"}},
		{"copilot: camelCase with workingDirectory", "copilot",
			`{"workingDirectory":"/w","sessionId":"s3"}`,
			Payload{Cwd: "/w", SessionID: "s3"}},
		// A field Worklode needs but the payload omits => zero value => the
		// guard NOPs. Never an error (spec 008 §17.4).
		{"missing fields degrade to zero value", "amp",
			`{"unrelated":true}`,
			Payload{}},
		{"non-JSON stdin degrades to zero value", "codex",
			`not json`,
			Payload{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizePayload(c.harness, []byte(c.raw))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("normalizePayload(%q, %q) = %+v, want %+v", c.harness, c.raw, got, c.want)
			}
		})
	}
}
