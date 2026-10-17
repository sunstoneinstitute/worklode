package api

import (
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// Every take consumes the code, whatever the caller then finds wrong with
// the request; an expired code does not redeem.
func TestMCPCodeStoreTake(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	s := newMCPCodeStore(func() time.Time { return now })

	code, err := s.mint(mcpCode{actorID: "grace", redirect: "http://localhost:33418/callback", challenge: "c"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if c, ok := s.take(code); !ok || c.actorID != "grace" {
		t.Fatalf("take = %+v,%v; want grace,true", c, ok)
	}
	if _, ok := s.take(code); ok {
		t.Fatal("second take should fail")
	}

	code, _ = s.mint(mcpCode{actorID: "grace"})
	now = now.Add(mcpCodeTTL + time.Second)
	if _, ok := s.take(code); ok {
		t.Fatal("expired code should not redeem")
	}
	if len(s.codes) != 0 {
		t.Fatalf("store still holds %d codes", len(s.codes))
	}
}

func TestPKCEMatches(t *testing.T) {
	t.Parallel()
	v := oauth2.GenerateVerifier()
	ch := oauth2.S256ChallengeFromVerifier(v)
	if !pkceMatches(v, ch) {
		t.Fatal("verifier does not match its own challenge")
	}
	for name, bad := range map[string]string{
		"other verifier": oauth2.GenerateVerifier(),
		"the challenge":  ch,
		"too short":      v[:42],
		"bad character":  v[:42] + "+",
		"empty":          "",
	} {
		if pkceMatches(bad, ch) {
			t.Fatalf("%s matched", name)
		}
	}
}

// A server built without registerMCPAuth has nil metrics; observing must not
// panic.
func TestMCPOAuthMetricsNilSafe(t *testing.T) {
	t.Parallel()
	var m *mcpOAuthMetrics
	m.observe("token", "ok")
}
