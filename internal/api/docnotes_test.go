package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocNotes walks POST/GET /api/v1/docs/{id}/notes (025 §8.5): a note
// against a real anchor lands and comes back on the note list and on the
// document's detail, and an anchor the document does not have is a 422 rather
// than a stored row nobody would ever meet.
func TestDocNotes(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-noted", 25)

	rr := doReq(t, h, "POST", docPath(spec.ID, "/notes"), token,
		model.AddDocNoteInput{Anchor: "sec-2", Body: "this needs an example", Session: "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("add note status = %d, body %s", rr.Code, rr.Body.String())
	}
	var note model.DocNote
	decodeInto(t, rr, &note)
	if note.ID == 0 || note.Anchor != "sec-2" || note.CreatedBy != "alice" || note.Session != "sess-1" {
		t.Fatalf("note = %+v, want sec-2 by alice in sess-1", note)
	}

	rr = doReq(t, h, "GET", docPath(spec.ID, "/notes"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list notes status = %d, body %s", rr.Code, rr.Body.String())
	}
	var notes []model.DocNote
	decodeInto(t, rr, &notes)
	if len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("notes = %+v, want the one just added", notes)
	}

	// The detail response carries them, so `lode doc get --json` needs no
	// second request to render a section's notes.
	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get doc status = %d, body %s", rr.Code, rr.Body.String())
	}
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if len(detail.Notes) != 1 || detail.Notes[0].Anchor != "sec-2" {
		t.Fatalf("detail.Notes = %+v, want the sec-2 note", detail.Notes)
	}

	rr = doReq(t, h, "POST", docPath(spec.ID, "/notes"), token,
		model.AddDocNoteInput{Anchor: "sec-99", Body: "nowhere"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("note on sec-99 status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestDocListHasNotes: ?has_notes=true selects exactly the documents carrying
// a note, server-side.
func TestDocListHasNotes(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	noted := acceptedSpec(t, h, token, "proj", "025-has-notes", 25)
	acceptedSpec(t, h, token, "proj", "026-quiet", 26)

	rr := doReq(t, h, "POST", docPath(noted.ID, "/notes"), token,
		model.AddDocNoteInput{Anchor: "sec-1", Body: "scope is too wide"})
	if rr.Code != http.StatusOK {
		t.Fatalf("add note status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/docs?has_notes=true", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rr.Code, rr.Body.String())
	}
	var list model.DocListResponse
	decodeInto(t, rr, &list)
	if len(list.Docs) != 1 || list.Docs[0].ID != noted.ID {
		t.Fatalf("docs = %+v, want only the noted one (%d)", list.Docs, noted.ID)
	}
}
