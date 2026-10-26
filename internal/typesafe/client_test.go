package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAsk(t *testing.T) {
	var got struct {
		Model     string              `json:"model"`
		State     map[string]any      `json:"state"`
		Questions map[string]Question `json:"questions"`
	}
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"r0":{"type":"choice","choice":"needs","confidence":0.8,
			"probabilities":{"needs":0.9,"references":0.1}}},"usage":{"input_tokens":10,"output_tokens":2}}`))
	}))
	defer srv.Close()

	m := NewMetrics(prometheus.NewRegistry())
	c := &Client{BaseURL: srv.URL + "/", APIKey: "k", Metrics: m}
	q := map[string]Question{"r0": {Type: "choice", Instructions: "which?", Criteria: map[string]string{"needs": "n", "references": "r"}}}
	ans, err := c.Ask(context.Background(), "jev-latest", map[string]any{"line": "x"}, q)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" || path != "/v1/systemone" || got.Model != "jev-latest" || got.State["line"] != "x" ||
		got.Questions["r0"].Type != "choice" || got.Questions["r0"].Criteria["needs"] != "n" {
		t.Errorf("request: auth %q path %q body %+v", auth, path, got)
	}
	a := ans["r0"]
	if a.Choice != "needs" || a.Confidence != 0.8 || a.Probabilities["references"] != 0.1 {
		t.Errorf("answer %+v", a)
	}
	if v := testutil.ToFloat64(m.requests.WithLabelValues("ok")); v != 1 {
		t.Errorf("ok count %v", v)
	}
	if n := testutil.CollectAndCount(m.duration); n != 1 {
		t.Errorf("duration series %d", n)
	}
}

func TestAskErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer missing" {
			w.Write([]byte(`{"model":"m","answers":{"other":{"choice":"a"}},"usage":{}}`))
			return
		}
		http.Error(w, `{"detail":[{"loc":["body","questions"],"msg":"bad","type":"x"}]}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	m := NewMetrics(prometheus.NewRegistry())
	q := map[string]Question{"r0": {Type: "choice", Instructions: "i", Criteria: map[string]string{"a": "a"}}}

	if _, err := (&Client{BaseURL: srv.URL, APIKey: "k", Metrics: m}).Ask(context.Background(), "m", "s", q); err == nil {
		t.Error("422 returned no error")
	}
	if _, err := (&Client{BaseURL: srv.URL, APIKey: "missing", Metrics: m}).Ask(context.Background(), "m", "s", q); err == nil {
		t.Error("a missing answer returned no error")
	}
	if _, err := (&Client{BaseURL: srv.URL, Metrics: m}).Ask(context.Background(), "m", "s", q); !errors.Is(err, ErrUnconfigured) {
		t.Errorf("no key: %v", err)
	}
	for outcome, want := range map[string]float64{"ok": 0, "error": 2, "unconfigured": 1} {
		if v := testutil.ToFloat64(m.requests.WithLabelValues(outcome)); v != want {
			t.Errorf("%s count %v, want %v", outcome, v, want)
		}
	}
	// A nil Metrics records nothing and does not panic.
	if _, err := (&Client{}).Ask(context.Background(), "m", "s", q); !errors.Is(err, ErrUnconfigured) {
		t.Errorf("nil metrics: %v", err)
	}
}
