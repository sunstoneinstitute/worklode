package implements_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
)

const (
	wlImplements    = "https://worklode.io/ns/ontology#implements"
	wlPinnedVersion = "https://worklode.io/ns/ontology#pinnedVersion"
	rdfReifies      = "http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies"
	idNS            = "https://worklode.io/ns/id/"
)

// lines renders the three N-Triples lines one claim contributes: the
// asserted edge, the reifier's rdf:reifies link to its triple term, and the
// pin on that reifier.
func lines(component, section, pinned string) []string {
	reifier := idNS + "claim/" +
		strings.TrimPrefix(component, idNS) + "/" + strings.TrimPrefix(section, idNS)
	return []string{
		"<" + component + "> <" + wlImplements + "> <" + section + "> .",
		"<" + reifier + "> <" + rdfReifies + "> <<( <" + component + "> <" + wlImplements + "> <" + section + "> )>> .",
		"<" + reifier + "> <" + wlPinnedVersion + "> <" + pinned + "> .",
	}
}

func TestTriples(t *testing.T) {
	const (
		worklode = idNS + "component/github.com/sunstoneinstitute/worklode"
		ingest   = idNS + "component/github.com/sunstoneinstitute/research-stack/ingest"
		sec4     = idNS + "section/spec-worklode-004/sec-4"
		sec31    = idNS + "section/spec-worklode-013/sec-3.1"
		v2       = idNS + "doc/spec-worklode-004/v2"
		v1       = idNS + "doc/spec-worklode-013/v1"
	)

	cases := []struct {
		name   string
		claims []implements.Claim
		want   []string
	}{
		{
			name:   "one claim",
			claims: []implements.Claim{{Component: worklode, Section: sec4, Pinned: v2}},
			want:   lines(worklode, sec4, v2),
		},
		{
			name: "multi-claim, one component",
			claims: []implements.Claim{
				{Component: worklode, Section: sec4, Pinned: v2},
				{Component: worklode, Section: sec31, Pinned: v1},
			},
			want: append(lines(worklode, sec4, v2), lines(worklode, sec31, v1)...),
		},
		{
			name: "one section, split across components",
			claims: []implements.Claim{
				{Component: worklode, Section: sec4, Pinned: v2},
				{Component: ingest, Section: sec4, Pinned: v2},
			},
			want: append(lines(worklode, sec4, v2), lines(ingest, sec4, v2)...),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(graphproj.Document(implements.Triples(tc.claims)))
			for _, want := range tc.want {
				if !strings.Contains(got, want+"\n") {
					t.Errorf("missing line:\n%s\ngot:\n%s", want, got)
				}
			}
			if n := strings.Count(got, "\n"); n != len(tc.want) {
				t.Errorf("document has %d lines, want %d:\n%s", n, len(tc.want), got)
			}
		})
	}
}

// Two claims on one section differing only in pin (two entries pre-dedupe,
// or historic data) collapse to the first claim's lines: one edge, one
// reifier, one pin — wl:pinnedVersion is functional.
func TestTriplesDedupesOnTheEdge(t *testing.T) {
	claims := []implements.Claim{
		{Component: idNS + "component/c", Section: idNS + "section/d/sec-1", Pinned: idNS + "doc/d/v1"},
		{Component: idNS + "component/c", Section: idNS + "section/d/sec-1", Pinned: idNS + "doc/d/v2"},
	}
	got := string(graphproj.Document(implements.Triples(claims)))
	want := lines(idNS+"component/c", idNS+"section/d/sec-1", idNS+"doc/d/v1")
	slices.Sort(want)
	if w := strings.Join(want, "\n") + "\n"; got != w {
		t.Fatalf("Document =\n%s\nwant\n%s", got, w)
	}
}
