package overview_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/overview"
)

const ttlPrefixes = "@prefix wl:   <https://worklode.io/ns/ontology#> .\n" +
	"@prefix wlc:  <https://worklode.io/ns/concept/> .\n" +
	"@prefix dct:  <http://purl.org/dc/terms/> .\n" +
	"@prefix dcat: <http://www.w3.org/ns/dcat#> .\n" +
	"@prefix prov: <http://www.w3.org/ns/prov#> .\n" +
	"@prefix rdf:  <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .\n" +
	"@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .\n"

// driftFixture is one run's §4.1/§4.2 fixture. The drift queries read every
// declared and observed graph, so another build's data or a crashed run's
// leftovers answer too: every IRI here is run-unique, and every assertion
// keeps only the rows naming this fixture's components (own*).
type driftFixture struct {
	a, b, c, doc, dev, declared, observed string
}

func newDriftFixture() driftFixture {
	slug := uniqueSlug("drift")
	comp := func(n string) string { return iri.Component("github.com/wl811/" + slug + "/" + n) }
	return driftFixture{
		a: comp("a"), b: comp("b"), c: comp("c"),
		doc:      "urn:wl811:" + slug + ":doc",
		dev:      "urn:wl811:" + slug + ":dev",
		declared: iri.DeclaredGraph(slug),
		observed: iri.RepoObservedGraph("go-imports", "github.com", "wl811", slug),
	}
}

func (f driftFixture) mine(comp string) bool { return comp == f.a || comp == f.b || comp == f.c }

func (f driftFixture) ownEdges(rows []model.DriftEdge) []model.DriftEdge {
	var out []model.DriftEdge
	for _, r := range rows {
		if f.mine(r.From) {
			out = append(out, r)
		}
	}
	return out
}

func (f driftFixture) ownDeviations(rows []model.Deviation) []model.Deviation {
	var out []model.Deviation
	for _, r := range rows {
		if f.mine(r.From) {
			out = append(out, r)
		}
	}
	return out
}

func (f driftFixture) ownGaps(rows []model.Gap) []string {
	var out []string
	for _, r := range rows {
		if f.mine(r.Component) {
			out = append(out, r.Component)
		}
	}
	return out
}

// declaredTTL plants A→B and B→C plus a doc governing A; extra is appended
// verbatim (the deviation tests re-PUT the graph with it).
func (f driftFixture) declaredTTL(extra string) []byte {
	return []byte(fmt.Sprintf(ttlPrefixes+`
<%s> dct:requires <%s> .
<%s> dct:requires <%s> .
<%s> a wl:DesignDoc ; wl:governs <%s> .
%s`, f.a, f.b, f.b, f.c, f.doc, f.a, extra))
}

// observedTTL plants A→B (agreement) and A→C (violation); components typed.
// B→C has no observed counterpart, so it is the stale-intent edge.
func (f driftFixture) observedTTL() []byte {
	return []byte(fmt.Sprintf(ttlPrefixes+`
<%s> dct:requires <%s> .
<%s> dct:requires <%s> .
<%s> a wl:Component . <%s> a wl:Component . <%s> a wl:Component .
`, f.a, f.b, f.a, f.c, f.a, f.b, f.c))
}

func (f driftFixture) deviationTTL(validUntil string) string {
	return fmt.Sprintf(`<%s> a wl:AcceptedDeviation ;
    rdf:subject <%s> ; rdf:predicate dct:requires ; rdf:object <%s> ;
    wl:sanctionedBy <%s> ;
    dct:valid "%s"^^xsd:date .
`, f.dev, f.a, f.c, f.doc, validUntil)
}

// seed loads both layers into Oxigraph and returns a production client
// whose reads go through the translating proxy.
func seed(t *testing.T, declaredExtra func(driftFixture) string) (driftFixture, *graphserver.Client) {
	t.Helper()
	f := newDriftFixture()
	base := graphtest.Endpoint(t)
	extra := ""
	if declaredExtra != nil {
		extra = declaredExtra(f)
	}
	graphtest.PutGraph(t, base, f.declared, f.declaredTTL(extra))
	graphtest.PutGraph(t, base, f.observed, f.observedTTL())
	return f, graphClient(t)
}

// graphClient returns a production client whose reads reach the test
// endpoint through the translating proxy, planting nothing.
func graphClient(t *testing.T) *graphserver.Client {
	t.Helper()
	return graphserver.New(sparqlProxy(t, graphtest.Endpoint(t)).URL, nil)
}

func TestDriftBothDirections(t *testing.T) {
	f, c := seed(t, nil)

	v, err := overview.Violations(t.Context(), c)
	if err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if v = f.ownEdges(v); len(v) != 1 || v[0].From != f.a || v[0].To != f.c {
		t.Fatalf("violations = %+v; want exactly A requires C", v)
	}

	st, err := overview.StaleIntent(t.Context(), c)
	if err != nil {
		t.Fatalf("StaleIntent: %v", err)
	}
	if st = f.ownEdges(st); len(st) != 1 || st[0].From != f.b || st[0].To != f.c {
		t.Fatalf("stale intent = %+v; want exactly B requires C", st)
	}
}

func TestDeviationSuppressesUntilExpiry(t *testing.T) {
	// Active deviation for A→C (expires next year): 4.1 must drop it.
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	f, c := seed(t, func(f driftFixture) string { return f.deviationTTL(future) })

	v, err := overview.Violations(t.Context(), c)
	if err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if v = f.ownEdges(v); len(v) != 0 {
		t.Fatalf("violations = %+v; the active deviation must suppress A→C", v)
	}
	// Stale intent is unaffected by suppression (the deviation never
	// asserts the edge into the declared layer).
	st, _ := overview.StaleIntent(t.Context(), c)
	if st = f.ownEdges(st); len(st) != 1 {
		t.Fatalf("stale intent = %+v; must be unchanged by the deviation", st)
	}
	// It is listable.
	ack, err := overview.Acknowledged(t.Context(), c)
	if ack = f.ownDeviations(ack); err != nil || len(ack) != 1 || ack[0].Expired {
		t.Fatalf("acknowledged = %+v, %v; want one active deviation", ack, err)
	}

	// Expire it — re-PUT the declared graph with a past dct:valid: the
	// violation re-surfaces and the deviation lists as expired.
	base := graphtest.Endpoint(t)
	graphtest.PutGraph(t, base, f.declared, f.declaredTTL(f.deviationTTL("2020-01-01")))
	v, _ = overview.Violations(t.Context(), c)
	if v = f.ownEdges(v); len(v) != 1 {
		t.Fatalf("violations after expiry = %+v; want A→C re-surfaced", v)
	}
	ack, _ = overview.Acknowledged(t.Context(), c)
	if ack = f.ownDeviations(ack); len(ack) != 1 || !ack[0].Expired {
		t.Fatalf("acknowledged after expiry = %+v; want it listed as expired", ack)
	}
}

func TestGaps(t *testing.T) {
	f, c := seed(t, nil)
	gaps, err := overview.Gaps(t.Context(), c)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	// B and C have no governing doc; A does.
	if got, want := f.ownGaps(gaps), []string{f.b, f.c}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("gaps = %v; want the two ungoverned components %v", got, want)
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

// uniqueSlug is run-unique: the pid separates concurrent test binaries, the
// clock separates runs and calls within one.
func uniqueSlug(prefix string) string {
	return fmt.Sprintf("wl811-%s-%d-%d", prefix, os.Getpid(), time.Now().UnixNano())
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

// plantDelivery writes the delivered-coverage fixture beside plantCoverage's
// claim: the deliverables comp delivers, in a declared graph, and the runtime
// nodes witnessing them, in an observed one. Both arms of 006 §9's witness
// table are planted, each with a deployed case and a not-deployed one.
//
// Deliverable and Effect nodes have no projector yet (006 §9, WL-PLAN-118),
// so they are planted by hand here exactly as plantCoverage plants claims.
func plantDelivery(t *testing.T, slug, compIRI string) {
	t.Helper()
	base := graphtest.Endpoint(t)
	shipped := iri.Artifact("oci", "wl811/"+slug, "1.0.0")
	unshipped := iri.Artifact("oci", "wl811/"+slug+"-side", "1.0.0")
	depDev := iri.Deployment("dev", "kustomization", slug)
	depProd := iri.Deployment("prod", "kustomization", slug)
	depSandbox := iri.Deployment("sandbox", "kustomization", slug+"-side")
	effectStage := iri.Deployment("stage", "kustomization", slug+"-effect")
	effectLab := iri.Deployment("lab", "kustomization", slug+"-effect")

	graphtest.PutGraph(t, base, iri.DeclaredGraph(slug+"-delivery"), []byte(fmt.Sprintf(ttlPrefixes+`
<%s> a wl:Deliverable ; wl:deliveredBy <%s> ; dct:relation <%s> .
<%s> a wl:Deliverable ; wl:deliveredBy <%s> ; dct:relation <%s> .
<%s> a wl:Effect ; wl:deliveredBy <%s> ; dct:relation <%s> .
<%s> a wl:Effect ; wl:deliveredBy <%s> ; dct:relation <%s> .
`,
		iri.Deliverable(slug+"-shipped"), compIRI, shipped,
		iri.Deliverable(slug+"-side"), compIRI, unshipped,
		iri.Deliverable(slug+"-effect"), compIRI, effectStage,
		iri.Deliverable(slug+"-effect-lab"), compIRI, effectLab)))

	graphtest.PutGraph(t, base, iri.ObservedGraph("deploy/"+slug), []byte(fmt.Sprintf(ttlPrefixes+`
<%s> a wl:Artifact .
<%s> a wl:Artifact .
<%s> a wl:Deployment ; prov:used <%s> ; wl:toEnvironment <%s> ; wl:deploymentStatus wlc:deployed .
<%s> a wl:Deployment ; prov:used <%s> ; wl:toEnvironment <%s> ; wl:deploymentStatus wlc:deployed .
<%s> a wl:Deployment ; prov:used <%s> ; wl:toEnvironment <%s> ; wl:deploymentStatus wlc:failed .
<%s> a wl:Deployment ; wl:toEnvironment <%s> ; wl:deploymentStatus wlc:deployed .
<%s> a wl:Deployment ; wl:toEnvironment <%s> ; wl:deploymentStatus wlc:failed .
`,
		shipped, unshipped,
		depDev, shipped, iri.Environment("dev"),
		depProd, shipped, iri.Environment("prod"),
		depSandbox, unshipped, iri.Environment("sandbox"),
		effectStage, iri.Environment("stage"),
		effectLab, iri.Environment("lab"))))
}

// TestDeliveredCoverage is §11.5 row 5: a claim is delivered where the
// component's deliverable is deployed, and nowhere else. The fixture covers
// both arms of 006 §9's witness table (an Artifact a Deployment prov:used,
// and an Effect whose Deployment is the target itself), each with a deployed
// and a not-deployed case, and the Artifact arm in two environments.
func TestDeliveredCoverage(t *testing.T) {
	slug := uniqueSlug("delivered")
	_, comp := plantCoverage(t, slug, []int{1},
		[]fixtureSection{{"sec-1", "accepted", 1}},
		[]fixtureClaim{{"sec-1", 1}})
	plantDelivery(t, slug, comp)

	rows, err := overview.DeliveredCoverage(t.Context(), graphClient(t))
	if err != nil {
		t.Fatalf("DeliveredCoverage: %v", err)
	}
	sec := iri.Section(slug, "sec-1")
	var got []string
	for _, r := range rows {
		if r.Component != comp {
			continue
		}
		if r.Section != sec {
			t.Errorf("row %+v names a section this component does not claim", r)
		}
		got = append(got, r.Environment)
	}
	want := []string{iri.Environment("dev"), iri.Environment("prod"), iri.Environment("stage")}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("delivered environments = %v, want %v (sandbox and lab are not deployed)", got, want)
	}
}

// TestDeliveredCoverageNeedsTheClaim: the query is a join, so a deliverable
// deployed by a component that claims nothing contributes no row. Without the
// wl:implements leg it would report delivery with no intent behind it, which
// is the thing §11.5 row 5 is for measuring.
func TestDeliveredCoverageNeedsTheClaim(t *testing.T) {
	slug := uniqueSlug("unclaimed")
	comp := iri.Component("github.com/wl811/" + slug)
	plantDelivery(t, slug, comp)

	rows, err := overview.DeliveredCoverage(t.Context(), graphClient(t))
	if err != nil {
		t.Fatalf("DeliveredCoverage: %v", err)
	}
	for _, r := range rows {
		if r.Component == comp {
			t.Errorf("row %+v: the component claims no section, so it has no delivered coverage", r)
		}
	}
}
