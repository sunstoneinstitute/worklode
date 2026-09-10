package overview_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/overview"
)

const (
	compA = "https://worklode.io/ns/id/component/github.com/acme/app/a"
	compB = "https://worklode.io/ns/id/component/github.com/acme/app/b"
	compC = "https://worklode.io/ns/id/component/github.com/acme/app/c"

	ttlPrefixes = "@prefix wl:   <https://worklode.io/ns/ontology#> .\n" +
		"@prefix wlc:  <https://worklode.io/ns/concept/> .\n" +
		"@prefix dct:  <http://purl.org/dc/terms/> .\n" +
		"@prefix dcat: <http://www.w3.org/ns/dcat#> .\n" +
		"@prefix rdf:  <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .\n" +
		"@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .\n"
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
	return graphClient(t)
}

// graphClient returns a production client whose reads reach the test
// endpoint through the translating proxy, planting nothing.
func graphClient(t *testing.T) *graphserver.Client {
	t.Helper()
	return graphserver.New(sparqlProxy(t, graphtest.Endpoint(t)).URL, nil)
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

// --- 025 §11.5: coverage, stale and orphaned claims -------------------------
//
// Nothing projects wl:implements yet, so these fixtures are hand-loaded: the
// point is that the query text cannot rot silently before the deriver exists.
// The queries leave every ?g unbound, so they also answer over whatever else
// the shared endpoint holds — every id below is run-unique and every
// assertion narrows to it.

type fixtureSection struct {
	anchor        string
	status        string // the wlc: concept local name
	lastRevisedIn int
}

// fixtureClaim is one wl:implements claim. anchor may name no section at all,
// which is what an orphaned claim looks like.
type fixtureClaim struct {
	anchor string
	pinned int
}

func uniqueSlug(prefix string) string {
	return fmt.Sprintf("wl811-%s-%d", prefix, time.Now().UnixNano())
}

// plantCoverage writes one document's canonical graph, one snapshot graph per
// version, and one observed claims graph, in the shapes graphproj emits
// (DocTriples/SectionTriples/DocVersionTriples) plus the RDF-1.2 claim shape
// of 006 §3. It returns the document and component IRIs to filter on.
func plantCoverage(t *testing.T, slug string, versions []int, secs []fixtureSection, cls []fixtureClaim) (docIRI, compIRI string) {
	t.Helper()
	base := graphtest.Endpoint(t)
	docIRI = iri.Doc(slug)
	compIRI = iri.Component("github.com/wl811/" + slug)

	var b strings.Builder
	b.WriteString(ttlPrefixes)
	fmt.Fprintf(&b, "<%s> a wl:Spec ; dcat:hasCurrentVersion <%s>",
		docIRI, iri.DocVersion(slug, versions[len(versions)-1]))
	for _, v := range versions {
		fmt.Fprintf(&b, " ; dcat:hasVersion <%s>", iri.DocVersion(slug, v))
	}
	b.WriteString(" .\n")
	for _, s := range secs {
		fmt.Fprintf(&b, "<%s> a wl:Section ; dct:isPartOf <%s> ; wl:status wlc:%s ; wl:lastRevisedIn <%s> .\n",
			iri.Section(slug, s.anchor), docIRI, s.status, iri.DocVersion(slug, s.lastRevisedIn))
	}
	graphtest.PutGraph(t, base, iri.DeclaredGraph(slug), []byte(b.String()))

	for _, v := range versions {
		graphtest.PutGraph(t, base, iri.DeclaredVersionGraph(slug, v), []byte(fmt.Sprintf(
			ttlPrefixes+"<%s> a wl:Spec ; dcat:version \"%d\" .\n", iri.DocVersion(slug, v), v)))
	}

	b.Reset()
	b.WriteString(ttlPrefixes)
	for _, c := range cls {
		sec := iri.Section(slug, c.anchor)
		claim := iri.Claim(compIRI, sec)
		fmt.Fprintf(&b, "<%s> wl:implements <%s> .\n<%s> rdf:reifies <<( <%s> wl:implements <%s> )>> .\n<%s> wl:pinnedVersion <%s> .\n",
			compIRI, sec, claim, compIRI, sec, claim, iri.DocVersion(slug, c.pinned))
	}
	graphtest.PutGraph(t, base,
		iri.RepoObservedGraph("repo-implements", "github.com", "wl811", slug), []byte(b.String()))
	return docIRI, compIRI
}

// ownClaims keeps the rows claimed by comp.
func ownClaims(rows []model.Claim, comp string) []model.Claim {
	var out []model.Claim
	for _, r := range rows {
		if r.Component == comp {
			out = append(out, r)
		}
	}
	return out
}

// TestSectionCoverageQueries exercises all four §11.5 queries this package
// implements against one fixture: sec-1 and sec-2 are claimed, sec-3 is not,
// sec-old is superseded, and sec-gone is claimed but is no section of the
// document at all.
func TestSectionCoverageQueries(t *testing.T) {
	slug := uniqueSlug("cov")
	doc, comp := plantCoverage(t, slug, []int{1, 2},
		[]fixtureSection{
			{"sec-1", "accepted", 1},
			{"sec-2", "accepted", 2},
			{"sec-3", "accepted", 1},
			{"sec-old", "superseded", 1},
		},
		[]fixtureClaim{
			{"sec-1", 2},    // pinned at the version that last revised it
			{"sec-2", 1},    // pinned behind it: stale
			{"sec-gone", 1}, // names no section of the document: orphaned
		})
	c := graphClient(t)

	un, err := overview.Unimplemented(t.Context(), c)
	if err != nil {
		t.Fatalf("Unimplemented: %v", err)
	}
	var mine []string
	for _, s := range un {
		if strings.Contains(s, slug) {
			mine = append(mine, s)
		}
	}
	if want := []string{iri.Section(slug, "sec-3")}; fmt.Sprint(mine) != fmt.Sprint(want) {
		t.Errorf("unimplemented = %v, want %v (superseded and claimed sections excluded)", mine, want)
	}

	cov, err := overview.Coverage(t.Context(), c)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	var row model.DocCoverage
	for _, r := range cov {
		if r.Doc == doc {
			row = r
		}
	}
	if row.Total != 3 || row.Implemented != 2 {
		t.Errorf("coverage = %d/%d, want 2/3 (sec-old superseded, sec-gone is no section)", row.Implemented, row.Total)
	}

	stale, err := overview.StaleClaims(t.Context(), c)
	if err != nil {
		t.Fatalf("StaleClaims: %v", err)
	}
	want := []model.Claim{{Component: comp, Section: iri.Section(slug, "sec-2")}}
	if got := ownClaims(stale, comp); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stale claims = %v, want %v", got, want)
	}

	orphaned, err := overview.OrphanedClaims(t.Context(), c)
	if err != nil {
		t.Fatalf("OrphanedClaims: %v", err)
	}
	want = []model.Claim{{Component: comp, Section: iri.Section(slug, "sec-gone")}}
	if got := ownClaims(orphaned, comp); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("orphaned claims = %v, want %v", got, want)
	}
}

// TestStaleClaimComparesVersionsNumerically is 025 §4.1's footgun: v3 and v10
// sort backwards as strings ("10" < "3") and as IRIs (".../v10" < ".../v3"),
// so a claim pinned at v9 only resolves right if dcat:version is compared as a
// number. String ordering makes both comparisons false and this set empty.
func TestStaleClaimComparesVersionsNumerically(t *testing.T) {
	slug := uniqueSlug("footgun")
	_, comp := plantCoverage(t, slug, []int{3, 9, 10},
		[]fixtureSection{
			{"sec-old", "accepted", 3},
			{"sec-new", "accepted", 10},
		},
		[]fixtureClaim{{"sec-old", 9}, {"sec-new", 9}})

	stale, err := overview.StaleClaims(t.Context(), graphClient(t))
	if err != nil {
		t.Fatalf("StaleClaims: %v", err)
	}
	want := []model.Claim{{Component: comp, Section: iri.Section(slug, "sec-new")}}
	if got := ownClaims(stale, comp); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stale claims at pin v9 = %v, want %v (numeric, not string or IRI, comparison)", got, want)
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
