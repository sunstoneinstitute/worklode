package corpusindex

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// bigText returns n runes of distinguishable, non-repeating-at-short-range
// content: a bug that mis-slices an overlap by a few runes shows up as a
// substring mismatch, which a uniform filler character would hide.
func bigText(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(byte('a' + i%26))
	}
	return b.String()
}

// TestChunkDocSplitsOversizedSectionInheritingAnchor is corpusindex's first
// test (WL-628): a 9000-rune section must split into overlapping sub-chunks
// that all carry the section's anchor, each capped at ChunkRunes including
// its header, overlapping the next by ChunkOverlap runes of raw text.
func TestChunkDocSplitsOversizedSectionInheritingAnchor(t *testing.T) {
	body := "# Test Spec\n\n" +
		"## 1 Big {#sec-1}\n\n" + bigText(9000) + "\n\n" +
		"## 2 Small {#sec-2}\n\nshort\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "spec", Number: 99, Title: "Test Spec", Body: body}
	sections := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Big", Depth: 2, Position: 0},
		{Anchor: "sec-2", Number: "2", Heading: "Small", Depth: 2, Position: 1},
	}

	chunks := ChunkDoc(doc, sections)

	var big []Chunk
	for _, c := range chunks {
		if c.Anchor == "sec-1" {
			big = append(big, c)
		}
	}
	if len(big) < 2 {
		t.Fatalf("want sec-1 split into multiple sub-chunks, got %d", len(big))
	}
	for i, c := range big {
		if c.Index != i {
			t.Errorf("sub-chunk %d: Index = %d, want %d", i, c.Index, i)
		}
		if got := len([]rune(c.Header + c.Text)); got > ChunkRunes {
			t.Errorf("sub-chunk %d: header+text = %d runes, want <= %d", i, got, ChunkRunes)
		}
	}
	for i := 1; i < len(big); i++ {
		prev := []rune(big[i-1].Text)
		cur := []rune(big[i].Text)
		if len(prev) < ChunkOverlap || len(cur) < ChunkOverlap {
			continue // a short final sub-chunk carries less than a full overlap
		}
		tail := string(prev[len(prev)-ChunkOverlap:])
		head := string(cur[:ChunkOverlap])
		if tail != head {
			t.Errorf("sub-chunk %d: overlap with previous does not match (want %d shared runes)", i, ChunkOverlap)
		}
	}
}

// TestChunkDocShortSectionsNotMerged asserts consecutive short sections each
// stay their own chunk (§4.2): a merged chunk would have to report one of
// two anchors.
func TestChunkDocShortSectionsNotMerged(t *testing.T) {
	body := "# Spec\n\n" +
		"## 1 First {#sec-1}\n\nfirst body\n\n" +
		"## 2 Second {#sec-2}\n\nsecond body\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "spec", Number: 1, Title: "Spec", Body: body}
	sections := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "First", Position: 0},
		{Anchor: "sec-2", Number: "2", Heading: "Second", Position: 1},
	}

	chunks := ChunkDoc(doc, sections)
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks (one per short section), got %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Anchor != "sec-1" || chunks[1].Anchor != "sec-2" {
		t.Fatalf("want anchors sec-1, sec-2 in order; got %q, %q", chunks[0].Anchor, chunks[1].Anchor)
	}
	if !strings.Contains(chunks[0].Text, "first body") || strings.Contains(chunks[0].Text, "second body") {
		t.Errorf("sec-1 chunk text = %q, want only its own body", chunks[0].Text)
	}
}

// TestChunkDocSectionOrderFollowsPosition asserts chunk order follows
// Position, not the order sections are passed in.
func TestChunkDocSectionOrderFollowsPosition(t *testing.T) {
	body := "# Spec\n\n" +
		"## 1 First {#sec-1}\n\nfirst body\n\n" +
		"## 2 Second {#sec-2}\n\nsecond body\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "spec", Number: 1, Title: "Spec", Body: body}
	// Passed out of position order.
	sections := []model.DocSection{
		{Anchor: "sec-2", Number: "2", Heading: "Second", Position: 1},
		{Anchor: "sec-1", Number: "1", Heading: "First", Position: 0},
	}

	chunks := ChunkDoc(doc, sections)
	if len(chunks) != 2 || chunks[0].Anchor != "sec-1" || chunks[1].Anchor != "sec-2" {
		t.Fatalf("want [sec-1, sec-2] in position order, got %+v", chunks)
	}
}

// TestChunkDocPlanHeadings asserts a plan (no sections passed, 025 §9)
// chunks on its ##/### headings with an empty anchor.
func TestChunkDocPlanHeadings(t *testing.T) {
	body := "# A Plan\n\nIntro text.\n\n" +
		"## Global constraints\n\nconstraints body\n\n" +
		"## Tasks\n\n" +
		"### Task 1 — Do the thing\n\ntask one body\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "plan", Title: "A Plan", Body: body}

	chunks := ChunkDoc(doc, nil)

	for _, c := range chunks {
		if c.Anchor != "" {
			t.Errorf("plan chunk carries anchor %q, want \"\"", c.Anchor)
		}
	}
	var gotHeadings []string
	for _, c := range chunks {
		gotHeadings = append(gotHeadings, c.Header)
	}
	wantSubstrings := []string{"Intro text", "Global constraints", "Task 1 — Do the thing"}
	joined := strings.Join(gotHeadings, " | ")
	for _, want := range wantSubstrings {
		found := false
		for _, c := range chunks {
			if strings.Contains(c.Header, want) || strings.Contains(c.Text, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no chunk carries %q in header or text; headers: %s", want, joined)
		}
	}
}

// TestChunkDocPlanUnstructuredFallsBackToWindows asserts a plan body with no
// heading structure at all chunks as fixed windows rather than producing
// zero chunks.
func TestChunkDocPlanUnstructuredFallsBackToWindows(t *testing.T) {
	body := "# A Plan\n\n" + bigText(500) + "\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "plan", Title: "A Plan", Body: body}

	chunks := ChunkDoc(doc, nil)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk for an unstructured body under budget, got %d", len(chunks))
	}
	if chunks[0].Anchor != "" {
		t.Errorf("anchor = %q, want \"\"", chunks[0].Anchor)
	}
	if !strings.Contains(chunks[0].Text, "A Plan") {
		t.Errorf("fallback chunk text = %q, want it to include the preamble", chunks[0].Text)
	}
}

// TestChunkDocPlanIndexUniquePerAnchor pins the fix for the duplicate-key
// regression a plan triggers: every plan chunk shares anchor "", so 040 §5's
// (doc_id, anchor, chunk_index) unique index requires Index to keep counting
// across headings rather than restart at 0 for each one.
func TestChunkDocPlanIndexUniquePerAnchor(t *testing.T) {
	body := "# A Plan\n\n" +
		"## First\n\none\n\n" +
		"## Second\n\ntwo\n\n" +
		"## Third\n\nthree\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "plan", Title: "A Plan", Body: body}

	chunks := ChunkDoc(doc, nil)

	seen := map[[2]any]bool{}
	for _, c := range chunks {
		key := [2]any{c.Anchor, c.Index}
		if seen[key] {
			t.Fatalf("duplicate (Anchor, Index) pair %v among chunks %+v", key, chunks)
		}
		seen[key] = true
	}
	// Anchor is always "" here, so distinctness above already forces Index
	// to run 0..n-1 without repeats; assert that explicitly too.
	for i, c := range chunks {
		if c.Anchor != "" {
			continue
		}
		if c.Index != i {
			t.Errorf("chunk %d: Index = %d, want %d (running count across all anchor-\"\" chunks)", i, c.Index, i)
		}
	}
}

// TestChunkTaskEmptyBodyIndexesTitle asserts a task with no body still
// yields one chunk, carrying the title (§4.4).
func TestChunkTaskEmptyBodyIndexesTitle(t *testing.T) {
	task := model.Task{ID: "WL-142", Kind: "feature", State: "in_progress", Title: "Fix the thing"}

	chunks := ChunkTask(task)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "Fix the thing") {
		t.Errorf("chunk text = %q, want it to contain the title", chunks[0].Text)
	}
	if chunks[0].Header != "WL-142 [feature/in_progress] Fix the thing" {
		t.Errorf("header = %q", chunks[0].Header)
	}
}

// TestChunkTaskOverBudgetSplits asserts a task body past ChunkRunes splits
// into multiple chunks, all headed the same way.
func TestChunkTaskOverBudgetSplits(t *testing.T) {
	task := model.Task{ID: "WL-1", Kind: "feature", State: "ready", Title: "Big task", Body: bigText(9000)}

	chunks := ChunkTask(task)
	if len(chunks) < 2 {
		t.Fatalf("want a split body, got %d chunk(s)", len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d: Index = %d", i, c.Index)
		}
		if c.Header != TaskHeader(task) {
			t.Errorf("chunk %d: header = %q, want %q", i, c.Header, TaskHeader(task))
		}
		if got := len([]rune(c.Header + c.Text)); got > ChunkRunes {
			t.Errorf("chunk %d: header+text = %d runes, want <= %d", i, got, ChunkRunes)
		}
	}
}

// TestChunkSkillWindowsDescriptionAndBody is a light sanity check that
// ChunkSkill windows description+SKILL.md the way skillsync does today.
func TestChunkSkillWindowsDescriptionAndBody(t *testing.T) {
	skill := model.Skill{Name: "test-driven-development", Description: "Write the test first"}
	chunks := ChunkSkill(skill, "# TDD\n\nRed, green, refactor.")
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk for a small skill, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "Red, green, refactor") {
		t.Errorf("chunk text = %q, want the SKILL.md body", chunks[0].Text)
	}
	if chunks[0].Header != "skill: test-driven-development — Write the test first" {
		t.Errorf("header = %q", chunks[0].Header)
	}
}

// TestChunkDocSectionsIndexUniquePerAnchor covers the same duplicate-key
// hazard as TestChunkDocPlanIndexUniquePerAnchor on the spec/ADR path: depth
// 5/6 headings legally carry no anchor (§4.2), so several sections share
// anchor "" and must not each restart Index at 0. Anchored sections still
// number from 0 independently of each other.
func TestChunkDocSectionsIndexUniquePerAnchor(t *testing.T) {
	body := "# Spec\n\n" +
		"## 1 First {#sec-1}\n\nfirst body\n\n" +
		"##### Unanchored one\n\nbody one\n\n" +
		"##### Unanchored two\n\nbody two\n"
	doc := model.Doc{ProjectKey: "WL", Kind: "spec", Number: 1, Title: "Spec", Body: body}
	sections := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "First", Position: 0},
		{Anchor: "", Heading: "Unanchored one", Position: 1},
		{Anchor: "", Heading: "Unanchored two", Position: 2},
	}

	chunks := ChunkDoc(doc, sections)
	seen := map[[2]any]bool{}
	for _, c := range chunks {
		key := [2]any{c.Anchor, c.Index}
		if seen[key] {
			t.Fatalf("duplicate (Anchor, Index) pair %v among chunks %+v", key, chunks)
		}
		seen[key] = true
	}
}
