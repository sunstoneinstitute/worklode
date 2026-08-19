package graphproj

import (
	"fmt"
	"os"
	"sort"
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
