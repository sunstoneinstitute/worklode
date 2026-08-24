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
	doc := string(Document(DocTriples(d)))
	subj := "<" + iri.Doc("025-backbone") + ">"
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
	doc = string(Document(DocTriples(d)))
	if strings.Contains(doc, ProvWasGeneratedBy) {
		t.Errorf("wasGeneratedBy emitted with no authoring task:\n%s", doc)
	}
	if !strings.Contains(doc, "<"+iri.Term("Plan")+">") {
		t.Errorf("plan kind did not map to wl:Plan:\n%s", doc)
	}
}

// TestSectionTriples covers 025 §3.3's section projection: a wl:Section node
// per published section, and the derived supersession the document store keeps
// as a query rather than a column (025 §6.2).
func TestSectionTriples(t *testing.T) {
	d := model.Doc{Slug: "025-backbone", Kind: "spec", Status: "accepted"}
	sections := []model.DocSection{
		{Anchor: "sec-1", Heading: "Scope", Published: true},
		{Anchor: "sec-2", Heading: "Retired", Published: true},
		{Anchor: "sec-3", Heading: "Gone", Published: true},
		{Anchor: "sec-4", Heading: "Still drafting", Published: false},
	}
	in := []model.DocEdge{
		// sec-2 was replaced by a section that resolves to a document here.
		{Type: "isReplacedBy", FromAnchor: "sec-2", ToSlug: "040-successor", ToAnchor: "sec-7"},
		// sec-3 was replaced by something that resolves to no section.
		{Type: "isReplacedBy", FromAnchor: "sec-3"},
		// An unrelated inbound edge type must not supersede anything.
		{Type: "isCoveredBy", FromAnchor: "sec-1", ToSlug: "some-plan"},
	}
	doc := string(Document(SectionTriples(d, sections, in)))

	sec1 := "<" + iri.Section("025-backbone", "sec-1") + ">"
	sec2 := "<" + iri.Section("025-backbone", "sec-2") + ">"
	sec3 := "<" + iri.Section("025-backbone", "sec-3") + ">"
	for _, want := range []string{
		sec1 + " <" + RDFType + "> <" + iri.Term("Section") + ">",
		sec1 + " <" + DCTTitle + "> \"Scope\"",
		sec1 + " <" + DCTIsPartOf + "> <" + iri.Doc("025-backbone") + ">",
		// A live section carries its document's status, not a superseded one.
		sec1 + " <" + iri.Term("status") + "> <" + iri.Concept("accepted") + ">",
		sec2 + " <" + iri.Term("status") + "> <" + iri.Concept("superseded") + ">",
		sec2 + " <" + DCTIsReplacedBy + "> <" + iri.Section("040-successor", "sec-7") + ">",
		// Superseded with no nameable successor: status, and no dangling edge.
		sec3 + " <" + iri.Term("status") + "> <" + iri.Concept("superseded") + ">",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("section projection missing %q\n%s", want, doc)
		}
	}
	if strings.Contains(doc, sec3+" <"+DCTIsReplacedBy+">") {
		t.Errorf("sec-3 has no successor to name, but an isReplacedBy was emitted:\n%s", doc)
	}
	if strings.Contains(doc, iri.Section("025-backbone", "sec-4")) {
		t.Errorf("unpublished section projected, so its anchor could still change:\n%s", doc)
	}

	// A superseded document supersedes every section it published, without
	// needing an edge per section.
	d.Status = "superseded"
	doc = string(Document(SectionTriples(d, sections, nil)))
	for _, subj := range []string{sec1, sec2, sec3} {
		want := subj + " <" + iri.Term("status") + "> <" + iri.Concept("superseded") + ">"
		if !strings.Contains(doc, want) {
			t.Errorf("section projection missing %q\n%s", want, doc)
		}
	}

	// A plan carries no sections at all (025 §9), so it projects none.
	if got := SectionTriples(model.Doc{Slug: "a-plan", Kind: "plan"}, nil, nil); got != nil {
		t.Errorf("plan projected %d section triples, want none", len(got))
	}
}
