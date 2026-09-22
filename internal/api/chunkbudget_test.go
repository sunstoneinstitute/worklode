package api

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
)

// TestEmbeddingBudget covers 07 §14.4: a model needs its context window
// declared, the budget derives from it, and no provider means the
// lexical-only default.
func TestEmbeddingBudget(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		want    corpusindex.Budget
		wantErr bool
	}{
		{"no provider", Config{}, corpusindex.DefaultBudget, false},
		{"model without window", Config{EmbeddingModel: "m"}, corpusindex.Budget{}, true},
		{"window without model", Config{EmbeddingContextTokens: "2048"}, corpusindex.Budget{}, true},
		{"not a number", Config{EmbeddingModel: "m", EmbeddingContextTokens: "big"}, corpusindex.Budget{}, true},
		{"zero", Config{EmbeddingModel: "m", EmbeddingContextTokens: "0"}, corpusindex.Budget{}, true},
		{"gemma", Config{EmbeddingModel: "m", EmbeddingContextTokens: "2048"}, corpusindex.Budget{Runes: 3584, Overlap: 597}, false},
		{"8k", Config{EmbeddingModel: "m", EmbeddingContextTokens: "8192"}, corpusindex.Budget{Runes: 14336, Overlap: 2389}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := embeddingBudget(c.cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("budget = %+v, want %+v", got, c.want)
			}
		})
	}
}
