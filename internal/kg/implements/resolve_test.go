package implements_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const twoComponentManifest = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["internal/ingest/**"]
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/pfas
    name: pfas
    paths: ["internal/pfas/**"]
`

func parseBoth(t *testing.T, implYAML string) (*implements.File, *manifest.Manifest) {
	t.Helper()
	f, err := implements.Parse([]byte(implYAML))
	if err != nil {
		t.Fatalf("parse implements: %v", err)
	}
	m, err := manifest.Parse([]byte(twoComponentManifest))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return f, m
}

// AC6b: paths spanning two components yield two claims, same pin each; a
// path matching no component is an error naming the path.
func TestResolveSplitsAcrossComponents(t *testing.T) {
	f, m := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/reader.go, internal/pfas/model.go]
`)
	claims, err := implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("claims = %+v; want 2 (one per component)", claims)
	}
	for _, c := range claims {
		if c.Section != "https://worklode.io/ns/id/section/spec-worklode-004/sec-4" ||
			c.Pinned != "https://worklode.io/ns/id/doc/spec-worklode-004/v2" {
			t.Fatalf("claim %+v carries the wrong section/pin", c)
		}
	}
	if claims[0].Component == claims[1].Component {
		t.Fatalf("both claims name %s; want one per component", claims[0].Component)
	}
}

func TestResolveUnmatchedPathIsError(t *testing.T) {
	f, m := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [README.md]
`)
	_, err := implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v; want an error naming README.md", err)
	}
}

// AC6a: no components.yaml → the implicit component, IRI = repo coords; an
// explicit whole-repo manifest naming the same IRI leaves subjects unchanged.
func TestResolveImplicitComponent(t *testing.T) {
	f, err := implements.Parse([]byte(`
implements:
  - section: wlid:section/spec-worklode-014/sec-6
    pinned:  wlid:doc/spec-worklode-014/v1
    by:      [internal/kg/implements/implements.go]
`))
	if err != nil {
		t.Fatalf("parse implements: %v", err)
	}
	claims, err := implements.Resolve(f, nil, "github.com/sunstoneinstitute/worklode")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode"
	if len(claims) != 1 || claims[0].Component != want {
		t.Fatalf("claims = %+v; want one from %s", claims, want)
	}

	wholeRepo, err := manifest.Parse([]byte(`
repo: github.com/sunstoneinstitute/worklode
components:
  - iri: ` + want + `
    name: worklode
    paths: ["**"]
`))
	if err != nil {
		t.Fatalf("parse whole-repo manifest: %v", err)
	}
	explicit, err := implements.Resolve(f, wholeRepo, "github.com/sunstoneinstitute/worklode")
	if err != nil {
		t.Fatalf("Resolve with explicit manifest: %v", err)
	}
	if len(explicit) != 1 || explicit[0] != claims[0] {
		t.Fatalf("promotion changed the claim: implicit %+v, explicit %+v", claims, explicit)
	}
}

func TestResolveDeduplicatesAndRejectsConflictingPins(t *testing.T) {
	f, m := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/a.go]
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/b.go]
`)
	claims, err := implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %+v; want the duplicate collapsed", claims)
	}

	f2, _ := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v1
    by:      [internal/ingest/a.go]
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/b.go]
`)
	if _, err := implements.Resolve(f2, m, "github.com/sunstoneinstitute/research-stack"); err == nil {
		t.Fatal("conflicting pins for one (component, section) resolved without error")
	}
}

// A component IRI is a triple subject, so it has to be an absolute IRI.
// manifest.Parse only requires iri: non-empty, which lets a manifest declare
// a bare name that would render as <ingest> — not a legal N-Triples subject.
func TestResolveRejectsRelativeComponentIRI(t *testing.T) {
	f, err := implements.Parse([]byte(`
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/reader.go]
`))
	if err != nil {
		t.Fatalf("parse implements: %v", err)
	}
	m, err := manifest.Parse([]byte(`
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: ingest
    name: ingest
    paths: ["internal/ingest/**"]
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	_, err = implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err == nil || !strings.Contains(err.Error(), "ingest") {
		t.Fatalf("err = %v; want an error naming the relative component IRI", err)
	}
}
