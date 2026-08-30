package store

import (
	"errors"
	"testing"
)

func TestGitHubUserTokenRoundTrip(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.UpsertHumanActor(ctx, "github:42", "octocat", false, "", "", nil); err != nil {
		t.Fatalf("actor: %v", err)
	}

	if err := s.UpsertGitHubUserToken(ctx, "github:42", []byte("cipher-a")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetGitHubUserToken(ctx, "github:42")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "cipher-a" {
		t.Fatalf("got %q", got)
	}

	if err := s.UpsertGitHubUserToken(ctx, "github:42", []byte("cipher-b")); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, err = s.GetGitHubUserToken(ctx, "github:42")
	if err != nil {
		t.Fatalf("get after re-upsert: %v", err)
	}
	if string(got) != "cipher-b" {
		t.Fatalf("upsert did not overwrite: got %q", got)
	}
}

func TestGetGitHubUserTokenNotFound(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()

	if _, err := s.GetGitHubUserToken(ctx, "github:nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
