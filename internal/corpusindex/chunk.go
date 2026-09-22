// Package corpusindex chunks a doc, task, or skill into the embeddable units
// spec 040 §4 defines, and composes the context header each one carries. It
// is pure: no store, no HTTP, no DB — internal/store calls it with rows it
// already has and writes what comes back.
package corpusindex

import (
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Budget is the chunk sizing (040 §4.1). It derives from the embedding
// model's declared context window (07 §14.4); nothing here carries a model's
// window as a constant.
type Budget struct {
	// Runes is a chunk's ceiling, header included.
	Runes int
	// Overlap is how many runes of raw text consecutive sub-chunks share.
	Overlap int
}

// BudgetFor sizes chunks for a model whose context window is contextTokens.
// 1.75 runes per token is the ratio that kept 3600 runes inside
// EmbeddingGemma's 2048-token window; the overlap is a sixth of the budget.
func BudgetFor(contextTokens int) Budget {
	runes := contextTokens * 7 / 4
	return Budget{Runes: runes, Overlap: runes / 6}
}

// DefaultBudget sizes chunks when no embedding provider is configured and
// only the lexical arm indexes them. It equals BudgetFor(2048), the smallest
// window a default deployment has used, so a provider added later does not
// re-chunk the corpus unless its window differs.
var DefaultBudget = BudgetFor(2048)

// Chunk is one embeddable unit of a doc, task, or skill's indexed text.
// Header and Text are stored in separate columns (§4.3, §5): embedding or
// lexical indexing concatenates them, and only Text is ever returned to a
// caller as an excerpt.
type Chunk struct {
	// Anchor is the frozen section anchor (025 §3.2) a doc sub-chunk
	// inherits from its section. "" for tasks, skills, and any doc chunk
	// that names no real anchor (a plan heading, an unstructured fallback).
	Anchor string
	// Index is 0-based within this anchor's sub-chunks — matching the
	// (doc_id, anchor, chunk_index) uniqueness §5 defines, not a position
	// across the whole subject.
	Index int
	// Header is the context header (§4.3). It counts against Budget.Runes.
	Header string
	// Text is the indexed body text, header excluded.
	Text string
}

// windowed splits text into Chunks that all share one header and anchor,
// each capped at b.Runes runes including the header (§4.1, §4.3). Index
// starts at start rather than always 0: §5's unique index is on
// (doc_id, anchor, chunk_index), so a caller that emits more than one span
// under the same anchor — an anchorless plan heading, a depth-5/6 unanchored
// section — must keep numbering across those spans rather than restart per
// span.
func windowed(b Budget, anchor, header, text string, start int) []Chunk {
	budget := b.Runes - len([]rune(header))
	if budget < 1 {
		budget = 1 // pathological: a header alone at or past the budget
	}
	pieces := embed.Chunks(text, budget, b.Overlap)
	if len(pieces) == 0 {
		// Empty text still yields one chunk. The header alone carries the
		// subject's title and address, and more importantly a subject with no
		// chunk rows is stale forever — the convergence loop would retry an
		// empty document on every pass (§7).
		pieces = []string{""}
	}
	out := make([]Chunk, len(pieces))
	for i, p := range pieces {
		out[i] = Chunk{Anchor: anchor, Index: start + i, Header: header, Text: p}
	}
	return out
}

// ChunkDoc splits a spec or ADR into one chunk per section (§4.2): sections
// carries every doc_sections row for doc, in whatever order the caller has
// them. A plan (sections empty, 025 §9) chunks on its own ##/### headings
// instead, anchor always "", falling back to fixed windows when the body has
// no heading structure at all — which also covers a spec or ADR somehow
// carrying none.
func ChunkDoc(b Budget, doc model.Doc, sections []model.DocSection) []Chunk {
	parsed, err := designdoc.Parse([]byte(doc.Body))
	if err != nil {
		return windowed(b, "", DocHeader(doc, "", ""), doc.Body, 0)
	}
	if len(parsed.Sections) == 0 {
		return windowed(b, "", DocHeader(doc, "", ""), parsed.Preamble, 0)
	}
	if len(sections) == 0 {
		return chunkPlan(b, doc, parsed)
	}
	return chunkSections(b, doc, sections, parsed)
}

// chunkSections zips sections (sorted by Position) against parsed.Sections
// by index: both enumerate the same document's headings in the same order,
// since internal/store builds sections the same way, via designdoc.Parse.
// Indexing rather than matching on Anchor also copes with the anchorless
// headings depth 5/6 legally carries — next tracks each anchor's running
// chunk_index across every section sharing it (§5's unique index is
// (doc_id, anchor, chunk_index), not per-section).
func chunkSections(b Budget, doc model.Doc, sections []model.DocSection, parsed *designdoc.Document) []Chunk {
	ordered := append([]model.DocSection(nil), sections...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })

	next := map[string]int{}
	var out []Chunk
	for i := range min(len(ordered), len(parsed.Sections)) {
		sec := ordered[i]
		header := DocHeader(doc, sec.Number, sec.Heading)
		chunks := windowed(b, sec.Anchor, header, parsed.Sections[i].Body, next[sec.Anchor])
		next[sec.Anchor] += len(chunks)
		out = append(out, chunks...)
	}
	return out
}

// chunkPlan chunks a plan on every heading designdoc.Parse finds — in
// practice ## and ###, the only depths a plan uses (WL-PLAN-2's "## Tasks" /
// "### Task N — ..." convention) — plus any leading preamble, each with an
// empty anchor since plans carry none (025 §9). Every chunk here shares that
// one empty anchor, so next is a single running counter rather than a map.
func chunkPlan(b Budget, doc model.Doc, parsed *designdoc.Document) []Chunk {
	var out []Chunk
	next := 0
	if pre := strings.TrimSpace(parsed.Preamble); pre != "" {
		chunks := windowed(b, "", DocHeader(doc, "", ""), parsed.Preamble, next)
		next += len(chunks)
		out = append(out, chunks...)
	}
	for _, sec := range parsed.Sections {
		chunks := windowed(b, "", DocHeader(doc, sec.Number, sec.Title), sec.Body, next)
		next += len(chunks)
		out = append(out, chunks...)
	}
	return out
}

// ChunkTask indexes a task as title + "\n\n" + body, one chunk unless the
// combined text overflows the budget (§4.4). An empty body still indexes the
// title: titles are the highest-signal text in the tracker.
func ChunkTask(b Budget, task model.Task) []Chunk {
	text := task.Title + "\n\n" + task.Body
	return windowed(b, "", TaskHeader(task), text, 0)
}

// ChunkSkill indexes a skill's description prepended to its SKILL.md body,
// windowed the same way 016's skillsync.embedSkill does today.
func ChunkSkill(b Budget, skill model.Skill, skillMD string) []Chunk {
	text := skill.Description + "\n\n" + skillMD
	return windowed(b, "", SkillHeader(skill), text, 0)
}
