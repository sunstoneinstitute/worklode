# One Postgres poll per open event stream

Notes on a performance problem found on 2026-09-09, plus the direction Stig
suggested for it. Nothing here is designed or decided; this is the writeup for
whoever designs against it later.

## The two streams

The server serves the event log live over server-sent events (SSE) on two
routes, both reading the same `events` table:

- `GET /api/v1/events/stream`, the admin log follow, handled by `streamEvents`
  (`internal/api/eventstream.go:76`).
- `GET /projects/{id}/progress/events`, the Progress page's live follow
  (WL-SPEC-66 §5.1), handled by `progressEvents`
  (`internal/api/progress.go:610`).

Both are pollers rather than Postgres `LISTEN`/`NOTIFY` listeners, and that is
deliberate: the comment at `internal/api/eventstream.go:18` records the reason.
A read through `store.ListEvents` is horizon-bounded, so a poll cannot show an
event id that a later read would order before it. A `NOTIFY` push, delivered
when the notify fires rather than when the horizon advances, could.

## What each open connection does

Each handler is a `for` loop that lives as long as one open HTTP connection.

- Every poll interval (default one second, `defaultStreamPollInterval`,
  `internal/api/eventstream.go:41`) the loop calls `s.st.ListEvents`
  (`internal/api/eventstream.go:152`, `internal/api/progress.go:672`).
- `ListEvents` (`internal/store/events.go:526`) runs one
  `SELECT ... FROM events WHERE ... ORDER BY id LIMIT ...` against the store's
  shared `*sql.DB` pool (`internal/store/events.go:546`).
- That pool is process-wide and capped at 16 connections
  (`db.SetMaxOpenConns(16)`, `internal/store/store.go:37`). Every request the
  server handles shares it, not just the streams.
- The Progress stream does a second store call, `progressFrames`
  (`internal/api/progress.go:758`), on any poll that returned events.

So N open streams on one pod means N independent per-second queries, each
re-reading mostly the same rows, each briefly checking out one of the 16 shared
connections. Nothing is shared between them: a stream is a goroutine with its
own cursor and its own poll.

SSE connections are long-lived by design, and a browser's `EventSource`
reconnects automatically when one drops, so "open for many minutes" is the
normal case, not the outlier. Every browser tab sitting on a Progress page is
one more poller.

## Why it became a suspect

A production log line showed a `progress/events` request held open about 60
seconds while two neighbouring, ordinarily fast task-lookup requests took 1.5
and 2 seconds. That raises the question of whether concurrently open streams
are contending for the connection pool and slowing down unrelated requests. It
is a suspect, not a confirmed cause.

The server runs one pod today (`replicas: 1`,
`deploy/base/deployment.yaml:10`). That is not guaranteed to stay true.

## What is now observable

Pool counters were added so this can be watched:
`worklode_db_open_connections`, `worklode_db_in_use_connections`,
`worklode_db_idle_connections`, `worklode_db_wait_count_total` and
`worklode_db_wait_duration_seconds_total`
(`internal/api/metrics.go:533`, fed from `database/sql` pool stats in
`internal/store/store.go`).

## Stig's suggestion

Recorded as his proposal. It has not been designed, sized or agreed.

There should be exactly one Postgres connection doing the actual polling per
(Postgres user, event table) pair. That is one shared poller per pod, not one
poller per open SSE connection. The single poller feeds an in-process
single-producer/multiple-consumer queue or channel, and every SSE stream open
on that pod is a consumer reading from that in-process fan-out instead of
querying Postgres itself.

If the server ever runs more than one pod, each pod opens its own single
producer connection. The suggestion is "one producer per pod", not "one
producer for the whole cluster".

He was explicit that the suggestion stops there.
