package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDocReferrers covers `lode doc referrers <ref>#sec-N` (025 §8.2): the
// section fragment is required, and it reaches the server as the anchor
// query parameter.
func TestDocReferrers(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"referrers":[
			{"kind":"doc","ref":"026-b","rel":"requires","title":"Referring spec"},
			{"kind":"task","ref":"WL-7","rel":"covers","title":"the claimed one"}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "doc", "referrers", "7#sec-2")
	if err != nil {
		t.Fatalf("doc referrers: %v\n%s", err, out)
	}
	if gotURL != "/api/v1/docs/7/referrers?anchor=sec-2" {
		t.Errorf("request = %s, want /api/v1/docs/7/referrers?anchor=sec-2", gotURL)
	}
	for _, want := range []string{"026-b", "requires", "Referring spec", "WL-7", "covers", "the claimed one"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestDocReferrersRequiresSection pins the fragment as required: a referrer
// is a fact about one section, so a whole-document ref is refused before any
// round trip rather than answered for the wrong thing.
func TestDocReferrersRequiresSection(t *testing.T) {
	out, err := runLode(t, "doc", "referrers", "026-b")
	if err == nil {
		t.Fatalf("doc referrers without a fragment succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "#sec-") {
		t.Errorf("error = %v, want it to name the #sec-N fragment", err)
	}
}
