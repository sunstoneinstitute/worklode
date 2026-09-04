package cmd

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docNoteSpecBody is a spec with two anchored sections, so a note has a real
// anchor to land on.
const docNoteSpecBody = `# Notable spec

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// TestDocNoteRendersInline: `lode doc note <ref>#sec-N` leaves a note, and
// `lode show <ref> -s sec-N` renders it under that section rather than in a
// list of its own (025 §8.5).
func TestDocNoteRendersInline(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	d, _, err := c.CreateDoc(ctx, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-notable", Body: docNoteSpecBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, _, err := c.AcceptDoc(ctx, d.ID); err != nil {
		t.Fatalf("accept doc: %v", err)
	}
	id := strconv.FormatInt(d.ID, 10)

	if _, err := runLode(t, "doc", "note", id+"#sec-2", "--body", "needs an example"); err != nil {
		t.Fatalf("doc note: %v", err)
	}

	out, err := runLode(t, "show", "025-notable", "-s", "sec-2")
	if err != nil {
		t.Fatalf("show --section: %v", err)
	}
	if !strings.Contains(out, "needs an example") {
		t.Fatalf("show -s sec-2 = %q, want the note rendered under the section", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("show -s sec-2 = %q, want the note attributed to its author", out)
	}

	// It renders under its own section only: sec-1 carries no note.
	out, err = runLode(t, "show", "025-notable", "-s", "sec-1")
	if err != nil {
		t.Fatalf("show sec-1: %v", err)
	}
	if strings.Contains(out, "needs an example") {
		t.Errorf("show -s sec-1 = %q, want no note from sec-2", out)
	}
}

// TestDocNoteAnchorRequired: a ref with no #sec-N fragment is refused before
// any round trip — a note without a section is not a note.
func TestDocNoteAnchorRequired(t *testing.T) {
	cmd := newDocNoteCmd()
	cmd.SetArgs([]string{"025-notable", "--body", "x"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "#sec-N") {
		t.Fatalf("err = %v, want it to name the #sec-N form", err)
	}
}

// TestDocNoteBodyFile: --body-file reads the note from a file, and from stdin
// for "-", the same convention `lode task edit --body-file` follows (025 §18).
func TestDocNoteBodyFile(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	d, _, err := c.CreateDoc(ctx, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-from-file", Body: docNoteSpecBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, _, err := c.AcceptDoc(ctx, d.ID); err != nil {
		t.Fatalf("accept doc: %v", err)
	}
	path := writeDocFile(t, "read from a file")
	id := strconv.FormatInt(d.ID, 10)
	if _, err := runLode(t, "doc", "note", id+"#sec-1", "--body-file", path); err != nil {
		t.Fatalf("doc note --body-file: %v", err)
	}
	notes, _, err := c.ListDocNotes(ctx, d.ID)
	if err != nil {
		t.Fatalf("ListDocNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].Body != "read from a file" || notes[0].Anchor != "sec-1" {
		t.Fatalf("notes = %+v, want the file's text on sec-1", notes)
	}
}

// TestDocListHasNotes: `lode doc list --has-notes` lists only the documents
// carrying a note, and the filter is the server's.
func TestDocListHasNotes(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	noted, _, err := c.CreateDoc(ctx, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-noted", Body: docNoteSpecBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, _, err := c.AcceptDoc(ctx, noted.ID); err != nil {
		t.Fatalf("accept doc: %v", err)
	}
	if _, _, err := c.CreateDoc(ctx, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 26, Slug: "026-quiet", Body: docNoteSpecBody,
	}); err != nil {
		t.Fatalf("create quiet doc: %v", err)
	}
	if _, _, err := c.AddDocNote(ctx, noted.ID, model.AddDocNoteInput{
		Anchor: "sec-1", Body: "scope is too wide",
	}); err != nil {
		t.Fatalf("AddDocNote: %v", err)
	}

	out, err := runLode(t, "doc", "list", "--has-notes", "--project", "proj")
	if err != nil {
		t.Fatalf("doc list --has-notes: %v", err)
	}
	if !strings.Contains(out, "025-noted") && !strings.Contains(out, "PROJ-SPEC-25") {
		t.Fatalf("doc list --has-notes = %q, want the noted document", out)
	}
	if strings.Contains(out, "026-quiet") || strings.Contains(out, "PROJ-SPEC-26") {
		t.Errorf("doc list --has-notes = %q, want the un-noted document left out", out)
	}
}
