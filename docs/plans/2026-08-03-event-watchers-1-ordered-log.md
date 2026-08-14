---
status: draft
covers:
  - docs/specs/025-documents-in-the-backbone.md#sec-15
  - docs/specs/025-documents-in-the-backbone.md#sec-15.1
  - docs/specs/025-documents-in-the-backbone.md#sec-15.2
  - docs/specs/025-documents-in-the-backbone.md#sec-15.3
  - docs/specs/025-documents-in-the-backbone.md#sec-18
  - docs/specs/025-documents-in-the-backbone.md#sec-15.7
---
# Event watchers 1/2: the ordered log

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 2 (spec 025 §19 mandates the split). Task numbers
restart at 1 per part.

- **Part 1 — the ordered log (this file, 9 tasks):** `events.txid` and the
  commit horizon, `event_subscribers` offsets, the advisory-lock consumer
  loop, the event vocabulary and typed emission, `lode event
  tail|subscribers|seek`, the three `internal/eventbus` metrics.
  Ships against the existing log; depends on nothing unmerged.
- **Part 2 — the doc-lifecycle subscriber
  (`2026-08-03-event-watchers-2-doc-lifecycle.md`):** the two §5 rules, §7's
  planning-cost skill change, the watcher metric. `requires` this part plus
  025's document store — until `docs` rows exist there is no
  `wl:DocumentAccepted` to emit.

**Goal:** Turn the append-only `events` table into a totally ordered,
offset-tracked log with at-least-once subscribers (025 §15–§4), plus the
`lode event` verbs (§6) and eventbus metrics (§8) — with the §9 ordering-trap
test proving a naive `last_seen_id` cursor would skip late-committing events.

**Architecture:** One migration adds `events.txid xid8` (default
`pg_current_xact_id()`) and the `event_subscribers` offsets table. Readers
take only rows below the commit horizon
(`txid < pg_snapshot_xmin(pg_current_snapshot())`), so the visible log grows
only at its tail and an offset is meaningful. `internal/store/events.go`
gains the horizon read, monotonic ack, and a per-subscriber
`pg_try_advisory_lock` held on a dedicated pool connection;
`internal/eventbus` owns the polling loop, the typed emit helpers with
deterministic `external_id`, the hand-mirrored event vocabulary, and the
metrics. The API gains three read/admin endpoints and the CLI three verbs.
No subscriber is wired into the server in this part — part 2 wires the first
one — so the loop is proven by store-backed package tests.

**Tech Stack:** Go 1.25+, Postgres via pgx stdlib (`database/sql`),
golang-migrate, cobra, prometheus/client_golang.

**Read first:**
- `docs/specs/025-documents-in-the-backbone.md` §1–§4, §6, §8–§10 — the whole design
- `internal/store/events.go` — `Event`, `RecordEvent` and its `apply` seam
- `internal/store/store.go:140-152` — `Tx`; note the pool is `database/sql`
  over pgx (16 conns max), which is why the subscriber lock must pin a
  `*sql.Conn`
- `docs/specs/022-prometheus-metrics.md` §1, §3, §7 — nil-safe metrics
  structs, the `worklode_leases_active` custom-collector precedent
- `internal/hooks/metrics.go` — the metrics-struct shape to copy
- `internal/api/server.go:362-413` — route registration, `s.auth`,
  `requireAdmin`
- `internal/cmd/timeline.go` — the client-command shape to copy

**Conventions:**
- `go test ./internal/... -count=1`. Store and API tests need Postgres with
  pgvector (CI uses `pgvector/pgvector:pg17`); default DSN
  `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`,
  override with `TEST_POSTGRES_DSN`. Tests skip silently without Postgres —
  run against a real one.
- Migration number `0013` is provisional: `0010`–`0012` are claimed by the
  keycloak-primary-auth and documents-in-the-backbone plans. Run
  `./scripts/check-migrations.sh --no-fix` and use whatever number it
  settles on; list both files in `deploy/base/kustomization.yaml`.
- Commit after every task, imperative mood, no trailers of any kind.
- `./scripts/secfmt.py -l` must stay clean after the docs task.

---

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0013_event_log.{up,down}.sql` (new) | `events.txid`, `events_txid_id` index, `event_subscribers` |
| `deploy/base/kustomization.yaml` | list the new pair |
| `internal/store/events.go` (+`events_test.go`) | horizon read, offsets, monotonic ack, subscriber lock, tail/status/seek queries, lag query, `RecordEventWithID` |
| `internal/eventbus/vocab.go` (+`vocab_test.go`) (new) | event-type constants + per-type payload property sets, hand-mirrored from `ns/` |
| `internal/eventbus/emit.go` (+`emit_test.go`) (new) | typed JSON-LD emission: deterministic `external_id`, `@id` from the reserved row id, payload validation |
| `internal/eventbus/loop.go` (+`loop_test.go`) (new) | polling consumer: lock lifecycle, batch/ack, redelivery |
| `internal/eventbus/metrics.go` (new) | `worklode_event_subscriber_lag`, `worklode_events_processed_total`, `worklode_event_batch_duration_seconds` |
| `internal/api/events.go` (+`events_test.go`) (new) | `GET /api/v1/events`, `GET /api/v1/event-subscribers`, `POST /api/v1/event-subscribers/{name}/seek` |
| `internal/cli/client.go`, `internal/cli/render.go` | client methods + table rendering |
| `internal/cmd/event.go` (new) | `lode event tail\|subscribers\|seek` |
| `ns/ontology.ttl` | `wl:Event`, `wl:DocumentSubmitted`, `wl:DocumentAccepted`, `wl:subject`, `wl:fromStatus`, `wl:toStatus` |
| `docs/specs/004-execution-backbone.md`, `docs/specs/025-documents-in-the-backbone.md`, `CLAUDE.md` | amendment note + mirrors, architecture blurb |
| `e2e/events_test.go` (new) | webhook delivery visible through `GET /api/v1/events` |

---

## Tasks

### Task 1 — Migration: `events.txid` and `event_subscribers`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `deploy/base/migrations/0013_event_log.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`

- [ ] **Step 1: Write the up migration**

`deploy/base/migrations/0013_event_log.up.sql`:

```sql
-- Spec 025 §15: total order for the event log. txid records the writing
-- transaction; readers take only rows below the commit horizon
-- (pg_snapshot_xmin), so a transaction that commits late can never surface
-- an id behind a subscriber's offset. xid8 is 64-bit: no wraparound
-- handling in the comparison.
--
-- The volatile DEFAULT forces a full rewrite of events under an ACCESS
-- EXCLUSIVE lock. Deliberate and acceptable today: the log is small
-- (thousands of rows), and migrations run before the server starts
-- (compose migrate service / K8s initContainer), so nothing reads or
-- writes events during the rewrite. Every pre-existing row takes the
-- migration's own transaction id and drops below the horizon the moment
-- the migration commits. If the table ever grows to where a rewrite hurts,
-- the two-step ADD NULL + backfill dance is the escape hatch — do not need
-- it now, do not build it now.
ALTER TABLE events ADD COLUMN txid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE INDEX events_txid_id ON events (txid, id);

-- Spec 025 §15.1: the durable half of a consumer group, one row per
-- subscriber. Offsets are event ids: monotonic positions, not counts —
-- aborted transactions leave holes and nothing depends on contiguity.
CREATE TABLE event_subscribers (
    name              text PRIMARY KEY,
    last_read_offset  bigint NOT NULL DEFAULT 0,
    last_acked_offset bigint NOT NULL DEFAULT 0,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT event_subscribers_acked_le_read
        CHECK (last_acked_offset <= last_read_offset)
);
```

`deploy/base/migrations/0013_event_log.down.sql`:

```sql
DROP TABLE event_subscribers;
DROP INDEX events_txid_id;
ALTER TABLE events DROP COLUMN txid;
```

- [ ] **Step 2: List both files in `deploy/base/kustomization.yaml`**

Append to the `worklode-migrations` file list, after the `0012_docs` pair
(or after the highest pair present when you get there).

- [ ] **Step 3: Verify**

```bash
./scripts/check-migrations.sh --no-fix   # renumbers? use the number it assigns everywhere below
go test ./internal/store -run TestRecordEvent -count=1
```

The store test harness migrates each test database, so a green run proves
the migration applies. Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add deploy/base/migrations/0013_event_log.* deploy/base/kustomization.yaml
git commit -m "Add events.txid and event_subscribers for the ordered log"
```

---

### Task 2 — Store: horizon read and monotonic ack

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/store/events.go`, `internal/store/events_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/events_test.go` (it defines a local
`openTestStore(t)` wrapping `OpenTestStore`; tests are in package `store`,
so `s.db` is reachable for raw transactions):

```go
// The §9 ordering trap, directly. A last_seen_id cursor sees B (id 2),
// advances, and never delivers A (id 1). The horizon delivers neither
// until A's transaction is finished, then both, in id order.
func TestReadEventBatchHonoursCommitHorizon(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "sub"); err != nil {
		t.Fatal(err)
	}

	insert := func(tx *sql.Tx, ext string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ('system', $1, 'test.event', '{}', now())`, ext); err != nil {
			t.Fatal(err)
		}
	}

	txA, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback()
	insert(txA, "slow") // id 1, uncommitted

	txB, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	insert(txB, "fast") // id 2
	if err := txB.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadEventBatch(ctx, "sub", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("delivered %d events while txA in flight, want 0", len(got))
	}

	if err := txA.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadEventBatch(ctx, "sub", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ExternalID != "slow" || got[1].ExternalID != "fast" {
		t.Fatalf("got %+v, want slow then fast", got)
	}
}
```

Then, same file, three more:

- `TestReadEventBatchSkipsAbortedHole` — begin a tx, insert, **rollback**;
  insert and commit another event via `RecordEvent`. One `ReadEventBatch`
  returns just the committed event and a second returns nothing: the read
  offset advanced past the hole without stalling.
- `TestAckEventsMonotonic` — `RecordEvent` three events; read a batch of 3;
  `AckEvents(ctx, "sub", 3)` then `AckEvents(ctx, "sub", 2)`; assert (via
  `EventSubscribers`) `last_acked_offset` is still 3 and no error was
  returned. Then `AckEvents(ctx, "sub", 99)` — beyond `last_read_offset` —
  must return an error (the CHECK trips), offsets unchanged.
- `TestResetEventReadRedeliversUnacked` — record 3 events, read all 3, ack
  only the first id, `ResetEventRead(ctx, "sub")`, read again: exactly
  events 2 and 3, in order — the §2 restart contract.

- [ ] **Step 2: Run them to verify they fail**

```bash
go test ./internal/store -run 'TestReadEventBatch|TestAckEvents|TestResetEventRead' -count=1
```

Expected: FAIL (undefined: EnsureEventSubscriber etc.).

- [ ] **Step 3: Implement in `internal/store/events.go`**

```go
// EventSubscriber mirrors one event_subscribers row (spec 025 §15.1).
type EventSubscriber struct {
	Name      string
	LastRead  int64
	LastAcked int64
	UpdatedAt time.Time
}

// EnsureEventSubscriber creates the subscriber row if absent. Offset 0
// means a new subscriber replays the whole log — the right default for a
// rule set that should have been running all along (025 §15.1).
func (s *Store) EnsureEventSubscriber(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO event_subscribers (name, updated_at) VALUES ($1, $2)
		 ON CONFLICT (name) DO NOTHING`, name, s.nowFn().UTC())
	return err
}

// ResetEventRead rewinds last_read_offset to last_acked_offset. Called
// once when a consumer acquires the subscriber lock: everything read but
// unacked by the previous holder is redelivered (at-least-once).
func (s *Store) ResetEventRead(ctx context.Context, name string) error { ... }

// ReadEventBatch returns up to limit events after the subscriber's
// last_read_offset that are below the commit horizon, in id order, and
// advances last_read_offset to the last id returned — one transaction.
func (s *Store) ReadEventBatch(ctx context.Context, name string, limit int) ([]Event, error) { ... }

// AckEvents advances last_acked_offset to upTo, forward only: a late or
// replayed lower ack is a silent no-op. Acking past last_read_offset is an
// error (the CHECK backstops it).
func (s *Store) AckEvents(ctx context.Context, name string, upTo int64) error { ... }

// EventSubscribers lists all subscriber rows (offsets only; Task 7 adds
// the lag/holder view).
func (s *Store) EventSubscribers(ctx context.Context) ([]EventSubscriber, error) { ... }
```

`ReadEventBatch`'s transaction, exactly:

```sql
SELECT last_read_offset FROM event_subscribers WHERE name = $1 FOR UPDATE;

SELECT id, source, external_id, type, payload, received_at
  FROM events
 WHERE id > $2                                          -- last_read_offset
   AND txid < pg_snapshot_xmin(pg_current_snapshot())   -- the horizon (025 §15)
 ORDER BY id
 LIMIT $3;

-- only when the batch is non-empty:
UPDATE event_subscribers
   SET last_read_offset = $2, updated_at = $3
 WHERE name = $1 AND last_read_offset < $2;
```

An unknown subscriber name returns `ErrNotFound` (`errors.go` sentinel) from
the `FOR UPDATE` scan. `AckEvents` is a single UPDATE with
`AND $2 > last_acked_offset`; distinguish "no-op because lower" (rows
affected 0, offset unchanged, nil error) from the CHECK violation
(return the wrapped error).

- [ ] **Step 4: Run the tests, expect PASS; run the whole store package**

```bash
go test ./internal/store -count=1
```

- [ ] **Step 5: Commit**

```bash
git commit -am "Add horizon-bounded event reads with subscriber offsets"
```

---

### Task 3 — Store: the subscriber lock on a dedicated connection

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/store/events.go`, `internal/store/events_test.go`

The trap here is `database/sql` pooling: an advisory lock is
**session**-scoped, and `(*sql.Conn).Close()` returns the session to the
pool still holding its locks. A lock released only by `Close` would be held
forever by an idle pooled connection. So release must (a) explicitly
`pg_advisory_unlock` on the same connection, and (b) discard the underlying
session rather than pooling it, so even a failed unlock cannot leak the
lock past the session's death.

- [ ] **Step 1: Write the failing tests**

```go
func TestSubscriberLockExclusive(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	l1, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	_, ok, err = s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || ok {
		t.Fatalf("second lock while held: ok=%v err=%v, want false", ok, err)
	}
	// A different subscriber name is a different lock.
	l2, ok, err := s.TryLockSubscriber(ctx, "other")
	if err != nil || !ok {
		t.Fatalf("other name: ok=%v err=%v", ok, err)
	}
	l2.Release(ctx)

	if err := l1.Release(ctx); err != nil {
		t.Fatal(err)
	}
	l3, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("after release: ok=%v err=%v, want acquired", ok, err)
	}
	l3.Release(ctx)
}
```

Plus `TestSubscriberLockSurvivesPoolChurn`: acquire the lock, then run 40
trivial queries through `s.db` (more than the pool's 16 conns) and assert a
second `TryLockSubscriber` still fails — proving the lock connection is
pinned out of the pool, not silently recycled.

- [ ] **Step 2: Run to verify FAIL, then implement**

```go
// SubscriberLock pins one pool connection holding the pg_try_advisory_lock
// for a subscriber (spec 025 §15.1: one active consumer). The connection is
// held for the consumer's lifetime; a crashed process drops it and
// Postgres releases the lock — failover with no lease table.
type SubscriberLock struct {
	conn *sql.Conn
	name string
}

// TryLockSubscriber takes a dedicated connection from the pool and
// attempts the advisory lock. ok=false means another consumer holds it;
// the connection is returned to the pool (safe: no lock was granted).
func (s *Store) TryLockSubscriber(ctx context.Context, name string) (l *SubscriberLock, ok bool, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	err = conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock(hashtext('wl:subscriber:' || $1)::bigint)`,
		name).Scan(&got)
	if err != nil || !got {
		conn.Close()
		return nil, false, err
	}
	return &SubscriberLock{conn: conn, name: name}, true, nil
}

// Release unlocks and then discards the underlying session instead of
// pooling it. driver.ErrBadConn from Raw marks the connection broken, so
// database/sql closes the real TCP session — Postgres then guarantees the
// lock is gone even if the unlock round-trip failed.
func (l *SubscriberLock) Release(ctx context.Context) error {
	_, unlockErr := l.conn.ExecContext(ctx,
		`SELECT pg_advisory_unlock(hashtext('wl:subscriber:' || $1)::bigint)`, l.name)
	l.conn.Raw(func(any) error { return driver.ErrBadConn })
	l.conn.Close()
	return unlockErr
}
```

(`hashtext` returns `int4`; the explicit `::bigint` picks the one-argument
`pg_try_advisory_lock(bigint)` and sign-extends deterministically, which
Task 7's `pg_locks` join relies on.)

- [ ] **Step 3: Tests PASS, commit**

```bash
go test ./internal/store -run TestSubscriberLock -count=1
git commit -am "Add per-subscriber advisory lock on a pinned connection"
```

---

### Task 4 — Event vocabulary: `ns/` terms and the Go mirror

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `ns/ontology.ttl`
- Create: `internal/eventbus/vocab.go`, `internal/eventbus/vocab_test.go`

**Codegen caveat (read before starting):** spec 025 §15.2 says the Go
constants are *generated* from `ns/` by 025 §17's codegen, which is Task 1 of
plan `2026-08-03-documents-in-the-backbone-1-kinds-and-containers.md`
(`scripts/nsgen.py`) and may or may not be merged when you get here.
- If `scripts/nsgen.py` **exists**: extend it to emit the event-type set
  and per-type property tables into `internal/ns`, and make
  `internal/eventbus/vocab.go` a thin alias over the generated code.
- If it **does not**: hand-mirror per the current `CLAUDE.md` `ns/` rule
  (Turtle first, then the mirror), mark `vocab.go` with a
  `// TODO(025 §17): fold into scripts/nsgen.py output` comment, and write
  the drift test below so the mirror cannot rot silently.

- [ ] **Step 1: Add the terms to `ns/ontology.ttl`**

Next to `wl:RuntimeEvent` (~line 203), matching the file's comment style:

```turtle
wl:Event a owl:Class ;
    rdfs:subClassOf prov:Activity ;
    wl:layer wlc:execution ;
    rdfs:comment "A domain event in the backbone's append-only log (spec 025 §15.2):
        a state change witnessed at commit, curie in events.type, JSON-LD in
        events.payload. One subclass per event type — types differ in shape, so a
        class hierarchy, not a SKOS kind scheme. Vendor webhook deliveries share
        the events table but are not wl:Events (025 §15.2: one log, two populations)." .

wl:DocumentSubmitted a owl:Class ;
    rdfs:subClassOf wl:Event ;
    wl:layer wlc:execution ;
    rdfs:comment "A draft document was handed to review (lode doc submit, 025 §15.4).
        Changes no document column: the open review task is 'under review' (025 §7)." .

wl:DocumentAccepted a owl:Class ;
    rdfs:subClassOf wl:Event ;
    wl:layer wlc:execution ;
    rdfs:comment "A document moved draft → accepted (025 §7). Carries the status
        transition as wl:fromStatus / wl:toStatus." .

wl:subject a owl:ObjectProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:Event ;
    rdfs:comment "The thing the event is about (025 §15.2). Range deliberately open:
        any backbone entity." .

wl:fromStatus a owl:ObjectProperty, owl:FunctionalProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:DocumentAccepted ; rdfs:range skos:Concept ;
    rdfs:comment "Status before the transition (a wlc: editorial status)." .

wl:toStatus a owl:ObjectProperty, owl:FunctionalProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:DocumentAccepted ; rdfs:range skos:Concept ;
    rdfs:comment "Status after the transition (a wlc: editorial status)." .
```

Validate: `riot --validate ns/*.ttl` (skip with a note if riot is not
installed locally; CI equivalents exist).

- [ ] **Step 2: Write the failing drift test**

`internal/eventbus/vocab_test.go`: read `../../ns/ontology.ttl` (via
`os.ReadFile` + a small regexp for
`^(wl:\w+) a owl:Class ;\n\s+rdfs:subClassOf wl:Event`) and assert the set
of `wl:Event` subclasses equals the keys of `payloadProperties`. Plus
table tests: `KnownType("wl:DocumentAccepted")` true, `KnownType("push")`
false.

- [ ] **Step 3: Implement `internal/eventbus/vocab.go`**

```go
// Package eventbus implements spec 027: typed domain-event emission and the
// offset-tracked subscriber loop over the store's events table.
package eventbus

// Hand-mirrored from ns/ontology.ttl (spec 025 §15.2).
// TODO(025 §17): fold into scripts/nsgen.py output when the codegen lands.
// vocab_test.go holds the mirror together.
const (
	TypeDocumentSubmitted = "wl:DocumentSubmitted"
	TypeDocumentAccepted  = "wl:DocumentAccepted"
)

// baseProperties are allowed in every domain-event payload.
var baseProperties = []string{
	"@context", "@type", "@id", "prov:atTime", "prov:wasAssociatedWith", "wl:subject",
}

// payloadProperties maps each event type to its additional allowed payload
// properties. Emit-time validation (emit.go) enforces membership; there is
// deliberately no CHECK on events.type — the log also holds vendor webhook
// deliveries with dotted types (025 §15.2).
var payloadProperties = map[string][]string{
	TypeDocumentSubmitted: {},
	TypeDocumentAccepted:  {"wl:fromStatus", "wl:toStatus"},
}

// KnownType reports whether typ is a generated domain-event type. Metrics
// use it to bound the type label (§8: unknown counts as "other").
func KnownType(typ string) bool { _, ok := payloadProperties[typ]; return ok }
```

- [ ] **Step 4: Tests PASS, commit**

```bash
go test ./internal/eventbus -count=1
git commit -am "Add wl:Event vocabulary to ns/ with mirrored eventbus constants"
```

---

### Task 5 — Typed emission: deterministic ids, JSON-LD payloads, validation

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

**Files:**
- Modify: `internal/store/events.go`, `internal/store/events_test.go`
- Create: `internal/eventbus/emit.go`, `internal/eventbus/emit_test.go`

Two pieces. The payload's `@id` is `wlid:event/<id>` (025 §15.2), but
`RecordEvent` writes the payload in the same INSERT that assigns the id —
so the store gains `RecordEventWithID`, which reserves the id from the
sequence first and builds the payload from it. Emission stays a thin typed
helper per event type (025 §15.3); nothing in this part calls the helpers yet
(part 2's doc lifecycle does), so the tests are the consumer.

- [ ] **Step 1: Failing store test**

`TestRecordEventWithID` in `internal/store/events_test.go`: call

```go
id, inserted, err := s.RecordEventWithID(ctx, "cli", "wl:Test:wlid:doc/x:1", "wl:Test",
	func(eventID int64) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"@id":"wlid:event/%d"}`, eventID)), nil
	}, nil)
```

Assert: `inserted`, and the stored payload's `@id` equals
`wlid:event/<id>`. Call again with the same source+external_id: same `id`
back, `inserted == false`, payload unchanged. Nil `payloadFor` is an error.

- [ ] **Step 2: Implement `RecordEventWithID`**

Same shape as `RecordEvent` (keep them adjacent), except inside the Tx:

```sql
SELECT nextval(pg_get_serial_sequence('events', 'id'));
```

then build the payload via `payloadFor(id)` and

```sql
INSERT INTO events (id, source, external_id, type, payload, received_at)
     OVERRIDING SYSTEM VALUE
     VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, external_id) DO NOTHING
RETURNING id;
```

On conflict, look up the existing id and skip `apply`, exactly as
`RecordEvent` does. A retry burns a sequence value — fine, offsets are
positions, not counts (025 §15).

- [ ] **Step 3: Failing eventbus tests**

`internal/eventbus/emit_test.go` (store-backed, use `store.OpenTestStore`):

- `TestEmitDocumentAcceptedRoundTrip` — build
  `DocumentAccepted{Doc: "wlid:doc/spec-025", Actor: "stig", At: ..., Version: 2, From: "wlc:draft", To: "wlc:accepted"}`,
  emit via `Emit(ctx, st, "cli", ev, nil)`; assert the stored row has
  `type = "wl:DocumentAccepted"`,
  `external_id = "wl:DocumentAccepted:wlid:doc/spec-025:2"`, and the
  payload unmarshals with `@type`, `@id = wlid:event/<id>`,
  `wl:subject = wlid:doc/spec-025`, `prov:wasAssociatedWith = wlid:actor/stig`,
  `wl:fromStatus`/`wl:toStatus`.
- `TestEmitIdempotentAtTheLog` — emit the same value twice; second call
  returns the same id, `inserted == false` (025 §15.3: a resent request gets
  the existing event, not a duplicate acceptance).
- `TestValidatePayloadRejectsUnknownProperty` — `validatePayload("wl:DocumentAccepted", keys)`
  with an extra `"wl:bogus"` key returns an error naming it (§9's
  emit-time validation test).

- [ ] **Step 4: Implement `internal/eventbus/emit.go`**

```go
// DomainEvent is one emittable event type (spec 025 §15.3): it knows its
// type curie, its deterministic external id, and its JSON-LD payload.
type DomainEvent interface {
	EventType() string
	// ExternalID is <type>:<subject>:<version> — what makes a retried
	// request idempotent at the log (025 §15.3).
	ExternalID() string
	// Properties returns the payload minus @context/@type/@id, keyed by
	// ontology property curie.
	Properties() map[string]any
}

type DocumentSubmitted struct {
	Doc     string    // subject IRI, e.g. wlid:doc/spec-025
	Actor   string    // actor id; rendered wlid:actor/<id>
	At      time.Time
	Version int
}

type DocumentAccepted struct {
	Doc      string
	Actor    string
	At       time.Time
	Version  int
	From, To string // wlc: status curies
}

// Emit validates ev's payload against the generated property set and
// records it through the store in one transaction with apply — the same
// seam every other write uses (025 §15.3: an event that could commit without
// its change is a log that lies).
func Emit(ctx context.Context, st *store.Store, source string, ev DomainEvent,
	apply func(tx *sql.Tx, eventID int64) error) (int64, bool, error) {
	props := ev.Properties()
	if err := validatePayload(ev.EventType(), keysOf(props)); err != nil {
		return 0, false, err
	}
	return st.RecordEventWithID(ctx, source, ev.ExternalID(), ev.EventType(),
		func(id int64) ([]byte, error) {
			full := map[string]any{
				"@context": "https://worklode.io/ns/ontology#",
				"@type":    ev.EventType(),
				"@id":      fmt.Sprintf("wlid:event/%d", id),
			}
			for k, v := range props {
				full[k] = v
			}
			return json.Marshal(full)
		}, apply)
}
```

`Properties()` for both types emits `prov:atTime` (RFC 3339 UTC),
`prov:wasAssociatedWith` (`wlid:actor/` + Actor), `wl:subject` (Doc), and
for `DocumentAccepted` the from/to pair. `validatePayload` checks keys ⊆
base + per-type set **and** that every per-type property is present
(a missing property fails at emit, 025 §15.3 — compile-time via the struct,
runtime via this check).

- [ ] **Step 5: Tests PASS, commit**

```bash
go test ./internal/eventbus ./internal/store -count=1
git commit -am "Add typed domain-event emission with deterministic ids"
```

---

### Task 6 — The consumer loop and the eventbus metrics

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 4]
```

**Files:**
- Create: `internal/eventbus/loop.go`, `internal/eventbus/metrics.go`,
  `internal/eventbus/loop_test.go`
- Modify: `internal/store/events.go` (the lag query)

- [ ] **Step 1: Failing tests** (`loop_test.go`, store-backed; use
  10–20 ms intervals so the tests run in well under a second)

- `TestLoopDeliversInOrderAndAcks` — record 3 events; run one loop with a
  handler appending ids to a slice; wait until 3 handled; cancel; assert
  order and `last_acked_offset == last id` (via `EventSubscribers`).
- `TestLoopSingleConsumer` — §9: two loops, same subscriber name, same
  store, each handler recording into its own slice; only one consumes.
  Cancel the first loop's context; append more events; the second takes
  over within a few lock-retry intervals and consumes the new events.
- `TestLoopRedeliversAfterHandlerError` — handler fails on the second
  event twice, then succeeds: every event is eventually processed exactly
  in order, and ids 1 is acked before 2 ever succeeds (prefix-ack).
- `TestLoopMetrics` — drive a loop with a fresh
  `prometheus.NewRegistry()`; assert with `prometheus/testutil`:
  `worklode_events_processed_total{subscriber,type="other",outcome="applied"}`
  counted (the test events use a dotted type), batch histogram observed,
  and `worklode_event_subscriber_lag{subscriber}` > 0 while the loop is
  stopped with events pending, 0 after it catches up (via
  `testutil.CollectAndCompare` or `ToFloat64` on a gather).

- [ ] **Step 2: Implement `metrics.go`** (022 conventions: nil-safe struct,
  `prometheus.Registerer` in, bounded labels)

```go
type Metrics struct {
	processed *prometheus.CounterVec   // worklode_events_processed_total{subscriber,type,outcome}
	batchDur  *prometheus.HistogramVec // worklode_event_batch_duration_seconds{subscriber}
}

// NewMetrics registers the eventbus instruments plus the lag gauge, a
// custom collector in the worklode_leases_active mould: it queries
// per-subscriber lag at scrape time (2 s timeout) and emits an invalid
// metric on failure rather than a stale zero.
func NewMetrics(reg prometheus.Registerer, st *store.Store) *Metrics { ... }

func (m *Metrics) event(subscriber, typ, outcome string) {
	if m == nil {
		return
	}
	if !KnownType(typ) {
		typ = "other" // §8: bounded label
	}
	m.processed.WithLabelValues(subscriber, typ, outcome).Inc()
}
```

Buckets for the histogram: `0.001, 0.01, 0.1, 1, 10`. The lag collector
calls a new store method:

```sql
-- Store.EventSubscriberLags: horizon tail minus acked, per subscriber.
-- Rises when a subscriber is stuck AND when any long transaction holds
-- the horizon back (025 §15/§8) — one gauge, both pathologies.
SELECT s.name, GREATEST(h.max_id - s.last_acked_offset, 0)
  FROM event_subscribers s,
       (SELECT COALESCE(MAX(id), 0) AS max_id
          FROM events
         WHERE txid < pg_snapshot_xmin(pg_current_snapshot())) h;
```

- [ ] **Step 3: Implement `loop.go`**

```go
// Outcome classifies one handled event for metrics (spec 025 §15.7).
type Outcome string

const (
	OutcomeApplied    Outcome = "applied"
	OutcomeSuppressed Outcome = "suppressed"
)

// Handler processes one event. Returning an error stops the batch: the
// prefix already handled is acked, the failed event is redelivered on the
// next poll, and in-order delivery means it blocks everything behind it —
// deliberate (at-least-once, no DLQ; 025 §22 keeps retention/partitioning
// out of scope). The error surfaces in the outcome="error" counter and in
// the lag gauge.
type Handler func(ctx context.Context, ev store.Event) (Outcome, error)

type Options struct {
	Store     *store.Store
	Name      string        // subscriber name; the row must exist (EnsureEventSubscriber)
	Handler   Handler
	Poll      time.Duration // default 1s (025 §15.1: polling, deliberately no LISTEN/NOTIFY)
	LockRetry time.Duration // default 15s: how often a standby retries the lock
	BatchSize int           // default 100
	Metrics   *Metrics      // nil-safe
	Log       *slog.Logger
}

// Run consumes until ctx is cancelled. Lifecycle per iteration:
//  1. no lock → TryLockSubscriber; on failure sleep LockRetry.
//  2. on acquiring the lock: ResetEventRead (redeliver read-but-unacked).
//  3. ReadEventBatch; empty → sleep Poll.
//  4. handle events in order; on first error ack the successful prefix,
//     count outcome=error, sleep Poll (head-of-line retry).
//  5. all applied → AckEvents(last id).
// Any store error on the lock connection path drops the lock (Release)
// and returns to 1 — a broken session must not be treated as held.
// On ctx.Done the lock is Released.
func Run(ctx context.Context, o Options) error { ... }
```

Write it exactly to that comment; it is the contract the tests pin. Batch
duration is observed around step 3–5.

- [ ] **Step 4: Tests PASS, commit**

```bash
go test ./internal/eventbus -count=1 -race
git commit -am "Add the polling subscriber loop with eventbus metrics"
```

---

### Task 7 — Read surfaces: store queries, API, CLI

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

**Files:**
- Modify: `internal/store/events.go`, `internal/store/events_test.go`,
  `internal/api/server.go` (routes), `internal/cli/client.go`,
  `internal/cli/render.go`
- Create: `internal/api/events.go`, `internal/api/events_test.go`,
  `internal/cmd/event.go`

- [ ] **Step 1: Store layer, failing tests first**

```go
// EventFilter narrows ListEvents. Zero values do not filter.
type EventFilter struct {
	Type  string
	Since time.Time
	Limit int // default/cap 200
}

// ListEvents returns matching events in id order (newest last, 025 §18),
// horizon-bounded like every subscriber read so the tail never shows an
// id that later reads would order before.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error)

// EventSubscriberStatus is the lode event subscribers row (025 §18).
type EventSubscriberStatus struct {
	EventSubscriber
	Lag       int64
	HolderPID int64 // Postgres backend pid holding the lock; 0 = none
}

func (s *Store) EventSubscriberStatuses(ctx context.Context) ([]EventSubscriberStatus, error)

// SeekEventSubscriber moves both offsets to the given position — the only
// path that moves an offset backwards (admin replay/skip, 025 §18). Safe
// precisely because handlers are idempotent.
func (s *Store) SeekEventSubscriber(ctx context.Context, name string, to int64) error
```

The holder join — the spec's "holder" column is the lock-holding
**Postgres backend pid** (which replica owns that backend is not knowable
from SQL; the pid still distinguishes "held" from "free"). `pg_locks`
splits a 64-bit advisory key into `classid` (high 32) / `objid` (low 32),
`objsubid = 1`:

```sql
SELECT s.name, s.last_read_offset, s.last_acked_offset, s.updated_at,
       GREATEST(h.max_id - s.last_acked_offset, 0) AS lag,
       COALESCE(l.pid, 0) AS holder_pid
  FROM event_subscribers s
 CROSS JOIN (SELECT COALESCE(MAX(id), 0) AS max_id
               FROM events
              WHERE txid < pg_snapshot_xmin(pg_current_snapshot())) h
  LEFT JOIN LATERAL (
       SELECT pid FROM pg_locks
        WHERE locktype = 'advisory' AND granted AND objsubid = 1
          AND classid = ((hashtext('wl:subscriber:' || s.name)::bigint >> 32) & 4294967295)::oid
          AND objid   = (hashtext('wl:subscriber:' || s.name)::bigint & 4294967295)::oid
        LIMIT 1) l ON true
 ORDER BY s.name;
```

Tests: statuses show `HolderPID != 0` while a `TryLockSubscriber` lock is
held and 0 after release; seek moves both offsets down and a following
`ReadEventBatch` redelivers from there; `SeekEventSubscriber` on an unknown
name returns `ErrNotFound`.

- [ ] **Step 2: API, failing tests first** (`internal/api/events_test.go`,
  copy the `tasks_test.go` bearer-token/`doReq` style)

| Route | Behaviour |
|---|---|
| `GET /api/v1/events?type=&since=&limit=` | 200, `{"events": [...]}`; `since` RFC 3339; any authed actor |
| `GET /api/v1/event-subscribers` | 200, `{"subscribers": [...]}` with lag + holder_pid; any authed actor |
| `POST /api/v1/event-subscribers/{name}/seek` | body `{"to": 42}`; admin only (403 otherwise, the `requireAdmin` wrapper); 404 unknown name; 200 with the updated row |

Register in `server.go` beside the inbox routes:

```go
mux.Handle("GET /api/v1/events", s.auth(s.listEvents))
mux.Handle("GET /api/v1/event-subscribers", s.auth(s.listEventSubscribers))
mux.Handle("POST /api/v1/event-subscribers/{name}/seek", s.auth(requireAdmin(s.seekEventSubscriber)))
```

Seek is an admin correction of consumer state, not a domain fact — it is
deliberately **not** wrapped in `RecordEvent` (nothing derives from
subscriber offsets, and logging offset moves into the log they index would
be noise).

- [ ] **Step 3: CLI**

`internal/cli/client.go`: `ListEvents(ctx, EventListFilter) ([]Event, []byte, error)`,
`EventSubscribers(ctx)`, `SeekEventSubscriber(ctx, name string, to int64)`.
`internal/cmd/event.go`, copying `timeline.go`'s shape (`newAPIClient...`,
`jsonOut`, `printRaw`):

```
lode event tail [--type <t>] [--since <duration>] [--limit <n>]   newest last
lode event subscribers          NAME  READ  ACKED  LAG  HOLDER  UPDATED
lode event seek <name> --to <offset>
```

`--since 2h` converts to `time.Now().Add(-d)` client-side. Table rendering
in `internal/cli/render.go` next to the existing renderers; `tail` prints
`ID  RECEIVED  SOURCE  TYPE  EXTERNAL_ID`. `seek` prints the updated row
and a one-line warning that replay relies on handler idempotency.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/store ./internal/api ./internal/cli ./internal/cmd -count=1
git commit -am "Add lode event tail/subscribers/seek over the ordered log"
```

---

### Task 8 — e2e: the log is readable through public surfaces

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [7]
```

**Files:**
- Create: `e2e/events_test.go`

- [ ] **Step 1: Write the test**

Copy `e2e/smoke_test.go`'s boot (its `store.OpenTestStore` +
`api.NewServer` + `httptest.NewServer` + bootstrap-admin steps and the
`deliverGitHub` helper). Then:

1. Deliver one signed GitHub `push` webhook via `deliverGitHub`.
2. As the admin client, `GET /api/v1/events` — assert the delivery
   appears with its dotted vendor type and `source: "github"` (025 §15.2's
   two populations share one log; acceptance 7).
3. `GET /api/v1/event-subscribers` — empty list, 200 (no subscriber is
   wired until part 2).
4. Admin `POST /api/v1/event-subscribers/nope/seek` — 404; agent-token
   seek — 403.

- [ ] **Step 2: Verify and commit**

```bash
go test -race -count=1 -tags e2e ./e2e/ -run TestEventLog
git commit -am "Add e2e coverage for the event log read surface"
```

---

### Task 9 — Docs: amend 004, fix 027's citation, CLAUDE.md

```yaml
kind: chore
priority: low
blockedBy: [ ]
```

**Files:**
- Modify: `docs/specs/004-execution-backbone.md`,
  `docs/specs/025-documents-in-the-backbone.md`, `CLAUDE.md`

Read `docs/authoring-design-docs.md` §"Amending a section" first — three
edits, all required. Note: 004's events table lives in **§1.5**
(`{#sec-1.5}` "events + state_log"), not §2 as spec 027 cites — 004 §3 is
the lease lifecycle. 027 is `draft`, so correcting its citations is a
plain edit.

- [ ] **Step 1: The amendment, all three edits**

In `004-execution-backbone.md` under the `### 1.5` heading:

```markdown
> **Amended by spec 027.** The events table gains `txid xid8` and a commit
> horizon so the log is totally ordered; `event_subscribers` rows give it
> offset-tracked, at-least-once subscribers. The log stops being write-only.
```

In 004's frontmatter:

```yaml
amendedBy:
  "#sec-1.5":
    - 027-event-watchers.md
```

In 027's frontmatter (merge with what is there; re-read the file first —
another session may have touched it):

```yaml
amends:
  ".":
    - 004-execution-backbone.md#sec-1.5
```

- [ ] **Step 2: Fix 027's `(004 §3)` citations**

In `027-event-watchers.md` §0 and §2, change the two `(004 §3)` references
for the events table to `(004 §1.4)`.

- [ ] **Step 3: CLAUDE.md**

In the "Architecture" cross-cutting paragraph, extend the list with one
clause: `internal/eventbus` (offset-tracked subscribers over the events
log, spec 027) — keep it to a line, per the comment-hygiene rule.

- [ ] **Step 4: Verify and commit**

```bash
./scripts/secfmt.py -l
./scripts/secindex.py && git diff --stat docs/specs/index.yaml docs/plans/index.yaml
git add -A && git commit -m "Record spec 027's amendment of 004 and the eventbus in CLAUDE.md"
```

---

## Done when (maps to 025 §24)

1. AC1: `TestReadEventBatchHonoursCommitHorizon` passes, and flipping the
   read predicate to a bare `id > last_seen` makes it fail — run that
   experiment once during Task 2 review and record it in the PR.
2. AC2: monotonic-ack and reset tests pass; the CHECK holds.
3. AC3: `TestLoopSingleConsumer` proves handover with no operator step.
4. AC7: the e2e test shows vendor events in `lode event tail`'s API with
   dotted types, no CHECK on `events.type`.
5. AC8's eventbus half: the three `internal/eventbus` metrics registered
   and asserted with `testutil`.
6. AC6 is **partial by design**: emit-time validation is enforced and
   tested, but "generated from `ns/` with CI drift-fail" is only fully
   satisfied once 025's codegen exists — the vocab drift test is the
   stand-in until then.
