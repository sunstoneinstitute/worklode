package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/review"
)

// relocateDocThreads carries the open range threads on document docID at
// version from to version to, where the document's stored text, read here,
// replaced old (WL-REQ-1317). Every docs.body writer calls it once the new
// text has settled.
func relocateDocThreads(tx *sql.Tx, docID int64, from, to int, old string, eventID int64) error {
	var body string
	if err := tx.QueryRow(`SELECT body FROM docs WHERE id = $1`, docID).Scan(&body); err != nil {
		return fmt.Errorf("read doc %d body: %w", docID, err)
	}
	return relocateThreads(tx, "doc_id", docID, from, to, old, body, eventID, func(start int) string {
		return sectionAt(body, start)
	})
}

// relocateRuleThreads carries the open range threads on rule ruleID at
// version from to version to, where body replaced old (WL-REQ-1317). A rule
// thread's doc_anchor is where the rule is arranged, which a text edit does
// not move.
func relocateRuleThreads(tx *sql.Tx, ruleID int64, from, to int, old, body string, eventID int64) error {
	return relocateThreads(tx, "rule_id", ruleID, from, to, old, body, eventID, nil)
}

// relocateThreads moves each open range thread on (col = id, version from)
// to where review.Relocate finds its quote in body, at version to, with the
// quote rewritten to the bytes it now covers; a quote not found makes the
// thread obsolete, with no resolved_by: no actor resolved it, and the event
// names the worklode system agent. anchor, when set, re-derives doc_anchor
// from the new start.
func relocateThreads(tx *sql.Tx, col string, id int64, from, to int, old, body string, eventID int64,
	anchor func(int) string) error {
	rows, err := tx.Query(
		`SELECT id, byte_start, byte_end, quote, coalesce(doc_anchor, '') FROM review_threads
		  WHERE `+col+` = $1 AND version = $2 AND status = 'open' AND byte_start IS NOT NULL
		  ORDER BY id FOR UPDATE`, id, from)
	if err != nil {
		return fmt.Errorf("read open threads on %s %d v%d: %w", col, id, from, err)
	}
	type thread struct {
		id               int64
		start, end       int
		quote, docAnchor string
	}
	threads, err := collectRows(rows, "read open threads", func(row rowScanner) (thread, error) {
		var t thread
		err := row.Scan(&t.id, &t.start, &t.end, &t.quote, &t.docAnchor)
		return t, err
	})
	if err != nil {
		return err
	}
	ts := time.Now().UTC().Truncate(time.Second)
	for _, t := range threads {
		s, e, _, ok := review.Relocate(old, body, t.start, t.end, t.quote)
		if !ok {
			if _, err := tx.Exec(`UPDATE review_threads SET status = 'obsolete', resolved_at = $2 WHERE id = $1`,
				t.id, ts); err != nil {
				return fmt.Errorf("mark thread %d obsolete: %w", t.id, err)
			}
			if err := reviewEvent(tx, ts, "wl:ReviewThreadResolved", t.id, FlowActorID, eventID,
				map[string]any{"wl:threadStatus": "obsolete"}); err != nil {
				return err
			}
			continue
		}
		if anchor != nil {
			t.docAnchor = anchor(s)
		}
		if _, err := tx.Exec(
			`UPDATE review_threads SET version = $2, byte_start = $3, byte_end = $4, quote = $5, doc_anchor = $6 WHERE id = $1`,
			t.id, to, s, e, body[s:e], nullText(t.docAnchor)); err != nil {
			return fmt.Errorf("move thread %d: %w", t.id, err)
		}
	}
	return nil
}
