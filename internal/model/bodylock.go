package model

import "time"

// BodyLock is the status of the lock on one document, rule or task body
// (WL-REQ-1868). Kind is "doc", "rule" or "task"; SubjectID is the subject's
// id as text. Session is set only on the answer to a take.
type BodyLock struct {
	Kind      string `json:"kind"`
	SubjectID string `json:"subject_id"`
	Holder    string `json:"holder"`
	// CanUnlock says whether the caller may release the lock: the subject's
	// owner (task: assignee) or an admin.
	CanUnlock bool   `json:"can_unlock"`
	Session   string `json:"session,omitempty"`
}

// LockCheckIn is the holder page's periodic check-in. Dirty says the editor
// has unsaved changes; LastActivity is the page's last user activity.
type LockCheckIn struct {
	Session      string    `json:"session"`
	Dirty        bool      `json:"dirty"`
	LastActivity time.Time `json:"last_activity"`
}

// LockLost is the answer to a check-in or save whose session has ended.
// Reason is the session's ended_reason; By is who ended it (an unlock) and
// Lock the lock as it stands now.
type LockLost struct {
	Reason string   `json:"reason"`
	By     string   `json:"by,omitempty"`
	Lock   BodyLock `json:"lock"`
}
