package ns_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// conceptsInScheme reads ns/concept.ttl and returns the local names declared
// in one scheme, in file order. It re-derives with a regexp what
// scripts/nsgen.py derives with a parser, so a bug in the parser — or a
// gen.go that was never regenerated — shows up as a disagreement between two
// independent readings of the same Turtle. `go test` alone therefore catches
// the drift that scripts/nsgen.py --check catches in CI.
func conceptsInScheme(t *testing.T, scheme string) []string {
	t.Helper()
	ttl, err := os.ReadFile(filepath.Join("..", "..", "ns", "concept.ttl"))
	if err != nil {
		t.Fatalf("read ns/concept.ttl: %v", err)
	}
	re := regexp.MustCompile(`wlc:(\w+) a skos:Concept ; skos:inScheme wlc:` + scheme + `\b`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(ttl), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("no wlc:%s concepts found in ns/concept.ttl", scheme)
	}
	return out
}

// TestTaskKindsMatchTurtle pins the generated slice to wlc:TaskKind, and to
// the alphabetical order internal/api's error message depends on.
func TestTaskKindsMatchTurtle(t *testing.T) {
	want := conceptsInScheme(t, "TaskKind")
	slices.Sort(want)
	if !slices.Equal(ns.TaskKinds, want) {
		t.Errorf("ns.TaskKinds = %v, want %v — run ./scripts/nsgen.py", ns.TaskKinds, want)
	}
}

// TestDesignDocStatusesMatchTurtle pins the generated slice to
// wlc:DesignDocStatus. Both internal/api and internal/store now gate document
// writes on it, so a wrong list here is a validation bug, not a cosmetic one.
// The order is checked separately: it is wlc:DesignDocStatusOrder's lifecycle
// order, not alphabetical, and sorting would mean the generator dropped the
// ordered collection.
func TestDesignDocStatusesMatchTurtle(t *testing.T) {
	want := conceptsInScheme(t, "DesignDocStatus")
	got := slices.Clone(ns.DesignDocStatuses)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ns.DesignDocStatuses = %v, want the %v of ns/concept.ttl — run ./scripts/nsgen.py",
			ns.DesignDocStatuses, want)
	}
	if lifecycle := []string{"draft", "accepted", "superseded"}; !slices.Equal(ns.DesignDocStatuses, lifecycle) {
		t.Errorf("ns.DesignDocStatuses = %v, want %v (wlc:DesignDocStatusOrder's order)",
			ns.DesignDocStatuses, lifecycle)
	}
}

// TestOrList renders the closed-set phrasing the 422 bodies use, including
// the two short cases that are easy to get wrong.
func TestOrList(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a or b"},
		{[]string{"a", "b", "c"}, "a, b, or c"},
	} {
		if got := ns.OrList(tc.in); got != tc.want {
			t.Errorf("OrList(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSet turns an enum into the lookup map the validation gates use.
func TestSet(t *testing.T) {
	got := ns.Set([]string{"a", "b"})
	if !got["a"] || !got["b"] || got["c"] || len(got) != 2 {
		t.Errorf("Set([a b]) = %v", got)
	}
}
