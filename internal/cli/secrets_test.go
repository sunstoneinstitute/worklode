package cli_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func TestSecretsCatalogClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/secrets/catalog" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"secrets":[{"name":"GITHUB_TOKEN","ref":"op://Employee/GitHub agent token/credential","description":"gh","baseline":true}]}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	resp, _, err := c.SecretsCatalog(context.Background())
	if err != nil {
		t.Fatalf("SecretsCatalog: %v", err)
	}
	if len(resp.Secrets) != 1 || resp.Secrets[0].Name != "GITHUB_TOKEN" ||
		!resp.Secrets[0].Baseline || resp.Secrets[0].Ref == "" {
		t.Fatalf("catalog = %+v", resp.Secrets)
	}
}

func TestRecordSecretsMaterialized(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	err := c.RecordSecretsMaterialized(context.Background(), "WL-7",
		[]string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV"})
	if err != nil {
		t.Fatalf("RecordSecretsMaterialized: %v", err)
	}
	if gotPath != "/api/v1/tasks/WL-7/secrets-materialized" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"KUBECONFIG_HZDEV"`) || strings.Contains(gotBody, "op://") {
		t.Fatalf("body = %q; want names only", gotBody)
	}
}
