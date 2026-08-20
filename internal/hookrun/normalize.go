package hookrun

import "encoding/json"

// normalizePayload maps one harness's hook payload onto hookrun's internal
// Payload before the guard runs (spec 008 §17.4). Unknown harnesses and
// missing fields degrade to zero values — the guard then NOPs; a payload
// never produces a user-visible error.
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
