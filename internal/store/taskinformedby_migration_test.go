package store

import (
	"fmt"
	"testing"
)

// TestTaskInformedByMigration: the backfill reads a minted task's
// prov:wasInformedBy from its task.created event payload, falls back to the
// line a Progress-page mint wrote in its body, and leaves every other task
// null (WL-REQ-109).
func TestTaskInformedByMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	version := migrationVersion(t, "_task-informed-by.up.sql")
	if err := s.Migrate(migrationsThrough(t, version-1)); err != nil {
		t.Fatalf("migrate through %d: %v", version-1, err)
	}
	db := s.DBForTests()
	event := func(typ, payload string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(`INSERT INTO events (source, external_id, type, payload, received_at)
			VALUES ('watcher', gen_random_uuid()::text, $1, $2::jsonb, now()) RETURNING id`, typ, payload).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	task := func(id, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, body, priority, kind, state, created_at, updated_at)
			VALUES ($1, 'pa', 't', $2, 'medium', 'design', 'ready', now(), now())`, id, body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, key) VALUES ('pa', 'PA', 'PA')`); err != nil {
		t.Fatal(err)
	}
	accepted := event("wl:DocumentAccepted", `{}`)
	task("PA-1", "Decide.\n\nprov:wasInformedBy wlid:event/1\n\nClaim it.")
	event("task.created", fmt.Sprintf(`{"task":"PA-1","prov:wasInformedBy":"wlid:event/%d"}`, accepted))
	own := event("task.created", `{"task":"PA-2","route":"progress/plan"}`)
	task("PA-2", fmt.Sprintf("Decide.\n\nprov:wasInformedBy wlid:event/%d\n\nClaim it.", own))
	task("PA-3", "prov:wasInformedBy wlid:event/999999")
	task("PA-4", "hand-written")

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	want := map[string]string{"PA-1": fmt.Sprint(accepted), "PA-2": fmt.Sprint(own), "PA-3": "<nil>", "PA-4": "<nil>"}
	for id, w := range want {
		var got *int64
		if err := db.QueryRow(`SELECT informed_by FROM tasks WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		g := "<nil>"
		if got != nil {
			g = fmt.Sprint(*got)
		}
		if g != w {
			t.Errorf("%s informed_by = %s, want %s", id, g, w)
		}
	}
}
