package implements_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
)

const specExample = `
# .worklode/implements.yaml
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2     # version validated against
    by:      [internal/store/lease.go, internal/store/sweeper.go]
  - section: wlid:section/spec-worklode-013/sec-3.1
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
	if e.Section != "https://worklode.io/ns/id/section/spec-worklode-004/sec-4" {
		t.Fatalf("section = %q", e.Section)
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
  - section: https://worklode.io/ns/id/section/spec-worklode-014/sec-3
    pinned:  https://worklode.io/ns/id/doc/spec-worklode-014/v1
    by:      [internal/kg/section/section.go]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Implements[0].Section != "https://worklode.io/ns/id/section/spec-worklode-014/sec-3" {
		t.Fatalf("section = %q", f.Implements[0].Section)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"not yaml", "{nope"},
		{"no entries", "implements: []"},
		{"not a section IRI", "implements: [{section: 'wlid:doc/x', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"bad anchor", "implements: [{section: 'wlid:section/x/NOT-AN-ANCHOR', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"pinned not versioned", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x', by: [a.go]}]"},
		{"pinned version zero", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v0', by: [a.go]}]"},
		{"pin names another doc", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/y/v1', by: [a.go]}]"},
		{"no paths", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: []}]"},
		{"absolute path", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: [/etc/passwd]}]"},
		{"dotdot path", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: ['../other/a.go']}]"},
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
