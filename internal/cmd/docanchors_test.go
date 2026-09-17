package cmd

import (
	"strconv"
	"strings"
	"testing"
)

// skewedBody has a section inserted ahead of the ones that were already
// numbered, so every number and anchor below it is one behind the heading
// structure: what --update-section-anchors exists to fix.
const skewedBody = `# Anchors Document

## 1. First {#sec-1}

## 1. Inserted {#sec-1}

### 1.1 Under the insert {#sec-1.1}

## 2. Last {#sec-2}
`

func TestDocUpdateSectionAnchors(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	// add renumbers before the body is posted, so the document is stored
	// with the numbering the headings imply.
	out, err := runLode(t, "doc", "add", "--project", "proj", "--kind", "spec",
		"--number", "1", "--slug", "anchors", "--file", writeDocFile(t, skewedBody),
		"--update-section-anchors", "--json")
	if err != nil {
		t.Fatalf("doc add: %v\noutput: %s", err, out)
	}
	id := strconv.FormatInt(docJSON(t, out).ID, 10)

	out, err = runLode(t, "doc", "show", id)
	if err != nil {
		t.Fatalf("doc show: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"2. Inserted", "2.1 Under the insert", "3. Last"} {
		if !strings.Contains(out, want) {
			t.Errorf("doc add --update-section-anchors: body missing %q\n%s", want, out)
		}
	}

	// edit does the same on a draft.
	out, err = runLode(t, "doc", "edit", id, "--file", writeDocFile(t, skewedBody),
		"--update-section-anchors", "--json")
	if err != nil {
		t.Fatalf("doc edit: %v\noutput: %s", err, out)
	}
	if out, err = runLode(t, "doc", "show", id); err != nil {
		t.Fatalf("doc show: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "{#sec-2.1}") {
		t.Errorf("doc edit --update-section-anchors: want the renumbered anchor\n%s", out)
	}

	// Once accepted, the anchors are published and the flag is refused
	// rather than moving them (025 §6).
	if out, err = runLode(t, "doc", "accept", id, "--json"); err != nil {
		t.Fatalf("doc accept: %v\noutput: %s", err, out)
	}
	out, err = runLode(t, "doc", "edit", id, "--file", writeDocFile(t, skewedBody),
		"--update-section-anchors", "--note", "n", "--json")
	if err == nil {
		t.Fatalf("doc edit on an accepted document: want a refusal, got: %s", out)
	}
	if !strings.Contains(err.Error()+out, "025 §6") {
		t.Errorf("refusal = %v / %s, want it to cite 025 §6", err, out)
	}
}
