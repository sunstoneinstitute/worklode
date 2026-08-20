package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReconcileFlagWiring(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/reconcile" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"run_id":"r1","dry_run":true,
			"replay":{"candidates":2,"replayed":2,"still_unmapped":0},
			"poll":null,"poll_skipped":"github app auth not configured"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "reconcile", "--repo", "acme/app", "--since", "720h", "--dry-run")
	if err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if gotBody != `{"repo":"acme/app","since":"720h","dry_run":true}`+"\n" &&
		gotBody != `{"repo":"acme/app","since":"720h","dry_run":true}` {
		t.Fatalf("request body = %s; want the three flags and nothing else", gotBody)
	}
	for _, want := range []string{"would repair 2", "skipped (github app auth not configured)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReconcileRejectsRepoAndTask(t *testing.T) {
	if out, err := runLode(t, "reconcile", "--repo", "a/b", "--task", "WL-1"); err == nil {
		t.Fatalf("reconcile accepted --repo with --task:\n%s", out)
	}
}
