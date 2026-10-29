package store

import (
	"fmt"
	"strings"
	"testing"
)

// TestDocNotesFoldMigration seeds doc_notes, one with no author and a
// non-numeric session id, and runs the fold up, down and up again: each note
// becomes a bare thread with its text as message 1 and its note/<id> inline
// refs following the new id (WL-REQ-1319, WL-REQ-1667), and down puts them
// back.
func TestDocNotesFoldMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	version := migrationVersion(t, "_doc-notes-fold.up.sql")
	if err := s.Migrate(migrationsThrough(t, version-1)); err != nil {
		t.Fatalf("migrate through %d: %v", version-1, err)
	}
	db := s.DBForTests()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	exec(`INSERT INTO projects (id, name, key) VALUES ('pa', 'PA', 'PA')`)
	exec(`INSERT INTO actors (id, kind) VALUES ('stig', 'human')`)
	var docID, anon, signed int64
	if err := db.QueryRow(`INSERT INTO docs (project_id, kind, number, slug, title, body, created_at, updated_at)
		VALUES ('pa', 'spec', 1, 'one', 'One', '# One', now(), now()) RETURNING id`).Scan(&docID); err != nil {
		t.Fatal(err)
	}
	// Burn ids so a note's id and its thread's id differ.
	exec(`SELECT nextval(pg_get_serial_sequence('doc_notes', 'id')) FROM generate_series(1, 5)`)
	if err := db.QueryRow(`INSERT INTO doc_notes (doc_id, anchor, body, session_id, created_at)
		VALUES ($1, 'sec-1', 'anonymous', 'sess-x', now()) RETURNING id`, docID).Scan(&anon); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO doc_notes (doc_id, anchor, body, session_id, created_by, created_at)
		VALUES ($1, 'sec-2', 'signed', '42', 'stig', now()) RETURNING id`, docID).Scan(&signed); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO inline_refs (from_kind, from_id, field, to_kind, to_id, lines, source)
		VALUES ('doc', $1, $2, 'doc', $1, '{1}', 'derived')`, fmt.Sprint(docID), fmt.Sprintf("note/%d", signed))

	checkFolded := func(when string) {
		t.Helper()
		rows, err := db.Query(`SELECT t.id, t.doc_anchor, t.author_id, t.raised_by_session IS NULL, t.intent, m.body, m.seq, m.role
			FROM review_threads t JOIN review_messages m ON m.thread_id = t.id
			WHERE t.review_id IS NULL AND t.version IS NULL ORDER BY t.id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		var signedThread int64
		for rows.Next() {
			var id int64
			var anchor, author, intent, body, role string
			var noSession bool
			var seq int
			if err := rows.Scan(&id, &anchor, &author, &noSession, &intent, &body, &seq, &role); err != nil {
				t.Fatal(err)
			}
			if body == "signed" {
				signedThread = id
			}
			got = append(got, fmt.Sprintf("%s/%s/%v/%s/%s/%d/%s", anchor, author, noSession, intent, body, seq, role))
		}
		want := "sec-1/worklode/true/note/anonymous/1/reviewer sec-2/stig/true/note/signed/1/reviewer"
		if strings.Join(got, " ") != want {
			t.Errorf("%s: threads %v, want %s", when, got, want)
		}
		if n := count(`SELECT count(*) FROM inline_refs WHERE field = $1`, fmt.Sprintf("note/%d", signedThread)); n != 1 {
			t.Errorf("%s: inline refs on note/%d = %d, want 1", when, signedThread, n)
		}
		if n := count(`SELECT count(*) FROM pg_tables WHERE tablename = 'doc_notes'`); n != 0 {
			t.Errorf("%s: doc_notes still exists", when)
		}
	}

	migrateSteps(t, s, 1)
	checkFolded("up")

	migrateSteps(t, s, -1)
	if n := count(`SELECT count(*) FROM review_threads`); n != 0 {
		t.Errorf("down: %d threads left, want 0", n)
	}
	if n := count(`SELECT count(*) FROM doc_notes n JOIN inline_refs r ON r.field = 'note/' || n.id WHERE n.body = 'signed'`); n != 1 {
		t.Errorf("down: the signed note lost its inline ref (%d)", n)
	}
	if n := count(`SELECT count(*) FROM doc_notes`); n != 2 {
		t.Errorf("down: %d doc_notes, want 2", n)
	}
	exec(`INSERT INTO doc_notes (doc_id, anchor, body, created_at) VALUES ($1, 'sec-3', 'after down', now())`, docID)
	exec(`DELETE FROM doc_notes WHERE body = 'after down'`)

	migrateSteps(t, s, 1)
	checkFolded("up again")
}
