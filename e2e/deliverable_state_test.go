//go:build e2e

package e2e

// deliverable_state_test.go drives spec 029 §3.2 end to end: the three ways a
// deliverable's state is reported, and the provenance split that keeps them
// apart. Public surfaces only — the prober binary is unit-tested in
// internal/probe, so what e2e owes is its wire contract, exercised the way the
// prober calls it.
//
// The claim under test is that a deliverable stores no state of its own. Every
// assertion below reads the state back off the project's Deliverables page,
// because the page is where a person decides whether the project has shipped,
// and the page is the surface that must never let a claim pass for a verified
// fact.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	pipelineSecret = "e2e-pipeline-secret"
	cmsSecret      = "e2e-cms-secret"

	// methodologyArtifact is the address deliverable B declares, and the one
	// the prober and the CMS both report about. Spelled once and compared byte
	// for byte, the way the ingest compares it.
	methodologyArtifact = "gs://sunstone-prod/cow/methodology.pdf"

	// userReportedMark is the row's provenance marker as the page renders it.
	// The page's closing paragraph explains the split in prose and uses the
	// same words, so a bare substring search counts that too.
	userReportedMark = `<div class="def muted">User-reported</div>`
)

func TestDeliverableState(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:        bootstrapToken,
		GitHubWebhookSecret:   githubSecret,
		FluxWebhookSecret:     fluxSecret,
		CatalogWebhookSecret:  catalogSecret,
		PipelineWebhookSecret: pipelineSecret,
		CMSWebhookSecret:      cmsSecret,
		// The Deliverables page is the surface under test and this instance
		// has no login provider, so it is served unauthenticated the way the
		// other page-reading e2e tests do it.
		WebOpen: true,
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

	// The three declaration forms 029 §3.1 allows: by label, by address, and
	// neither. Each is reported about by a different surface below.
	a := declareDeliverable(t, srv.URL, "cow", model.CreateDeliverableInput{
		Name: "Casualties dashboard", Label: true,
	})
	b := declareDeliverable(t, srv.URL, "cow", model.CreateDeliverableInput{
		Name: "Methodology note", Artifact: methodologyArtifact,
	})
	c := declareDeliverable(t, srv.URL, "cow", model.CreateDeliverableInput{
		Name: "Field guide",
	})

	aLabel, _ := a["label"].(string)
	if aLabel == "" {
		t.Fatalf("label declaration minted no selector: %v", a)
	}
	// The hook routes a delivery's labels by rendering each pair "k=v", which
	// is the whole stored selector. The delivery carries the two halves
	// separately, so split what was minted rather than rebuilding it.
	labelKey, labelValue, ok := strings.Cut(aLabel, "=")
	if !ok {
		t.Fatalf("minted selector %q is not k=v", aLabel)
	}

	// Nothing has reported, so every row says Declared rather than dressing a
	// declaration up as a status.
	for _, d := range []map[string]any{a, b, c} {
		row := deliverableRow(t, srv.URL, id(t, d))
		assertChip(t, row, id(t, d), "Declared")
		assertNotUserReported(t, row, id(t, d))
	}

	// 1. Observed, by label. A build-time emitter names the selector, not the
	// deliverable, and the fact still lands on A.
	if got := deliverPipeline(t, srv.URL, "e2e-pipeline-1", map[string]any{
		"event":  "dashboard.published",
		"labels": map[string]string{labelKey: labelValue},
		"state":  "published",
	}); got != "ok" {
		t.Fatalf("pipeline delivery acked %q, want ok", got)
	}
	rowA := deliverableRow(t, srv.URL, id(t, a))
	assertChip(t, rowA, id(t, a), "Published")
	assertNotUserReported(t, rowA, id(t, a))

	// 2. Observed, by the prober's bearer API. The dedupe key is the event's
	// external id, so a replayed sweep files nothing twice.
	if got := postArtifactReport(t, srv.URL, model.ArtifactReportInput{
		Artifact: methodologyArtifact, State: "published", DedupeKey: "e2e-probe-1",
	}); got != "ok" {
		t.Fatalf("artifact report acked %q, want ok", got)
	}
	if got := postArtifactReport(t, srv.URL, model.ArtifactReportInput{
		Artifact: methodologyArtifact, State: "published", DedupeKey: "e2e-probe-1",
	}); got != "duplicate" {
		t.Fatalf("replayed artifact report acked %q, want duplicate", got)
	}
	rowB := deliverableRow(t, srv.URL, id(t, b))
	assertChip(t, rowB, id(t, b), "Published")
	assertNotUserReported(t, rowB, id(t, b))

	// 3. User-reported. C declares no address at all, so nothing will ever
	// observe it — and the page says in words that a person claimed this.
	postDeliverableReport(t, srv.URL, id(t, c), model.ReportDeliverableInput{
		State: "published", Note: "checked the shared drive by hand",
	})
	rowC := deliverableRow(t, srv.URL, id(t, c))
	assertChip(t, rowC, id(t, c), "Published")
	if !strings.Contains(rowC, userReportedMark) {
		t.Errorf("deliverable %s was reported by a person but its row does not say so:\n%s", id(t, c), rowC)
	}

	// 4. The CMS ingest carries the person who published and the person who
	// approved. Without them the publish fact is exactly the invisible
	// sign-off 029 exists to remove, so it is refused before anything is
	// recorded (029 §8.3).
	body := mustJSON(t, map[string]any{
		"artifact": methodologyArtifact, "state": "updated",
	})
	if status, _ := postCMS(t, srv.URL, "e2e-cms-unsigned-off", body); status != http.StatusBadRequest {
		t.Errorf("CMS delivery without published_by: status = %d, want 400", status)
	}
	if got := deliverableRow(t, srv.URL, id(t, b)); !strings.Contains(got, "Published") {
		t.Errorf("the refused CMS delivery changed %s anyway:\n%s", id(t, b), got)
	}

	body = mustJSON(t, map[string]any{
		"artifact": methodologyArtifact, "state": "updated",
		"published_by": "ed@sunstone.example", "approved_by": "jo@sunstone.example",
	})
	status, ack := postCMS(t, srv.URL, "e2e-cms-signed-off", body)
	if status != http.StatusOK {
		t.Fatalf("CMS delivery with both person fields: status = %d, body %s", status, ack)
	}
	if got := ackStatus(t, ack); got != "ok" {
		t.Fatalf("CMS delivery acked %q, want ok", got)
	}
	// The evidence landed: the newest report about B's address is the CMS one,
	// and it is still an observed fact rather than a person's claim.
	rowB = deliverableRow(t, srv.URL, id(t, b))
	assertChip(t, rowB, id(t, b), "Updated")
	assertNotUserReported(t, rowB, id(t, b))

	// The provenance split survives everything above: only C carries it.
	_, page := getPage(t, srv.URL+"/projects/cow/deliverables")
	if n := strings.Count(page, userReportedMark); n != 1 {
		t.Errorf("Deliverables page says User-reported %d times, want 1 (deliverable %s only)", n, id(t, c))
	}
}

// declareDeliverable declares one deliverable over the JSON API and returns the
// created row as decoded JSON, so the test reads the wire shape rather than a
// Go struct the server and the test could drift on together.
func declareDeliverable(t *testing.T, baseURL, project string, in model.CreateDeliverableInput) map[string]any {
	t.Helper()
	body := mustJSON(t, in)
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/v1/projects/"+project+"/deliverables", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build deliverable request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bootstrapToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("declare deliverable %s: %v", in.Name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("declare deliverable %s: status = %d, body %s", in.Name, resp.StatusCode, respBody)
	}
	var d map[string]any
	if err := json.Unmarshal(respBody, &d); err != nil {
		t.Fatalf("decode deliverable %s: %v (body %s)", in.Name, err, respBody)
	}
	if _, ok := d["id"].(string); !ok {
		t.Fatalf("declare deliverable %s returned no id: %s", in.Name, respBody)
	}
	return d
}

// id reads a decoded deliverable's id.
func id(t *testing.T, d map[string]any) string {
	t.Helper()
	s, _ := d["id"].(string)
	if s == "" {
		t.Fatalf("deliverable has no id: %v", d)
	}
	return s
}

// deliverableRow fetches the project's Deliverables page and returns the one
// row naming this deliverable. Slicing the row out is what makes an assertion
// about a deliverable rather than about the page: three rows are on it, and
// "the page contains Published" would pass for any of them.
func deliverableRow(t *testing.T, baseURL, deliverableID string) string {
	t.Helper()
	status, page := getPage(t, baseURL+"/projects/cow/deliverables")
	if status != http.StatusOK {
		t.Fatalf("GET the Deliverables page: status = %d", status)
	}
	const rowOpen = `<div class="dodrow">`
	at := strings.Index(page, deliverableID)
	if at < 0 {
		t.Fatalf("deliverable %s is not on the Deliverables page", deliverableID)
	}
	start := strings.LastIndex(page[:at], rowOpen)
	if start < 0 {
		t.Fatalf("deliverable %s appears outside any row on the Deliverables page", deliverableID)
	}
	// The last row ends at the list, not at the next row — and what follows
	// the list is a paragraph explaining the provenance split, which names
	// every word these assertions look for.
	end := len(page)
	if i := strings.Index(page[start+len(rowOpen):], rowOpen); i >= 0 {
		end = start + len(rowOpen) + i
	}
	if i := strings.Index(page[start:], "</section>"); i >= 0 && start+i < end {
		end = start + i
	}
	return page[start:end]
}

// assertChip holds that a row's state chip reads exactly this. The chip is the
// one word a reader takes the project's status from.
func assertChip(t *testing.T, row, deliverableID, want string) {
	t.Helper()
	if !strings.Contains(row, ">"+want+"</span>") {
		t.Errorf("deliverable %s: state chip is not %q:\n%s", deliverableID, want, row)
	}
}

// assertNotUserReported holds the other half of the provenance split: an
// observed fact must not be marked as a person's claim.
func assertNotUserReported(t *testing.T, row, deliverableID string) {
	t.Helper()
	if strings.Contains(row, userReportedMark) {
		t.Errorf("deliverable %s is marked User-reported but nothing was reported by a person:\n%s", deliverableID, row)
	}
}

// deliverPipeline signs and posts one pipeline delivery, returning the ack
// status.
func deliverPipeline(t *testing.T, baseURL, deliveryID string, payload any) string {
	t.Helper()
	body := mustJSON(t, payload)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/hooks/pipeline", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build pipeline request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sign(pipelineSecret, body))
	req.Header.Set("X-Pipeline-Delivery", deliveryID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deliver pipeline event %s: %v", deliveryID, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pipeline delivery %s: status = %d, body %s", deliveryID, resp.StatusCode, respBody)
	}
	return ackStatus(t, string(respBody))
}

// postCMS signs and posts one CMS delivery, returning the status code and the
// raw body — both halves matter here, since the refusal path is a 400 and the
// accepted path is an ack.
func postCMS(t *testing.T, baseURL, deliveryID string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/hooks/cms", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build cms request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sign(cmsSecret, body))
	req.Header.Set("X-CMS-Delivery", deliveryID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deliver cms event %s: %v", deliveryID, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

// postArtifactReport posts one prober report over the bearer API and returns
// the ack status.
func postArtifactReport(t *testing.T, baseURL string, in model.ArtifactReportInput) string {
	t.Helper()
	body := mustJSON(t, in)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/artifact-reports", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build artifact report request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bootstrapToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post artifact report %s: %v", in.DedupeKey, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact report %s: status = %d, body %s", in.DedupeKey, resp.StatusCode, respBody)
	}
	return ackStatus(t, string(respBody))
}

// postDeliverableReport files one person's report over the bearer API.
func postDeliverableReport(t *testing.T, baseURL, deliverableID string, in model.ReportDeliverableInput) {
	t.Helper()
	body := mustJSON(t, in)
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/v1/deliverables/"+deliverableID+"/report", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build deliverable report request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bootstrapToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("report deliverable %s: %v", deliverableID, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report deliverable %s: status = %d, body %s", deliverableID, resp.StatusCode, respBody)
	}
}

// ackStatus reads the "status" field out of an ingest ack.
func ackStatus(t *testing.T, body string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode ack: %v (body %s)", err, body)
	}
	s, _ := m["status"].(string)
	return s
}
