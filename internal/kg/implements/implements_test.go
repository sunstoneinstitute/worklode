package implements_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

const specExample = `
# .worklode/implements.yaml
implements:
  - rule:    wlid:rule/worklode/165
    pinned:  wlid:doc/spec-worklode-004/v2     # version validated against
    by:      [internal/store/lease.go, internal/store/sweeper.go]
  - rule:    wlid:rule/worklode/166
    pinned:  wlid:doc/spec-worklode-013/v1
    by:      [internal/hooks/apply.go]
`

func TestParseSpecExample(t *testing.T) {
	f, err := implements.Parse([]byte(specExample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Implements) != 2 {
		t.Fatalf("entries = %d; want 2", len(f.Implements))
	}
	e := f.Implements[0]
	// The wlid: CURIE expands to the full instance IRI.
	if e.Rule != "https://worklode.io/ns/id/rule/worklode/165" {
		t.Fatalf("rule = %q", e.Rule)
	}
	if e.Pinned != "https://worklode.io/ns/id/doc/spec-worklode-004/v2" {
		t.Fatalf("pinned = %q", e.Pinned)
	}
	if len(e.By) != 2 || e.By[0] != "internal/store/lease.go" {
		t.Fatalf("by = %v", e.By)
	}
}

func TestParseAcceptsFullIRIs(t *testing.T) {
	f, err := implements.Parse([]byte(`
implements:
  - rule:    https://worklode.io/ns/id/rule/worklode/14
    pinned:  https://worklode.io/ns/id/doc/spec-worklode-014/v1
    by:      [internal/kg/section/section.go]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Implements[0].Rule != "https://worklode.io/ns/id/rule/worklode/14" {
		t.Fatalf("rule = %q", f.Implements[0].Rule)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"not yaml", "{nope"},
		{"no entries", "implements: []"},
		{"not a rule IRI", "implements: [{rule: 'wlid:doc/x', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"a section IRI", "implements: [{rule: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"the retired section key", "implements: [{section: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"rule number not a number", "implements: [{rule: 'wlid:rule/x/sec-1', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"rule number zero", "implements: [{rule: 'wlid:rule/x/0', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"pinned version out of range", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v99999999999999999999', by: [a.go]}]"},
		// WL-REQ-177: the claiming component is derived, never declared, so a
		// component: key must fail rather than be quietly ignored.
		{"declares a component", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: [a.go], component: 'wlid:component/x'}]"},
		{"unknown top-level key", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: [a.go]}]\ncovers: [y]"},
		{"project id with a space", "implements: [{rule: 'wlid:rule/two words/1', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"doc slug with an angle bracket", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x>y/v1', by: [a.go]}]"},
		{"empty project id", "implements: [{rule: 'wlid:rule//1', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"pinned not versioned", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x', by: [a.go]}]"},
		{"pinned version zero", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v0', by: [a.go]}]"},
		{"no paths", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: []}]"},
		{"absolute path", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: [/etc/passwd]}]"},
		{"dotdot path", "implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: ['../other/a.go']}]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if f, err := implements.Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse accepted %+v; want an error", f)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := implements.Load(filepath.Join(t.TempDir(), "implements.yaml"))
	if !os.IsNotExist(err) {
		t.Fatalf("Load on a missing file: %v; want os.IsNotExist", err)
	}
}

// Parse's output is whatever internal/kg/iri mints, not the input spelling.
// Today the two agree for every accepted input, so this cannot fail on the
// current code — it is here to fail the day iri.Rule or iri.DocVersion
// changes shape without this package noticing.
func TestParseMintsCanonicalIRIs(t *testing.T) {
	f, err := implements.Parse([]byte(specExample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	e := f.Implements[0]
	if want := iri.Rule("worklode", 165); e.Rule != want {
		t.Errorf("rule = %q; want the minted %q", e.Rule, want)
	}
	if want := iri.DocVersion("spec-worklode-004", 2); e.Pinned != want {
		t.Errorf("pinned = %q; want the minted %q", e.Pinned, want)
	}
}

// A leading ".." segment is what makes a path escape the repo; a file whose
// name merely starts with dots does not.
func TestParseAcceptsDotPrefixedNames(t *testing.T) {
	f, err := implements.Parse([]byte(
		"implements: [{rule: 'wlid:rule/x/1', pinned: 'wlid:doc/x/v1', by: ['..hidden/a.go']}]"))
	if err != nil {
		t.Fatalf("Parse rejected a ..hidden path: %v", err)
	}
	if got := f.Implements[0].By[0]; got != "..hidden/a.go" {
		t.Fatalf("by[0] = %q; want ..hidden/a.go", got)
	}
}
