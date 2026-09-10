package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedProjectWithKey creates a project whose id is key lowercased, so a test
// can pass the project key it wants deliverable ids stamped with directly
// (createProject in tasks_test.go derives the key from the id the other way
// around).
func seedProjectWithKey(t *testing.T, st *store.Store, key string) string {
	t.Helper()
	id := strings.ToLower(key)
	if err := st.CreateProject(context.Background(), id, id, key); err != nil {
		t.Fatalf("create project %s: %v", id, err)
	}
	return id
}

// seedAddressDeliverable creates a deliverable declaring artifact as its
// address (029 §3.1) — CreateDeliverable declares it as a side effect of a
// non-empty Artifact — and returns the deliverable id.
func seedAddressDeliverable(t *testing.T, st *store.Store, projectID, name, artifact string) string {
	t.Helper()
	var id string
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		d, err := store.CreateDeliverable(tx, st.Now(), store.DeliverableInput{
			ProjectID: projectID, Name: name, Artifact: artifact,
		})
		if err != nil {
			return err
		}
		id = d.ID
		return nil
	}); err != nil {
		t.Fatalf("seed address deliverable %s: %v", artifact, err)
	}
	return id
}

// seedLabelDeliverable creates a deliverable declaring a worklode.deliverable
// label instead of an address, and returns its id. Used to pin that a label
// declaration never surfaces as a probe target (029 §3.2): its address is
// minted at build time and reaches worklode by push, not poll.
func seedLabelDeliverable(t *testing.T, st *store.Store, projectID, name string) string {
	t.Helper()
	var id string
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		d, err := store.CreateDeliverable(tx, st.Now(), store.DeliverableInput{
			ProjectID: projectID, Name: name, Label: true,
		})
		if err != nil {
			return err
		}
		id = d.ID
		return nil
	}); err != nil {
		t.Fatalf("seed label deliverable: %v", err)
	}
	return id
}

// queryString runs query (which must select exactly one text-like column)
// through the store's own transaction helper, since Store.db is unexported
// and this is an external test package.
func queryString(t *testing.T, st *store.Store, query string, args ...any) string {
	t.Helper()
	var got string
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(query, args...).Scan(&got)
	}); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return got
}

func artifactReportBody(artifact string) map[string]any {
	return map[string]any{
		"artifact":    artifact,
		"state":       "published",
		"version":     "v1",
		"url":         "https://example.com/report",
		"dedupe_key":  "probe-1",
		"occurred_at": "2026-07-19T12:00:00Z",
	}
}

// TestArtifactReportFilesObservedEvidence pins the happy path: a probe
// result for a declared address files evidence with Source "prober" and
// Provenance "observed" (029 §3.2), against the entity that declared it.
func TestArtifactReportFilesObservedEvidence(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projectID := seedProjectWithKey(t, st, "COW")
	deliverableID := seedAddressDeliverable(t, st, projectID, "Datapackage", "https://data.example/cow.zip")

	rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, artifactReportBody("https://data.example/cow.zip"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["status"]; got != "ok" {
		t.Fatalf("status field = %v, want ok", got)
	}

	source := queryString(t, st,
		`SELECT source FROM artifact_evidence WHERE entity_kind = 'deliverable' AND entity_id = $1`, deliverableID)
	if source != "prober" {
		t.Fatalf("evidence source = %q, want prober", source)
	}
	provenance := queryString(t, st,
		`SELECT provenance FROM artifact_evidence WHERE entity_kind = 'deliverable' AND entity_id = $1`, deliverableID)
	if provenance != "observed" {
		t.Fatalf("evidence provenance = %q, want observed", provenance)
	}
	state := queryString(t, st,
		`SELECT state FROM artifact_evidence WHERE entity_kind = 'deliverable' AND entity_id = $1`, deliverableID)
	if state != "published" {
		t.Fatalf("evidence state = %q, want published", state)
	}
}

// TestProbeTargetsListsAddressNotLabel pins the routing boundary: an address
// declaration is a probe target, a label declaration is not — its address is
// minted at build time and reaches worklode by push (029 §3.2).
func TestProbeTargetsListsAddressNotLabel(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projectID := seedProjectWithKey(t, st, "COW")
	seedAddressDeliverable(t, st, projectID, "Datapackage", "https://data.example/cow.zip")
	seedLabelDeliverable(t, st, projectID, "Report")

	rr := doReq(t, h, "GET", "/api/v1/probe-targets", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Artifacts []string `json:"artifacts"`
	}
	decodeInto(t, rr, &got)
	if len(got.Artifacts) != 1 || got.Artifacts[0] != "https://data.example/cow.zip" {
		t.Fatalf("artifacts = %+v, want just the declared address", got.Artifacts)
	}
}

// TestArtifactReportDuplicateDedupeKey pins the redelivery contract: the same
// dedupe_key acks "duplicate" and writes no second evidence row.
func TestArtifactReportDuplicateDedupeKey(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projectID := seedProjectWithKey(t, st, "COW")
	deliverableID := seedAddressDeliverable(t, st, projectID, "Datapackage", "https://data.example/cow.zip")

	body := artifactReportBody("https://data.example/cow.zip")
	rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, body)
	if rr.Code != http.StatusOK || decodeMap(t, rr)["status"] != "ok" {
		t.Fatalf("first report status = %d body %s, want 200/ok", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/artifact-reports", token, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["status"]; got != "duplicate" {
		t.Fatalf("redelivery status field = %v, want duplicate", got)
	}

	count := queryString(t, st,
		`SELECT COUNT(*)::text FROM artifact_evidence WHERE entity_kind = 'deliverable' AND entity_id = $1`, deliverableID)
	if count != "1" {
		t.Fatalf("evidence rows = %s, want 1", count)
	}
}

// TestArtifactReportUnroutedAddress pins the miss case: an address nobody
// declares acks "unrouted", files no evidence, and (per RecordEvent, which
// never sets applied_at) stays a replay candidate for when the declaration
// arrives.
func TestArtifactReportUnroutedAddress(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)

	rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, artifactReportBody("https://data.example/nobody-declares-this.zip"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["status"]; got != "unrouted" {
		t.Fatalf("status field = %v, want unrouted", got)
	}

	count := queryString(t, st, `SELECT COUNT(*)::text FROM artifact_evidence`)
	if count != "0" {
		t.Fatalf("evidence rows = %s, want 0", count)
	}
}

// TestArtifactReportsRequireAuth pins that both prober routes are bearer-token
// guarded, not public.
func TestArtifactReportsRequireAuth(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)

	rr := doReq(t, h, "POST", "/api/v1/artifact-reports", "", artifactReportBody("https://data.example/cow.zip"))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("POST status = %d, want 401", rr.Code)
	}

	rr = doReq(t, h, "GET", "/api/v1/probe-targets", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET status = %d, want 401", rr.Code)
	}
}

// TestArtifactReportsValidation pins the two 422s: an unknown state and a
// blank dedupe_key, matching the runtime-events endpoint's shape.
func TestArtifactReportsValidation(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)

	t.Run("bad state", func(t *testing.T) {
		body := artifactReportBody("https://data.example/cow.zip")
		body["state"] = "bogus"
		rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing dedupe_key", func(t *testing.T) {
		body := artifactReportBody("https://data.example/cow.zip")
		body["dedupe_key"] = ""
		rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
		}
	})
}

// TestArtifactReportsMetric pins worklode_probe_reports_total: it carries the
// state and result of a report, visible on the admin /metrics endpoint.
func TestArtifactReportsMetric(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	projectID := seedProjectWithKey(t, st, "COW")
	seedAddressDeliverable(t, st, projectID, "Datapackage", "https://data.example/cow.zip")

	rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, artifactReportBody("https://data.example/cow.zip"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil)
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", metrics.Code)
	}
	body := metrics.Body.String()
	want := `worklode_probe_reports_total{result="ok",state="published"} 1`
	if !strings.Contains(body, want) {
		t.Fatalf("metrics body missing %q:\n%s", want, body)
	}
}
