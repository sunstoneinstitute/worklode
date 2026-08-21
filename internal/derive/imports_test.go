package derive_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const importsManifest = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["cmd/ingest/**", "internal/ingest/**"]
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv
    name: graphsrv
    paths: ["cmd/graph-server/**", "internal/graph/**"]
`

// goListStream mimics go list -deps -json ./... : one JSON object per
// package. Dir/Module.Dir use the module root /r.
const goListStream = `
{"ImportPath":"example.com/rs/internal/ingest","Dir":"/r/internal/ingest",
 "GoFiles":["ingest.go"],"Module":{"Path":"example.com/rs","Dir":"/r"},
 "Imports":["example.com/rs/internal/graph","fmt"]}
{"ImportPath":"example.com/rs/internal/graph","Dir":"/r/internal/graph",
 "GoFiles":["graph.go"],"Module":{"Path":"example.com/rs","Dir":"/r"},
 "Imports":["fmt"]}
{"ImportPath":"example.com/rs/internal/ingest/parse","Dir":"/r/internal/ingest/parse",
 "GoFiles":["parse.go"],"Module":{"Path":"example.com/rs","Dir":"/r"},
 "Imports":["example.com/rs/internal/ingest"]}
{"ImportPath":"fmt","Dir":"/goroot/src/fmt","GoFiles":["print.go"],"Imports":[]}
`

func TestImportsTriples(t *testing.T) {
	m, err := manifest.Parse([]byte(importsManifest))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	doc, err := derive.ImportsTriples(strings.NewReader(goListStream), "/r", m)
	if err != nil {
		t.Fatalf("ImportsTriples: %v", err)
	}
	got := string(doc)
	want := "<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> " +
		"<http://purl.org/dc/terms/requires> " +
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv> .\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant exactly the one cross-component edge:\n%s", got, want)
	}
}
