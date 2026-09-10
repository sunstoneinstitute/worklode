package store

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// AcceptDoc's approval gate (025 §7.3, 029 §7.3) and the pre-flight that has
// to answer the same — checkDocReviewerGate and checkDocReviewerGateCtx, in
// both their halves.

// approveDocReviewerLane resolves reviewer's open lane on doc's current
// version 'approved', the way a decide-approval act would.
func approveDocReviewerLane(t *testing.T, s *Store, docID int64, reviewer string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	a, err := OpenApprovalForLane(tx, "doc", DocEntityID(docID), reviewer)
	if err != nil {
		t.Fatalf("open approval for lane %s: %v", reviewer, err)
	}
	if err := ResolveApproval(tx, a.ID, "approved", &reviewer, s.Now()); err != nil {
		t.Fatalf("resolve approval for lane %s: %v", reviewer, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestAcceptDocReviewerGate is AcceptDoc's mechanical multi-approval gate
// (025 §7.3): a spec/ADR with an assigned reviewer set cannot be accepted
// until every reviewer has approved the current version, and a document with
// no reviewers assigned accepts exactly as it did before this gate existed.
func TestAcceptDocReviewerGate(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	seedDocsActor(t, s, "rev-a")
	seedDocsActor(t, s, "rev-b")
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	assignDocReviewers(t, s, doc.ID, []string{"rev-a", "rev-b"})

	// submit -> two awaiting lanes at v1.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := RequestDocApproval(tx, s.Now(), doc.ID, doc.Version); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	_, _, err = acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("accept with two open lanes: err = %v, want ErrForbidden", err)
	}
	if !strings.Contains(err.Error(), "rev-a") || !strings.Contains(err.Error(), "rev-b") {
		t.Errorf("err = %v, want it to name both rev-a and rev-b", err)
	}
	if !strings.Contains(err.Error(), "/reviews") {
		t.Errorf("err = %v, want it to point at /reviews", err)
	}

	// approve rev-a's lane; accept again -> still refused, names only rev-b.
	approveDocReviewerLane(t, s, doc.ID, "rev-a")
	_, _, err = acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("accept with rev-b still open: err = %v, want ErrForbidden", err)
	}
	if strings.Contains(err.Error(), "rev-a") {
		t.Errorf("err = %v, should no longer name rev-a", err)
	}
	if !strings.Contains(err.Error(), "rev-b") {
		t.Errorf("err = %v, want it to name rev-b", err)
	}

	// approve rev-b's lane; accept -> succeeds.
	approveDocReviewerLane(t, s, doc.ID, "rev-b")
	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("accept once both reviewers approved: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}

	// a document with no reviewers accepts as today.
	doc2 := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-y", Body: specBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc2.ID, "stig"); err != nil {
		t.Fatalf("accept doc with no reviewers: %v", err)
	}
}

// openDocApproval opens one unlaned awaiting row on a document revision —
// what the doc-lifecycle watcher's approval-on-submit rule materializes on
// submission (029 §7.3). It names no reviewer, so the reviewer-set gate never
// sees it.
func openDocApproval(t *testing.T, s *Store, docID int64, version int) int64 {
	t.Helper()
	tx := mustBegin(t, s)
	if _, err := InsertAwaitingApproval(tx, s.Now(), "doc", DocEntityID(docID),
		strconv.Itoa(version), "", nil, nil, nil); err != nil {
		t.Fatalf("open approval on doc %d@%d: %v", docID, version, err)
	}
	a, err := ApprovalByKey(tx, "doc", DocEntityID(docID), strconv.Itoa(version), "")
	if err != nil {
		t.Fatalf("read approval on doc %d@%d: %v", docID, version, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// approveApproval resolves one row as approved, by an actor who did not
// author the document.
func approveApproval(t *testing.T, s *Store, id int64) {
	t.Helper()
	tx := mustBegin(t, s)
	actor := "ada"
	if err := ResolveApproval(tx, id, "approved", &actor, s.Now()); err != nil {
		t.Fatalf("approve approval %d: %v", id, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestAcceptDocOpenApprovalGate is the second half of AcceptDoc's gate (029
// §7.3): the unlaned row a submission materializes names no reviewer, so the
// reviewer-set gate above cannot see it, and accepting past an open decision
// would make the row a lie. An open row at the version being accepted refuses;
// approving it lets the accept through; an open row at an older version never
// blocks, since the newer version's own row superseded it. CheckDocAcceptable
// must answer the same at every step.
func TestAcceptDocOpenApprovalGate(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 27, Slug: "027-z", Body: specBody, CreatedBy: "stig",
	})
	// An open row left behind by a version this document has moved past. It
	// stays open for the whole test: it must never block.
	stale := openDocApproval(t, s, doc.ID, doc.Version-1)
	if _, err := s.CheckDocAcceptable(t.Context(), doc.ID, "stig"); err != nil {
		t.Fatalf("CheckDocAcceptable with only an older version's row open: %v", err)
	}

	current := openDocApproval(t, s, doc.ID, doc.Version)
	_, _, err := acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrForbidden) || !errors.Is(err, ErrMissingApprovals) {
		t.Fatalf("accept with an open row at the current version: err = %v, want ErrForbidden/ErrMissingApprovals", err)
	}
	if !strings.Contains(err.Error(), "1") || !strings.Contains(err.Error(), "/reviews") {
		t.Errorf("err = %v, want it to name the count and point at /reviews", err)
	}
	if _, err := s.CheckDocAcceptable(t.Context(), doc.ID, "stig"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CheckDocAcceptable disagrees with AcceptDoc: err = %v, want ErrForbidden", err)
	}

	approveApproval(t, s, current)
	if _, err := s.CheckDocAcceptable(t.Context(), doc.ID, "stig"); err != nil {
		t.Fatalf("CheckDocAcceptable once approved: %v", err)
	}
	accepted, _, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("accept once the current version's row is approved: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}
	if stale == current {
		t.Fatal("the two rows collapsed into one: the older version's row is not distinct")
	}
}

// TestCheckDocAcceptableReviewerGate: the pre-flight CheckDocAcceptable runs
// (025 §7.3) must answer the same as AcceptDoc, so a caller that checks
// first never sees "acceptable" for a document AcceptDoc would then refuse.
func TestCheckDocAcceptableReviewerGate(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	seedDocsActor(t, s, "rev-a")
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	assignDocReviewers(t, s, doc.ID, []string{"rev-a"})
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := RequestDocApproval(tx, s.Now(), doc.ID, doc.Version); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CheckDocAcceptable(t.Context(), doc.ID, "stig"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CheckDocAcceptable with an open lane: err = %v, want ErrForbidden", err)
	}

	approveDocReviewerLane(t, s, doc.ID, "rev-a")
	settled, err := s.CheckDocAcceptable(t.Context(), doc.ID, "stig")
	if err != nil {
		t.Fatalf("CheckDocAcceptable once approved: %v", err)
	}
	if settled {
		t.Errorf("settled = true, want false: this is a first accept, not a re-accepted plan")
	}
}
