package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Sentinel errors returned by store operations.
var (
	// ErrNotFound means the requested entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrBadTransition means the requested task state transition is not allowed.
	ErrBadTransition = errors.New("invalid state transition")
	// ErrLeased means the task already has an active lease.
	ErrLeased = errors.New("task is leased")
	// ErrBlocked means the task has open blocking edges.
	ErrBlocked = errors.New("task is blocked")
	// ErrRepoTaken means the repo is already mapped to another project.
	ErrRepoTaken = errors.New("repo already mapped to a project")
	// ErrCycle means the edge would make the child_of hierarchy cyclic.
	ErrCycle = errors.New("edge would create a cycle")
	// ErrEdgeExists means the exact edge (from, to, type) already exists.
	ErrEdgeExists = errors.New("edge already exists")
	// ErrInvalidInput means a field value failed validation.
	ErrInvalidInput = errors.New("invalid input")
)

// isUniqueViolation reports whether err is a Postgres unique-index violation
// (SQLSTATE 23505), the backstop for claim races and duplicate edges.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
