package ns_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestGeneratedListsAreNonEmpty is the cheap smoke test that the generator
// ran at all: an empty slice would silently turn validKinds into a gate that
// rejects everything.
func TestGeneratedListsAreNonEmpty(t *testing.T) {
	if len(ns.TaskKinds) == 0 {
		t.Error("ns.TaskKinds is empty — run ./scripts/nsgen.py")
	}
	if len(ns.DesignDocStatuses) == 0 {
		t.Error("ns.DesignDocStatuses is empty — run ./scripts/nsgen.py")
	}
	if !slices.IsSorted(ns.TaskKinds) {
		t.Errorf("ns.TaskKinds = %v, want alphabetical", ns.TaskKinds)
	}
}

// TestTaskStateShapeMatchesStateMachine pins ns/shapes.ttl's wl:taskState
// sh:in list to the states store's transition table can reach. The shape is
// hand-written Turtle with no generator behind it (the state machine is Go,
// not an enum in ns/concept.ttl), so this test is what keeps the duplicate
// honest — docs/follow-ups.md flagged exactly this drift.
func TestTaskStateShapeMatchesStateMachine(t *testing.T) {
	shapes, err := os.ReadFile(filepath.Join("..", "..", "ns", "shapes.ttl"))
	if err != nil {
		t.Fatalf("read ns/shapes.ttl: %v", err)
	}

	// The sh:in list on the property shape whose sh:path is wl:taskState.
	// [^]]*? keeps the match inside the one property shape's brackets.
	re := regexp.MustCompile(`sh:path wl:taskState ;[^\]]*?sh:in \(([^)]*)\)`)
	m := re.FindSubmatch(shapes)
	if m == nil {
		t.Fatal("no `sh:path wl:taskState` property shape with an sh:in list in ns/shapes.ttl")
	}
	inShape := strings.FieldsFunc(string(m[1]), func(r rune) bool {
		return r == '"' || r == ' ' || r == '\n' || r == '\t'
	})
	slices.Sort(inShape)

	if want := store.AllStates(); !slices.Equal(inShape, want) {
		t.Errorf("wl:taskState sh:in = %v, want %v\n"+
			"ns/shapes.ttl and internal/store's legalTransitions disagree; widen both together",
			inShape, want)
	}
}
