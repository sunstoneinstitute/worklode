package store

import (
	"context"
	"testing"
)

// An absent row is the normal first-boot state — no provider has been used
// yet — so it reads back as the empty string rather than as an error.
func TestEmbeddingProviderIDUnset(t *testing.T) {
	s := OpenTestStore(t)

	got, err := s.EmbeddingProviderID(context.Background())
	if err != nil {
		t.Fatalf("unset provider id: %v", err)
	}
	if got != "" {
		t.Fatalf("unset provider id = %q, want empty", got)
	}
}

func TestSetEmbeddingProviderID(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	const small = "openai:text-embedding-3-small@api.openai.com/v1"
	if err := s.SetEmbeddingProviderID(ctx, small); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.EmbeddingProviderID(ctx)
	if err != nil || got != small {
		t.Fatalf("get = %q err=%v, want %q", got, err, small)
	}

	// The row is a singleton: a second set replaces it instead of conflicting.
	const large = "openai:text-embedding-3-large@api.openai.com/v1"
	if err := s.SetEmbeddingProviderID(ctx, large); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	got, err = s.EmbeddingProviderID(ctx)
	if err != nil || got != large {
		t.Fatalf("get after re-set = %q err=%v, want %q", got, err, large)
	}
}
