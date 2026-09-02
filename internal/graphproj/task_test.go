package graphproj

import (
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// triplesSet renders each triple's predicate+object into a set of strings,
// keyed the same way regardless of triple order, and returns the distinct
// subjects seen.
func triplesSet(t *testing.T, triples []Triple) (set map[string]bool, subjects map[string]bool) {
	t.Helper()
	set = make(map[string]bool)
	subjects = make(map[string]bool)
	for _, tr := range triples {
		set[tr.P+" "+tr.O.String()] = true
		subjects[tr.S] = true
	}
	return set, subjects
}

func TestTaskTriples(t *testing.T) {
	created := time.Date(2026, 7, 30, 12, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	updated := time.Date(2026, 7, 31, 9, 30, 0, 0, time.UTC)

	task := model.Task{
		ID:        "WL-42",
		Project:   "worklode",
		Title:     "Fix login",
		Priority:  "high",
		Kind:      "bug",
		State:     "in_progress",
		Concern:   "security",
		HumanOnly: true,
		CreatedBy: "stig",
		CreatedAt: created,
		UpdatedAt: updated,
	}

	out := []model.Edge{
		{From: "WL-42", To: "WL-1", Type: "child_of"},     // WL-42 is a child of WL-1
		{From: "WL-42", To: "WL-99", Type: "blocks"},      // WL-42 blocks WL-99
		{From: "WL-42", To: "WL-7", Type: "follow_up_to"}, // WL-42 is a follow-up to WL-7
		{From: "WL-42", To: "WL-5", Type: "duplicate_of"}, // WL-42 duplicates WL-5
	}
	in := []model.Edge{
		{From: "WL-3", To: "WL-42", Type: "blocks"},       // WL-3 blocks WL-42
		{From: "WL-9", To: "WL-42", Type: "child_of"},     // WL-9 is a child of WL-42 — belongs on WL-9's subject, must emit nothing here
		{From: "WL-8", To: "WL-42", Type: "follow_up_to"}, // WL-8 is a follow-up to WL-42 — no named inverse, must emit nothing here
		{From: "WL-6", To: "WL-42", Type: "duplicate_of"}, // WL-6 duplicates WL-42 — no named inverse, must emit nothing here
	}

	triples := TaskTriples(task, out, in)

	set, subjects := triplesSet(t, triples)

	wantSubject := iri.Task("WL-42")
	if len(subjects) != 1 || !subjects[wantSubject] {
		t.Fatalf("subjects = %v; want exactly {%s}", subjects, wantSubject)
	}

	want := map[string]bool{
		RDFType + " " + IRIRef(iri.Term("Task")).String():                                   true,
		DCTTitle + " " + Text("Fix login").String():                                         true,
		iri.Term("taskState") + " " + Text("in_progress").String():                          true,
		iri.Term("taskKind") + " " + IRIRef(iri.Concept("bug")).String():                    true,
		iri.Term("priority") + " " + Text("high").String():                                  true,
		iri.Term("concern") + " " + Text("security").String():                               true,
		iri.Term("humanOnly") + " " + Typed("true", XSDBoolean).String():                    true,
		iri.Term("inProject") + " " + IRIRef(iri.Project("worklode")).String():              true,
		ProvWasAssociatedWith + " " + IRIRef(iri.Agent("stig")).String():                    true,
		DCTCreated + " " + Typed(created.UTC().Format(time.RFC3339), XSDDateTime).String():  true,
		DCTModified + " " + Typed(updated.UTC().Format(time.RFC3339), XSDDateTime).String(): true,
		DCTIsPartOf + " " + IRIRef(iri.Task("WL-1")).String():                               true,
		iri.Term("blocks") + " " + IRIRef(iri.Task("WL-99")).String():                       true,
		iri.Term("followUpTo") + " " + IRIRef(iri.Task("WL-7")).String():                    true,
		iri.Term("duplicateOf") + " " + IRIRef(iri.Task("WL-5")).String():                   true,
		iri.Term("dependsOn") + " " + IRIRef(iri.Task("WL-3")).String():                     true,
	}

	if len(set) != len(want) {
		t.Fatalf("got %d distinct triples, want %d\ngot:  %v\nwant: %v", len(set), len(want), set, want)
	}
	for k := range want {
		if !set[k] {
			t.Errorf("missing triple: %s", k)
		}
	}
	for k := range set {
		if !want[k] {
			t.Errorf("unexpected triple: %s", k)
		}
	}
}

func TestTaskTriplesOmitsUnsetOptionalFields(t *testing.T) {
	task := model.Task{
		ID:        "WL-1",
		Project:   "worklode",
		Title:     "Nothing optional set",
		Priority:  "low",
		Kind:      "chore",
		State:     "draft",
		Concern:   "",
		HumanOnly: false,
		CreatedBy: "",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	triples := TaskTriples(task, nil, nil)

	for _, tr := range triples {
		if tr.P == iri.Term("concern") {
			t.Errorf("unexpected wl:concern triple for empty Concern: %v", tr)
		}
		if tr.P == iri.Term("humanOnly") {
			t.Errorf("unexpected wl:humanOnly triple for a task that is not human-only: %v", tr)
		}
		if tr.P == ProvWasAssociatedWith {
			t.Errorf("unexpected prov:wasAssociatedWith triple for empty CreatedBy: %v", tr)
		}
	}
}

func TestProjectTriples(t *testing.T) {
	proj := model.Project{
		ID:   "worklode",
		Name: "Worklode",
		Key:  "WL",
	}

	triples := ProjectTriples(proj)

	set, subjects := triplesSet(t, triples)

	wantSubject := iri.Project("worklode")
	if len(subjects) != 1 || !subjects[wantSubject] {
		t.Fatalf("subjects = %v; want exactly {%s}", subjects, wantSubject)
	}

	want := map[string]bool{
		RDFType + " " + IRIRef(iri.Term("Project")).String(): true,
		DCTTitle + " " + Text("Worklode").String():           true,
	}
	if len(set) != len(want) {
		t.Fatalf("got %d distinct triples, want %d\ngot:  %v\nwant: %v", len(set), len(want), set, want)
	}
	for k := range want {
		if !set[k] {
			t.Errorf("missing triple: %s", k)
		}
	}
}
