package graphproj

import (
	"bytes"
	"testing"
)

func TestTermRendering(t *testing.T) {
	cases := []struct {
		name string
		term Term
		want string
	}{
		{"iri", IRIRef("https://worklode.io/ns/id/task/WL-1"),
			"<https://worklode.io/ns/id/task/WL-1>"},
		{"plain literal", Text("fix login"), `"fix login"`},
		{"quote escaped", Text(`say "hi"`), `"say \"hi\""`},
		{"backslash escaped", Text(`a\b`), `"a\\b"`},
		{"newline escaped", Text("a\nb"), `"a\nb"`},
		{"typed literal", Typed("2026-07-30T00:00:00Z", "http://www.w3.org/2001/XMLSchema#dateTime"),
			`"2026-07-30T00:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.term.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}

func TestDocumentIsDeterministic(t *testing.T) {
	fwd := []Triple{
		{S: "urn:s", P: "urn:p", O: IRIRef("urn:o")},
		{S: "urn:s", P: "urn:q", O: Text("v")},
		{S: "urn:a", P: "urn:p", O: Text("w")},
	}
	rev := []Triple{fwd[2], fwd[1], fwd[0], fwd[0]} // reordered + duplicate
	want := "<urn:a> <urn:p> \"w\" .\n" +
		"<urn:s> <urn:p> <urn:o> .\n" +
		"<urn:s> <urn:q> \"v\" .\n"
	if got := Document(fwd); string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !bytes.Equal(Document(fwd), Document(rev)) {
		t.Fatal("Document is order- or duplicate-sensitive; must be byte-identical")
	}
}
