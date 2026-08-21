package derive_test

import (
	"context"
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
		[]byte("<urn:s> <urn:p> <urn:o> .\n"))
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

	res, err := derive.Run(context.Background(), c, "urn:g", payload)
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

func TestRunRewritesOnChangedHash(t *testing.T) {
	f := &fakeGraphServer{storedHash: "sha256:stale"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", []byte("<urn:s> <urn:p> <urn:o> .\n"))
	if err != nil || res.Skipped {
		t.Fatalf("Run = %+v, %v; want a fresh write", res, err)
	}
}
