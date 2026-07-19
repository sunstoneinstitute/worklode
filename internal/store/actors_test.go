package store

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

var tokenPattern = regexp.MustCompile(`^wt_[0-9a-f]{40}$`)

func TestCreateAndGetActor(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "alice", "human", "Alice Example", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}

	got, err := s.GetActor(ctx, "alice")
	if err != nil {
		t.Fatalf("GetActor: %v", err)
	}
	want := &Actor{ID: "alice", Kind: "human", DisplayName: "Alice Example"}
	if *got != *want {
		t.Fatalf("GetActor: got %+v, want %+v", got, want)
	}
}

func TestGetActorNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	_, err := s.GetActor(ctx, "nobody")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetActor: want ErrNotFound, got %v", err)
	}
}

func TestCreateTokenRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}

	plaintext, err := s.CreateToken(ctx, "alice", "laptop", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !tokenPattern.MatchString(plaintext) {
		t.Fatalf("CreateToken: plaintext %q does not match %s", plaintext, tokenPattern)
	}

	// Only the hash may be stored, never the plaintext.
	rows, err := s.db.QueryContext(ctx, `SELECT token_hash FROM tokens`)
	if err != nil {
		t.Fatalf("query tokens: %v", err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found++
		if hash == plaintext {
			t.Fatalf("token_hash stores the plaintext token verbatim")
		}
		if strings.Contains(hash, "wt_") {
			t.Fatalf("token_hash %q looks like it contains the plaintext prefix", hash)
		}
	}
	if found != 1 {
		t.Fatalf("want 1 token row, got %d", found)
	}
}

func TestAuthenticateSuccess(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	plaintext, err := s.CreateToken(ctx, "alice", "laptop", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	actor, err := s.Authenticate(ctx, plaintext)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if actor.ID != "alice" {
		t.Fatalf("Authenticate: got actor %q, want alice", actor.ID)
	}
}

func TestAuthenticateUnknownToken(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	_, err := s.Authenticate(ctx, "wt_"+strings.Repeat("0", 40))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate: want ErrNotFound, got %v", err)
	}
}

func TestAuthenticateRevoked(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	plaintext, err := s.CreateToken(ctx, "alice", "laptop", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	if err := s.RevokeToken(ctx, plaintext); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	_, err = s.Authenticate(ctx, plaintext)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate after revoke: want ErrNotFound, got %v", err)
	}
}

func TestAuthenticateExpired(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetNowFunc(func() time.Time { return base })

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	expiresAt := base.Add(1 * time.Hour)
	plaintext, err := s.CreateToken(ctx, "alice", "laptop", &expiresAt)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Before expiry: still valid.
	if _, err := s.Authenticate(ctx, plaintext); err != nil {
		t.Fatalf("Authenticate before expiry: %v", err)
	}

	// Move the clock past expiry.
	s.SetNowFunc(func() time.Time { return base.Add(2 * time.Hour) })

	_, err = s.Authenticate(ctx, plaintext)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate after expiry: want ErrNotFound, got %v", err)
	}
}

func TestCreateTokenNoExpiry(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetNowFunc(func() time.Time { return base })

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	plaintext, err := s.CreateToken(ctx, "alice", "laptop", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Move the clock far into the future; token has no expiry so it stays valid.
	s.SetNowFunc(func() time.Time { return base.Add(24 * 365 * time.Hour) })

	if _, err := s.Authenticate(ctx, plaintext); err != nil {
		t.Fatalf("Authenticate: want no expiry to still be valid, got %v", err)
	}
}

func TestRevokeTokenByHash(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	plaintext, err := s.CreateToken(ctx, "alice", "laptop", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	hash := sha256Hex(plaintext)
	if err := s.RevokeToken(ctx, hash); err != nil {
		t.Fatalf("RevokeToken by hash: %v", err)
	}

	_, err = s.Authenticate(ctx, plaintext)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate after revoke by hash: want ErrNotFound, got %v", err)
	}
}
