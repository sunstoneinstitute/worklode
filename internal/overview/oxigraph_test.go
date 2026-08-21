package overview_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/overview"
)

const (
	compA = "https://worklode.io/ns/id/component/github.com/acme/app/a"
	compB = "https://worklode.io/ns/id/component/github.com/acme/app/b"
	compC = "https://worklode.io/ns/id/component/github.com/acme/app/c"

	ttlPrefixes = "@prefix wl:  <https://worklode.io/ns/ontology#> .\n" +
		"@prefix dct: <http://purl.org/dc/terms/> .\n" +
		"@prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .\n" +
		"@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .\n"
)

// declaredTTL plants A→B and B→C plus a doc governing A; extra is appended
// verbatim (the deviation tests re-PUT the graph with it).
func declaredTTL(extra string) []byte {
	return []byte(fmt.Sprintf(ttlPrefixes+`
<%s> dct:requires <%s> .
<%s> dct:requires <%s> .
<urn:doc:1> a wl:DesignDoc ; wl:governs <%s> .
%s`, compA, compB, compB, compC, compA, extra))
}

// observedTTL plants A→B (agreement) and A→C (violation); components typed.
// B→C has no observed counterpart, so it is the stale-intent edge.
func observedTTL() []byte {
	return []byte(fmt.Sprintf(ttlPrefixes+`
<%s> dct:requires <%s> .
<%s> dct:requires <%s> .
<%s> a wl:Component . <%s> a wl:Component . <%s> a wl:Component .
`, compA, compB, compA, compC, compA, compB, compC))
}

// seed loads both layers into Oxigraph and returns a production client
// whose reads go through the translating proxy.
func seed(t *testing.T, declaredExtra string) *graphserver.Client {
	t.Helper()
	base := graphtest.Endpoint(t)
	graphtest.PutGraph(t, base, iri.DeclaredGraph("adr-test-0001"), declaredTTL(declaredExtra))
	graphtest.PutGraph(t, base,
		iri.RepoObservedGraph("go-imports", "github.com", "sunstoneinstitute", "worklode"),
		observedTTL())
	return graphserver.New(sparqlProxy(t, base).URL, nil)
}

func deviationTTL(validUntil string) string {
	return fmt.Sprintf(`<urn:dev:1> a wl:AcceptedDeviation ;
    rdf:subject <%s> ; rdf:predicate dct:requires ; rdf:object <%s> ;
    wl:sanctionedBy <urn:doc:1> ;
    dct:valid "%s"^^xsd:date .
`, compA, compC, validUntil)
}

func TestDriftBothDirections(t *testing.T) {
	c := seed(t, "")

	v, err := overview.Violations(t.Context(), c)
	if err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if len(v) != 1 || v[0].From != compA || v[0].To != compC {
		t.Fatalf("violations = %+v; want exactly A requires C", v)
	}

	st, err := overview.StaleIntent(t.Context(), c)
	if err != nil {
		t.Fatalf("StaleIntent: %v", err)
	}
	if len(st) != 1 || st[0].From != compB || st[0].To != compC {
		t.Fatalf("stale intent = %+v; want exactly B requires C", st)
	}
}

func TestDeviationSuppressesUntilExpiry(t *testing.T) {
	// Active deviation for A→C (expires next year): 4.1 must drop it.
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	c := seed(t, deviationTTL(future))

	v, err := overview.Violations(t.Context(), c)
	if err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if len(v) != 0 {
		t.Fatalf("violations = %+v; the active deviation must suppress A→C", v)
	}
	// Stale intent is unaffected by suppression (the deviation never
	// asserts the edge into the declared layer).
	st, _ := overview.StaleIntent(t.Context(), c)
	if len(st) != 1 {
		t.Fatalf("stale intent = %+v; must be unchanged by the deviation", st)
	}
	// It is listable.
	ack, err := overview.Acknowledged(t.Context(), c)
	if err != nil || len(ack) != 1 || ack[0].Expired {
		t.Fatalf("acknowledged = %+v, %v; want one active deviation", ack, err)
	}

	// Expire it — re-PUT the declared graph with a past dct:valid: the
	// violation re-surfaces and the deviation lists as expired.
	base := graphtest.Endpoint(t)
	graphtest.PutGraph(t, base, iri.DeclaredGraph("adr-test-0001"), declaredTTL(deviationTTL("2020-01-01")))
	v, _ = overview.Violations(t.Context(), c)
	if len(v) != 1 {
		t.Fatalf("violations after expiry = %+v; want A→C re-surfaced", v)
	}
	ack, _ = overview.Acknowledged(t.Context(), c)
	if len(ack) != 1 || !ack[0].Expired {
		t.Fatalf("acknowledged after expiry = %+v; want it listed as expired", ack)
	}
}

func TestGaps(t *testing.T) {
	c := seed(t, "")
	gaps, err := overview.Gaps(t.Context(), c)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	// B and C have no governing doc; A does.
	if len(gaps) != 2 {
		t.Fatalf("gaps = %+v; want the two ungoverned components", gaps)
	}
}

// sparqlProxy bridges the production graphserver.Client's read surface
// (POST /sparql) to Oxigraph's POST /query, passing body, Content-Type and
// Accept through unchanged and copying the status and body back. Everything
// upstream of the proxy — the client, the queries — is production code.
func sparqlProxy(t *testing.T, oxigraphBase string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/sparql", func(w http.ResponseWriter, r *http.Request) {
		fwd, err := http.NewRequestWithContext(r.Context(), http.MethodPost, oxigraphBase+"/query", r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fwd.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		fwd.Header.Set("Accept", r.Header.Get("Accept"))
		resp, err := http.DefaultClient.Do(fwd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
