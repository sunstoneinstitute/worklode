package graphproj

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The endpoint may be shared with other runs, so every id these tests mint
// carries a run-unique suffix and every graph they write is dropped on
// cleanup (graphtest.PutGraph).
func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func testTask(id, project, state string) model.Task {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return model.Task{
		ID:        id,
		Project:   project,
		Title:     "Task " + id,
		State:     state,
		Kind:      "feature",
		Priority:  "high",
		CreatedAt: ts,
		UpdatedAt: ts,
	}
}

// TestNSVocabularyParses is the in-repo parse gate for ns/: Oxigraph answers
// 400 on any Turtle syntax error, so a successful load is the check. It then
// asserts Task 2's execution-layer mirror is present in the loaded graph.
func TestNSVocabularyParses(t *testing.T) {
	base := graphtest.Endpoint(t)

	graphs := make(map[string]string)
	for _, name := range []string{"ontology", "concept", "shapes"} {
		// The test's working directory is internal/graphproj.
		data, err := os.ReadFile("../../ns/" + name + ".ttl")
		if err != nil {
			t.Fatalf("read ns/%s.ttl: %v", name, err)
		}
		g := iri.GraphNS + "test/ns/" + uniqueID(name)
		graphtest.PutGraph(t, base, g, data)
		graphs[name] = g
	}

	rows := graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?term WHERE { GRAPH <%s> { ?term <%s> <%s> } }",
		graphs["ontology"], iri.Term("layer"), iri.Concept("execution")))

	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		seen[r["term"]] = true
	}
	for _, local := range []string{"priority", "concern"} {
		if !seen[iri.Term(local)] {
			t.Errorf("wl:%s missing from the execution layer (%d terms found)", local, len(seen))
		}
	}
}

// TestProjectGraphReplaceRoundTrip proves 006 §11's write mechanism: a
// project's graph is replaced whole, so a re-projection leaves exactly one
// value per functional property, and another project's graph is untouched.
func TestProjectGraphReplaceRoundTrip(t *testing.T) {
	base := graphtest.Endpoint(t)

	alpha := uniqueID("alpha")
	beta := uniqueID("beta")
	alphaGraph := iri.ProjectGraph(alpha)
	betaGraph := iri.ProjectGraph(beta)

	blocks := model.Edge{From: "WL-101", To: "WL-102", Type: "blocks"}
	render := func(secondState string) []byte {
		first := testTask("WL-101", alpha, "ready")
		second := testTask("WL-102", alpha, secondState)
		triples := TaskTriples(first, []model.Edge{blocks}, nil)
		triples = append(triples, TaskTriples(second, nil, []model.Edge{blocks})...)
		return Document(append(triples, ProjectTriples(model.Project{ID: alpha, Name: "Alpha"})...))
	}

	graphtest.PutGraph(t, base, alphaGraph, render("ready"))
	third := testTask("WL-201", beta, "ready")
	graphtest.PutGraph(t, base, betaGraph, Document(TaskTriples(third, nil, nil)))

	// Re-project alpha with WL-102 merged; the PUT replaces the whole graph.
	graphtest.PutGraph(t, base, alphaGraph, render("merged"))

	states := func(graph string) map[string][]string {
		rows := graphtest.Select(t, base, fmt.Sprintf(
			"SELECT ?task ?state WHERE { GRAPH <%s> { ?task <%s> ?state } }",
			graph, iri.Term("taskState")))
		out := make(map[string][]string)
		for _, r := range rows {
			out[r["task"]] = append(out[r["task"]], r["state"])
		}
		return out
	}

	got := states(alphaGraph)
	want := map[string][]string{
		iri.Task("WL-101"): {"ready"},  // sibling untouched by the replace
		iri.Task("WL-102"): {"merged"}, // new state, and only the new state
	}
	for task, states := range want {
		if fmt.Sprint(got[task]) != fmt.Sprint(states) {
			t.Errorf("%s: wl:taskState = %v, want %v", task, got[task], states)
		}
	}
	if len(got) != len(want) {
		t.Errorf("alpha graph has %d tasks with a state, want %d: %v", len(got), len(want), got)
	}

	if betaStates := states(betaGraph); fmt.Sprint(betaStates[iri.Task("WL-201")]) != "[ready]" {
		t.Errorf("beta graph disturbed by the alpha replace: %v", betaStates)
	}
}

// TestDependsOnPath proves the §3 transitive-property promise is answerable
// as a query-time property path (no reasoner), and 025 acceptance criterion
// 20's shape: every projected task binds exactly one wl:inProject.
func TestDependsOnPath(t *testing.T) {
	base := graphtest.Endpoint(t)

	project := uniqueID("chain")
	graph := iri.ProjectGraph(project)
	first := model.Edge{From: "WL-1", To: "WL-2", Type: "blocks"}
	second := model.Edge{From: "WL-2", To: "WL-3", Type: "blocks"}

	var triples []Triple
	triples = append(triples, TaskTriples(testTask("WL-1", project, "ready"), []model.Edge{first}, nil)...)
	triples = append(triples, TaskTriples(testTask("WL-2", project, "ready"), []model.Edge{second}, []model.Edge{first})...)
	triples = append(triples, TaskTriples(testTask("WL-3", project, "ready"), nil, []model.Edge{second})...)
	graphtest.PutGraph(t, base, graph, Document(triples))

	rows := graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?x WHERE { GRAPH <%s> { <%s> <%s>+ ?x } }",
		graph, iri.Task("WL-3"), iri.Term("dependsOn")))
	var reached []string
	for _, r := range rows {
		reached = append(reached, r["x"])
	}
	sort.Strings(reached)
	want := []string{iri.Task("WL-1"), iri.Task("WL-2")}
	if fmt.Sprint(reached) != fmt.Sprint(want) {
		t.Errorf("wl:dependsOn+ from WL-3 reached %v, want %v", reached, want)
	}

	rows = graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?task (COUNT(?p) AS ?n) WHERE { GRAPH <%s> { ?task <%s> ?p } } GROUP BY ?task",
		graph, iri.Term("inProject")))
	if len(rows) != 3 {
		t.Errorf("%d tasks bind wl:inProject, want 3", len(rows))
	}
	for _, r := range rows {
		if r["n"] != "1" {
			t.Errorf("%s binds %s wl:inProject values, want 1", r["task"], r["n"])
		}
	}
}

// buildStaleFixture projects a document with the given versions and
// sections (anchor -> version that last revised it) the way a real
// projection run would: DocTriples/SectionTriples into the document's
// DeclaredGraph, DocVersionTriples for each version into its own
// DeclaredVersionGraph (025 §4.1). d.Version is the last entry of versions,
// so DocTriples emits dcat:hasCurrentVersion at it. Returns the run-unique
// doc slug the caller builds query IRIs from.
func buildStaleFixture(t *testing.T, base, prefix string, versions []int, revisedIn map[string]int) string {
	t.Helper()
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	slug := uniqueID(prefix)
	d := model.Doc{
		Slug:      slug,
		Kind:      "spec",
		Title:     "Staleness fixture",
		Status:    "accepted",
		Version:   versions[len(versions)-1],
		CreatedAt: ts,
		UpdatedAt: ts,
	}

	var summaries []model.DocVersionSummary
	for _, v := range versions {
		summaries = append(summaries, model.DocVersionSummary{Version: v, Title: d.Title, CreatedAt: ts})
	}

	anchors := make([]string, 0, len(revisedIn))
	for a := range revisedIn {
		anchors = append(anchors, a)
	}
	sort.Strings(anchors)
	var sections []model.DocSection
	for i, a := range anchors {
		sections = append(sections, model.DocSection{
			Anchor: a, Heading: a, Position: i, LastRevisedIn: revisedIn[a], Published: true,
		})
	}

	declared := append(DocTriples(d, summaries), SectionTriples(d, sections, nil)...)
	graphtest.PutGraph(t, base, iri.DeclaredGraph(slug), Document(declared))
	for _, v := range versions {
		dv := model.DocVersion{Version: v, Title: d.Title, CreatedAt: ts}
		graphtest.PutGraph(t, base, iri.DeclaredVersionGraph(slug, v), Document(DocVersionTriples(d, dv, nil)))
	}
	return slug
}

// stalenessQuery is 025 §4.4's query shape verbatim: a claim pinned at pin
// is stale for any section whose wl:lastRevisedIn snapshot's dcat:version
// exceeds pin, compared as xsd:integer rather than as strings or IRIs. ?g
// and ?vg are unbound — the query does not know in advance which graph
// holds a section's declaration or which holds its snapshot's version, the
// way a real reader over the graph union would not either.
// ownSections keeps the rows naming slug's own sections. The staleness query
// leaves ?g unbound on purpose, so it also answers for whatever else the
// endpoint holds — another run of this package against the same Oxigraph, for
// one. The run-unique slug is what makes an exact-set assertion safe anyway.
func ownSections(rows []map[string]string, slug string) []string {
	var got []string
	for _, r := range rows {
		if strings.Contains(r["sec"], slug) {
			got = append(got, r["sec"])
		}
	}
	return got
}

func stalenessQuery(pin int) string {
	return fmt.Sprintf(`PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
SELECT ?sec WHERE {
  GRAPH ?g  { ?sec <%s> ?snap }
  GRAPH ?vg { ?snap <%s> ?n }
  FILTER (xsd:integer(?n) > %d)
}`, iri.Term("lastRevisedIn"), DCATVersion, pin)
}

// TestOxigraphStalenessQuery proves 025 §4.4's staleness query: a claim
// pinned at v1 is stale against any section last revised after v1. sec-a
// (revised in v1) is current at that pin; sec-b (revised in v2) is stale.
func TestOxigraphStalenessQuery(t *testing.T) {
	base := graphtest.Endpoint(t)
	slug := buildStaleFixture(t, base, "stale", []int{1, 2}, map[string]int{"sec-a": 1, "sec-b": 2})

	got := ownSections(graphtest.Select(t, base, stalenessQuery(1)), slug)
	want := []string{iri.Section(slug, "sec-b")}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sections stale at pin v1 = %v, want %v", got, want)
	}
}

// TestOxigraphStalenessQueryNumericComparison proves 025 §4.1's footgun: v3
// and v10 sort backwards both as strings ("10" < "3") and as their
// DocVersion IRIs (".../v10" < ".../v3"), so a claim pinned at v9 only
// resolves correctly if dcat:version is compared as a number. sec-old
// (revised in v3) is current at that pin under numeric comparison; sec-new
// (revised in v10) is stale. A string or IRI comparison would call v3 the
// newer one and get this backwards.
func TestOxigraphStalenessQueryNumericComparison(t *testing.T) {
	base := graphtest.Endpoint(t)
	slug := buildStaleFixture(t, base, "footgun", []int{3, 10}, map[string]int{"sec-old": 3, "sec-new": 10})

	got := ownSections(graphtest.Select(t, base, stalenessQuery(9)), slug)
	want := []string{iri.Section(slug, "sec-new")}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sections stale at pin v9 = %v, want %v (numeric, not string/IRI, comparison of dcat:version)", got, want)
	}
}

// TestOxigraphCurrentVersionRoundTrip proves dcat:hasCurrentVersion on the
// canonical document node joins to its target snapshot's own dcat:version
// literal (025 §4.1) — the join a pinned-claim reader leans on to resolve
// "current" to a version number.
func TestOxigraphCurrentVersionRoundTrip(t *testing.T) {
	base := graphtest.Endpoint(t)
	slug := buildStaleFixture(t, base, "current", []int{1, 2}, map[string]int{"sec-a": 1, "sec-b": 2})

	q := fmt.Sprintf(`SELECT ?n WHERE {
  GRAPH <%s> { <%s> <%s> ?snap }
  GRAPH ?vg { ?snap <%s> ?n }
}`, iri.DeclaredGraph(slug), iri.Doc(slug), DCATHasCurrentVersion, DCATVersion)

	rows := graphtest.Select(t, base, q)
	if len(rows) != 1 || rows[0]["n"] != "2" {
		t.Errorf("dcat:hasCurrentVersion round trip = %v, want [{n:2}]", rows)
	}
}
