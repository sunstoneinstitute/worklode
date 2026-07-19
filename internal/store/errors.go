package store

import "errors"

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
)
