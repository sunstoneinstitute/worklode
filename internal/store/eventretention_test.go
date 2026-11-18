package store

import (
	"context"
	"path"
	"regexp"
	"testing"
	"time"
)

func TestParseEventRetention(t *testing.T) {
	t.Parallel()
	got, err := ParseEventRetention(" github:push.ignored=1h , github:*=720h,flux:*=10m")
	if err != nil {
		t.Fatal(err)
	}
	want := []RetentionRule{{"github:push.ignored", time.Hour}, {"github:*", 720 * time.Hour}, {"flux:*", 10 * time.Minute}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule %d = %v, want %v", i, got[i], want[i])
		}
	}
	def, err := ParseEventRetention("")
	if err != nil || len(def) != 1 || def[0] != (RetentionRule{"github:*", 720 * time.Hour}) {
		t.Fatalf("default = %v, %v; want github:*=720h", def, err)
	}
	for _, bad := range []string{
		"github:*",              // no =
		"github:[=1h",           // bad glob
		"=1h",                   // empty glob
		"github:*=soon",         // bad duration
		"github:*=0s",           // not positive
		"github:*=-1h",          // not positive
		"github:*=1h,",          // empty entry
		"github:*=1h,flux:*\\=", // trailing escape
	} {
		if r, err := ParseEventRetention(bad); err == nil {
			t.Errorf("ParseEventRetention(%q) = %v, want an error", bad, r)
		}
	}
}

// TestGlobRegexAgreesWithPathMatch: the SQL selects by globRegex, the
// operator writes path.Match globs; both must accept the same strings.
func TestGlobRegexAgreesWithPathMatch(t *testing.T) {
	t.Parallel()
	db := openTestStore(t).DBForTests()
	globs := []string{"github:*", "github:*.ignored", "g?thub:push", "[fg]*:*", "[^g]*:*", "[a-c]x", `a\*b`, `[\]x]`, "a.b+c(d)", "*", "x/*"}
	inputs := []string{"github:push", "github:push.ignored", "flux:kustomization", "watcher:x", "bx", "dx", "a*b", "axb", "]", "x", "a.b+c(d)", "aXb+c(d)", "x/y", "x/y/z", "", "github:a/b"}
	for _, g := range globs {
		re := regexp.MustCompile(globRegex(g))
		for _, in := range inputs {
			want, err := path.Match(g, in)
			if err != nil {
				t.Fatalf("path.Match(%q): %v", g, err)
			}
			if got := re.MatchString(in); got != want {
				t.Errorf("glob %q (regex %q) on %q: regex %v, path.Match %v", g, globRegex(g), in, got, want)
			}
			var pg bool
			if err := db.QueryRow(`SELECT $1::text ~ $2`, in, globRegex(g)).Scan(&pg); err != nil {
				t.Fatalf("postgres regex %q: %v", globRegex(g), err)
			}
			if pg != want {
				t.Errorf("glob %q (regex %q) on %q: postgres %v, path.Match %v", g, globRegex(g), in, pg, want)
			}
		}
	}
}

func TestSweepEvents(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	db := s.DBForTests()
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)
	ev := func(source, typ string, at time.Time) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(`INSERT INTO events (source, external_id, type, received_at)
			VALUES ($1, gen_random_uuid()::text, $2, $3) RETURNING id`, source, typ, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	gone := ev("github", "push", old)
	logged := ev("github", "pull_request.closed", old)
	evidenced := ev("github", "release.published", old)
	informed := ev("github", "issues.opened", old)
	young := ev("github", "push", now.Add(-time.Hour))
	unmatched := ev("watcher", "pod.crashloop", old)
	ignored := ev("github", "push.ignored", now.Add(-2*time.Hour))         // first rule, 1h TTL: deleted
	shortTTL := ev("github", "check_run.completed", now.Add(-2*time.Hour)) // first rule matches, TTL not elapsed; must not fall through
	above := ev("github", "push", old)

	if _, err := db.Exec(`INSERT INTO state_log (entity_kind, entity_id, change, event_id, at) VALUES ('task', 'X-1', '{}', $1, now())`, logged); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO artifact_evidence (entity_kind, entity_id, artifact_uri, source, state, provenance, event_id, occurred_at)
		VALUES ('task', 'X-1', 'u', 'ci', 'published', 'observed', $1, now())`, evidenced); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, key) VALUES ('pa', 'PA', 'PA')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, body, priority, kind, state, created_at, updated_at, informed_by)
		VALUES ('PA-1', 'pa', 't', '', 'medium', 'design', 'ready', now(), now(), $1)`, informed); err != nil {
		t.Fatal(err)
	}

	rules := []RetentionRule{
		{"github:push.ignored", time.Hour},
		{"github:check_run.*", 24 * time.Hour},
		{"github:*", 90 * time.Minute},
	}

	// No subscribers: nothing is known to be consumed, so nothing goes.
	if n, err := s.SweepEvents(ctx, rules, now); err != nil || n["github:*"]+n["github:push.ignored"]+n["github:check_run.*"] != 0 {
		t.Fatalf("sweep without subscribers = %v, %v; want nothing deleted", n, err)
	}
	for _, sub := range []struct {
		name  string
		acked int64
	}{{"a", above - 1}, {"b", above + 5}} {
		if _, err := db.Exec(`INSERT INTO event_subscribers (name, last_read_offset, last_acked_offset, updated_at) VALUES ($1, $2, $2, now())`, sub.name, sub.acked); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.SweepEvents(ctx, rules, now)
	if err != nil {
		t.Fatal(err)
	}
	if n["github:push.ignored"] != 1 || n["github:check_run.*"] != 0 || n["github:*"] != 1 {
		t.Fatalf("deleted per pattern = %v, want push.ignored 1, check_run.* 0, github:* 1", n)
	}
	exists := func(id int64) bool {
		var ok bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE id = $1)`, id).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for name, id := range map[string]int64{"old unreferenced": gone, "ignored past its TTL": ignored} {
		if exists(id) {
			t.Errorf("%s event %d kept, want deleted", name, id)
		}
	}
	for name, id := range map[string]int64{
		"state_log referenced": logged, "artifact_evidence referenced": evidenced, "task informed_by": informed,
		"young": young, "unmatched": unmatched, "first match not expired": shortTTL, "above an acked offset": above,
	} {
		if !exists(id) {
			t.Errorf("%s event %d deleted, want kept", name, id)
		}
	}
}

func TestSweepEventsBatches(t *testing.T) {
	s := openTestStore(t)
	db := s.DBForTests()
	prev := sweepEventsBatch
	sweepEventsBatch = 2
	t.Cleanup(func() { sweepEventsBatch = prev })
	if _, err := db.Exec(`INSERT INTO events (source, external_id, type, received_at)
		SELECT 'github', 'e' || g, 'push', now() - interval '1 day' FROM generate_series(1, 5) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO event_subscribers (name, last_read_offset, last_acked_offset, updated_at)
		SELECT 'a', max(id), max(id), now() FROM events`); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepEvents(context.Background(), []RetentionRule{{"github:*", time.Hour}}, time.Now())
	if err != nil || n["github:*"] != 5 {
		t.Fatalf("sweep = %v, %v; want 5 deleted across batches of 2", n, err)
	}
}
