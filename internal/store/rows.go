package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// collectRows drains rows into a slice, building one T per row through scan,
// and closes rows before returning. Scan failures and iteration failures are
// both wrapped under what, so a list query reports every way it can fail with
// the same prefix and no caller has to repeat the Close/Err/append triple.
//
// The result is nil for an empty set, matching what the hand-written loops
// returned; a caller whose payload must marshal as [] rather than null still
// has to say so.
func collectRows[T any](rows *sql.Rows, what string, scan func(rowScanner) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}

// byValue adapts a scanX function that hands back *T into the form
// collectRows wants, for the entities whose scanner returns a pointer.
func byValue[T any](scan func(rowScanner) (*T, error)) func(rowScanner) (T, error) {
	return func(r rowScanner) (T, error) {
		v, err := scan(r)
		if err != nil {
			var zero T
			return zero, err
		}
		return *v, nil
	}
}

// qualifyColumns renders a SELECT list with every column prefixed by alias,
// for queries that join the table against another one carrying a column of
// the same name. It splits naively on ", ", so every entry in cols must stay
// comma-free.
func qualifyColumns(cols, alias string) string {
	parts := strings.Split(cols, ", ")
	for i, c := range parts {
		parts[i] = alias + "." + c
	}
	return strings.Join(parts, ", ")
}

// nonNil substitutes an empty slice for a nil one, for the readers whose
// payload must marshal as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// scanColumn is collectRows for a query that selects a single value per row.
func scanColumn[T any](rows *sql.Rows, what string) ([]T, error) {
	return collectRows(rows, what, func(r rowScanner) (T, error) {
		var v T
		err := r.Scan(&v)
		return v, err
	})
}
