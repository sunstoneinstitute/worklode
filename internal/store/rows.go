package store

import (
	"database/sql"
	"fmt"
	"strconv"
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

// groupRows is collectRows for a bulk reader: it drains rows into a map of
// key to slice, preserving row order within each key. scan hands back the key
// the row belongs to alongside the value, so a query that batches N singular
// reads into one can regroup the result by whatever the singular reader was
// keyed on.
//
// Keys with no rows are absent from the map — callers get a nil slice for
// them, which is what the singular reader returns for an empty set.
func groupRows[K comparable, T any](rows *sql.Rows, what string, scan func(rowScanner) (K, T, error)) (map[K][]T, error) {
	defer rows.Close()
	out := map[K][]T{}
	for rows.Next() {
		k, v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		out[k] = append(out[k], v)
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
// the same name. It splits naively on ",", so every entry in cols must stay
// comma-free.
func qualifyColumns(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
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

// nullText maps "" to NULL, for the columns where absent and empty are the
// same thing.
func nullText(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullID maps 0 to NULL, for the nullable id references (doc_edges.to_doc,
// tasks.plan_doc).
func nullID(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id != 0}
}

// nullableID is nullID's read counterpart: nil when the column was NULL.
func nullableID(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

// sqlArgs accumulates the positional arguments of a dynamically built query.
// next appends a value and hands back its $n placeholder, so a filter clause
// names the value it binds instead of counting how many came before it.
type sqlArgs struct{ vals []any }

func (a *sqlArgs) next(v any) string {
	a.vals = append(a.vals, v)
	return "$" + strconv.Itoa(len(a.vals))
}
