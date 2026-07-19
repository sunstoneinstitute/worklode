package api

import (
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(secret, "alice", now.Add(time.Hour))

	user, ok := verifySession(secret, cookie, now)
	if !ok || user != "alice" {
		t.Fatalf("verifySession = (%q, %v), want (alice, true)", user, ok)
	}
}

func TestSessionRejectsTamper(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(secret, "alice", now.Add(time.Hour))

	// Flip the last byte of the cookie.
	bad := cookie[:len(cookie)-1] + string([]byte{cookie[len(cookie)-1] ^ 0x01})
	if _, ok := verifySession(secret, bad, now); ok {
		t.Fatal("verifySession accepted a tampered cookie")
	}
	// A different secret must also fail.
	if _, ok := verifySession("other", cookie, now); ok {
		t.Fatal("verifySession accepted a cookie under the wrong secret")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(secret, "alice", now.Add(time.Hour))

	later := now.Add(2 * time.Hour)
	if _, ok := verifySession(secret, cookie, later); ok {
		t.Fatal("verifySession accepted an expired cookie")
	}
}

func TestOAuthStateRoundTrip(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signOAuthState(secret, oauthState{
		State: "st", Verifier: "vfy", Next: "/tasks/WT-1", Exp: now.Add(10 * time.Minute).Unix(),
	})
	got, ok := verifyOAuthState(secret, cookie, now)
	if !ok {
		t.Fatal("verifyOAuthState = !ok")
	}
	if got.State != "st" || got.Verifier != "vfy" || got.Next != "/tasks/WT-1" {
		t.Fatalf("state = %+v", got)
	}
}

func TestOAuthStateRejectsExpired(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signOAuthState(secret, oauthState{
		State: "st", Verifier: "vfy", Next: "/", Exp: now.Add(-time.Minute).Unix(),
	})
	if _, ok := verifyOAuthState(secret, cookie, now); ok {
		t.Fatal("verifyOAuthState accepted an expired cookie")
	}
}

func TestSafeNext(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/", "/"},
		{"/tasks/WT-1", "/tasks/WT-1"},
		{"", "/"},
		{"foo", "/"},
		{"//evil.com", "/"},
		{"/\\evil.com", "/"},
		{"///evil.com", "/"},
		{"http://evil.com", "/"},
	}
	for _, c := range cases {
		if got := safeNext(c.in); got != c.want {
			t.Errorf("safeNext(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
