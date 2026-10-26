package inlineref

import (
	"encoding/json"
	"testing"
)

func TestJevQuestions(t *testing.T) {
	node := JevNode{Kind: "task", Title: "Ship it", Text: "Intro.\n\nFirst line\nwaits for WL-5 to land\nlast line\n\nOther."}
	refs := []JevRef{
		{Kind: "task", Title: "Build it", Line: 4},
		{Kind: "milestone", Title: "M1", Line: 4}, // no candidates: skipped
		{Kind: "rule", Title: "A rule", Line: 1},
	}
	state, qs := JevQuestions(node, refs)
	if len(qs) != 2 {
		t.Fatalf("questions %v", qs)
	}
	q := qs["r0"]
	if q.Type != "choice" || q.Instructions != "Which relation does `node` have to `refs.r0.target`, as stated by `refs.r0.line` and `refs.r0.paragraph`?" {
		t.Errorf("r0 = %+v", q)
	}
	if len(q.Criteria) != 5 || q.Criteria["dependsOn"] != "depends on" || q.Criteria["references"] != referencesCriterion {
		t.Errorf("r0 criteria %v", q.Criteria)
	}
	if c := qs["r2"].Criteria; len(c) != 2 || c["governedBy"] != "is governed by" {
		t.Errorf("r2 criteria %v", c)
	}
	b, _ := json.Marshal(state)
	want := `{"node":{"kind":"task","title":"Ship it"},"refs":{` +
		`"r0":{"line":"waits for WL-5 to land","paragraph":"First line\nwaits for WL-5 to land\nlast line","target":{"kind":"task","title":"Build it"}},` +
		`"r2":{"line":"Intro.","paragraph":"Intro.","target":{"kind":"rule","title":"A rule"}}}}`
	if string(b) != want {
		t.Errorf("state\n got %s\nwant %s", b, want)
	}
}
