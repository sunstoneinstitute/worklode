package hookrun

import "encoding/json"

// normalizePayload maps a harness hook payload onto hookrun's internal Payload
// before the guard runs (spec 008 §17.4). There is no per-harness dispatch:
// each field is read from a harness-agnostic list of aliases, canonical
// (claude-code) name first, so a payload from any harness — named, misnamed or
// unnamed — resolves through the same list. A field no alias matches stays
// empty and the guard NOPs; a payload never produces a user-visible error.
//
// harnessID is therefore unused. It is kept in the signature so a harness that
// later needs a shape the union cannot express has somewhere to branch,
// without changing every call site.
func normalizePayload(harnessID string, raw []byte) Payload {
	var p Payload
	_ = json.Unmarshal(raw, &p) // canonical (claude-code) field names first
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return p
	}
	pick := func(dst *string, keys ...string) {
		if *dst != "" {
			return
		}
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				*dst = v
				return
			}
		}
	}
	pick(&p.Cwd, "workingDirectory", "working_directory", "workspacePath", "workspace_path")
	pick(&p.SessionID, "sessionId", "session", "threadId", "thread_id")
	pick(&p.TranscriptPath, "transcriptPath", "rolloutPath", "rollout_path")
	return p
}
