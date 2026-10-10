package store

import (
	"errors"
	"testing"
)

// TestDocCoversRejections covers the covers defects a single header settles
// on its own (WL-RULE-202, WL-REQ-207): the key on a document that is not a
// plan, both spellings of it at once, an entry naming no spec, and the
// retired `coverage` and `fullCoverageWith` keys.
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
  - fullCoverageWith: [x.md]
---

# A plan
`},
		{"retired coverage key", "plan", `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-5
    coverage: full
---

# A plan
`},
		{"retired level", "plan", `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-5
    coverage: partial
---

# A plan
`},
		{"retired fullCoverageWith key", "plan", `---
status: draft
covers:
  - spec: 025-documents-in-the-backbone.md#sec-5
    fullCoverageWith: [x.md]
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

// TestDocCoversPlainEntries pins what the rejections above must not catch:
// a bare reference and a mapping naming only `spec` both write a covers edge.
func TestDocCoversPlainEntries(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 1, Slug: "plan-1", CreatedBy: "stig",
		Body: "---\nstatus: draft\ncovers:\n  - 025-documents-in-the-backbone.md#sec-1\n" +
			"  - spec: 025-documents-in-the-backbone.md#sec-2\n---\n\n# A plan\n",
	})
	var n int
	if err := s.db.QueryRow(
		`SELECT count(DISTINCT dr.anchor) FROM doc_edges e JOIN doc_entries dr ON dr.rule_id = e.to_rule
		  WHERE e.from_doc = $1 AND e.type = 'covers' AND dr.doc_id = $2 AND dr.anchor IN ('sec-1', 'sec-2')`,
		plan.ID, spec.ID).Scan(&n); err != nil {
		t.Fatalf("read covers edges: %v", err)
	}
	if n != 2 {
		t.Errorf("covered sections = %d, want 2", n)
	}
}
