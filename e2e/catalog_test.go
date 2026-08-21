//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// catalogArtifact is the address the deliverable declares and the catalog
// reports about. The two must match byte for byte — that exactness is part of
// the hook's contract, so spelling it once here would hide a mismatch bug.
const catalogArtifact = "bigquery://sunstone-prod/cow/casualties"

// TestCatalogEvidenceRoundTrip drives the third ingest path end to end through
// public surfaces only: declare a deliverable with an artifact address over
// the JSON API, deliver a signed catalog webhook naming that address, and read
// the reported state back off the deliverable.
//
// The two other ingest paths correlate a delivery to work by repo and branch;
// this one routes by declaration lookup, and nothing below tells the server
// which deliverable the delivery concerns. That the fact lands anyway is the
// whole claim being tested.
func TestCatalogEvidenceRoundTrip(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:       bootstrapToken,
		GitHubWebhookSecret:  githubSecret,
		FluxWebhookSecret:    fluxSecret,
		CatalogWebhookSecret: catalogSecret,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "cow", Name: "Cost of War", Key: "COW",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Two deliverables: one declaring the address the catalog will report on,
	// one declaring another. The second is the negative control — routing must
	// pick by declared address, not by "some deliverable in this project".
	declared := createDeliverable(t, srv.URL, "Casualties dataset", catalogArtifact)
	other := createDeliverable(t, srv.URL, "Methodology note", "gs://sunstone-prod/cow/method")

	if got := deliverableByID(t, srv.URL, "cow", declared)["reported_state"]; got != "" {
		t.Fatalf("reported_state before any delivery = %v, want empty", got)
	}

	// A delivery for an address nobody declares is recorded, not rejected.
	if got := deliverCatalog(t, srv.URL, "e2e-unrouted",
		"iceberg://sunstone-prod/nobody/declares-this", "published"); got != "unrouted" {
		t.Fatalf("undeclared address acked %q, want unrouted", got)
	}

	if got := deliverCatalog(t, srv.URL, "e2e-published", catalogArtifact, "published"); got != "ok" {
		t.Fatalf("first delivery acked %q, want ok", got)
	}
	// Redelivery of the same delivery id must not file a second fact.
	if got := deliverCatalog(t, srv.URL, "e2e-published", catalogArtifact, "published"); got != "duplicate" {
		t.Fatalf("redelivery acked %q, want duplicate", got)
	}

	row := deliverableByID(t, srv.URL, "cow", declared)
	if row["artifact"] != catalogArtifact {
		t.Errorf("artifact = %v, want %s", row["artifact"], catalogArtifact)
	}
	if row["reported_state"] != "published" {
		t.Errorf("reported_state = %v, want published", row["reported_state"])
	}
	if row["reported_at"] == nil {
		t.Error("reported_at is null, want the delivery's timestamp")
	}
	if got := deliverableByID(t, srv.URL, "cow", other)["reported_state"]; got != "" {
		t.Errorf("deliverable declaring another address = %v, want no reported state", got)
	}

	// A later fact about the same address supersedes the earlier one: state is
	// what was most recently reported, never something a human set.
	if got := deliverCatalog(t, srv.URL, "e2e-deprecated", catalogArtifact, "deprecated"); got != "ok" {
		t.Fatalf("second delivery acked %q, want ok", got)
	}
	if got := deliverableByID(t, srv.URL, "cow", declared)["reported_state"]; got != "deprecated" {
		t.Errorf("reported_state after the second report = %v, want deprecated", got)
	}
}

// createDeliverable declares one deliverable over the JSON API and returns its
// id.
func createDeliverable(t *testing.T, baseURL, name, artifact string) string {
	t.Helper()
	body := mustJSON(t, map[string]string{"name": name, "artifact": artifact})
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/v1/projects/cow/deliverables", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build deliverable request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bootstrapToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create deliverable %s: %v", name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create deliverable %s: status = %d, body %s", name, resp.StatusCode, respBody)
	}
	var d map[string]any
	if err := json.Unmarshal(respBody, &d); err != nil {
		t.Fatalf("decode deliverable %s: %v (body %s)", name, err, respBody)
	}
	id, _ := d["id"].(string)
	if id == "" {
		t.Fatalf("create deliverable %s returned no id: %s", name, respBody)
	}
	return id
}

// deliverableByID lists a project's deliverables and returns the one with this
// id, as decoded JSON so the test asserts on the wire shape rather than on a
// Go struct the server and the test could drift on together.
func deliverableByID(t *testing.T, baseURL, project, id string) map[string]any {
	t.Helper()
	status, body := getAuthed(t, baseURL+"/api/v1/projects/"+project+"/deliverables", bootstrapToken)
	if status != http.StatusOK {
		t.Fatalf("list deliverables: status = %d, body %s", status, body)
	}
	var out struct {
		Deliverables []map[string]any `json:"deliverables"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode deliverables: %v (body %s)", err, body)
	}
	for _, d := range out.Deliverables {
		if d["id"] == id {
			return d
		}
	}
	t.Fatalf("deliverable %s not in %s", id, body)
	return nil
}

// deliverCatalog signs and posts one catalog delivery, returning the ack
// status. Unlike postSigned it does not require "ok": which of ok, duplicate
// and unrouted comes back is exactly what these assertions are about.
func deliverCatalog(t *testing.T, baseURL, deliveryID, artifact, state string) string {
	t.Helper()
	body := mustJSON(t, map[string]string{"artifact": artifact, "state": state})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/hooks/catalog", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build catalog request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sign(catalogSecret, body))
	req.Header.Set("X-Catalog-Delivery", deliveryID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deliver catalog event %s: %v", deliveryID, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("catalog delivery %s: status = %d, body %s", deliveryID, resp.StatusCode, respBody)
	}
	var m map[string]string
	if err := json.Unmarshal(respBody, &m); err != nil {
		t.Fatalf("decode catalog ack: %v (body %s)", err, respBody)
	}
	return m["status"]
}
