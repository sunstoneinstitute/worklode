package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeliverableAdd covers `lode deliverable add <project> <name>
// --artifact <uri>`: it POSTs to the project's deliverables endpoint and the
// confirmation names the minted id.
func TestDeliverableAdd(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"COW-DEL-1","name":"Datasets","artifact":"bigquery://p/d/t"}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	out, err := runLode(t, "deliverable", "add", "COW", "Datasets", "--artifact", "bigquery://p/d/t")
	if err != nil {
		t.Fatalf("deliverable add: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/projects/COW/deliverables" {
		t.Errorf("request = %s %s, want POST /api/v1/projects/COW/deliverables", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"name":"Datasets"`) || !strings.Contains(gotBody, `"artifact":"bigquery://p/d/t"`) {
		t.Errorf("request body = %q, want the name and artifact", gotBody)
	}
	if !strings.Contains(out, "COW-DEL-1") {
		t.Errorf("output %q missing minted id", out)
	}
}

// TestDeliverableAddJSON: --json prints the server's own body, unreformatted.
func TestDeliverableAddJSON(t *testing.T) {
	const body = `{"id":"COW-DEL-1","name":"Datasets","artifact":"bigquery://p/d/t"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, body)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	out, err := runLode(t, "deliverable", "add", "COW", "Datasets", "--artifact", "bigquery://p/d/t", "--json")
	if err != nil {
		t.Fatalf("deliverable add --json: %v", err)
	}
	if !strings.Contains(out, body) {
		t.Errorf("output = %q, want the raw server body", out)
	}
}

// TestDeliverableList covers `lode deliverable list <project>`: it GETs the
// project's deliverables endpoint and renders the table, including a
// declared deliverable with no reported state or artifact.
func TestDeliverableList(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"deliverables":[{"id":"COW-DEL-1","project":"COW","name":"Datasets",`+
			`"artifact":"bigquery://p/d/t","reported_state":"published",`+
			`"reported_at":"2026-09-03T10:00:00Z"},`+
			`{"id":"COW-DEL-2","project":"COW","name":"Report"}]}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	out, err := runLode(t, "deliverable", "list", "COW")
	if err != nil {
		t.Fatalf("deliverable list: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/projects/COW/deliverables" {
		t.Errorf("request = %s %s, want GET /api/v1/projects/COW/deliverables", gotMethod, gotPath)
	}
	for _, want := range []string{"COW-DEL-1", "Datasets", "published", "bigquery://p/d/t", "COW-DEL-2", "Report", "declared"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestDeliverableListJSON: --json prints the server's own body, unreformatted.
func TestDeliverableListJSON(t *testing.T) {
	const body = `{"deliverables":[{"id":"COW-DEL-1","project":"COW","name":"Datasets"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, body)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	out, err := runLode(t, "deliverable", "list", "COW", "--json")
	if err != nil {
		t.Fatalf("deliverable list --json: %v", err)
	}
	if !strings.Contains(out, body) {
		t.Errorf("output = %q, want the raw server body", out)
	}
}
