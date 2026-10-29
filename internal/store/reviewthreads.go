package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// threadIntents are the WL-REQ-1318 intents, the review_threads_intent_known
// CHECK.
var threadIntents = map[string]bool{"note": true, "change_request": true, "question": true}

// OpenThread opens a thread on a document or a rule (WL-REQ-1317). reviewID 0
// opens a bare note (WL-REQ-1319). A range anchor (Version, ByteStart,
// ByteEnd, Quote) is checked against the stored Markdown of that version and
// its DocAnchor derived from the range; a section anchor (DocAnchor alone) is
// stored with no range (WL-REQ-1667) and is legal only on a bare note. The
// client stated the intent, so intent_classifier stays null (WL-REQ-1885).
func OpenThread(tx *sql.Tx, now time.Time, reviewID int64, in model.OpenThreadInput,
	actorID, taskID, sessionID string, eventID int64) (*model.ReviewThread, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, fmt.Errorf("a thread needs a body: %w", ErrInvalidInput)
	}
	if !threadIntents[in.Intent] {
		return nil, fmt.Errorf("intent %q is not note, change_request or question: %w", in.Intent, ErrInvalidInput)
	}
	a := in.Anchor
	if (a.Doc == 0) == (a.Rule == 0) {
		return nil, fmt.Errorf("a thread anchors to exactly one of a document and a rule: %w", ErrInvalidInput)
	}
	if a.Repo != "" || a.Path != "" || a.Side != "" || a.Line != 0 || a.AnchorText != "" || a.BlobSHA != "" {
		return nil, fmt.Errorf("change anchors come with review surface part 2 (WL-PLAN-133): %w", ErrInvalidInput)
	}
	var rev *model.Review
	if reviewID != 0 {
		var err error
		if rev, err = lockReview(tx, reviewID); err != nil {
			return nil, err
		}
		if err := checkThreadSubject(tx, rev, a); err != nil {
			return nil, err
		}
	}
	isRange := a.Version != 0 || a.ByteEnd != 0 || a.Quote != ""
	switch {
	case isRange:
		anchor, err := checkRangeAnchor(tx, rev, a)
		if err != nil {
			return nil, err
		}
		a.DocAnchor = anchor
	case rev != nil:
		return nil, fmt.Errorf("a review thread anchors to a byte range and its quote (WL-REQ-1317): %w", ErrInvalidInput)
	default:
		a.DocAnchor = strings.TrimPrefix(strings.TrimSpace(a.DocAnchor), "#")
		if a.Doc == 0 || a.DocAnchor == "" {
			return nil, fmt.Errorf("a thread with no byte range anchors to a section of a document: %w", ErrInvalidInput)
		}
		var exists bool
		err := tx.QueryRow(`SELECT true FROM doc_sections WHERE doc_id = $1 AND anchor = $2`, a.Doc, a.DocAnchor).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("doc %d has no section #%s: %w", a.Doc, a.DocAnchor, ErrInvalidInput)
		}
		if err != nil {
			return nil, fmt.Errorf("check section #%s of doc %d: %w", a.DocAnchor, a.Doc, err)
		}
	}

	ts := now.UTC().Truncate(time.Second)
	th := &model.ReviewThread{
		Review: reviewID, Anchor: a, Intent: in.Intent, Status: "open", Author: actorID,
		RaisedByTask: taskID, RaisedBySession: sessionID, CreatedAt: ts,
	}
	var version, start, end sql.NullInt64
	if isRange {
		version = sql.NullInt64{Int64: int64(a.Version), Valid: true}
		start = sql.NullInt64{Int64: int64(a.ByteStart), Valid: true}
		end = sql.NullInt64{Int64: int64(a.ByteEnd), Valid: true}
	}
	if err := tx.QueryRow(
		`INSERT INTO review_threads (review_id, doc_id, rule_id, version, byte_start, byte_end, quote, doc_anchor,
		                             raised_by_task, raised_by_session, intent, status, author_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'open', $12, $13) RETURNING id`,
		nullID(reviewID), nullID(a.Doc), nullID(a.Rule), version, start, end, nullText(a.Quote), nullText(a.DocAnchor),
		nullText(taskID), nullText(sessionID), in.Intent, actorID, ts).Scan(&th.ID); err != nil {
		return nil, fmt.Errorf("open thread: %w", err)
	}
	m, err := addThreadMessage(tx, ts, th.ID, rev, a.Doc, body, actorID)
	if err != nil {
		return nil, err
	}
	th.Messages = []model.ReviewMessage{m}
	if in.Intent == "question" {
		if err := reviewEvent(tx, ts, "wl:ReviewQuestionAsked", th.ID, actorID, eventID,
			map[string]any{"wl:threadIntent": "wlc:question"}); err != nil {
			return nil, err
		}
	}
	return th, nil
}

// lockReview reads and locks one open review row.
func lockReview(tx *sql.Tx, id int64) (*model.Review, error) {
	r, err := scanReviewRow(tx.QueryRow(`SELECT `+reviewColumns+` FROM reviews r WHERE r.id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("review %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read review %d: %w", id, err)
	}
	if r.State == "closed" {
		return nil, fmt.Errorf("review %d is closed: %w", id, ErrInvalidInput)
	}
	return r, nil
}

// checkThreadSubject refuses an anchor outside the review's subject: a
// document review takes threads on the document or on a rule it arranges.
func checkThreadSubject(tx *sql.Tx, r *model.Review, a model.ReviewAnchor) error {
	switch {
	case r.Doc != 0 && a.Doc == r.Doc, r.Rule != 0 && a.Rule == r.Rule:
		return nil
	case r.Doc != 0 && a.Rule != 0:
		var ok bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM doc_entries WHERE doc_id = $1 AND rule_id = $2)`,
			r.Doc, a.Rule).Scan(&ok); err != nil {
			return fmt.Errorf("check rule %d in doc %d: %w", a.Rule, r.Doc, err)
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("the anchor is outside review %d's subject: %w", r.ID, ErrInvalidInput)
}

// checkRangeAnchor checks a byte range against the stored Markdown of the
// version it names (WL-REQ-1317) and returns the section anchor the range
// falls in. A rule's text is rule_versions.body; a plan's is docs.body at
// its current version.
func checkRangeAnchor(tx *sql.Tx, rev *model.Review, a model.ReviewAnchor) (string, error) {
	if a.Version <= 0 || a.Quote == "" {
		return "", fmt.Errorf("a range anchor needs a version and a quote: %w", ErrInvalidInput)
	}
	if a.Rule != 0 {
		var body string
		err := tx.QueryRow(`SELECT body FROM rule_versions WHERE rule_id = $1 AND version = $2`, a.Rule, a.Version).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("rule %d has no version %d: %w", a.Rule, a.Version, ErrInvalidInput)
		}
		if err != nil {
			return "", fmt.Errorf("read rule %d v%d: %w", a.Rule, a.Version, err)
		}
		if err := checkRange(body, a); err != nil {
			return "", err
		}
		// The section the rule is arranged at, in the review's document first.
		var reviewDoc int64
		if rev != nil {
			reviewDoc = rev.Doc
		}
		var anchor string
		err = tx.QueryRow(
			`SELECT coalesce(anchor, '') FROM doc_entries WHERE rule_id = $1
			  ORDER BY doc_id = $2 DESC, doc_id, position LIMIT 1`, a.Rule, reviewDoc).Scan(&anchor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("read arrangement of rule %d: %w", a.Rule, err)
		}
		return anchor, nil
	}
	var kind, body string
	var version int
	err := tx.QueryRow(`SELECT kind, body, version FROM docs WHERE id = $1`, a.Doc).Scan(&kind, &body, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("doc %d: %w", a.Doc, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read doc %d: %w", a.Doc, err)
	}
	// Seam for WL-1404: a spec is read rendered, and mapping a range of the
	// rendered body onto rule or template bytes is that change's.
	if kind != "plan" {
		return "", fmt.Errorf("range anchors on a spec are mapped by a later change; anchor on the rule: %w", ErrInvalidInput)
	}
	if a.Version != version {
		return "", fmt.Errorf("doc %d is at version %d; open threads on the current version: %w", a.Doc, version, ErrInvalidInput)
	}
	if err := checkRange(body, a); err != nil {
		return "", err
	}
	return sectionAt(body, a.ByteStart), nil
}

// checkRange holds WL-REQ-1317's three conditions: the range lies in body,
// both ends on UTF-8 character boundaries, and it covers exactly Quote.
func checkRange(body string, a model.ReviewAnchor) error {
	s, e := a.ByteStart, a.ByteEnd
	if s < 0 || s >= e || e > len(body) {
		return fmt.Errorf("byte range [%d,%d) is not inside a %d-byte text: %w", s, e, len(body), ErrInvalidInput)
	}
	if !utf8.RuneStart(body[s]) || (e < len(body) && !utf8.RuneStart(body[e])) {
		return fmt.Errorf("byte range [%d,%d) splits a UTF-8 character: %w", s, e, ErrInvalidInput)
	}
	got := body[s:e]
	if got == a.Quote {
		return nil
	}
	i := 0
	for i < len(got) && i < len(a.Quote) && got[i] == a.Quote[i] {
		i++
	}
	return fmt.Errorf("the quote differs from bytes [%d,%d) at byte %d: %w", s, e, s+i, ErrInvalidInput)
}

// sectionAt is the anchor of the section of body holding byte off, from the
// section map designdoc.Parse computes, or "" when it carries none.
func sectionAt(body string, off int) string {
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return ""
	}
	// An unedited Document's Bytes is its source, so the first section starts
	// where the sections' lengths, taken from the end, run out.
	pos := len(body)
	for _, sec := range doc.Sections {
		pos -= len(sec.HeadingAndBody())
	}
	anchor := ""
	for _, sec := range doc.Sections {
		if pos > off {
			break
		}
		anchor = sec.Anchor
		pos += len(sec.HeadingAndBody())
	}
	return anchor
}

// threadAuthor is the actor whose messages carry role author: the review's
// author, or for a bare note on a document the document's owner.
func threadAuthor(tx *sql.Tx, rev *model.Review, docID int64) (string, error) {
	switch {
	case rev != nil:
		return reviewAuthor(tx, rev)
	case docID != 0:
		return reviewAuthor(tx, &model.Review{Doc: docID})
	}
	// ponytail: a bare note on a rule has no author; the rule's owner if needed.
	return "", nil
}

// addThreadMessage appends one message. seq continues the review's next_seq,
// or counts within the thread for a bare note (WL-REQ-1322).
func addThreadMessage(tx *sql.Tx, ts time.Time, threadID int64, rev *model.Review, docID int64, body, actorID string) (model.ReviewMessage, error) {
	m := model.ReviewMessage{Thread: threadID, Author: actorID, Role: "reviewer", Body: body, CreatedAt: ts}
	author, err := threadAuthor(tx, rev, docID)
	if err != nil {
		return m, err
	}
	if author != "" && actorID == author {
		m.Role = "author"
	}
	var reviewID int64
	if rev != nil {
		reviewID = rev.ID
		err = tx.QueryRow(`UPDATE reviews SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1`, rev.ID).Scan(&m.Seq)
	} else {
		err = tx.QueryRow(`SELECT coalesce(max(seq), 0) + 1 FROM review_messages WHERE thread_id = $1`, threadID).Scan(&m.Seq)
	}
	if err != nil {
		return m, fmt.Errorf("next seq of thread %d: %w", threadID, err)
	}
	if err := tx.QueryRow(
		`INSERT INTO review_messages (thread_id, review_id, seq, actor_id, role, body, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		threadID, nullID(reviewID), m.Seq, actorID, m.Role, body, ts).Scan(&m.ID); err != nil {
		return m, fmt.Errorf("add message to thread %d: %w", threadID, err)
	}
	return m, nil
}

// lockedThread is the part of a thread row ReplyThread and ResolveThread
// decide on.
type lockedThread struct {
	review    *model.Review
	doc       int64
	intent    string
	status    string
	createdAt time.Time
}

func lockThread(tx *sql.Tx, id int64) (*lockedThread, error) {
	var t lockedThread
	var reviewID sql.NullInt64
	err := tx.QueryRow(
		`SELECT review_id, coalesce(doc_id, 0), intent, status, created_at FROM review_threads WHERE id = $1 FOR UPDATE`, id,
	).Scan(&reviewID, &t.doc, &t.intent, &t.status, &t.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("thread %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read thread %d: %w", id, err)
	}
	if t.status != "open" {
		return nil, fmt.Errorf("thread %d is %s: %w", id, t.status, ErrInvalidInput)
	}
	if reviewID.Valid {
		if t.review, err = lockReview(tx, reviewID.Int64); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

// ReplyThread adds a message to an open thread. firstAuthorReply is true for
// the author's first reply on a question, which emits
// wl:ReviewQuestionAnswered; askedAt is the question's created_at, for the
// question latency metric.
func ReplyThread(tx *sql.Tx, now time.Time, threadID int64, body, actorID string, eventID int64) (
	m model.ReviewMessage, firstAuthorReply bool, askedAt time.Time, err error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return m, false, askedAt, fmt.Errorf("a reply needs a body: %w", ErrInvalidInput)
	}
	t, err := lockThread(tx, threadID)
	if err != nil {
		return m, false, askedAt, err
	}
	var answered bool
	if t.intent == "question" {
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM review_messages WHERE thread_id = $1 AND role = 'author')`,
			threadID).Scan(&answered); err != nil {
			return m, false, askedAt, fmt.Errorf("read replies of thread %d: %w", threadID, err)
		}
	}
	ts := now.UTC().Truncate(time.Second)
	if m, err = addThreadMessage(tx, ts, threadID, t.review, t.doc, body, actorID); err != nil {
		return m, false, askedAt, err
	}
	if t.intent == "question" && !answered && m.Role == "author" {
		if err := reviewEvent(tx, ts, "wl:ReviewQuestionAnswered", threadID, actorID, eventID, nil); err != nil {
			return m, false, askedAt, err
		}
		return m, true, t.createdAt.UTC(), nil
	}
	return m, false, t.createdAt.UTC(), nil
}

// ResolveThread closes an open thread as resolved or obsolete and emits
// wl:ReviewThreadResolved with wl:threadStatus.
func ResolveThread(tx *sql.Tx, now time.Time, threadID int64, actorID, status string, eventID int64) (*model.ReviewThread, error) {
	if status != "resolved" && status != "obsolete" {
		return nil, fmt.Errorf("status %q is not resolved or obsolete: %w", status, ErrInvalidInput)
	}
	if _, err := lockThread(tx, threadID); err != nil {
		return nil, err
	}
	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(`UPDATE review_threads SET status = $2, resolved_by = $3, resolved_at = $4 WHERE id = $1`,
		threadID, status, actorID, ts); err != nil {
		return nil, fmt.Errorf("resolve thread %d: %w", threadID, err)
	}
	if err := reviewEvent(tx, ts, "wl:ReviewThreadResolved", threadID, actorID, eventID,
		map[string]any{"wl:threadStatus": status}); err != nil {
		return nil, err
	}
	threads, err := threadsWhere(context.Background(), tx, `t.id = $1`, threadID)
	if err != nil {
		return nil, err
	}
	return &threads[0], nil
}

// reviewEvent records one WL-REQ-1780 event about a thread as its own row,
// informed by the event of the write that caused it, the way
// classificationEvent does: the store cannot import eventbus.
func reviewEvent(tx *sql.Tx, at time.Time, typ string, threadID int64, actorID string, informedBy int64, props map[string]any) error {
	return recordReviewEvent(tx, at, typ, iri.CURIE(iri.ReviewThread(threadID)), actorID, informedBy, props)
}

// recordReviewEvent records one WL-REQ-1780 event about subject, a review or
// a thread CURIE. An empty actorID leaves prov:wasAssociatedWith out. The
// external id names the informing event, since a review sees many rounds.
func recordReviewEvent(tx *sql.Tx, at time.Time, typ, subject, actorID string, informedBy int64, props map[string]any) error {
	payload := map[string]any{
		"@context":    "https://worklode.io/ns/ontology#",
		"@type":       typ,
		"prov:atTime": at.UTC().Format(time.RFC3339),
		"wl:subject":  subject,
	}
	if actorID != "" {
		payload["prov:wasAssociatedWith"] = iri.CURIE(iri.Agent(actorID))
	}
	if informedBy != 0 {
		payload["prov:wasInformedBy"] = iri.CURIE(iri.Event(informedBy))
	}
	maps.Copy(payload, props)
	b, err := EventPayload(payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO events (source, external_id, type, payload, received_at) VALUES ('cli', $1, $2, $3, $4)`,
		typ+":"+subject+":"+strconv.FormatInt(informedBy, 10), typ, b, at.UTC()); err != nil {
		return fmt.Errorf("record %s for %s: %w", typ, subject, err)
	}
	return nil
}

// ThreadsForSubject lists the bare notes and review threads on one document
// or rule, obsolete included, oldest first, narrowed to a version, a section
// or a status when given (WL-REQ-1319, WL-REQ-1323). A document's list holds
// the threads on the rules it arranges.
func (s *Store) ThreadsForSubject(ctx context.Context, doc, rule int64, version int, section, status string) ([]model.ReviewThread, error) {
	if (doc == 0) == (rule == 0) {
		return nil, fmt.Errorf("name exactly one of a document and a rule: %w", ErrInvalidInput)
	}
	conds := []string{`t.rule_id = $1`}
	args := []any{rule}
	if doc != 0 {
		conds = []string{`(t.doc_id = $1 OR t.rule_id IN (SELECT rule_id FROM doc_entries WHERE doc_id = $1 AND rule_id IS NOT NULL))`}
		args = []any{doc}
	}
	add := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, cond+strconv.Itoa(len(args)))
	}
	if version != 0 {
		add(`t.version = $`, version)
	}
	if section != "" {
		add(`t.doc_anchor = $`, strings.TrimPrefix(section, "#"))
	}
	if status != "" {
		add(`t.status = $`, status)
	}
	return threadsWhere(ctx, s.db, strings.Join(conds, " AND "), args...)
}

// threadsWhere loads the threads matching where (over review_threads t) in
// creation order, each with its messages in seq order.
func threadsWhere(ctx context.Context, db rowsQueryer, where string, args ...any) ([]model.ReviewThread, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT t.id, coalesce(t.review_id, 0), coalesce(t.doc_id, 0), coalesce(t.rule_id, 0), coalesce(t.version, 0),
		        coalesce(t.byte_start, 0), coalesce(t.byte_end, 0), coalesce(t.quote, ''), coalesce(t.doc_anchor, ''),
		        coalesce(t.repo, ''), coalesce(t.path, ''), coalesce(t.side, ''), coalesce(t.line_number, 0),
		        coalesce(t.anchor_text, ''), coalesce(t.blob_sha, ''),
		        t.intent, coalesce(t.intent_classifier, ''), coalesce(t.intent_confidence, 0), t.status, t.author_id,
		        coalesce(t.raised_by_task, ''), coalesce(t.raised_by_session, ''), t.created_at,
		        coalesce(t.resolved_by, ''), t.resolved_at
		   FROM review_threads t WHERE `+where+` ORDER BY t.created_at, t.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("load threads: %w", err)
	}
	threads, err := collectRows(rows, "load threads", func(row rowScanner) (model.ReviewThread, error) {
		var t model.ReviewThread
		a := &t.Anchor
		err := row.Scan(&t.ID, &t.Review, &a.Doc, &a.Rule, &a.Version, &a.ByteStart, &a.ByteEnd, &a.Quote, &a.DocAnchor,
			&a.Repo, &a.Path, &a.Side, &a.Line, &a.AnchorText, &a.BlobSHA,
			&t.Intent, &t.IntentClassifier, &t.IntentConfidence, &t.Status, &t.Author,
			&t.RaisedByTask, &t.RaisedBySession, &t.CreatedAt, &t.ResolvedBy, &t.ResolvedAt)
		return t, err
	})
	if err != nil || len(threads) == 0 {
		return threads, err
	}
	ids := make([]int64, len(threads))
	for i, t := range threads {
		ids[i] = t.ID
	}
	rows, err = db.QueryContext(ctx,
		`SELECT id, thread_id, seq, actor_id, role, body, created_at
		   FROM review_messages WHERE thread_id = ANY($1) ORDER BY thread_id, seq, id`, ids)
	if err != nil {
		return nil, fmt.Errorf("load thread messages: %w", err)
	}
	byThread, err := groupRows(rows, "load thread messages", func(row rowScanner) (int64, model.ReviewMessage, error) {
		var m model.ReviewMessage
		err := row.Scan(&m.ID, &m.Thread, &m.Seq, &m.Author, &m.Role, &m.Body, &m.CreatedAt)
		return m.Thread, m, err
	})
	if err != nil {
		return nil, err
	}
	for i := range threads {
		threads[i].Messages = byThread[threads[i].ID]
	}
	return threads, nil
}
