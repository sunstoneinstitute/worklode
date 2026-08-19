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
	// ErrBlocked means something holds the task: an open blocking edge, or a
	// plan ordered before its plan (see IsBlocked).
	ErrBlocked = errors.New("task is blocked")
	// ErrRepoTaken means the repo is already mapped to another project.
	ErrRepoTaken = errors.New("repo already mapped to a project")
	// ErrKeyTaken means the project key is already used by another project.
	ErrKeyTaken = errors.New("project key already in use")
	// ErrCycle means the edge would make the child_of hierarchy cyclic.
	ErrCycle = errors.New("edge would create a cycle")
	// ErrEdgeExists means the exact edge (from, to, type) already exists.
	ErrEdgeExists = errors.New("edge already exists")
	// ErrInvalidInput means a field value failed validation.
	ErrInvalidInput = errors.New("invalid input")
	// ErrDocExists means the project already holds a document with that slug
	// or that (kind, number).
	ErrDocExists = errors.New("document already exists")
	// ErrForbidden means the actor may not perform this operation on this
	// entity — a document accept is the assignee's act (025 §7).
	ErrForbidden = errors.New("forbidden")
	// ErrRevisionExists means the document already has an open candidate
	// revision; 025 §7.2 allows one at a time.
	ErrRevisionExists = errors.New("revision already open")
)

// isUniqueViolation reports whether err is a Postgres unique-index violation
// (SQLSTATE 23505), the backstop for claim races and duplicate edges.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isUniqueViolationOn reports whether err is a unique violation on the named
// constraint/index, for callers that need to know which backstop fired.
func isUniqueViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// isCheckViolationOn reports whether err is a Postgres CHECK-constraint
// violation (SQLSTATE 23514) on the named constraint.
func isCheckViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.ConstraintName == constraint
}
