// Package indexer runs spec 040 §7's convergence loop: a background pass that
// makes index_chunks agree with the corpus, one subject at a time, plus §8's
// provider-change invalidation. It is the only writer of chunk rows and
// vectors — skill sync's job ends at upserting the skill.
//
// It sits beside internal/corpusindex rather than inside it because
// internal/store imports corpusindex for the Chunk type, so a loop holding a
// *store.Store cannot live there without an import cycle. corpusindex stays
// pure chunking; this package is the part that touches the database and the
// embedding provider.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// DefaultInterval is how often the loop converges when LODE_INDEX_INTERVAL
// says nothing (040 §7).
const DefaultInterval = 5 * time.Minute

// defaultBatch is how many stale subjects one query claims. A pass keeps
// asking for pages until it stops making progress, so this bounds memory and
// transaction spacing, not how much a pass converges.
const defaultBatch = 100

// kinds is the fixed set of subject kinds a pass walks, in a stable order so
// a slow kind cannot starve the others across restarts.
var kinds = []string{store.SubjectDoc, store.SubjectTask, store.SubjectSkill}

// Indexer converges the index. Embed nil is a fully supported configuration:
// the pass still chunks and still writes chunk_text, so the lexical arm works
// with no provider at all (§11); only the vectors are absent.
type Indexer struct {
	Store   *store.Store
	Embed   embed.Provider
	Metrics *Metrics     // nil-safe
	Log     *slog.Logger // nil = slog.Default()
	Batch   int          // 0 = defaultBatch
}

func (ix *Indexer) log() *slog.Logger {
	if ix.Log != nil {
		return ix.Log
	}
	return slog.Default()
}

// Loop converges every interval until ctx is done, starting with one pass
// immediately: an instance that was down while the corpus moved should not
// wait a full interval to catch up.
func (ix *Indexer) Loop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := ix.RunOnce(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return
		case err != nil:
			ix.log().Warn("corpus index convergence pass failed", "indexed", n, "err", err)
		case n > 0:
			ix.log().Info("corpus index converged", "subjects", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce converges every kind once and reports how many subjects it
// re-indexed. A subject that fails is logged, counted, and left stale for the
// next pass; the pass carries on with the rest, and so does the loop (§7:
// self-healing is the whole point of converging rather than hooking writes).
func (ix *Indexer) RunOnce(ctx context.Context) (int, error) {
	start := time.Now()
	var (
		total int
		errs  []error
	)
	for _, kind := range kinds {
		n, err := ix.convergeKind(ctx, kind)
		total += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	ix.Metrics.Convergence(time.Since(start))
	if err := ix.observe(ctx); err != nil {
		errs = append(errs, err)
	}
	return total, errors.Join(errs...)
}

// convergeKind re-indexes stale subjects of one kind until a page makes no
// progress. Failed subjects stay stale, so they come back in the next page:
// stopping on zero progress is what keeps a permanently failing subject from
// spinning the pass forever, while a page that was not full means nothing is
// left but those failures.
func (ix *Indexer) convergeKind(ctx context.Context, kind string) (int, error) {
	batch := ix.Batch
	if batch <= 0 {
		batch = defaultBatch
	}
	done := 0
	for {
		subjects, err := ix.Store.StaleSubjects(ctx, kind, batch, ix.Embed != nil)
		if err != nil {
			return done, fmt.Errorf("converge %s: %w", kind, err)
		}
		progress := 0
		for _, subj := range subjects {
			if err := ctx.Err(); err != nil {
				return done, err
			}
			if err := ix.index(ctx, subj); err != nil {
				if errors.Is(err, context.Canceled) {
					return done, err
				}
				ix.log().Warn("index subject failed",
					"kind", kind, "subject", subjectID(subj), "err", err)
				ix.Metrics.Reembed(kind, "error")
				continue
			}
			ix.Metrics.Reembed(kind, "ok")
			progress++
		}
		done += progress
		if progress == 0 || len(subjects) < batch {
			return done, nil
		}
	}
}

// index rebuilds one subject's chunk set: read the live row, chunk it, embed
// it when there is a provider, and swap the whole set in one transaction.
func (ix *Indexer) index(ctx context.Context, subj store.ChunkSubject) error {
	chunks, err := ix.chunks(ctx, subj)
	if err != nil {
		return err
	}
	vectors, err := ix.vectors(ctx, chunks)
	if err != nil {
		return err
	}
	return ix.Store.ReplaceSubjectChunks(ctx, subj, chunks, vectors)
}

// chunks reads the live row StaleSubjects named and hands it to corpusindex.
// One read per subject: a pass is background work over a page of at most
// defaultBatch subjects, so the round trips are cheaper than a join that has
// to reproduce three different shapes.
func (ix *Indexer) chunks(ctx context.Context, subj store.ChunkSubject) ([]corpusindex.Chunk, error) {
	switch subj.Kind {
	case store.SubjectDoc:
		doc, err := ix.Store.GetDoc(ctx, subj.DocID)
		if err != nil {
			return nil, err
		}
		// Empty for a plan, which carries no anchors (025 §9) — ChunkDoc
		// chunks those on their own headings instead.
		sections, err := ix.Store.ListDocSections(ctx, subj.DocID)
		if err != nil {
			return nil, err
		}
		return corpusindex.ChunkDoc(*doc, sections), nil
	case store.SubjectTask:
		task, err := ix.Store.GetTask(ctx, subj.TaskID)
		if err != nil {
			return nil, err
		}
		return corpusindex.ChunkTask(*task), nil
	case store.SubjectSkill:
		skill, err := ix.Store.SkillByID(ctx, subj.SkillID)
		if err != nil {
			return nil, err
		}
		return corpusindex.ChunkSkill(
			model.Skill{Name: skill.Name, Description: skill.Description}, skill.SkillMD), nil
	default:
		return nil, fmt.Errorf("index subject: unknown kind %q", subj.Kind)
	}
}

// vectors embeds the chunks, or returns nil when no provider is configured —
// which ReplaceSubjectChunks writes as null embeddings, leaving the lexical
// arm fully served (§11). The header is prepended to the embed input so the
// vector is conditioned on where the text lives (§4.3); the two stay in
// separate columns.
func (ix *Indexer) vectors(ctx context.Context, chunks []corpusindex.Chunk) ([][]float32, error) {
	if ix.Embed == nil || len(chunks) == 0 {
		return nil, nil
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Header + "\n\n" + c.Text
	}
	return ix.Embed.Embed(ctx, embed.RoleDocument, texts)
}

// observe refreshes §10's gauges after a pass: index size per kind, rows with
// no vector, and what is still stale — which should be zero every pass, and
// is the metric to alert on when it is not.
func (ix *Indexer) observe(ctx context.Context) error {
	if ix.Metrics == nil {
		return nil
	}
	counts, err := ix.Store.IndexCounts(ctx)
	if err != nil {
		return fmt.Errorf("index counts: %w", err)
	}
	ix.Metrics.Counts(counts)
	for _, kind := range kinds {
		// Whatever is still stale after a pass is the set that failed, so
		// one page is enough to see it.
		// ponytail: the gauge saturates at the batch size; a floor above zero
		// is the alerting signal, and the exact height of a backlog that big
		// is a log-reading problem, not a gauge problem.
		batch := ix.Batch
		if batch <= 0 {
			batch = defaultBatch
		}
		stale, err := ix.Store.StaleSubjects(ctx, kind, batch, ix.Embed != nil)
		if err != nil {
			return fmt.Errorf("stale count %s: %w", kind, err)
		}
		ix.Metrics.Stale(kind, len(stale))
	}
	return nil
}

// subjectID renders the id of whichever kind subj is, for logs.
func subjectID(subj store.ChunkSubject) string {
	switch subj.Kind {
	case store.SubjectTask:
		return subj.TaskID
	case store.SubjectDoc:
		return fmt.Sprintf("doc %d", subj.DocID)
	default:
		return fmt.Sprintf("skill %d", subj.SkillID)
	}
}
