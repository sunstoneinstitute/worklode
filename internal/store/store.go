// Package store provides the SQLite-backed data store for the work tracker.
// The server is the single writer; SetMaxOpenConns(1) serializes access at
// the connection-pool level.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "modernc.org/sqlite"
)

// Store wraps the single-writer SQLite database.
type Store struct {
	db    *sql.DB
	nowFn func() time.Time
}

// Open opens (creating if necessary) the SQLite database at path. Callers
// are responsible for applying migrations (see Migrate) before relying on
// the schema being present — Open no longer does this implicitly.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

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
	absPath, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("resolve migrations path %s: %w", migrationsPath, err)
	}
	src, err := source.Open("file://" + absPath)
	if err != nil {
		return fmt.Errorf("load migrations from %s: %w", absPath, err)
	}
	drv, err := migratesqlite.WithInstance(s.db, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("init migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("file", src, "sqlite", drv)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
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
