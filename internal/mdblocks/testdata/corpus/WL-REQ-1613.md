
Typed errors: `ErrNotFound`, `ErrInvalidInput`, `ErrLeased`, `ErrBlocked`, `ErrBadTransition`, `ErrCycle`, `ErrEdgeExists`. Unique violations are detected through `pgconn.PgError.Code == "23505"`.

