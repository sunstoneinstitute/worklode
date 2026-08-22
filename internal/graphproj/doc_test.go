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
