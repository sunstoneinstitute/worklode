package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// gateSpecBody puts "the gate" in sec-2's rule text after a multibyte
// character, so a range can start inside one.
const gateSpecBody = `---
status: draft
---

# Gates

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Gate {#sec-2}

Ærlig: the gate has two lanes.
`

func openThread(t *testing.T, s *Store, reviewID int64, in model.OpenThreadInput, actor string) (*model.ReviewThread, error) {
	t.Helper()
	var th *model.ReviewThread
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.thread_opened", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			th, err = OpenThread(tx, taskTestNow, reviewID, in, actor, "", "", eventID)
			return err
		})
	return th, err
}

func replyThread(t *testing.T, s *Store, threadID int64, body, actor string) (model.ReviewMessage, bool, time.Time, error) {
	t.Helper()
	var m model.ReviewMessage
	var first bool
	var askedAt time.Time
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.thread_replied", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			m, first, askedAt, err = ReplyThread(tx, taskTestNow.Add(time.Minute), threadID, body, actor, eventID)
			return err
		})
	return m, first, askedAt, err
}

func resolveThread(t *testing.T, s *Store, threadID int64, actor, status string) (*model.ReviewThread, error) {
	t.Helper()
	var th *model.ReviewThread
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.thread_resolved", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			th, err = ResolveThread(tx, taskTestNow, threadID, actor, status, eventID)
			return err
		})
	return th, err
}

// threadEvents counts the events of type typ about thread id.
func threadEvents(t *testing.T, s *Store, typ string, id int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE type = $1 AND payload->>'wl:subject' = $2`,
		typ, iri.CURIE(iri.ReviewThread(id))).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// gateFixture is a draft spec owned by stig with reviewers a and b, an open
// review over it, and the rule arranged at sec-2 with its version and text.
func gateFixture(t *testing.T) (s *Store, doc *model.Doc, review *model.Review, ruleID int64, version int, body string) {
	t.Helper()
	s = reviewFixture(t)
	doc = mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "spec", Number: 7, Slug: "gates", Body: gateSpecBody, CreatedBy: "stig",
	})
	assignDocReviewers(t, s, doc.ID, []string{"a", "b"})
	review, err := openReview(t, s, doc.ID)
	if err != nil {
		t.Fatalf("OpenReview: %v", err)
	}
	if err := s.db.QueryRow(
		`SELECT e.rule_id, v.version, v.body FROM doc_entries e
		   JOIN rule_versions v ON v.rule_id = e.rule_id AND v.version = e.rule_version
		  WHERE e.doc_id = $1 AND e.anchor = 'sec-2'`, doc.ID).Scan(&ruleID, &version, &body); err != nil {
		t.Fatalf("read the rule at sec-2: %v", err)
	}
	return s, doc, review, ruleID, version, body
}

// TestThreadLifecycle: a question on bytes of a rule's text, answered by the
// document's owner, resolved; the refusals of WL-REQ-1317's range check; and a
// doc note stored as a bare thread with its section alone (WL-REQ-1667).
func TestThreadLifecycle(t *testing.T) {
	s, doc, r, ruleID, version, body := gateFixture(t)
	start := strings.Index(body, "the gate")
	if start < 0 {
		t.Fatalf("rule body %q has no %q", body, "the gate")
	}
	anchor := model.ReviewAnchor{Rule: ruleID, Version: version, ByteStart: start, ByteEnd: start + len("the gate"), Quote: "the gate"}

	th, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: anchor, Intent: "question", Body: "why two lanes?"}, "a")
	if err != nil {
		t.Fatalf("OpenThread: %v", err)
	}
	if th.Status != "open" || th.Anchor.DocAnchor != "sec-2" || th.Review != r.ID || th.IntentClassifier != "" {
		t.Errorf("thread = %+v, want open on sec-2 in review %d with no classifier", th, r.ID)
	}
	if len(th.Messages) != 1 || th.Messages[0].Role != "reviewer" || th.Messages[0].Seq != 1 {
		t.Errorf("messages = %+v, want one reviewer message at seq 1", th.Messages)
	}
	if n := threadEvents(t, s, "wl:ReviewQuestionAsked", th.ID); n != 1 {
		t.Errorf("wl:ReviewQuestionAsked events = %d, want 1", n)
	}

	m, first, askedAt, err := replyThread(t, s, th.ID, "one per reviewer", "stig")
	if err != nil {
		t.Fatalf("owner reply: %v", err)
	}
	if !first || m.Role != "author" || m.Seq != 2 || !askedAt.Equal(th.CreatedAt) {
		t.Errorf("owner reply = %+v first=%v askedAt=%v, want author at seq 2, first, asked at %v", m, first, askedAt, th.CreatedAt)
	}
	if _, first, _, err = replyThread(t, s, th.ID, "and one more thing", "stig"); err != nil || first {
		t.Errorf("second owner reply first=%v err=%v, want not first", first, err)
	}
	if n := threadEvents(t, s, "wl:ReviewQuestionAnswered", th.ID); n != 1 {
		t.Errorf("wl:ReviewQuestionAnswered events = %d, want 1", n)
	}

	resolved, err := resolveThread(t, s, th.ID, "a", "resolved")
	if err != nil || resolved.Status != "resolved" || resolved.ResolvedBy != "a" {
		t.Fatalf("resolve = %+v, %v; want resolved by a", resolved, err)
	}
	if n := threadEvents(t, s, "wl:ReviewThreadResolved", th.ID); n != 1 {
		t.Errorf("wl:ReviewThreadResolved events = %d, want 1", n)
	}
	if _, _, _, err := replyThread(t, s, th.ID, "late", "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("reply after resolve err = %v, want ErrInvalidInput", err)
	}

	refused := map[string]model.ReviewAnchor{
		"quote differs from the range":  {Rule: ruleID, Version: version, ByteStart: start, ByteEnd: start + len("the gate"), Quote: "the gat"},
		"start inside a multibyte char": {Rule: ruleID, Version: version, ByteStart: strings.Index(body, "Æ") + 1, ByteEnd: start, Quote: body[strings.Index(body, "Æ")+1 : start]},
		"section anchor on a review":    {Doc: doc.ID, DocAnchor: "sec-2"},
		"range past the end":            {Rule: ruleID, Version: version, ByteStart: start, ByteEnd: len(body) + 1, Quote: "x"},
		"range on a spec":               {Doc: doc.ID, Version: doc.Version, ByteStart: 0, ByteEnd: 3, Quote: "---"},
	}
	for name, a := range refused {
		if _, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: a, Intent: "note", Body: "x"}, "a"); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}

	note, err := addDocNote(t, s, doc.ID, model.AddDocNoteInput{Anchor: "sec-2", Body: "lanes are confusing"}, "b")
	if err != nil {
		t.Fatalf("AddDocNote: %v", err)
	}
	var reviewID, version2, byteStart sql.NullInt64
	var quote sql.NullString
	var docAnchor, intent string
	if err := s.db.QueryRow(
		`SELECT review_id, version, byte_start, quote, doc_anchor, intent FROM review_threads WHERE id = $1`, note.ID,
	).Scan(&reviewID, &version2, &byteStart, &quote, &docAnchor, &intent); err != nil {
		t.Fatal(err)
	}
	if reviewID.Valid || version2.Valid || byteStart.Valid || quote.Valid || docAnchor != "sec-2" || intent != "note" {
		t.Errorf("note row review=%v version=%v start=%v quote=%v anchor=%q intent=%q, want a bare note on sec-2 with no range",
			reviewID, version2, byteStart, quote, docAnchor, intent)
	}

	threads, err := s.ThreadsForSubject(t.Context(), doc.ID, 0, 0, "sec-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].ID != th.ID || threads[1].ID != note.ID {
		t.Errorf("ThreadsForSubject(doc, sec-2) = %+v, want the question then the note", threads)
	}
}

// TestThreadsForSubject narrows by status and version and reads a rule's
// threads, obsolete ones included; a bare note's messages count per thread.
func TestThreadsForSubject(t *testing.T) {
	s, doc, r, ruleID, version, body := gateFixture(t)
	start := strings.Index(body, "two lanes")
	a := model.ReviewAnchor{Rule: ruleID, Version: version, ByteStart: start, ByteEnd: start + len("two lanes"), Quote: "two lanes"}
	cr, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: a, Intent: "change_request", Body: "three"}, "b")
	if err != nil {
		t.Fatal(err)
	}
	bare, err := openThread(t, s, 0, model.OpenThreadInput{Anchor: a, Intent: "note", Body: "aside"}, "a")
	if err != nil {
		t.Fatalf("bare range note on a rule: %v", err)
	}
	if bare.Review != 0 || bare.Messages[0].Seq != 1 {
		t.Errorf("bare note = %+v, want no review and seq 1", bare)
	}
	if m, _, _, err := replyThread(t, s, bare.ID, "reply", "b"); err != nil || m.Seq != 2 {
		t.Errorf("bare reply = %+v, %v; want seq 2", m, err)
	}
	if _, err := resolveThread(t, s, cr.ID, "b", "obsolete"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveThread(t, s, cr.ID, "b", "resolved"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("resolve an obsolete thread err = %v, want ErrInvalidInput", err)
	}
	if _, err := resolveThread(t, s, bare.ID, "b", "wontfix"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("resolve with an unknown status err = %v, want ErrInvalidInput", err)
	}

	ids := func(ts []model.ReviewThread) []int64 {
		var out []int64
		for _, th := range ts {
			out = append(out, th.ID)
		}
		return out
	}
	for name, c := range map[string]struct {
		doc, rule int64
		version   int
		status    string
		want      []int64
	}{
		"rule, all":       {0, ruleID, 0, "", []int64{cr.ID, bare.ID}},
		"rule, obsolete":  {0, ruleID, 0, "obsolete", []int64{cr.ID}},
		"rule, version":   {0, ruleID, version, "open", []int64{bare.ID}},
		"rule, other ver": {0, ruleID, version + 1, "", nil},
		"doc, all":        {doc.ID, 0, 0, "", []int64{cr.ID, bare.ID}},
	} {
		got, err := s.ThreadsForSubject(t.Context(), c.doc, c.rule, c.version, "", c.status)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if g := ids(got); len(g) != len(c.want) || (len(g) > 0 && (g[0] != c.want[0] || g[len(g)-1] != c.want[len(c.want)-1])) {
			t.Errorf("%s: threads %v, want %v", name, g, c.want)
		}
	}
	if _, err := s.ThreadsForSubject(t.Context(), doc.ID, ruleID, 0, "", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("both doc and rule err = %v, want ErrInvalidInput", err)
	}
}
