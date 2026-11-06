package store

import (
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func migrateSteps(t *testing.T, s *Store, steps int) {
	t.Helper()
	m, err := s.newMigrate(MigrationsDirForTests())
	if err != nil {
		t.Fatalf("open migration: %v", err)
	}
	stepErr := m.Steps(steps)
	sourceErr, dbErr := m.Close()
	if stepErr != nil {
		t.Fatalf("migrate %d step(s): %v", steps, stepErr)
	}
	if sourceErr != nil || dbErr != nil {
		t.Fatalf("close migration driver: source=%v db=%v", sourceErr, dbErr)
	}
}

// migrateTo moves the schema to version, up or down, so a migration's test
// keeps working once later migrations exist. version 0 means the newest.
func migrateTo(t *testing.T, s *Store, version uint) {
	t.Helper()
	m, err := s.newMigrate(MigrationsDirForTests())
	if err != nil {
		t.Fatalf("open migration: %v", err)
	}
	var moveErr error
	if version == 0 {
		moveErr = m.Up()
	} else {
		moveErr = m.Migrate(version)
	}
	sourceErr, dbErr := m.Close()
	if moveErr != nil && !errors.Is(moveErr, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", version, moveErr)
	}
	if sourceErr != nil || dbErr != nil {
		t.Fatalf("close migration driver: source=%v db=%v", sourceErr, dbErr)
	}
}
