package store

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocVersionsPlanBodyEdit: editing a plan's body snapshots the version it
// leaves into doc_versions before overwriting it (025 §4.5), and
// ListDocVersions/GetDocVersion serve the archived and the current version
// off that split.
func TestDocVersionsPlanBodyEdit(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "version-plan", Body: planMintBody, CreatedBy: "stig",
	})
	edited := strings.Replace(planMintBody, "Do the first thing.", "Do it now.", 1)
	updated, err := updateDocBody(t, s, doc.ID, edited)
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}

	rows := docVersionRows(t, s, doc.ID)
	if len(rows) != 1 {
		t.Fatalf("doc_versions rows = %+v, want 1 row", rows)
	}
	if rows[0].version != 1 || rows[0].body != planMintBody || rows[0].title != doc.Title {
		t.Errorf("snapshot = %+v, want version 1 of the pre-edit body/title", rows[0])
	}

	versions, err := s.ListDocVersions(t.Context(), doc.ID)
	if err != nil {
		t.Fatalf("ListDocVersions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v, want [2, 1]", versions)
	}

	v1, err := s.GetDocVersion(t.Context(), doc.ID, 1)
	if err != nil {
		t.Fatalf("GetDocVersion(1): %v", err)
	}
	if v1.Body != planMintBody {
		t.Errorf("GetDocVersion(1).Body = %q, want the pre-edit body", v1.Body)
	}

	v2, err := s.GetDocVersion(t.Context(), doc.ID, 2)
	if err != nil {
		t.Fatalf("GetDocVersion(2): %v", err)
	}
	if v2.Body != edited {
		t.Errorf("GetDocVersion(2).Body = %q, want the current body", v2.Body)
	}

	if _, err := s.GetDocVersion(t.Context(), doc.ID, 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDocVersion(3) err = %v, want ErrNotFound", err)
	}
}

// TestDocVersionsRevisionAccept: landing a revision snapshots the accepted
// version it replaces (025 §4.5).
func TestDocVersionsRevisionAccept(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	updated, err := acceptRevision(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}

	rows := docVersionRows(t, s, doc.ID)
	if len(rows) != 1 {
		t.Fatalf("doc_versions rows = %+v, want 1 row", rows)
	}
	if rows[0].version != 1 || rows[0].body != specBody {
		t.Errorf("snapshot = %+v, want version 1 of the accepted body", rows[0])
	}

	v1, err := s.GetDocVersion(t.Context(), doc.ID, 1)
	if err != nil {
		t.Fatalf("GetDocVersion(1): %v", err)
	}
	if v1.Body != specBody {
		t.Errorf("GetDocVersion(1).Body = %q, want the pre-revision body", v1.Body)
	}
}

// TestDocVersionsDraftEditNoSnapshot: a draft spec/ADR body edit does not
// bump docs.version (025 §7), and the snapshot sits inside the
// version-bumping branches only (025 §4.5) — so it must not run here.
func TestDocVersionsDraftEditNoSnapshot(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 92, Slug: "092-x", Body: specBody, CreatedBy: "stig",
	})
	if _, err := updateDocBody(t, s, doc.ID, specBody+"\nmore\n"); err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if rows := docVersionRows(t, s, doc.ID); len(rows) != 0 {
		t.Errorf("doc_versions rows = %+v, want none", rows)
	}
	versions, err := s.ListDocVersions(t.Context(), doc.ID)
	if err != nil {
		t.Fatalf("ListDocVersions: %v", err)
	}
	if len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("versions = %+v, want just [1]", versions)
	}
}

// TestDocReviseOpensOneCandidate: a revision copies the accepted body to edit,
// and a second open revision is refused (025 §7.2, one candidate per doc).
func TestDocReviseOpensOneCandidate(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatalf("GetDocRevision: %v", err)
	}
	if rev.Body != specBody {
		t.Error("candidate body is not a copy of the accepted body")
	}
	if rev.CreatedBy != "ada" {
		t.Errorf("created_by = %q, want ada", rev.CreatedBy)
	}

	if err := reviseDoc(t, s, doc.ID, "stig"); !errors.Is(err, ErrRevisionExists) {
		t.Fatalf("second ReviseDoc err = %v, want ErrRevisionExists", err)
	}
}

// TestDocRevisePlanRejected: plans are edited in place (025 §9), never revised.
func TestDocRevisePlanRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})

	err := reviseDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "in place") {
		t.Errorf("err = %v, want it to say plans are edited in place", err)
	}
}

// TestDocReviseDraftRejected: a draft is edited in place — there is no
// accepted version to revise against.
func TestDocReviseDraftRejected(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	if err := reviseDoc(t, s, doc.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDocUpdateRevision: the candidate body is editable, and a malformed one
// is refused before it can reach the accept gate.
func TestDocUpdateRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}

	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Body != revisedSpecBody {
		t.Error("candidate body not swapped")
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Body != specBody {
		t.Fatal("the accepted body must stay authoritative throughout (025 §7.2)")
	}

	bad := "---\nstatus: draft\n---\n\n# T\n\n## 1. A {#sec-1}\n\na\n\n## 2. B {#sec-1}\n\nb\n"
	if err := updateRevision(t, s, doc.ID, bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput on a duplicate anchor", err)
	}
}

// TestDocUpdateRevisionWithoutOpenRevision: nothing to edit is ErrNotFound.
func TestDocUpdateRevisionWithoutOpenRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if err := updateRevision(t, s, doc.ID, revisedSpecBody); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocDiscardRevisionStanding: the assignee and the revision's author may
// each withdraw an open candidate; a third party may not (025 §7.2).
//
// mustAcceptedSpec's documents are created by stig, and CreateDoc defaults the
// assignee to the creator, so stig is the assignee throughout and ada is the
// proposer.
func TestDocDiscardRevisionStanding(t *testing.T) {
	t.Run("author", func(t *testing.T) {
		s := openDocStore(t)
		doc := mustAcceptedSpec(t, s, "025-x")
		if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("ReviseDoc: %v", err)
		}
		if _, err := discardRevision(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("DiscardRevision by the author: %v", err)
		}
		if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDocRevision after discard = %v, want ErrNotFound", err)
		}
	})

	t.Run("assignee", func(t *testing.T) {
		s := openDocStore(t)
		doc := mustAcceptedSpec(t, s, "025-x")
		if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("ReviseDoc: %v", err)
		}
		if _, err := discardRevision(t, s, doc.ID, "stig"); err != nil {
			t.Fatalf("DiscardRevision by the assignee: %v", err)
		}
		if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDocRevision after discard = %v, want ErrNotFound", err)
		}
	})

	t.Run("third party", func(t *testing.T) {
		s := openDocStore(t)
		doc := mustAcceptedSpec(t, s, "025-x")
		if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
			t.Fatalf("ReviseDoc: %v", err)
		}
		if _, err := discardRevision(t, s, doc.ID, "bob"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("DiscardRevision by a third party = %v, want ErrForbidden", err)
		}
		if _, err := s.GetDocRevision(t.Context(), doc.ID); err != nil {
			t.Fatalf("the refused discard removed the candidate anyway: %v", err)
		}
	})
}

// TestDocDiscardRevisionFreesTheSlot: doc_revisions is keyed on doc_id, so the
// point of a discard is that the next ReviseDoc succeeds immediately rather
// than hitting ErrRevisionExists. The accepted version is untouched by either.
func TestDocDiscardRevisionFreesTheSlot(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	back, err := discardRevision(t, s, doc.ID, "ada")
	if err != nil {
		t.Fatalf("DiscardRevision: %v", err)
	}
	if back.Version != 1 || back.Body != specBody {
		t.Errorf("returned doc = version %d, want the accepted version untouched", back.Version)
	}

	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc after a discard: %v, want the slot free", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.CreatedBy != "stig" || rev.Body != specBody {
		t.Errorf("revision = %+v, want a fresh copy of the accepted body opened by stig", rev)
	}
	got, err := s.GetDoc(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Body != specBody {
		t.Errorf("doc = version %d, want the accepted version untouched by a discard", got.Version)
	}
}

// TestDocDiscardRevisionWithoutOpenRevision: nothing to withdraw is
// ErrNotFound, for the assignee as much as for anyone.
func TestDocDiscardRevisionWithoutOpenRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if _, err := discardRevision(t, s, doc.ID, "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocUpdateRevisionOnSupersededDoc: a document superseded since the
// revision opened has nothing left to land, and says so at the edit rather
// than at the accept gate.
func TestDocUpdateRevisionOnSupersededDoc(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	setDocStatus(t, s, doc.ID, "superseded")

	err := updateRevision(t, s, doc.ID, revisedSpecBody)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "superseded") {
		t.Errorf("err = %v, want it to name the status", err)
	}
}

// TestDocDiscardRevisionOnSupersededDoc: the case that separates discard from
// the other two revision verbs. A candidate stranded on a document superseded
// since it opened can no longer be edited or landed, so withdrawing it must
// still work — otherwise the row is unremovable.
func TestDocDiscardRevisionOnSupersededDoc(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	setDocStatus(t, s, doc.ID, "superseded")

	if _, err := discardRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("DiscardRevision on a superseded doc: %v", err)
	}
	if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDocRevision after discard = %v, want ErrNotFound", err)
	}
}

// TestDocDiscardRevisionLogsTheWithdrawnBody: doc_revisions has no history and
// the discard is a hard delete, so the state_log row is the only surviving
// copy of the withdrawn text.
func TestDocDiscardRevisionLogsTheWithdrawnBody(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := discardRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("DiscardRevision: %v", err)
	}

	var change []byte
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT change FROM state_log
		  WHERE entity_kind = 'doc' AND entity_id = $1
		    AND change->>'new' = 'discarded'
		  ORDER BY id DESC LIMIT 1`, strconv.FormatInt(doc.ID, 10)).Scan(&change); err != nil {
		t.Fatalf("read the discard's state_log row: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(change, &got); err != nil {
		t.Fatal(err)
	}
	if got["discarded_body"] != revisedSpecBody {
		t.Errorf("discarded_body = %q, want the withdrawn candidate", got["discarded_body"])
	}
}

// TestDocAcceptRevisionRejectsRemovedPublishedAnchor: the one invariant that
// survives into draft (025 §7.2) — an anchor the accepted version published
// may not disappear.
func TestDocAcceptRevisionRejectsRemovedPublishedAnchor(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	shortened := "---\nstatus: accepted\n---\n\n# Documents in the backbone\n\n" +
		"## 1. Scope {#sec-1}\n\nScope body.\n\n## 2. Model {#sec-2}\n\nModel body.\n"
	if err := updateRevision(t, s, doc.ID, shortened); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	_, err := acceptRevision(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "sec-2.1") || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("err = %v, want the SectionDiff violation naming sec-2.1", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Version != 1 {
		t.Fatalf("doc = %+v, %v; want the accepted version untouched", got, err)
	}
}

// TestDocAcceptRevisionRejectsRenumber: anchors are immutable, so an accepted
// section is never renumbered (025 §6 rule 3). Renumbering while keeping the
// anchor — "## 3. … {#sec-2}" — is a lintAnchors defect and never reaches the
// diff, so the renumber arrives here the other way: the anchor moves with the
// number and sec-2 reads as removed. Its twin below covers the form that does
// reach rule 3.
func TestDocAcceptRevisionRejectsRenumber(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	renumbered := strings.Replace(revisedSpecBody, "## 2. Model {#sec-2}", "## 3. Model {#sec-3}", 1)
	if err := updateRevision(t, s, doc.ID, renumbered); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	_, err := acceptRevision(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "sec-2") {
		t.Errorf("err = %v, want it to name sec-2", err)
	}
}

// TestDocAcceptRevisionRejectsDroppedNumber: dropping a section's number while
// keeping its anchor passes lintAnchors — which only compares a number it has
// — and so reaches rule 3 as an actual renumber, "2" to "".
func TestDocAcceptRevisionRejectsDroppedNumber(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	unnumbered := strings.Replace(specBody, "## 2. Model {#sec-2}", "## Model {#sec-2}", 1)
	if err := updateRevision(t, s, doc.ID, unnumbered); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	_, err := acceptRevision(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), `sec-2: renumbered from "2" to ""`) {
		t.Errorf("err = %v, want the rule 3 violation naming both numbers", err)
	}
}

// TestDocAcceptRevisionAllowsUnpublishedAnchorRemoval: the append-only gate
// protects anchors the accepted version published (025 §7.2), not every row.
// An unpublished anchor on an accepted document is what a corpus import
// leaves behind, and dropping one is legal.
func TestDocAcceptRevisionAllowsUnpublishedAnchorRemoval(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE doc_sections SET published = false WHERE doc_id = $1 AND anchor = 'sec-2.1'`,
		doc.ID); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	shortened := "---\nstatus: accepted\n---\n\n# Documents in the backbone\n\n" +
		"## 1. Scope {#sec-1}\n\nScope body.\n\n## 2. Model {#sec-2}\n\nModel body.\n"
	if err := updateRevision(t, s, doc.ID, shortened); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	updated, err := acceptRevision(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	if secs := docSections(t, s, doc.ID); len(secs) != 2 {
		t.Errorf("sections = %+v, want sec-2.1 gone", secs)
	}
}

// TestDocAcceptRevision: the clean path — body swapped, version bumped,
// last_revised_in stamped on exactly the changed anchor, the insert published
// from this version, and the candidate row consumed.
func TestDocAcceptRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	updated, err := acceptRevision(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	if updated.Body != revisedSpecBody {
		t.Error("body not swapped")
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	if updated.Status != "accepted" {
		t.Errorf("status = %q, want accepted", updated.Status)
	}

	want := map[string]int{"sec-1": 1, "sec-2": 2, "sec-2.1": 1, "sec-2a": 2}
	secs := docSections(t, s, doc.ID)
	if len(secs) != len(want) {
		t.Fatalf("sections = %+v, want %d", secs, len(want))
	}
	for _, sec := range secs {
		if !sec.Published {
			t.Errorf("section %s not published", sec.Anchor)
		}
		if sec.LastRevisedIn != want[sec.Anchor] {
			t.Errorf("section %s last_revised_in = %d, want %d",
				sec.Anchor, sec.LastRevisedIn, want[sec.Anchor])
		}
	}

	if _, err := s.GetDocRevision(t.Context(), doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDocRevision err = %v, want the candidate consumed", err)
	}
}

// TestDocAcceptRevisionStampsEveryChangedSection: last_revised_in moves on
// each of several changed anchors and on none of the untouched ones — the
// stamp is one UPDATE over the anchor set, so a whole-document or
// first-anchor-only stamp would show here and not in the single-change case.
// It also checks the rebuilt rows' own columns, since they are written from
// parallel arrays where a transposition would silently swap headings.
func TestDocAcceptRevisionStampsEveryChangedSection(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	// sec-2 and sec-2.1 both get new bodies; sec-1 is left verbatim.
	revised := strings.NewReplacer(
		"status: draft", "status: accepted",
		"Model body.", "Model body, revised.",
		"Detail body.", "Detail body, revised.",
	).Replace(specBody)
	if err := updateRevision(t, s, doc.ID, revised); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}

	want := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Scope", Depth: 2, Position: 0, LastRevisedIn: 1, Published: true},
		{Anchor: "sec-2", Number: "2", Heading: "Model", Depth: 2, Position: 1, LastRevisedIn: 2, Published: true},
		{Anchor: "sec-2.1", Number: "2.1", Heading: "Detail", Depth: 3, Position: 2, LastRevisedIn: 2, Published: true},
	}
	if got := docSections(t, s, doc.ID); !slices.Equal(got, want) {
		t.Errorf("sections =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDocAcceptRevisionStampsAnchorlessSubheadingEdit: an edit confined to an
// anchorless subheading moves its anchored ancestor's last_revised_in in the
// database. Section.Body stops at the next heading of any level, so a diff
// over bodies alone would accept this revision as touching nothing and leave
// every coverage claim against sec-2 falsely fresh — the silent-staleness half
// of 025 §6 rule 5.
func TestDocAcceptRevisionStampsAnchorlessSubheadingEdit(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-subheading",
		Body: subheadingSpecBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	revised := strings.NewReplacer(
		"status: draft", "status: accepted",
		"Oldest first.", "Highest priority first.",
	).Replace(subheadingSpecBody)
	if err := updateRevision(t, s, doc.ID, revised); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}

	want := []model.DocSection{
		{Anchor: "sec-1", Number: "1", Heading: "Scope", Depth: 2, Position: 0, LastRevisedIn: 1, Published: true},
		{Anchor: "sec-2", Number: "2", Heading: "Model", Depth: 2, Position: 1, LastRevisedIn: 2, Published: true},
	}
	if got := docSections(t, s, doc.ID); !slices.Equal(got, want) {
		t.Errorf("sections =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDocAcceptRevisionWrongActorForbidden: the revision accept is gated like
// the first one.
func TestDocAcceptRevisionWrongActorForbidden(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "ada"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}

	if _, err := acceptRevision(t, s, doc.ID, "ada"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// TestDocAcceptRevisionWithoutOpenRevision: nothing to accept is ErrNotFound.
func TestDocAcceptRevisionWithoutOpenRevision(t *testing.T) {
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")

	if _, err := acceptRevision(t, s, doc.ID, "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDocAcceptRevisionWrongStatus: only an accepted document has a revision
// to land. Both other statuses are refused, whatever left a candidate row
// behind.
func TestDocAcceptRevisionWrongStatus(t *testing.T) {
	for _, status := range []string{"draft", "superseded"} {
		t.Run(status, func(t *testing.T) {
			s := openDocStore(t)
			doc := mustAcceptedSpec(t, s, "025-x")
			if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
				t.Fatalf("ReviseDoc: %v", err)
			}
			setDocStatus(t, s, doc.ID, status)

			_, err := acceptRevision(t, s, doc.ID, "stig")
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), status) {
				t.Errorf("err = %v, want it to name the status", err)
			}
		})
	}
}

// TestDocAcceptRevisionSupersedesReplacedDoc: a replaces edge added by the
// revision takes effect when the revision lands, not before.
func TestDocAcceptRevisionSupersedesReplacedDoc(t *testing.T) {
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	doc := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	withReplaces := strings.Replace(revisedSpecBody,
		"requires: 004-execution-backbone.md#sec-6",
		"requires: 004-execution-backbone.md#sec-6\nreplaces:\n  \".\":\n    - 006-old.md", 1)
	if err := updateRevision(t, s, doc.ID, withReplaces); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if got, err := s.GetDoc(t.Context(), old.ID); err != nil || got.Status != "accepted" {
		t.Fatalf("doc = %+v, %v; want the target untouched until the revision lands", got, err)
	}

	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	got, err := s.GetDoc(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "superseded" {
		t.Errorf("replaced doc status = %q, want superseded", got.Status)
	}
}
