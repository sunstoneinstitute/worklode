package graphproj

import (
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocTriples covers the v1 document projection (WL-289): the canonical
// node's type, title, status concept, version literal, timestamps, and the
// prov:wasGeneratedBy edge when the authoring task is recorded.
func TestDocTriples(t *testing.T) {
	at := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	d := model.Doc{
		ID: 7, Project: "alpha", Kind: "spec", Number: 25,
		Slug: "025-backbone", Title: "Spec 025 — Backbone", Status: "accepted",
		Version: 3, GeneratedByTask: "AL-9", CreatedAt: at, UpdatedAt: at,
	}
	doc := string(Document(DocTriples(d, nil)))
	subj := "<" + iri.Doc("spec-alpha-025") + ">"
	for _, want := range []string{
		subj + " <" + RDFType + "> <" + iri.Term("Spec") + ">",
		subj + " <" + DCTTitle + "> \"Spec 025 — Backbone\"",
		subj + " <" + iri.Term("status") + "> <" + iri.Concept("accepted") + ">",
		subj + " <" + DCATVersion + "> \"3\"",
		subj + " <" + ProvWasGeneratedBy + "> <" + iri.Task("AL-9") + ">",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc projection missing %q\n%s", want, doc)
		}
	}
	// Nil versions (a draft): no version-pointer edges at all.
	if strings.Contains(doc, DCATHasVersion) || strings.Contains(doc, DCATHasCurrentVersion) {
		t.Errorf("version pointers emitted with nil versions:\n%s", doc)
	}

	// Subject-completeness: every line's subject is the doc's IRI.
	for _, line := range strings.Split(strings.TrimSpace(doc), "\n") {
		if line != "" && !strings.HasPrefix(line, subj+" ") {
			t.Errorf("foreign subject in doc projection: %s", line)
		}
	}

	// No authoring task: the prov edge is simply absent, and the kinds map
	// to their classes.
	d.GeneratedByTask = ""
	d.Kind = "plan"
	doc = string(Document(DocTriples(d, nil)))
	if strings.Contains(doc, ProvWasGeneratedBy) {
		t.Errorf("wasGeneratedBy emitted with no authoring task:\n%s", doc)
	}
	if !strings.Contains(doc, "<"+iri.Term("Plan")+">") {
		t.Errorf("plan kind did not map to wl:Plan:\n%s", doc)
	}

	// Non-nil versions: one dcat:hasVersion per entry, plus
	// dcat:hasCurrentVersion naming the doc's current version.
	d.Kind = "spec"
	versions := []model.DocVersionSummary{
		{Version: 1, Title: "v1"},
		{Version: 2, Title: "v2"},
		{Version: 3, Title: "v3"},
	}
	doc = string(Document(DocTriples(d, versions)))
	for _, want := range []string{
		subj + " <" + DCATHasVersion + "> <" + iri.DocVersion("spec-alpha-025", 1) + ">",
		subj + " <" + DCATHasVersion + "> <" + iri.DocVersion("spec-alpha-025", 2) + ">",
		subj + " <" + DCATHasVersion + "> <" + iri.DocVersion("spec-alpha-025", 3) + ">",
		subj + " <" + DCATHasCurrentVersion + "> <" + iri.DocVersion("spec-alpha-025", 3) + ">",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc projection missing %q\n%s", want, doc)
		}
	}
}

// TestDocVersionTriples covers WL-REQ-166's immutable version-snapshot
// projection: the snapshot node's type/title/version/timestamps, the
// previousVersion/wasRevisionOf edges to the prior version, the optional
// dct:issued, and each rule the version arranged pointing dct:isPartOf at
// the snapshot rather than the canonical document.
func TestDocVersionTriples(t *testing.T) {
	d := model.Doc{Slug: "025-backbone", Kind: "spec", Project: "alpha", Number: 25, Status: "accepted"}
	v := model.DocVersion{Version: 3, Title: "Spec 025 — Backbone",
		Issued: "2026-07-26", CreatedAt: time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)}
	v.Rules = []model.DocVersionRule{{Rule: "AL-REQ-12", RuleVersion: 1}, {Heading: "A spec heading"}}
	doc := string(Document(DocVersionTriples(d, v, map[string]string{"AL": "alpha"})))
	subj := "<" + iri.DocVersion("spec-alpha-025", 3) + ">"
	for _, want := range []string{
		subj + " <" + RDFType + "> <" + iri.Term("Spec") + ">",
		subj + " <" + DCATVersion + "> \"3\"",
		subj + " <" + DCATPreviousVersion + "> <" + iri.DocVersion("spec-alpha-025", 2) + ">",
		subj + " <" + ProvWasRevisionOf + "> <" + iri.DocVersion("spec-alpha-025", 2) + ">",
		subj + " <" + DCTIssued + "> \"2026-07-26\"^^<" + XSDDate + ">",
		"<" + iri.Rule("alpha", 12) + "> <" + DCTIsPartOf + "> " + subj,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("version projection missing %q\n%s", want, doc)
		}
	}

	// First version: no previousVersion/wasRevisionOf. No issued date: no
	// dct:issued. Nil sections: a node-only graph whose every subject is the
	// snapshot IRI.
	v = model.DocVersion{Version: 1, Title: "Spec 025 — Backbone",
		CreatedAt: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	doc = string(Document(DocVersionTriples(d, v, nil)))
	subj1 := "<" + iri.DocVersion("spec-alpha-025", 1) + ">"
	if strings.Contains(doc, DCATPreviousVersion) {
		t.Errorf("previousVersion emitted for version 1:\n%s", doc)
	}
	if strings.Contains(doc, ProvWasRevisionOf) {
		t.Errorf("wasRevisionOf emitted for version 1:\n%s", doc)
	}
	if strings.Contains(doc, DCTIssued) {
		t.Errorf("dct:issued emitted with no Issued date:\n%s", doc)
	}
	for _, line := range strings.Split(strings.TrimSpace(doc), "\n") {
		if line != "" && !strings.HasPrefix(line, subj1+" ") {
			t.Errorf("foreign subject in version projection: %s", line)
		}
	}
}

// TestRuleTriples covers the rule projection (WL-REQ-247, WL-REQ-1709): a
// two-rule spec projects two wl:Rule nodes keyed rule/<project-id>/<number>
// and no section node.
func TestRuleTriples(t *testing.T) {
	d := model.Doc{Slug: "025-backbone", Kind: "spec", Project: "alpha", Number: 25, Status: "accepted"}
	rules := []model.Rule{
		{Project: "alpha", ProjectKey: "AL", Ref: "AL-REQ-12", Number: 12, Kind: "requirement", Status: "accepted", Heading: "Scope"},
		{Project: "alpha", ProjectKey: "AL", Ref: "AL-RULE-13", Number: 13, Kind: "invariant", Status: "superseded", Heading: "Retired",
			Edges: []model.RuleEdge{
				{Type: "supersedes", From: "BE-REQ-7", To: "AL-RULE-13"},
				{Type: "references", From: "AL-REQ-12", To: "AL-RULE-13"},
			}},
	}
	ids := map[string]string{"AL": "alpha", "BE": "beta"}
	doc := string(Document(RuleTriples(d, rules, map[string]int{"AL-REQ-12": 2}, ids)))

	r12 := "<" + iri.Rule("alpha", 12) + ">"
	r13 := "<" + iri.Rule("alpha", 13) + ">"
	for _, want := range []string{
		r12 + " <" + RDFType + "> <" + iri.Term("Rule") + ">",
		r12 + " <" + DCTIdentifier + "> \"AL-REQ-12\"",
		r12 + " <" + DCTTitle + "> \"Scope\"",
		r12 + " <" + iri.Term("ruleKind") + "> <" + iri.Concept("requirement") + ">",
		r12 + " <" + iri.Term("status") + "> <" + iri.Concept("accepted") + ">",
		r12 + " <" + DCTIsPartOf + "> <" + iri.Doc("spec-alpha-025") + ">",
		r12 + " <" + iri.Term("lastRevisedIn") + "> <" + iri.DocVersion("spec-alpha-025", 2) + ">",
		r13 + " <" + RDFType + "> <" + iri.Term("Rule") + ">",
		r13 + " <" + iri.Term("status") + "> <" + iri.Concept("superseded") + ">",
		"<" + iri.Rule("beta", 7) + "> <" + DCTReplaces + "> " + r13,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("rule projection missing %q\n%s", want, doc)
		}
	}
	if n := strings.Count(doc, " <"+RDFType+"> <"+iri.Term("Rule")+">"); n != 2 {
		t.Errorf("projected %d rule nodes, want 2:\n%s", n, doc)
	}
	if strings.Contains(doc, iri.Term("Section")) || strings.Contains(doc, iri.IDNS+"section/") {
		t.Errorf("rule projection emitted a section node:\n%s", doc)
	}
	if strings.Contains(doc, r13+" <"+iri.Term("lastRevisedIn")+">") {
		t.Errorf("lastRevisedIn emitted for a rule with no revision version:\n%s", doc)
	}
}

// TestDocEdgeTriples: a plan's covers edge names the rule IRI, never the
// document or a section.
func TestDocEdgeTriples(t *testing.T) {
	d := model.Doc{Kind: "plan", Project: "alpha", Number: 4}
	out := []model.DocEdge{
		{Type: "covers", ToRule: "AL-REQ-12"},
		{Type: "defers", ToRule: "AL-REQ-13"},
		{Type: "requires", ToKind: "spec", ToProject: "alpha", ToNumber: 25},
		{Type: "covers", ToRule: "ZZ-REQ-1"}, // unknown project key: skipped
	}
	doc := string(Document(DocEdgeTriples(d, out, map[string]string{"AL": "alpha"})))
	plan := "<" + iri.Doc("plan-alpha-004") + ">"
	for _, want := range []string{
		plan + " <" + iri.Term("covers") + "> <" + iri.Rule("alpha", 12) + ">",
		plan + " <" + iri.Term("defers") + "> <" + iri.Rule("alpha", 13) + ">",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("edge projection missing %q\n%s", want, doc)
		}
	}
	if n := strings.Count(strings.TrimSpace(doc), "\n") + 1; n != 2 {
		t.Errorf("projected %d edges, want 2:\n%s", n, doc)
	}
}
