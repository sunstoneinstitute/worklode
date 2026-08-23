// Package migrateapp applies Worklode database migrations.
package migrateapp

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func Run(_ context.Context, dsn, migrationsPath string, stdout io.Writer) error {
	if migrationsPath == "" {
		return errors.New("--migrations-path is required")
	}
	if dsn == "" {
		return errors.New("no DSN: set --dsn or LODE_DSN")
	}
	s, err := store.Open(dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(migrationsPath); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "migrations applied")
	return err
}
