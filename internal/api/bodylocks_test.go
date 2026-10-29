package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// lockFixture is a server with a document owned by bob, a rule owned by
// alice, a task assigned to bob and an unassigned task. alice is the admin
// newTestServer seeds; carol and dave are plain users.
type lockFixture struct {
	h                         http.Handler
	admin, bob, carol, dave   string // tokens
	docID, ruleID, task, free string
}

func newLockFixture(t *testing.T) lockFixture {
	t.Helper()
	st, h, admin := newTestServer(t)
	createProject(t, st, "proj")
	f := lockFixture{h: h, admin: admin, bob: docActor(t, st, "bob"), carol: docActor(t, st, "carol"), dave: docActor(t, st, "dave")}
	d := createDocViaAPI(t, h, admin, model.CreateDocInput{Project: "proj", Kind: "plan", Slug: "001-x", Owner: "bob"})
	f.docID = fmt.Sprint(d.ID)
	var rule model.Rule
	rr := doReq(t, h, http.MethodPost, "/api/v1/rules", admin, model.AddRuleInput{Project: "proj", Heading: "R", Body: "x", Kind: "invariant"})
	decodeInto(t, rr, &rule)
	f.ruleID = fmt.Sprint(rule.ID)
	if rr := doReq(t, h, http.MethodPost, "/api/v1/projects/proj/participants", admin, map[string]any{"actor": "bob", "role": "editor"}); rr.Code != http.StatusCreated {
		t.Fatalf("crew: %d %s", rr.Code, rr.Body)
	}
	f.task = seedCrewTask(t, h, admin, "proj", "bob", "T")
	f.free = createTaskViaAPI(t, h, admin, map[string]any{"project": "proj", "title": "U", "priority": "high", "kind": "bug"})["id"].(string)
	return f
}

func lockPath(kind, id string) string { return "/api/v1/locks/" + kind + "/" + id }

func takeLock(t *testing.T, h http.Handler, token, kind, id string) model.BodyLock {
	t.Helper()
	rr := doReq(t, h, http.MethodPost, lockPath(kind, id), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("take %s %s: %d %s", kind, id, rr.Code, rr.Body)
	}
	var l model.BodyLock
	decodeInto(t, rr, &l)
	return l
}

// TestUnlockNeedsOwnerOrAdmin: a stranger cannot unlock, the owner can, and
// the holder's next check-in learns who did (WL-REQ-1868).
func TestUnlockNeedsOwnerOrAdmin(t *testing.T) {
	t.Parallel()
	f := newLockFixture(t)
	l := takeLock(t, f.h, f.carol, "doc", f.docID)
	if rr := doReq(t, f.h, http.MethodPost, lockPath("doc", f.docID)+"/unlock", f.dave, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("stranger: %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, f.h, http.MethodPost, lockPath("doc", f.docID)+"/unlock", f.bob, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("owner: %d %s", rr.Code, rr.Body)
	}
	rr := doReq(t, f.h, http.MethodPut, lockPath("doc", f.docID), f.carol, model.LockCheckIn{Session: l.Session, LastActivity: time.Now()})
	var lost model.LockLost
	decodeInto(t, rr, &lost)
	if rr.Code != http.StatusConflict || lost.Reason != "unlocked" || lost.By != "bob" {
		t.Fatalf("check-in after unlock: %d %+v", rr.Code, lost)
	}
}

// TestTakeLockHeld: a second holder gets 409 with the lock; the holder's
// second tab gets a new session.
func TestTakeLockHeld(t *testing.T) {
	t.Parallel()
	f := newLockFixture(t)
	a := takeLock(t, f.h, f.carol, "rule", f.ruleID)
	rr := doReq(t, f.h, http.MethodPost, lockPath("rule", f.ruleID), f.dave, nil)
	var held model.BodyLock
	decodeInto(t, rr, &held)
	if rr.Code != http.StatusConflict || held.Holder != "carol" || held.Session != "" {
		t.Fatalf("second holder: %d %+v", rr.Code, held)
	}
	if b := takeLock(t, f.h, f.carol, "rule", f.ruleID); b.Session == a.Session {
		t.Fatalf("second tab reused session %q", b.Session)
	}
}

// TestLockNotFound: an unknown kind or a subject that does not exist is 404.
func TestLockNotFound(t *testing.T) {
	t.Parallel()
	f := newLockFixture(t)
	for _, p := range []string{lockPath("idea", "1"), lockPath("doc", "999999"), lockPath("doc", "x"), lockPath("doc", "0"+f.docID), lockPath("rule", "999999"), lockPath("task", "WL-99999")} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			if rr := doReq(t, f.h, m, p, f.admin, nil); rr.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d %s", m, p, rr.Code, rr.Body)
			}
		}
	}
}

// TestLockCanUnlock: can_unlock is true for the document's and rule's
// owner, the task's assignee and an admin; false for anyone else, and for
// everyone but an admin on an unassigned task.
func TestLockCanUnlock(t *testing.T) {
	t.Parallel()
	f := newLockFixture(t)
	cases := []struct {
		kind, id, token string
		want            bool
	}{
		{"doc", f.docID, f.bob, true},
		{"doc", f.docID, f.admin, true},
		{"doc", f.docID, f.dave, false},
		{"rule", f.ruleID, f.admin, true}, // the creator owns it
		{"rule", f.ruleID, f.bob, false},
		{"task", f.task, f.bob, true},
		{"task", f.task, f.dave, false},
		{"task", f.free, f.admin, true},
		{"task", f.free, f.bob, false},
	}
	for _, c := range cases {
		rr := doReq(t, f.h, http.MethodGet, lockPath(c.kind, c.id), c.token, nil)
		var l model.BodyLock
		decodeInto(t, rr, &l)
		if rr.Code != http.StatusOK || l.CanUnlock != c.want || l.Holder != "" {
			t.Errorf("%s %s: %d %+v, want can_unlock %v", c.kind, c.id, rr.Code, l, c.want)
		}
	}
	// The take and the 409 carry it too.
	if l := takeLock(t, f.h, f.dave, "task", f.task); l.CanUnlock {
		t.Errorf("take by dave: %+v", l)
	}
	rr := doReq(t, f.h, http.MethodPost, lockPath("task", f.task), f.bob, nil)
	var held model.BodyLock
	decodeInto(t, rr, &held)
	if rr.Code != http.StatusConflict || !held.CanUnlock || held.Holder != "dave" {
		t.Errorf("409 for the assignee: %d %+v", rr.Code, held)
	}
}

// TestLockCheckInAndRelease: a check-in on a live session is 200; DELETE
// releases that one session only, after which its check-in is 409 released.
func TestLockCheckInAndRelease(t *testing.T) {
	t.Parallel()
	f := newLockFixture(t)
	a := takeLock(t, f.h, f.carol, "doc", f.docID)
	b := takeLock(t, f.h, f.carol, "doc", f.docID)
	in := model.LockCheckIn{Session: a.Session, Dirty: true, LastActivity: time.Now()}
	if rr := doReq(t, f.h, http.MethodPut, lockPath("doc", f.docID), f.carol, in); rr.Code != http.StatusOK {
		t.Fatalf("check-in: %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, f.h, http.MethodDelete, lockPath("doc", f.docID), f.carol, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("release without session: %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, f.h, http.MethodDelete, lockPath("doc", f.docID)+"?session="+a.Session, f.carol, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("release: %d %s", rr.Code, rr.Body)
	}
	rr := doReq(t, f.h, http.MethodPut, lockPath("doc", f.docID), f.carol, in)
	var lost model.LockLost
	decodeInto(t, rr, &lost)
	if rr.Code != http.StatusConflict || lost.Reason != "released" || lost.Lock.Holder != "carol" {
		t.Fatalf("check-in after release: %d %+v", rr.Code, lost)
	}
	in.Session = b.Session
	if rr := doReq(t, f.h, http.MethodPut, lockPath("doc", f.docID), f.carol, in); rr.Code != http.StatusOK {
		t.Fatalf("other tab: %d %s", rr.Code, rr.Body)
	}
	in.Session = "no-such-session"
	if rr := doReq(t, f.h, http.MethodPut, lockPath("doc", f.docID), f.carol, in); rr.Code != http.StatusNotFound {
		t.Errorf("unknown session: %d %s", rr.Code, rr.Body)
	}
}

// TestLockSessionWrites: a session-authenticated take passes the SPA write
// checks of WL-REQ-1770 and is refused without them.
func TestLockSessionWrites(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	cookie := loginSession(t, h, iss, "erin")
	createProject(t, st, "proj")
	token := docActor(t, st, "frank")
	d := createDocViaAPI(t, h, token, model.CreateDocInput{Project: "proj", Kind: "plan", Slug: "001-x"})
	take := func(hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, lockPath("doc", fmt.Sprint(d.ID)), nil)
		req.AddCookie(cookie)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := take(nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no page headers: %d %s", rec.Code, rec.Body)
	}
	rec := take(map[string]string{"Sec-Fetch-Site": "same-origin", "X-Requested-With": "lode-web"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"holder":"erin"`) {
		t.Fatalf("session take: %d %s", rec.Code, rec.Body)
	}
}

// TestLockNeedsActor: the open subject has no actor to hold a lock.
func TestLockNeedsActor(t *testing.T) {
	t.Parallel()
	f := newLockFixture(t)
	req := httptest.NewRequest(http.MethodPost, lockPath("doc", f.docID), nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Requested-With", "lode-web")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("open subject take: %d %s", rec.Code, rec.Body)
	}
}
