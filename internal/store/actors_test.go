package store

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var tokenPattern = regexp.MustCompile(`^wl_[0-9a-f]{40}$`)

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
	// Compared field-by-field rather than by struct equality: Groups is a
	// []string (a NULL groups column, as here, scans to nil), which is not
	// comparable with ==.
	if got.ID != "alice" || got.Kind != "human" || got.DisplayName != "Alice Example" ||
		got.Admin || got.ExpectedGitHubLogin != "" || got.Email != "" || got.Groups != nil {
		t.Fatalf("GetActor: got %+v", got)
	}
}

// TestEnsureServiceActorIsIdempotent pins the property the boot path needs:
// CreateActor is a plain INSERT and fails the second time, so a server that
// asserts its service identity at every start needs this instead.
func TestEnsureServiceActorIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	for i := range 2 {
		if err := s.EnsureServiceActor(ctx, "watcher", "doc-lifecycle watcher"); err != nil {
			t.Fatalf("EnsureServiceActor call %d: %v", i+1, err)
		}
	}

	got, err := s.GetActor(ctx, "watcher")
	if err != nil {
		t.Fatalf("GetActor: %v", err)
	}
	if got.ID != "watcher" || got.Kind != "service" || got.DisplayName != "doc-lifecycle watcher" ||
		got.Admin || got.ExpectedGitHubLogin != "" || got.Email != "" || got.Groups != nil {
		t.Fatalf("GetActor: got %+v", got)
	}
}

// TestEnsureServiceActorLeavesAnExistingRowAlone: DO NOTHING, not DO UPDATE —
// a service identity has no external source of truth to re-sync from, so a
// second call must not rewrite a display name an operator changed.
func TestEnsureServiceActorLeavesAnExistingRowAlone(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "watcher", "service", "original", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	if err := s.EnsureServiceActor(ctx, "watcher", "replacement"); err != nil {
		t.Fatalf("EnsureServiceActor: %v", err)
	}

	got, err := s.GetActor(ctx, "watcher")
	if err != nil {
		t.Fatalf("GetActor: %v", err)
	}
	if got.DisplayName != "original" {
		t.Fatalf("display name = %q, want it left at %q", got.DisplayName, "original")
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
		if strings.Contains(hash, "wl_") {
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

	_, err := s.Authenticate(ctx, "wl_"+strings.Repeat("0", 40))
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

func TestUpsertHumanActor(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	// Insert.
	if err := s.UpsertHumanActor(ctx, "alice", "Alice Example", false, "", "", nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	a, err := s.GetActor(ctx, "alice")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.Kind != "human" || a.DisplayName != "Alice Example" || a.Admin {
		t.Fatalf("after insert: %+v", a)
	}

	// Re-login promotes to admin and updates the display name.
	if err := s.UpsertHumanActor(ctx, "alice", "Alice E.", true, "", "", nil); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	a, _ = s.GetActor(ctx, "alice")
	if !a.Admin || a.DisplayName != "Alice E." || a.Kind != "human" {
		t.Fatalf("after promote: %+v", a)
	}

	// Re-login demotes back to non-admin (demotion takes effect at next login).
	if err := s.UpsertHumanActor(ctx, "alice", "Alice E.", false, "", "", nil); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	a, _ = s.GetActor(ctx, "alice")
	if a.Admin {
		t.Fatalf("after demote still admin: %+v", a)
	}
}

// TestUpsertHumanActorSyncsGitHubExpectation asserts expected_github_login is
// re-synced on every login exactly like the admin flag (spec 001 §9.2): the
// first upsert with a github_username persists it, and a later login without
// the attribute clears it back to NULL (round-tripped as "").
func TestUpsertHumanActorSyncsGitHubExpectation(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.UpsertHumanActor(ctx, "alice", "Alice Example", false, "stigsb", "", nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	a, err := s.GetActor(ctx, "alice")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.ExpectedGitHubLogin != "stigsb" {
		t.Fatalf("ExpectedGitHubLogin = %q, want %q", a.ExpectedGitHubLogin, "stigsb")
	}

	// A later login where Keycloak no longer asserts the attribute clears it.
	if err := s.UpsertHumanActor(ctx, "alice", "Alice Example", false, "", "", nil); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	a, err = s.GetActor(ctx, "alice")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.ExpectedGitHubLogin != "" {
		t.Fatalf("ExpectedGitHubLogin after clear = %q, want empty", a.ExpectedGitHubLogin)
	}
}

// TestUpsertHumanActorStoresIdentityClaims asserts the email and groups
// claims are stored in full at login and re-synced on every login — Keycloak
// stays the sole authority (spec 029 §6.2) — exactly like the admin flag and
// expected GitHub login.
func TestUpsertHumanActorStoresIdentityClaims(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	groups := []string{"user", "science-lead"}
	if err := s.UpsertHumanActor(ctx, "ada", "Ada", false, "adal", "ada@example.org", groups); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetActor(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "ada@example.org" || !slices.Equal(a.Groups, groups) {
		t.Fatalf("claims not stored: %+v", a)
	}

	// Re-login with narrower claims replaces the stored value in full —
	// Keycloak stays the sole authority (029 §6.2).
	if err := s.UpsertHumanActor(ctx, "ada", "Ada", false, "adal", "", []string{"user"}); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetActor(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "" || !slices.Equal(a.Groups, []string{"user"}) {
		t.Fatalf("claims not replaced: %+v", a)
	}
}
