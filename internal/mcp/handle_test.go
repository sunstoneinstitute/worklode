package mcp

import (
	"testing"
	"time"
)

func TestHandle(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	exp := t0.Add(24 * time.Hour)
	h := signHandle("s3cret", "alice", "WL", exp)
	valid := h[len("WL."):]
	cases := []struct {
		name, secret, actor, handle string
		now                         time.Time
		key, reason                 string
	}{
		{"round trip", "s3cret", "alice", h, t0, "WL", ""},
		{"just before 24h", "s3cret", "alice", h, exp.Add(-time.Second), "WL", ""},
		{"exactly 24h", "s3cret", "alice", h, exp, "", "expired"},
		{"other actor", "s3cret", "bob", h, t0, "", "wrong_user"},
		{"other secret", "other", "alice", h, t0, "", "wrong_user"},
		{"other key", "s3cret", "alice", "XY." + valid, t0, "", "wrong_user"},
		{"bare key", "s3cret", "alice", "WL", t0, "", "bare_key"},
		{"bad base64", "s3cret", "alice", "WL.!!!", t0, "", "malformed"},
		{"short token", "s3cret", "alice", "WL.abc", t0, "", "malformed"},
		{"no dot not key", "s3cret", "alice", "rollout-2", t0, "", "malformed"},
		{"bad key part", "s3cret", "alice", "wl." + valid, t0, "", "malformed"},
		{"empty", "s3cret", "alice", "", t0, "", "malformed"},
	}
	for _, c := range cases {
		key, reason := verifyHandle(c.secret, c.actor, c.handle, c.now)
		if key != c.key || reason != c.reason {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, key, reason, c.key, c.reason)
		}
	}
}

func TestFullRef(t *testing.T) {
	cases := []struct {
		in, key string
		ok      bool
	}{
		{"WL-12", "WL", true},
		{"WL-SPEC-88", "WL", true},
		{"WL-PLAN-158", "WL", true},
		{"WL-REQ-1828", "WL", true},
		{"WL-RULE-1826", "WL", true},
		{"DATA-7", "DATA", true},
		{"rollout-2", "", false},
		{"WL", "", false},
		{"WL-SPEC", "", false},
		{"WL-FOO-3", "", false},
		{"WL-12 ", "", false},
	}
	for _, c := range cases {
		key, ok := fullRef(c.in)
		if key != c.key || ok != c.ok {
			t.Errorf("fullRef(%q) = (%q, %v), want (%q, %v)", c.in, key, ok, c.key, c.ok)
		}
	}
}
