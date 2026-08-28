package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestDocFilterByOwner: DocFilter.Owner narrows ListDocs to that owner's
// documents (025 §7.3, WL-382 task 4), served by the docs_owner partial index
// (migration 0058). It composes with Project and Kind rather than replacing
// them, and an owner with no documents returns an empty list, not an error.
func TestDocFilterByOwner(t *testing.T) {
	s := openDocStore(t)
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	stigAdr := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 1, Slug: "001-x", Body: specBody, CreatedBy: "stig",
	})
	adaSpec := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 25, Slug: "025-y", Body: specBody, CreatedBy: "ada",
	})

	docs, err := s.ListDocs(t.Context(), DocFilter{Owner: "stig"})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("owner=stig: got %d docs, want 2", len(docs))
	}

	docs, err = s.ListDocs(t.Context(), DocFilter{Owner: "stig", Kind: "adr"})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != stigAdr.ID {
		t.Fatalf("owner=stig,kind=adr = %+v, want just the ADR", docs)
	}

	docs, err = s.ListDocs(t.Context(), DocFilter{Owner: "ada", Project: "p2"})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != adaSpec.ID {
		t.Fatalf("owner=ada,project=p2 = %+v, want just ada's spec", docs)
	}

	docs, err = s.ListDocs(t.Context(), DocFilter{Owner: "ada", Project: "p1"})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("owner=ada,project=p1 = %+v, want none: the composed filter excludes ada's other project", docs)
	}

	docs, err = s.ListDocs(t.Context(), DocFilter{Owner: "nobody"})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("owner=nobody = %+v, want an empty list, not an error", docs)
	}
}

// TestDocTransferOwner: the owner may hand the document to another actor
// (025 §7.3), which lands as a doc.owner_changed event and a state_log entry
// naming the old and new owner.
func TestDocTransferOwner(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, eventID, err := transferDocOwner(t, s, doc.ID, "ada", "stig")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "ada" {
		t.Errorf("owner = %q, want ada", got.Owner)
	}

	ev, err := s.GetEvent(t.Context(), eventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if ev.Type != "doc.owner_changed" {
		t.Errorf("event type = %q, want doc.owner_changed", ev.Type)
	}

	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(doc.ID, 10))
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	last := entries[len(entries)-1]
	if !strings.Contains(last.Change, `"field": "owner"`) ||
		!strings.Contains(last.Change, `"old": "stig"`) || !strings.Contains(last.Change, `"new": "ada"`) {
		t.Errorf("state log entry = %q, want field/old/new naming the transfer", last.Change)
	}
}

// TestDocTransferOwnerAdminNotOwner: an admin may transfer a document it does
// not own (025 §7.3) — the mechanism a document whose owner left the org is
// rescued through.
func TestDocTransferOwnerAdminNotOwner(t *testing.T) {
	s := openDocStore(t)
	if err := s.CreateActor(t.Context(), "root", "human", "root", true); err != nil {
		t.Fatalf("create admin actor: %v", err)
	}
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, _, err := transferDocOwner(t, s, doc.ID, "ada", "root")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "ada" {
		t.Errorf("owner = %q, want ada", got.Owner)
	}
}

// nullDocOwner clears a document's owner column directly, for the ownerless
// cases only a raw write can reach: CreateDoc always defaults owner to the
// creator.
func nullDocOwner(t *testing.T, s *Store, id int64) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(), `UPDATE docs SET owner = NULL WHERE id = $1`, id); err != nil {
		t.Fatalf("null owner of doc %d: %v", id, err)
	}
}

// TestDocTransferOwnerEmptyActorForbidden: an ownerless document's owner
// column flattens to "" (lockDoc), so an empty actorID must not satisfy the
// owner-match branch by accident — the same defense checkDocOwner and
// checkRevisionDiscarder both keep.
func TestDocTransferOwnerEmptyActorForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	nullDocOwner(t, s, doc.ID)

	_, _, err := transferDocOwner(t, s, doc.ID, "ada", "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// TestDocTransferOwnerAdminRescuesOwnerlessDoc: an admin can still transfer a
// document with no owner — the rescue path 025 §7.3 exists for, and the one
// the empty-actorID defense above must not break.
func TestDocTransferOwnerAdminRescuesOwnerlessDoc(t *testing.T) {
	s := openDocStore(t)
	if err := s.CreateActor(t.Context(), "root", "human", "root", true); err != nil {
		t.Fatalf("create admin actor: %v", err)
	}
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	nullDocOwner(t, s, doc.ID)

	got, _, err := transferDocOwner(t, s, doc.ID, "ada", "root")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "ada" {
		t.Errorf("owner = %q, want ada", got.Owner)
	}
}

// TestDocTransferOwnerThirdPartyForbidden: neither the owner nor an admin
// refuses with ErrForbidden.
func TestDocTransferOwnerThirdPartyForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	_, _, err := transferDocOwner(t, s, doc.ID, "ada", "ada")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Owner != "stig" {
		t.Fatalf("doc owner = %+v, %v; want it still stig", got, err)
	}
}

// TestDocTransferOwnerSelfNoop: transferring to the actor that already owns
// the document is a legal no-op, not a refusal — Task 5's bulk transfer loops
// this endpoint over many documents and relies on re-runs being safe.
func TestDocTransferOwnerSelfNoop(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	got, _, err := transferDocOwner(t, s, doc.ID, "stig", "stig")
	if err != nil {
		t.Fatalf("TransferDocOwner: %v", err)
	}
	if got.Owner != "stig" {
		t.Errorf("owner = %q, want stig", got.Owner)
	}
	entries, err := s.StateLogForEntity(t.Context(), "doc", strconv.FormatInt(doc.ID, 10))
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("state log entries = %d, want 1 (no-op writes nothing new)", len(entries))
	}
}

// TestDocTransferOwnerUnknownActor: the new owner must be an existing actor
// (owner REFERENCES actors), surfaced as ErrInvalidInput naming the field
// rather than a raw constraint failure.
func TestDocTransferOwnerUnknownActor(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	_, _, err := transferDocOwner(t, s, doc.ID, "nobody", "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
