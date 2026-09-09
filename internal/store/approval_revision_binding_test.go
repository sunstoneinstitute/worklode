package store

import "testing"

func TestApprovalRevisionBindingMigrationRoundTrip(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 73)); err != nil {
		t.Fatalf("migrate through 0073: %v", err)
	}

	migrateSteps(t, s, 1)
	assertApprovalRevisionBindingSchema(t, s, true)

	migrateSteps(t, s, -1)
	assertApprovalRevisionBindingSchema(t, s, false)

	migrateSteps(t, s, 1)
	assertApprovalRevisionBindingSchema(t, s, true)
}

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

func assertApprovalRevisionBindingSchema(t *testing.T, s *Store, applied bool) {
	t.Helper()
	var constraint string
	var definition string
	err := s.DBForTests().QueryRow(
		`SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint
		 WHERE conrelid = 'approvals'::regclass AND contype = 'u'`).Scan(&constraint, &definition)
	if err != nil {
		t.Fatalf("query approvals unique constraint: %v", err)
	}
	wantConstraint := "approvals_entity_revision_lane_key"
	if applied {
		wantConstraint = "approvals_entity_revision_lane_review_kind_key"
	}
	if constraint != wantConstraint {
		t.Fatalf("approvals unique constraint = %q, want %q", constraint, wantConstraint)
	}
	wantDefinition := "UNIQUE (entity_kind, entity_id, subject_revision, lane)"
	if applied {
		wantDefinition = "UNIQUE (entity_kind, entity_id, subject_revision, lane, review_kind)"
	}
	if definition != wantDefinition {
		t.Fatalf("approvals unique constraint definition = %q, want %q", definition, wantDefinition)
	}

	for _, column := range []string{"review_kind", "note", "exception_authorized_by"} {
		var exists bool
		if err := s.DBForTests().QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'approvals' AND column_name = $1)`, column).Scan(&exists); err != nil {
			t.Fatalf("check approvals.%s: %v", column, err)
		}
		if exists != applied {
			t.Errorf("approvals.%s exists = %v, want %v", column, exists, applied)
		}
	}
}
