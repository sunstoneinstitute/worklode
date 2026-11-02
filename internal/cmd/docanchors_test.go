package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
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
	// rather than moving them (WL-REQ-167).
	if out, err = runLode(t, "doc", "accept", id, "--json"); err != nil {
		t.Fatalf("doc accept: %v\noutput: %s", err, out)
	}
	out, err = runLode(t, "doc", "edit", id, "--file", writeDocFile(t, skewedBody),
		"--update-section-anchors", "--note", "n", "--json")
	if err == nil {
		t.Fatalf("doc edit on an accepted document: want a refusal, got: %s", out)
	}
	if !strings.Contains(err.Error()+out, "WL-RULE-201") {
		t.Errorf("refusal = %v / %s, want it to cite WL-RULE-201", err, out)
	}
}

const anchorsBody = `# Anchored

Template text.

## 1. First {#sec-1}

Body with a fence:

` + "```" + `
## not a heading
` + "```" + `

## 2. Second {#sec-2}

More.
`

// TestDocShowAnchors: --anchors writes the body exactly as GetDoc returns it
// and a sidecar naming the doc, its version, and runs that tile the body
// (WL-REQ-1776).
func TestDocShowAnchors(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	out, err := runLode(t, "doc", "add", "--project", "proj", "--kind", "spec",
		"--number", "1", "--slug", "anch", "--file", writeDocFile(t, anchorsBody), "--json")
	if err != nil {
		t.Fatalf("doc add: %v\n%s", err, out)
	}
	id := docJSON(t, out).ID
	if out, err := runLode(t, "doc", "accept", strconv.FormatInt(id, 10)); err != nil {
		t.Fatalf("doc accept: %v\n%s", err, out)
	}
	dir := t.TempDir()
	body, side := filepath.Join(dir, "doc.md"), filepath.Join(dir, "doc.anchors.json")
	if out, err := runLode(t, "doc", "show", strconv.FormatInt(id, 10), "--anchors", side, "--out", body); err != nil {
		t.Fatalf("doc show --anchors: %v\n%s", err, out)
	}
	docs, _, err := c.GetDoc(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(body)
	if string(got) != string(docs.Body) {
		t.Errorf("written body differs from GetDoc's:\n%q\n%q", got, docs.Body)
	}
	var sc designdoc.AnchorSidecar
	raw, _ := os.ReadFile(side)
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("sidecar: %v\n%s", err, raw)
	}
	if sc.Doc != id || sc.Version != docs.Version || len(sc.Lines) == 0 {
		t.Errorf("sidecar = %+v, want doc %d version %d and runs", sc, id, docs.Version)
	}
}
