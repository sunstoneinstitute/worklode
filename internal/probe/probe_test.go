package probe_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/probe"
)

// fakeWorklode serves GET /api/v1/probe-targets (returning targets) and
// POST /api/v1/artifact-reports, decoding the last posted report into got.
func fakeWorklode(t *testing.T, targets []string, got *model.ArtifactReportInput) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/probe-targets", func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer wl_test" {
			t.Errorf("probe-targets: Authorization = %q, want Bearer wl_test", auth)
		}
		if err := json.NewEncoder(w).Encode(model.ProbeTargetsResponse{Artifacts: targets}); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("/api/v1/artifact-reports", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Fatalf("decode artifact report: %v", err)
		}
		if err := json.NewEncoder(w).Encode(model.WebhookAck{Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	})
	return httptest.NewServer(mux)
}

// fakeWorklodeNoReports serves probe-targets and fails the test the moment
// any artifact-report is posted — used to prove a sweep reported nothing.
func fakeWorklodeNoReports(t *testing.T, targets []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/probe-targets", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(model.ProbeTargetsResponse{Artifacts: targets}); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("/api/v1/artifact-reports", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected artifact-report POST: %s", r.URL)
	})
	return httptest.NewServer(mux)
}

func TestSweepReportsPublishedWithETagFingerprint(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v7"`)
	}))
	defer origin.Close()

	var got model.ArtifactReportInput
	api := fakeWorklode(t, []string{origin.URL + "/cow.zip"}, &got)
	defer api.Close()

	if err := probe.SweepOnce(context.Background(), probe.Options{Server: api.URL, Token: "wl_test"}); err != nil {
		t.Fatal(err)
	}
	if got.State != "published" || !strings.Contains(got.DedupeKey, `"v7"`) {
		t.Errorf("reported %+v", got)
	}
}

func TestSweepReportsRemovedOn404(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer origin.Close()

	var got model.ArtifactReportInput
	api := fakeWorklode(t, []string{origin.URL + "/gone.zip"}, &got)
	defer api.Close()

	if err := probe.SweepOnce(context.Background(), probe.Options{Server: api.URL, Token: "wl_test"}); err != nil {
		t.Fatal(err)
	}
	if got.State != "removed" {
		t.Errorf("reported %+v, want state removed", got)
	}
}

func TestSweep500ReportsNothing(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer origin.Close()

	api := fakeWorklodeNoReports(t, []string{origin.URL + "/flaky.zip"})
	defer api.Close()

	if err := probe.SweepOnce(context.Background(), probe.Options{Server: api.URL, Token: "wl_test"}); err != nil {
		t.Fatal(err)
	}
}

func TestSweepSkipsUnsupportedScheme(t *testing.T) {
	api := fakeWorklodeNoReports(t, []string{"bigquery://project/dataset/table"})
	defer api.Close()

	if err := probe.SweepOnce(context.Background(), probe.Options{Server: api.URL, Token: "wl_test"}); err != nil {
		t.Fatal(err)
	}
}
