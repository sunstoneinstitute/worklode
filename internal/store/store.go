// Package store provides the Postgres-backed data store for the work tracker.
// It holds a pgx-backed database/sql connection pool; concurrency control is
// delegated to Postgres (transactions plus unique-constraint backstops).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Store wraps the Postgres connection pool.
type Store struct {
	db    *sql.DB
	nowFn func() time.Time
}

// Open opens a Postgres-backed store for the given postgres:// DSN. Callers
// are responsible for applying migrations (see Migrate) before relying on
// the schema being present — Open does not do this implicitly.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Store{db: db, nowFn: func() time.Time { return time.Now().UTC() }}, nil
}

// SetNowFunc overrides the clock used for timestamps written by the store
// (e.g. events.received_at, lease expiry). Intended for tests that need to
// control time deterministically.
func (s *Store) SetNowFunc(f func() time.Time) {
	s.nowFn = f
}

// Now returns the store's current time. It respects SetNowFunc, so callers
// that need a timestamp for tx-scoped store functions (e.g. CreateTask) stay
// consistent with the store's own clock in tests.
func (s *Store) Now() time.Time {
	return s.nowFn()
}

// Migrate applies all pending migrations found as *.up.sql/*.down.sql files
// in migrationsPath. A database that is already up to date is not an error.
func (s *Store) Migrate(migrationsPath string) error {
	m, err := s.newMigrate(migrationsPath)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrateDown reverts all applied migrations found as *.up.sql/*.down.sql
// files in migrationsPath. A database with nothing to revert is not an error.
func (s *Store) MigrateDown(migrationsPath string) error {
	m, err := s.newMigrate(migrationsPath)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}

func (s *Store) newMigrate(migrationsPath string) (*migrate.Migrate, error) {
	absPath, err := filepath.Abs(migrationsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve migrations path %s: %w", migrationsPath, err)
	}
	src, err := source.Open("file://" + absPath)
	if err != nil {
		return nil, fmt.Errorf("load migrations from %s: %w", absPath, err)
	}
	drv, err := migratepgx.WithInstance(s.db, &migratepgx.Config{})
	if err != nil {
		return nil, fmt.Errorf("init migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("file", src, "pgx5", drv)
	if err != nil {
		return nil, fmt.Errorf("init migrate: %w", err)
	}
	return m, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Tx runs fn inside a transaction, committing on nil and rolling back on error.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
