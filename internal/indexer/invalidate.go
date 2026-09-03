package indexer

import (
	"context"
	"log/slog"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// InvalidateOnProviderChange nulls every stored vector when p is not the
// provider they were computed with, and records p as the new one (040 §8).
// Re-indexing on content change alone cannot recover from a swap: a subject
// whose content did not change is never re-indexed, and vectors from two
// models are not comparable — at two widths they make every query error
// outright.
//
// The chunk rows themselves stay: their text and tsv are provider-
// independent, so an instance mid-re-embed degrades to lexical-only, never to
// nothing. The next convergence pass rebuilds the vectors — StaleSubjects
// counts a null-vector row as needing work whenever a provider is configured.
//
// It takes a Store and a Provider rather than an Indexer because it runs at
// boot, before any loop starts. A caller may pass a nil log.
func InvalidateOnProviderChange(ctx context.Context, st *store.Store, p embed.Provider, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	id := p.ID()
	stored, err := st.EmbeddingProviderID(ctx)
	if err != nil {
		return err
	}
	if stored == id {
		return nil
	}
	n, err := st.ClearAllChunkVectors(ctx)
	if err != nil {
		return err
	}
	if err := st.SetEmbeddingProviderID(ctx, id); err != nil {
		return err
	}
	// Silent only on the usual first boot: no id recorded and nothing stored.
	// A real swap that finds zero vectors still gets logged — that is exactly
	// the state an operator debugging empty results needs to see.
	if stored != "" || n > 0 {
		log.Info("embedding provider changed, cleared vectors",
			"from", stored, "to", id, "cleared", n)
	}
	return nil
}
