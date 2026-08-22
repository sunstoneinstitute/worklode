package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

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

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"not yaml", "{nope"},
		{"missing repo", "components: [{iri: i, name: n, paths: ['**']}]"},
		{"no components", "repo: r\ncomponents: []"},
		{"component without iri", "repo: r\ncomponents: [{name: n, paths: ['**']}]"},
		{"component without name", "repo: r\ncomponents: [{iri: i, paths: ['**']}]"},
		{"component without paths", "repo: r\ncomponents: [{iri: i, name: n}]"},
		{"bad glob", "repo: r\ncomponents: [{iri: i, name: n, paths: ['[']}]"},
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
