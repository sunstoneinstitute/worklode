package api

import (
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// The body editor's lock routes (WL-REQ-1868). The SPA calls them with the
// session actor; the CLI and MCP take no lock. Metrics are WL-1355's.

// lockSubject resolves the path's {kind} and {id} and checks the caller's
// write permission on that kind: permDocWrite (in routeGuards) for doc and
// rule, permTaskWrite for task. It returns the subject's owner (a task's
// assignee). An unknown kind or subject is 404; on a refusal it writes the
// answer and returns ok=false.
func (s *server) lockSubject(w http.ResponseWriter, r *http.Request) (kind, id, owner string, ok bool) {
	kind, id = r.PathValue("kind"), r.PathValue("id")
	perm := permDocWrite
	switch kind {
	case "doc", "rule":
	case "task":
		perm = permTaskWrite
	default:
		writeErr(w, http.StatusNotFound, "not found")
		return "", "", "", false
	}
	sub := subjectFrom(r)
	if d := Decide(Request{Subject: sub, Permission: perm}); !d.Allowed {
		writeErr(w, http.StatusForbidden, denialMessage(d))
		return "", "", "", false
	}
	if sub.ActorID == "" {
		writeErr(w, http.StatusForbidden, "a lock needs a signed-in actor")
		return "", "", "", false
	}
	owner, err := s.st.LockSubjectOwner(r.Context(), kind, id)
	if err != nil {
		s.mapStoreErr(w, err)
		return "", "", "", false
	}
	return kind, id, owner, true
}

// canUnlock: the subject's owner (a task's assignee) or an admin. A task
// with no assignee is an admin's to unlock.
func canUnlock(sub Subject, owner string) bool {
	return sub.HasRole(RoleAdmin) || (owner != "" && sub.ActorID == owner)
}

// getLock handles GET /api/v1/locks/{kind}/{id}: the lock, with an empty
// holder when it is free.
func (s *server) getLock(w http.ResponseWriter, r *http.Request) {
	kind, id, owner, ok := s.lockSubject(w, r)
	if !ok {
		return
	}
	l, err := s.st.GetLock(r.Context(), kind, id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if l == nil {
		l = &model.BodyLock{Kind: kind, SubjectID: id}
	}
	l.CanUnlock = canUnlock(subjectFrom(r), owner)
	writeJSON(w, http.StatusOK, l)
}

// takeLock handles POST /api/v1/locks/{kind}/{id}: a new session, or 409
// with the lock when another actor holds it.
func (s *server) takeLock(w http.ResponseWriter, r *http.Request) {
	kind, id, owner, ok := s.lockSubject(w, r)
	if !ok {
		return
	}
	sub := subjectFrom(r)
	l, err := s.st.TakeLock(r.Context(), kind, id, sub.ActorID)
	var held *store.ErrLockHeld
	if errors.As(err, &held) {
		held.Lock.CanUnlock = canUnlock(sub, owner)
		writeJSON(w, http.StatusConflict, held.Lock)
		return
	}
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	l.CanUnlock = canUnlock(sub, owner)
	writeJSON(w, http.StatusOK, l)
}

// checkInLock handles PUT /api/v1/locks/{kind}/{id}: the lock, or 409
// LockLost when the session has ended.
func (s *server) checkInLock(w http.ResponseWriter, r *http.Request) {
	kind, id, owner, ok := s.lockSubject(w, r)
	if !ok {
		return
	}
	var in model.LockCheckIn
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	sub := subjectFrom(r)
	l, err := s.st.CheckInLock(r.Context(), kind, id, in)
	var lost *store.ErrLockLost
	if errors.As(err, &lost) {
		lost.Lost.Lock.CanUnlock = canUnlock(sub, owner)
		writeJSON(w, http.StatusConflict, lost.Lost)
		return
	}
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	l.CanUnlock = canUnlock(sub, owner)
	writeJSON(w, http.StatusOK, l)
}

// releaseLock handles DELETE /api/v1/locks/{kind}/{id}?session=: ends that
// one session.
func (s *server) releaseLock(w http.ResponseWriter, r *http.Request) {
	kind, id, _, ok := s.lockSubject(w, r)
	if !ok {
		return
	}
	var p model.LockReleaseParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Session == "" {
		writeErr(w, http.StatusUnprocessableEntity, "session is required")
		return
	}
	if err := s.st.ReleaseLockSession(r.Context(), kind, id, p.Session); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unlockBody handles POST /api/v1/locks/{kind}/{id}/unlock: ends every
// session as unlocked by the caller, who must be able to (canUnlock).
func (s *server) unlockBody(w http.ResponseWriter, r *http.Request) {
	kind, id, owner, ok := s.lockSubject(w, r)
	if !ok {
		return
	}
	sub := subjectFrom(r)
	if !canUnlock(sub, owner) {
		writeErr(w, http.StatusForbidden, "only the owner or an admin may unlock")
		return
	}
	if err := s.st.UnlockBody(r.Context(), kind, id, sub.ActorID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// bodylocksRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var bodylocksRouteDocs = map[string]routeDoc{
	"GET /api/v1/locks/{kind}/{id}": {
		summary:   "Get the body lock on a doc, rule or task; holder is empty when free",
		responses: map[int]any{http.StatusOK: model.BodyLock{}},
	},
	"POST /api/v1/locks/{kind}/{id}": {
		summary:   "Take the body lock, getting a new session; 409 with the lock when another actor holds it",
		responses: map[int]any{http.StatusOK: model.BodyLock{}, http.StatusConflict: model.BodyLock{}},
	},
	"PUT /api/v1/locks/{kind}/{id}": {
		summary:   "Check in a lock session; 409 when the session has ended",
		request:   model.LockCheckIn{},
		responses: map[int]any{http.StatusOK: model.BodyLock{}, http.StatusConflict: model.LockLost{}},
	},
	"DELETE /api/v1/locks/{kind}/{id}": {
		summary:   "Release one lock session",
		responses: map[int]any{http.StatusNoContent: nil},
		params:    model.LockReleaseParams{},
	},
	"POST /api/v1/locks/{kind}/{id}/unlock": {
		summary:   "End every session on the lock; the subject's owner (a task's assignee) or an admin only",
		responses: map[int]any{http.StatusNoContent: nil},
	},
}
