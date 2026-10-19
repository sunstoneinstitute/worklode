package mcp

import (
	"slices"
	"testing"
)

func TestKindAliasesTable(t *testing.T) {
	rows := map[string]KindTarget{}
	for _, r := range []struct {
		entity, kind string
		words        []string
	}{
		{"task", "bug", []string{"bug", "defect", "problem", "issue", "broken"}},
		{"task", "feature", []string{"feature", "enhancement", "story", "capability"}},
		{"task", "chore", []string{"chore", "cleanup", "maintenance", "housekeeping", "upgrade"}},
		{"task", "spike", []string{"spike", "experiment", "investigation", "prototype"}},
		{"task", "decision", []string{"decision", "question", "choice"}},
		{"task", "review", []string{"review"}},
		{"task", "design", []string{"design task"}},
		{"doc", "spec", []string{"spec", "specification", "design doc", "design document", "ADR"}},
		{"doc", "plan", []string{"plan", "implementation plan"}},
		{"rule", "requirement", []string{"requirement", "rule"}},
		{"rule", "invariant", []string{"invariant"}},
		{"rule", "definition", []string{"definition"}},
		{"rule", "principle", []string{"principle"}},
		{"rule", "catalogue", []string{"catalogue"}},
	} {
		for _, w := range r.words {
			rows[normalizeKindWord(w)] = KindTarget{r.entity, r.kind}
			kind, amb, closest := resolveKind(" " + w + " ")
			if kind != r.kind || amb || closest != nil {
				t.Errorf("resolveKind(%q) = %q, %v, %v", w, kind, amb, closest)
			}
		}
	}
	if len(rows) != len(KindAliases) {
		t.Errorf("table has %d words, want %d", len(KindAliases), len(rows))
	}
	for w, want := range rows {
		if KindAliases[w] != want {
			t.Errorf("KindAliases[%q] = %v, want %v", w, KindAliases[w], want)
		}
	}
}

func TestResolveKind(t *testing.T) {
	if kind, amb, _ := resolveKind("Design"); kind != "" || !amb {
		t.Errorf("design: %q, %v", kind, amb)
	}
	kind, amb, closest := resolveKind("bgu")
	if kind != "" || amb || len(closest) != 3 || !slices.Contains(closest, "bug") {
		t.Errorf("bgu: %q, %v, %v", kind, amb, closest)
	}
	if kind, _, _ := resolveKind("Design Doc"); kind != "spec" || KindAliases["design doc"].Entity != "doc" {
		t.Errorf("Design Doc: %q", kind)
	}
}
