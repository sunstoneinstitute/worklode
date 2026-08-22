package hooks_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	catalogTestSecret = "test-catalog-secret"
	catalogArtifact   = "bigquery://sunstone-prod/cow/casualties"
)

// catalogEnv is a webhook test fixture: a store with project "demo" and the
// catalog handler. The raw-SQL assertions come from the embedded dbEnv,
// shared with the GitHub and Flux fixtures.
type catalogEnv struct {
	dbEnv
	h http.Handler
}

func newCatalogEnv(t *testing.T) *catalogEnv {
	t.Helper()
	return newCatalogEnvWith(t, catalogTestSecret, nil)
}

func newCatalogEnvWith(t *testing.T, secret string, m *hooks.Metrics) *catalogEnv {
	t.Helper()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(context.Background(), "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return &catalogEnv{
		dbEnv: dbEnv{st: st},
		h:     hooks.NewCatalogHandler(st, secret, nil, m),
	}
}

// catalogHandlerNoDB builds the handler over a nil store. The 503, signature
// and payload paths all return before the handler reaches its store, so the
// cases most worth having green without Postgres are asserted here rather than
// behind a fixture that skips. The cross-check that such a delivery records no
// event does need a database, and lives in a subtest that skips on its own.
func catalogHandlerNoDB(secret string, m *hooks.Metrics) http.Handler {
	return hooks.NewCatalogHandler(nil, secret, nil, m)
}

// seedDeliverable declares a deliverable in project "demo" with the given
// artifact address, which is what makes it a routing target.
func (e *catalogEnv) seedDeliverable(t *testing.T, name, artifact string) string {
	t.Helper()
	var id string
	_, _, err := e.st.RecordEvent(context.Background(), "cli", "seed-del:"+name+t.Name(),
		"deliverable.created", nil, func(tx *sql.Tx, _ int64) error {
			d, err := store.CreateDeliverable(tx, e.st.Now(), store.DeliverableInput{
				ProjectID: "demo", Name: name, Artifact: artifact,
			})
			if err != nil {
				return err
			}
			id = d.ID
			return nil
		})
	if err != nil {
		t.Fatalf("seed deliverable %s: %v", name, err)
	}
	return id
}

// seedTaskDeclaring creates a task in project "demo", walks it to state, and
// declares artifact against it.
func (e *catalogEnv) seedTaskDeclaring(t *testing.T, state, artifact string) string {
	t.Helper()
	var id string
	_, _, err := e.st.RecordEvent(context.Background(), "cli", "seed-task:"+state+t.Name(),
		"task.created", nil, func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, e.st.Now(), store.TaskInput{
				ProjectID: "demo", Title: "ship " + state, Priority: "medium", Kind: "feature",
			}, eventID)
			if err != nil {
				return err
			}
			id = task.ID
			if state != "ready" {
				if err := store.Transition(tx, e.st.Now(), id, "ready", state, eventID); err != nil {
					return err
				}
			}
			return store.DeclareArtifact(tx, e.st.Now(), "task", id, artifact)
		})
	if err != nil {
		t.Fatalf("seed %s task: %v", state, err)
	}
	return id
}

// evidenceRows counts the evidence filed against one entity.
func (e *catalogEnv) evidenceRows(t *testing.T, kind, id string) int {
	return e.rawQueryInt(t,
		`SELECT COUNT(*) FROM artifact_evidence WHERE entity_kind = $1 AND entity_id = $2`, kind, id)
}

func (e *catalogEnv) catalogEventCount(t *testing.T) int {
	return e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE source = 'catalog'`)
}

func (e *catalogEnv) eventTypeFor(t *testing.T, delivery string) string {
	return e.rawQueryString(t,
		`SELECT type FROM events WHERE source = 'catalog' AND external_id = $1`, delivery)
}

func catalogSign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(catalogTestSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// catalogDeliver posts a signed body. delivery "" omits X-Catalog-Delivery,
// which is what makes the body hash the idempotency key.
func catalogDeliver(t *testing.T, h http.Handler, delivery string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/hooks/catalog", bytes.NewReader(body))
	req.Header.Set("X-Signature", catalogSign(body))
	if delivery != "" {
		req.Header.Set("X-Catalog-Delivery", delivery)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func catalogBody(artifact, state string) []byte {
	return []byte(`{"artifact":"` + artifact + `","state":"` + state + `"}`)
}

// TestCatalogSecretUnconfigured: a server with no secret refuses every
// delivery rather than accepting unauthenticated ones.
func TestCatalogSecretUnconfigured(t *testing.T) {
	rr := catalogDeliver(t, catalogHandlerNoDB("", nil), "d-1",
		catalogBody(catalogArtifact, "published"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}

	t.Run("records no event", func(t *testing.T) {
		e := newCatalogEnvWith(t, "", nil)
		catalogDeliver(t, e.h, "d-1", catalogBody(catalogArtifact, "published"))
		if got := e.catalogEventCount(t); got != 0 {
			t.Fatalf("recorded %d event(s), want 0", got)
		}
	})
}

// TestCatalogSignatureRejected: a bad or absent signature is 401, records no
// event, and the delivery is counted as rejected rather than as an error.
func TestCatalogSignatureRejected(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	h := catalogHandlerNoDB(catalogTestSecret, m)
	body := catalogBody(catalogArtifact, "published")

	deliverUnsigned := func(t *testing.T, h http.Handler, sig string) int {
		req := httptest.NewRequest("POST", "/hooks/catalog", bytes.NewReader(body))
		if sig != "" {
			req.Header.Set("X-Signature", sig)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	for _, tc := range []struct{ name, sig string }{
		{"missing", ""},
		{"wrong", "sha256=" + hex.EncodeToString(make([]byte, 32))},
		{"malformed", "not-a-signature"},
	} {
		if code := deliverUnsigned(t, h, tc.sig); code != http.StatusUnauthorized {
			t.Errorf("%s signature: status = %d, want 401", tc.name, code)
		}
	}
	if got := testutil.ToFloat64(m.Events().WithLabelValues("catalog", "invalid", "rejected")); got != 3 {
		t.Fatalf("events{catalog,invalid,rejected} = %v, want 3", got)
	}

	t.Run("records no event", func(t *testing.T) {
		e := newCatalogEnvWith(t, catalogTestSecret, nil)
		if code := deliverUnsigned(t, e.h, ""); code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", code)
		}
		if got := e.catalogEventCount(t); got != 0 {
			t.Fatalf("recorded %d event(s), want 0", got)
		}
	})
}

// TestCatalogPayloadRejected: a signed but unusable payload is 400 and
// records nothing. The state set is closed, so an unknown state cannot reach
// the CHECK constraint or the metric label.
func TestCatalogPayloadRejected(t *testing.T) {
	bad := []struct {
		name string
		body []byte
	}{
		{"malformed JSON", []byte(`{"artifact":`)},
		{"missing artifact", []byte(`{"state":"published"}`)},
		{"blank artifact", catalogBody("   ", "published")},
		{"missing state", []byte(`{"artifact":"` + catalogArtifact + `"}`)},
		{"unknown state", catalogBody(catalogArtifact, "vanished")},
	}

	h := catalogHandlerNoDB(catalogTestSecret, nil)
	for _, tc := range bad {
		rr := catalogDeliver(t, h, "d-"+tc.name, tc.body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, rr.Code)
		}
	}

	t.Run("records no event", func(t *testing.T) {
		e := newCatalogEnv(t)
		for _, tc := range bad {
			catalogDeliver(t, e.h, "d-"+tc.name, tc.body)
		}
		if got := e.catalogEventCount(t); got != 0 {
			t.Fatalf("recorded %d event(s), want 0", got)
		}
	})
}

// TestCatalogFilesEvidenceAgainstDeclarer is the happy path: the fact lands
// as one evidence row against the deliverable that declared the address,
// carrying the emitter's state, time and detail, and the provenance says
// "observed" because a machine reported it (029 §3.2).
func TestCatalogFilesEvidenceAgainstDeclarer(t *testing.T) {
	e := newCatalogEnv(t)
	id := e.seedDeliverable(t, "casualties", catalogArtifact)
	other := e.seedDeliverable(t, "unrelated", "gs://sunstone-prod/other")

	body := []byte(`{"event":"dataset.published","artifact":"` + catalogArtifact + `",` +
		`"state":"published","catalog":"prod","version":"v7",` +
		`"url":"https://catalog.example.org/d/casualties",` +
		`"occurred_at":"2026-08-19T09:12:03Z","detail":{"rows":42}}`)
	rr := catalogDeliver(t, e.h, "d-happy", body)
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("status = %d, ack = %q, want 200 ok", rr.Code, ackStatus(t, rr))
	}
	if got := e.eventTypeFor(t, "d-happy"); got != "catalog.dataset.published" {
		t.Errorf("event type = %q, want catalog.dataset.published", got)
	}

	var state, provenance, source, version, url, detail string
	var occurredAt time.Time
	if !e.rawQueryRow(t, []any{&state, &provenance, &source, &version, &url, &detail, &occurredAt},
		`SELECT state, provenance, source, version, url, detail::text, occurred_at
		 FROM artifact_evidence WHERE entity_kind = 'deliverable' AND entity_id = $1`, id) {
		t.Fatalf("no evidence row for %s", id)
	}
	if state != "published" || provenance != "observed" || source != "catalog" ||
		version != "v7" || url != "https://catalog.example.org/d/casualties" {
		t.Errorf("evidence = %s/%s/%s version %q url %q, want published/observed/catalog v7",
			state, provenance, source, version, url)
	}
	if detail != `{"rows": 42}` {
		t.Errorf("detail = %q, want the emitter's object", detail)
	}
	want := time.Date(2026, 8, 19, 9, 12, 3, 0, time.UTC)
	if !occurredAt.Equal(want) {
		t.Errorf("occurred_at = %v, want %v", occurredAt, want)
	}
	if got := e.evidenceRows(t, "deliverable", other); got != 0 {
		t.Errorf("deliverable declaring another address got %d evidence row(s), want 0", got)
	}
}

// TestCatalogOccurredAtDefaultsToNow: an emitter that sends no timestamp gets
// the store clock, not a zero time.
func TestCatalogOccurredAtDefaultsToNow(t *testing.T) {
	e := newCatalogEnv(t)
	id := e.seedDeliverable(t, "casualties", catalogArtifact)

	before := e.st.Now()
	if rr := catalogDeliver(t, e.h, "d-now", catalogBody(catalogArtifact, "updated")); rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var occurredAt time.Time
	if !e.rawQueryRow(t, []any{&occurredAt},
		`SELECT occurred_at FROM artifact_evidence WHERE entity_id = $1`, id) {
		t.Fatalf("no evidence row for %s", id)
	}
	if occurredAt.Before(before.Add(-time.Minute)) || occurredAt.After(e.st.Now().Add(time.Minute)) {
		t.Fatalf("occurred_at = %v, want the store clock around %v", occurredAt, before)
	}
}

// TestCatalogRedeliveryIsIdempotent: both idempotency keys — the emitter's
// delivery id and, absent one, the body hash — ack "duplicate" and write no
// second evidence row.
func TestCatalogRedeliveryIsIdempotent(t *testing.T) {
	t.Run("delivery header", func(t *testing.T) {
		e := newCatalogEnv(t)
		id := e.seedDeliverable(t, "casualties", catalogArtifact)
		body := catalogBody(catalogArtifact, "published")

		if got := ackStatus(t, catalogDeliver(t, e.h, "d-dup", body)); got != "ok" {
			t.Fatalf("first ack = %q, want ok", got)
		}
		if got := ackStatus(t, catalogDeliver(t, e.h, "d-dup", body)); got != "duplicate" {
			t.Fatalf("redelivery ack = %q, want duplicate", got)
		}
		if got := e.evidenceRows(t, "deliverable", id); got != 1 {
			t.Fatalf("evidence rows = %d, want 1", got)
		}
	})

	t.Run("body hash", func(t *testing.T) {
		e := newCatalogEnv(t)
		id := e.seedDeliverable(t, "casualties", catalogArtifact)
		body := catalogBody(catalogArtifact, "published")

		if got := ackStatus(t, catalogDeliver(t, e.h, "", body)); got != "ok" {
			t.Fatalf("first ack = %q, want ok", got)
		}
		if got := ackStatus(t, catalogDeliver(t, e.h, "", body)); got != "duplicate" {
			t.Fatalf("redelivery ack = %q, want duplicate", got)
		}
		if got := e.evidenceRows(t, "deliverable", id); got != 1 {
			t.Fatalf("evidence rows = %d, want 1", got)
		}
		if got := e.catalogEventCount(t); got != 1 {
			t.Fatalf("recorded %d event(s), want 1", got)
		}
	})
}

// TestCatalogUnroutedDeliveryIsStillRecorded: nothing declares the address,
// so no evidence is written — but the delivery lands in events, so a
// declaration added later can be reconciled by replay.
func TestCatalogUnroutedDeliveryIsStillRecorded(t *testing.T) {
	e := newCatalogEnv(t)
	rr := catalogDeliver(t, e.h, "d-unrouted", catalogBody("gs://nobody/declares-this", "published"))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "unrouted" {
		t.Fatalf("status = %d, ack = %q, want 200 unrouted", rr.Code, ackStatus(t, rr))
	}
	if got := e.catalogEventCount(t); got != 1 {
		t.Fatalf("recorded %d event(s), want 1", got)
	}
	if got := e.rawQueryInt(t, `SELECT COUNT(*) FROM artifact_evidence`); got != 0 {
		t.Fatalf("wrote %d evidence row(s), want 0", got)
	}
}

// TestCatalogRoutesOnlyToOpenDeclarers: the handler files against declarers
// that are still open. A closed task is not one — closing it is exactly the
// claim that it no longer awaits evidence. (internal/store's
// TestOpenDeclarationsForArtifactPerKindOpenness covers each kind's predicate;
// this pins that the handler uses it.)
func TestCatalogRoutesOnlyToOpenDeclarers(t *testing.T) {
	e := newCatalogEnv(t)
	open := e.seedTaskDeclaring(t, "in_progress", catalogArtifact)
	abandoned := e.seedTaskDeclaring(t, "abandoned", catalogArtifact)

	rr := catalogDeliver(t, e.h, "d-open", catalogBody(catalogArtifact, "published"))
	if got := ackStatus(t, rr); got != "ok" {
		t.Fatalf("ack = %q, want ok", got)
	}
	if got := e.evidenceRows(t, "task", open); got != 1 {
		t.Errorf("open task evidence rows = %d, want 1", got)
	}
	if got := e.evidenceRows(t, "task", abandoned); got != 0 {
		t.Errorf("abandoned task evidence rows = %d, want 0", got)
	}
}

// TestCatalogWebhookMetrics: a routed delivery counts one webhook event under
// its validated state and one evidence row under (state, entity_kind).
func TestCatalogWebhookMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	e := newCatalogEnvWith(t, catalogTestSecret, m)
	e.seedDeliverable(t, "casualties", catalogArtifact)

	if rr := catalogDeliver(t, e.h, "d-m1", catalogBody(catalogArtifact, "published")); rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr := catalogDeliver(t, e.h, "d-m2", catalogBody("gs://nobody/declares-this", "failed")); rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	for _, tc := range []struct {
		event, result string
	}{
		{"published", "ok"},
		{"failed", "unrouted"},
	} {
		if got := testutil.ToFloat64(m.Events().WithLabelValues("catalog", tc.event, tc.result)); got != 1 {
			t.Errorf("events{catalog,%s,%s} = %v, want 1", tc.event, tc.result, got)
		}
	}
	if got := testutil.ToFloat64(m.CatalogEvidence().WithLabelValues("published", "deliverable")); got != 1 {
		t.Errorf("catalog_evidence{published,deliverable} = %v, want 1", got)
	}
}
