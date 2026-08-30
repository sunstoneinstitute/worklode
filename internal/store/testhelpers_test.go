package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenTestStoreClonesFullSchema verifies that a database created via
// OpenTestStore's template-clone path has the same schema a full migration
// run produces (see wantTables in store_test.go).
func TestOpenTestStoreClonesFullSchema(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	rows, err := s.db.Query(
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("query information_schema.tables: %v", err)
	}
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, table := range wantTables {
		if !got[table] {
			t.Errorf("table %q missing from template-cloned database", table)
		}
	}
}

// TestOpenTestStoreDatabasesAreIndependent verifies that two sequential
// OpenTestStore calls, though both cloned from the same template, produce
// independent databases: writes in one are invisible in the other.
func TestOpenTestStoreDatabasesAreIndependent(t *testing.T) {
	t.Parallel()
	s1 := OpenTestStore(t)
	s2 := OpenTestStore(t)

	if _, err := s1.DBForTests().Exec(
		`INSERT INTO actors (id, kind) VALUES ('only-in-s1', 'human')`); err != nil {
		t.Fatalf("insert into s1: %v", err)
	}

	var count int
	if err := s2.DBForTests().QueryRow(
		`SELECT count(*) FROM actors WHERE id = 'only-in-s1'`).Scan(&count); err != nil {
		t.Fatalf("query s2: %v", err)
	}
	if count != 0 {
		t.Errorf("row written to s1 is visible in s2: count = %d, want 0", count)
	}

	if err := s1.DBForTests().QueryRow(
		`SELECT count(*) FROM actors WHERE id = 'only-in-s1'`).Scan(&count); err != nil {
		t.Fatalf("query s1: %v", err)
	}
	if count != 1 {
		t.Errorf("row written to s1 is missing from s1: count = %d, want 1", count)
	}
}

// TestTemplateNameForMigrationsReflectsContent guards against the stale-
// template trap: the template name must change whenever the migrations
// directory changes, or an edited migration would silently reuse a
// template built from the old contents.
func TestTemplateNameForMigrationsReflectsContent(t *testing.T) {
	t.Parallel()
	realDir := MigrationsDirForTests()
	realName, err := templateNameForMigrations(realDir)
	if err != nil {
		t.Fatalf("templateNameForMigrations(real): %v", err)
	}

	changedDir := t.TempDir()
	copyMigrationsDir(t, realDir, changedDir)
	flipOneByte(t, filepath.Join(changedDir, firstMigrationFile(t, changedDir)))

	changedName, err := templateNameForMigrations(changedDir)
	if err != nil {
		t.Fatalf("templateNameForMigrations(changed): %v", err)
	}

	if realName == changedName {
		t.Fatalf("template name unchanged after editing a migration: both %q", realName)
	}

	// Sanity check: an unmodified copy hashes to the same name, so the
	// difference above is attributable to the byte flip and not e.g. path
	// handling.
	unchangedDir := t.TempDir()
	copyMigrationsDir(t, realDir, unchangedDir)
	unchangedName, err := templateNameForMigrations(unchangedDir)
	if err != nil {
		t.Fatalf("templateNameForMigrations(unchanged copy): %v", err)
	}
	if unchangedName != realName {
		t.Fatalf("unmodified copy hashed differently: got %q, want %q", unchangedName, realName)
	}
}

// TestTemplateBuiltOnce verifies that repeated OpenTestStore calls within a
// single process ask Postgres for the template at most once; every later
// call must reuse the process-local cache instead of re-checking.
func TestTemplateBuiltOnce(t *testing.T) {
	t.Parallel()
	for i := 0; i < 5; i++ {
		OpenTestStore(t)
	}
	if templateEnsures != 1 {
		t.Errorf("templateEnsures = %d, want 1 (template must be found/built at most once per process)", templateEnsures)
	}
}

func copyMigrationsDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read dir %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
}

func firstMigrationFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			return e.Name()
		}
	}
	t.Fatalf("no migration files in %s", dir)
	return ""
}

func flipOneByte(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("empty migration file %s", path)
	}
	data[0] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
