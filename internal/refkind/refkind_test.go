package refkind

import "testing"

func TestRegistry(t *testing.T) {
	seen := map[string]bool{}
	used := map[Target]bool{}
	for _, k := range All {
		if seen[k.Infix] {
			t.Errorf("infix %s registered twice", k.Infix)
		}
		seen[k.Infix] = true
		used[k.Target] = true
		if got, ok := Lookup(k.Infix); !ok || got != k.Target {
			t.Errorf("Lookup(%s) = %q, %v", k.Infix, got, ok)
		}
	}
	for _, target := range Targets {
		if !used[target] {
			t.Errorf("target %s has no infix", target)
		}
	}
	if got := Alternation(Doc); got != "SPEC|ADR|PLAN" {
		t.Errorf("Alternation(Doc) = %q", got)
	}
}
