package graphproj

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestProjectGraphTriples(t *testing.T) {
	g := model.ProjectGraph{
		Project: "worklode",
		Tasks: []model.Task{
			{ID: "WL-1", Project: "worklode", Title: "One", State: "ready", Kind: "feature", Priority: "high"},
			{ID: "WL-2", Project: "worklode", Title: "Two", State: "ready", Kind: "feature", Priority: "low"},
		},
		TaskEdges: []model.Edge{{From: "WL-1", To: "WL-2", Type: "blocks"}},
		Docs: []model.Doc{
			{ID: 10, Project: "worklode", Kind: "spec", Slug: "010-spec", Title: "Spec", Status: "accepted", Version: 1},
			{ID: 20, Project: "worklode", Kind: "plan", Slug: "020-plan", Title: "Plan", Status: "accepted", Version: 1, GeneratedByTask: "WL-2"},
			{ID: 30, Project: "worklode", Kind: "spec", Slug: "030-new", Title: "New", Status: "draft", Version: 1},
		},
		DocEdges: []model.GraphDocEdge{
			{From: 20, To: 10, Type: "covers"},
			{From: 30, To: 10, Type: "replaces"},
			{From: 30, To: 10, Type: "amends"},
			{From: 20, To: 99, Type: "requires"}, // 99 is not in Docs: skipped
		},
		Links: []model.GraphLink{
			{Task: "WL-1", Doc: 20, Type: "planned_in"},
			{Task: "WL-1", Doc: 10, Type: "about"},
			{Task: "WL-2", Doc: 20, Type: "generated_by"},
		},
	}
	doc := string(Document(ProjectGraphTriples(g)))
	want := []string{
		"<" + iri.Task("WL-1") + "> <" + iri.Term("blocks") + "> <" + iri.Task("WL-2") + ">",
		"<" + iri.Task("WL-2") + "> <" + iri.Term("dependsOn") + "> <" + iri.Task("WL-1") + ">",
		"<" + iri.Doc("020-plan") + "> <" + iri.Term("covers") + "> <" + iri.Doc("010-spec") + ">",
		"<" + iri.Doc("010-spec") + "> <" + DCTIsReplacedBy + "> <" + iri.Doc("030-new") + ">",
		"<" + iri.Doc("030-new") + "> <" + iri.Term("amends") + "> <" + iri.Doc("010-spec") + ">",
		"<" + iri.Task("WL-1") + "> <" + iri.Term("plannedIn") + "> <" + iri.Doc("020-plan") + ">",
		"<" + iri.Task("WL-1") + "> <" + iri.Term("about") + "> <" + iri.Doc("010-spec") + ">",
		"<" + iri.Doc("020-plan") + "> <" + ProvWasGeneratedBy + "> <" + iri.Task("WL-2") + ">",
	}
	for _, w := range want {
		if !strings.Contains(doc, w) {
			t.Errorf("missing triple %s\n%s", w, doc)
		}
	}
	if strings.Contains(doc, DCTRequires) {
		t.Errorf("edge to an unknown doc must be skipped:\n%s", doc)
	}
	if strings.Count(doc, "<"+ProvWasGeneratedBy+">") != 1 {
		t.Errorf("generated_by must be emitted once (by DocTriples), got:\n%s", doc)
	}
}
