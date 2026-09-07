package store

import (
	"errors"
	"testing"
)

// TestDocCoversRejections covers the three 026 §5.1 defects a single header
// settles on its own, which nothing enforced after scripts/secmeta.py went
// with the file corpus (055 §4): the key on a document that is not a plan,
// both spellings of it at once, and a qualified entry missing a required key.
func TestDocCoversRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, kind, body string
	}{
		{"on a spec", "spec", `---
status: draft
covers: 025-documents-in-the-backbone.md#sec-5
---

# A spec

## 1. Scope {#sec-1}

Scope body.
`},
		{"both spellings", "plan", `---
status: draft
covers: 025-documents-in-the-backbone.md#sec-5
implements: 025-documents-in-the-backbone.md#sec-5
---

# A plan
`},
		{"entry without spec", "plan", `---
status: draft
covers:
  - coverage: full
---

# A plan
`},
		{"entry without coverage", "plan", `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-5
---

# A plan
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openDocStore(t)
			mustCreateDoc(t, s, DocInput{
				Project: "p1", Kind: "spec", Number: 25,
				Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
			})
			_, err := createDoc(t, s, DocInput{
				Project: "p1", Kind: tc.kind, Number: 90, Slug: "090-x",
				Body: tc.body, CreatedBy: "stig",
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestDocCoversBareFormStillFull pins what the missing-coverage rejection
// above must not catch: the bare form is the qualified entry with
// `coverage: full` (026 §5.1), not an entry that omitted the key.
func TestDocCoversBareFormStillFull(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 1, Slug: "plan-1",
		Body: planBody, CreatedBy: "stig",
	})
	var level string
	if err := s.db.QueryRow(
		`SELECT coverage FROM doc_edges
		  WHERE from_doc = $1 AND type = 'covers' AND to_doc = $2`,
		plan.ID, spec.ID).Scan(&level); err != nil {
		t.Fatalf("read covers edge: %v", err)
	}
	if level != "full" {
		t.Errorf("coverage = %q, want full", level)
	}
}
