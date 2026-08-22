package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const twoComponents = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["cmd/ingest/**", "internal/ingest/**"]
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/pfas
    name: pfas
    paths: ["internal/**"]
`

func TestParseAndMatch(t *testing.T) {
	m, err := manifest.Parse([]byte(twoComponents))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Repo != "github.com/sunstoneinstitute/research-stack" {
		t.Fatalf("Repo = %q", m.Repo)
	}

	cases := []struct {
		path string
		want string // component name; "" = no component
	}{
		{"cmd/ingest/main.go", "ingest"},
		{"cmd/ingest/deep/nested/file.go", "ingest"},
		{"internal/ingest/reader.go", "ingest"}, // first match wins over pfas's internal/**
		{"internal/pfas/model.go", "pfas"},
		{"internal/x.go", "pfas"},
		{"cmd/ingestx/main.go", ""}, // * and ** do not cross a segment name
		{"README.md", ""},           // unmatched: no component, reported as a gap by callers
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			c, ok := m.Match(tc.path)
			switch {
			case tc.want == "" && ok:
				t.Fatalf("Match(%q) = %s; want no component", tc.path, c.Name)
			case tc.want != "" && (!ok || c.Name != tc.want):
				t.Fatalf("Match(%q) = %v, %v; want %s", tc.path, c, ok, tc.want)
			}
		})
	}
}

func TestGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**", "anything/at/all.go", true},
		{"**", "top.go", true},
		{"cmd/ingest/**", "cmd/ingest", true}, // ** matches zero segments
		{"cmd/*/main.go", "cmd/ingest/main.go", true},
		{"cmd/*/main.go", "cmd/ingest/sub/main.go", false},
		{"internal/**/db/**", "internal/a/b/db/x.go", true},
		{"internal/**/db/**", "internal/db/x.go", true},
		{"internal/**/db/**", "internal/a/x.go", false},
		{"*.go", "main.go", true},
		{"*.go", "cmd/main.go", false},
	}
	for _, tc := range cases {
		m := mustParse(t, `
repo: r
components:
  - iri: https://worklode.io/ns/id/component/r/c
    name: c
    paths: ["`+tc.pattern+`"]
`)
		_, ok := m.Match(tc.path)
		if ok != tc.want {
			t.Errorf("pattern %q vs %q = %v; want %v", tc.pattern, tc.path, ok, tc.want)
		}
	}
}

// TestGlobBacktrackingIsBounded is the WL-265 regression case: a pattern of
// 25 alternating "**"/literal segments matched against a path that holds
// every "a" segment but not the trailing literal. Unmemoized, this pattern
// shape is exponential in the number of "**" segments (the WL-120 review
// measured ~3.2 billion recursive calls, ~20s, for this exact case); with
// matchSegs memoized on (pattern index, segment index) it must finish
// quickly regardless.
func TestGlobBacktrackingIsBounded(t *testing.T) {
	var patSegs []string
	for i := 0; i < 12; i++ {
		patSegs = append(patSegs, "**", "a")
	}
	patSegs = append(patSegs, "ZZZ") // never present, so every "**" is tried in full
	pattern := strings.Join(patSegs, "/")

	pathSegs := make([]string, 30)
	for i := range pathSegs {
		pathSegs[i] = "a"
	}
	pathSegs[len(pathSegs)-1] = "not-zzz" // holds every "a" but not the trailing literal
	p := strings.Join(pathSegs, "/")

	m := mustParse(t, `
repo: r
components:
  - iri: https://worklode.io/ns/id/component/r/c
    name: c
    paths: ["`+pattern+`"]
`)

	done := make(chan bool, 1)
	start := time.Now()
	go func() {
		_, ok := m.Match(p)
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Fatalf("Match(%q) against pattern %q = true; want false (no ZZZ segment)", p, pattern)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("Match took %s; want well under the multi-second blowup this regression guards against", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Match did not return within 5s; ** backtracking is unbounded again")
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"not yaml", "{nope"},
		{"missing repo", "components: [{iri: i, name: n, paths: ['**']}]"},
		{"no components", "repo: r\ncomponents: []"},
		{"component without iri", "repo: r\ncomponents: [{name: n, paths: ['**']}]"},
		{"component without name", "repo: r\ncomponents: [{iri: i, paths: ['**']}]"},
		{"component without paths", "repo: r\ncomponents: [{iri: i, name: n}]"},
		{"bad glob", "repo: r\ncomponents: [{iri: i, name: n, paths: ['[']}]"},
		{"leading slash", "repo: r\ncomponents: [{iri: i, name: n, paths: ['/internal/**']}]"},
		{"trailing slash", "repo: r\ncomponents: [{iri: i, name: n, paths: ['internal/**/']}]"},
		{"doubled slash", "repo: r\ncomponents: [{iri: i, name: n, paths: ['internal//**']}]"},
		{"duplicate name", "repo: r\ncomponents: [{iri: i1, name: n, paths: ['a/**']}, {iri: i2, name: n, paths: ['b/**']}]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if m, err := manifest.Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse accepted %+v; want an error", m)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := manifest.Load(filepath.Join(t.TempDir(), "components.yaml")); !os.IsNotExist(err) {
		t.Fatalf("Load on a missing file: %v; want os.IsNotExist", err)
	}
}

func TestWorklodeRepoManifest(t *testing.T) {
	m, err := manifest.Load(filepath.Join("..", "..", "..", ".worklode", "components.yaml"))
	if err != nil {
		t.Fatalf("load repo manifest: %v", err)
	}
	if m.Repo != "github.com/sunstoneinstitute/worklode" {
		t.Fatalf("Repo = %q", m.Repo)
	}
	c, ok := m.Match("internal/store/tasks.go")
	if !ok || c.Name != "worklode" {
		t.Fatalf("Match = %v, %v; want the whole-repo worklode component", c, ok)
	}
	if want := iri.Component(m.Repo); c.IRI != want {
		t.Fatalf("component IRI = %q; want %q (006 scheme)", c.IRI, want)
	}
}

func mustParse(t *testing.T, y string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}
