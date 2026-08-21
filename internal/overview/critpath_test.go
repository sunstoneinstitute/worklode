package overview

import (
	"reflect"
	"sort"
	"testing"
)

// Edges are (from, to) = "from must be done before to" — a blocks edge or a
// reversed dependsOn edge, normalized by the caller.
func TestAnalyzeKnownDAG(t *testing.T) {
	//   A → B → C
	//        ↘  D
	//   E (isolated)
	a := Analyze([][2]string{{"A", "B"}, {"B", "C"}, {"B", "D"}}, []string{"E"})

	wantDepth := map[string]int{"A": 0, "B": 1, "C": 2, "D": 2, "E": 0}
	if !reflect.DeepEqual(a.Depth, wantDepth) {
		t.Fatalf("Depth = %v; want %v", a.Depth, wantDepth)
	}
	wantFan := map[string]int{"A": 3, "B": 2, "C": 0, "D": 0, "E": 0}
	if !reflect.DeepEqual(a.FanOut, wantFan) {
		t.Fatalf("FanOut = %v; want %v", a.FanOut, wantFan)
	}
	// Longest chain is length 2 (A→B→C and A→B→D): A, B, C, D all critical.
	for _, n := range []string{"A", "B", "C", "D"} {
		if !a.Critical[n] {
			t.Errorf("%s not critical; want on a longest chain", n)
		}
	}
	if a.Critical["E"] {
		t.Error("isolated E marked critical")
	}
	if len(a.Cycles) != 0 {
		t.Fatalf("Cycles = %v; want none", a.Cycles)
	}
}

func TestAnalyzeExcludesAndSurfacesCycle(t *testing.T) {
	// X ↔ Y is a cycle; A → B is healthy and must keep correct numbers.
	a := Analyze([][2]string{{"X", "Y"}, {"Y", "X"}, {"A", "B"}}, nil)

	if len(a.Cycles) != 1 {
		t.Fatalf("Cycles = %v; want one", a.Cycles)
	}
	got := append([]string(nil), a.Cycles[0]...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"X", "Y"}) {
		t.Fatalf("cycle members = %v; want [X Y]", got)
	}
	for _, n := range []string{"X", "Y"} {
		if _, ok := a.Depth[n]; ok {
			t.Errorf("%s in Depth; cycle members must be excluded, not looped over", n)
		}
	}
	if a.Depth["B"] != 1 || a.FanOut["A"] != 1 {
		t.Fatalf("healthy chain wrong: depth[B]=%d fanout[A]=%d", a.Depth["B"], a.FanOut["A"])
	}
}

func TestAnalyzeSelfLoopIsACycle(t *testing.T) {
	a := Analyze([][2]string{{"X", "X"}}, nil)
	if len(a.Cycles) != 1 || len(a.Cycles[0]) != 1 || a.Cycles[0][0] != "X" {
		t.Fatalf("Cycles = %v; want [[X]]", a.Cycles)
	}
}
