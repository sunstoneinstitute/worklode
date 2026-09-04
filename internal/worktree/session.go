package worktree

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SessionMarkerFile is written in the worktree-private git dir to mark a live
// coding session in that worktree.
const SessionMarkerFile = "worklode-session.json"

// SessionMarker records the process owning a live coding session in a
// worktree. A marker is stale once its pid is no longer alive.
//
// It lives here rather than with the hooks that maintain it because two
// binaries read it: `lode-hook` writes and refreshes it, and `lode` reads it
// to learn which session a command it is running belongs to.
type SessionMarker struct {
	SessionID       string `json:"session_id"`
	PID             int    `json:"pid"`
	StartedAt       string `json:"started_at"`
	LastHeartbeatAt string `json:"last_heartbeat_at,omitempty"`
}

// SessionMarkerPath returns the marker file path inside root's worktree-private
// git dir.
func SessionMarkerPath(root string) (string, error) {
	gitDir, err := GitDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, SessionMarkerFile), nil
}

// ReadSessionMarker reads root's marker. A missing or unparseable marker
// returns ok=false — never an error, since every caller treats "no marker" as
// "nothing to do".
func ReadSessionMarker(root string) (SessionMarker, bool) {
	path, err := SessionMarkerPath(root)
	if err != nil {
		return SessionMarker{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionMarker{}, false
	}
	var m SessionMarker
	if json.Unmarshal(data, &m) != nil {
		return SessionMarker{}, false
	}
	return m, true
}

// WriteSessionMarker serializes m to root's marker path.
func WriteSessionMarker(root string, m SessionMarker) error {
	path, err := SessionMarkerPath(root)
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// SessionID returns the session id recorded for root, or ok=false when the
// worktree has no live marker. Hooks that receive no stdin (git pre-commit)
// learn the session this way, and so does any `lode` command recording which
// session it acted for.
func SessionID(root string) (string, bool) {
	m, ok := ReadSessionMarker(root)
	if !ok || m.SessionID == "" {
		return "", false
	}
	return m.SessionID, true
}
