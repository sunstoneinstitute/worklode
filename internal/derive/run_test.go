package derive_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// fakeGraphServer fakes graph-server's two relevant routes: the read-only
// POST /sparql answers the hash SELECT with storedHash, and the branch-scoped
// GSP PUT /branches/main/graphs counts writes. There is deliberately no
// update route — graph-server exposes only whole-graph GSP writes plus the
// read-only SPARQL proxy (spec 009), so Run must never need one.
type fakeGraphServer struct {
	storedHash string
	puts       atomic.Int32
	lastPut    atomic.Pointer[string]
}

func (f *fakeGraphServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sparql" && r.Method == http.MethodPost:
			bindings := ""
			if f.storedHash != "" {
				bindings = `{"h": {"type": "literal", "value": "` + f.storedHash + `"}}`
			}
			w.Header().Set("Content-Type", "application/sparql-results+json")
			io.WriteString(w, `{"head":{"vars":["h"]},"results":{"bindings":[`+bindings+`]}}`)
		case r.URL.Path == "/branches/main/graphs" && r.Method == http.MethodPut:
			f.puts.Add(1)
			body, _ := io.ReadAll(r.Body)
			s := string(body)
			f.lastPut.Store(&s)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestRunWritesPayloadWithEmbeddedHash(t *testing.T) {
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g",
		[]byte("<urn:s> <urn:p> <urn:o> .\n"), derive.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped || res.Graph != "urn:g" || res.Hash == "" {
		t.Fatalf("result = %+v; want an unskipped write with a hash", res)
	}
	if f.puts.Load() != 1 {
		t.Fatalf("PUTs = %d; want exactly 1 (write must be a single atomic PUT)", f.puts.Load())
	}
	got := *f.lastPut.Load()
	if !strings.Contains(got, "<urn:s> <urn:p> <urn:o> .") {
		t.Fatalf("PUT body = %q; want the payload", got)
	}
	// The hash triple rides inside the same PUT, so it lands atomically with
	// the data and needs no SPARQL Update (which graph-server lacks).
	if !strings.Contains(got, `<urn:g> <http://purl.org/dc/terms/identifier> "`+res.Hash+`"`) {
		t.Fatalf("PUT body = %q; want the embedded hash triple", got)
	}
}

func TestRunSkipsOnMatchingHash(t *testing.T) {
	payload := []byte("<urn:s> <urn:p> <urn:o> .\n")
	f := &fakeGraphServer{storedHash: derive.HashOf(payload)}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", payload, derive.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("result = %+v; want Skipped", res)
	}
	if f.puts.Load() != 0 {
		t.Fatalf("puts=%d; a matching hash must write nothing", f.puts.Load())
	}
}

func TestRunRejectsUnsafeGraphIRI(t *testing.T) {
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	_, err := derive.Run(context.Background(), c, "urn:g> } INSERT { <urn:s",
		[]byte("<urn:s> <urn:p> <urn:o> .\n"), derive.Options{})
	if err == nil {
		t.Fatal("Run = nil error; a graph IRI that escapes the <...> must be rejected")
	}
	if f.puts.Load() != 0 {
		t.Fatalf("puts=%d; a rejected graph IRI must write nothing", f.puts.Load())
	}
}

func TestRunRewritesOnChangedHash(t *testing.T) {
	f := &fakeGraphServer{storedHash: "sha256:stale"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", []byte("<urn:s> <urn:p> <urn:o> .\n"), derive.Options{})
	if err != nil || res.Skipped {
		t.Fatalf("Run = %+v, %v; want a fresh write", res, err)
	}
}

// TestRunRefusesToEmptyANonEmptyGraph: the failure this guard exists for —
// a deriver whose inputs broke computes nothing, and a blind full-replace
// would wipe a graph that held real edges with no trace of why.
func TestRunRefusesToEmptyANonEmptyGraph(t *testing.T) {
	f := &fakeGraphServer{storedHash: derive.HashOf([]byte("<urn:s> <urn:p> <urn:o> .\n"))}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	_, err := derive.Run(context.Background(), c, "urn:g", nil, derive.Options{})
	if !errors.Is(err, derive.ErrWouldEmptyGraph) {
		t.Fatalf("Run = %v; want ErrWouldEmptyGraph", err)
	}
	if f.puts.Load() != 0 {
		t.Fatalf("puts=%d; a refused run must write nothing", f.puts.Load())
	}
}

// TestRunAllowEmptyWritesTheEmptyDocument: the escape hatch — a caller that
// knows the source really has no edges opts in and the graph is replaced.
func TestRunAllowEmptyWritesTheEmptyDocument(t *testing.T) {
	f := &fakeGraphServer{storedHash: derive.HashOf([]byte("<urn:s> <urn:p> <urn:o> .\n"))}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", nil, derive.Options{AllowEmpty: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Empty || res.Skipped {
		t.Fatalf("result = %+v; want an unskipped empty write", res)
	}
	if f.puts.Load() != 1 {
		t.Fatalf("puts=%d; want the opted-in write", f.puts.Load())
	}
	if got := *f.lastPut.Load(); !strings.HasPrefix(got, "<urn:g> <") {
		t.Fatalf("PUT body = %q; want the hash triple alone", got)
	}
}

// TestRunWritesEmptyOnAFirstRun: worklode's own go-imports document is
// legitimately empty (one whole-repo component, so every import edge is
// intra-component and dropped by design). Nothing is stored yet, so there is
// no content to lose and no opt-in is needed; the second run then
// short-circuits on the matching hash.
func TestRunWritesEmptyOnAFirstRun(t *testing.T) {
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", nil, derive.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Empty || res.Skipped || f.puts.Load() != 1 {
		t.Fatalf("result = %+v, puts=%d; want a first empty write", res, f.puts.Load())
	}

	f.storedHash = res.Hash
	res, err = derive.Run(context.Background(), c, "urn:g", nil, derive.Options{})
	if err != nil {
		t.Fatalf("re-Run: %v", err)
	}
	if !res.Skipped || !res.Empty || f.puts.Load() != 1 {
		t.Fatalf("result = %+v, puts=%d; a re-run must skip", res, f.puts.Load())
	}
}
