Applies only to Go files under `internal/store/`.

# A store write that can collide returns a sentinel, never a raw error

`internal/api`'s `mapStoreErr` (`internal/api/server.go`) switches on the
sentinels in `internal/store/errors.go`, and its `default` branch is a logged
500. A write that returns the raw pgx error turns a caller mistake into a
server fault.

When adding or changing a write:

1. Name every constraint a caller can trip: a primary key or unique index
   whose value they choose, a CHECK on a field they supply, an FK to a row
   they name.
2. Map each with `isUniqueViolationOn` / `isCheckViolationOn` to a sentinel —
   an existing one if it already says the right thing (`ErrDocExists`,
   `ErrEdgeExists`, `ErrKeyTaken`), else a new one in `errors.go` with a
   comment naming the constraint it stands for.
3. Add the sentinel to `mapStoreErr`'s 409 case (already exists) or its 422
   case (invalid value), and test the status in `internal/api`.

A constraint only a bug can trip stays a 500 — that is what the default
branch is for. `ErrUnknownBlob`'s comment in `errors.go` is the worked
example: the insert direction of one FK is user error, the delete direction
of the same FK is a GC bug, and only the first is mapped.
