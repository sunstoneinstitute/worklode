package hooks_test

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestGitHubWebhookMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	h := hooks.NewGitHubHandler(st, testSecret, nil, nil, m)

	// Unmapped repo → ignored (no project mapping exists in this store).
	body := []byte(`{"action":"opened","repository":{"full_name":"acme/unmapped"}}`)
	rr := deliverBody(t, h, "issues", "d-1", body)
	if rr.Code != 200 {
		t.Fatalf("delivery status = %d, want 200", rr.Code)
	}

	// Bad signature → rejected.
	req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "d-2")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("bad-signature status = %d, want 401", rr.Code)
	}

	// Unknown event name lands in the "other" bucket (still recorded ok/ignored).
	rr = deliverBody(t, h, "watch", "d-3", body)
	if rr.Code != 200 {
		t.Fatalf("unknown-event status = %d, want 200", rr.Code)
	}

	// Redelivering d-1 dedupes: !inserted wins over ignored, so it counts ok.
	if rr := deliverBody(t, h, "issues", "d-1", body); rr.Code != 200 {
		t.Fatalf("redelivery status = %d, want 200", rr.Code)
	}

	for _, tc := range []struct {
		event, result string
		want          float64
	}{
		{"issues", "ignored", 1},
		{"issues", "ok", 1},
		{"issues", "rejected", 1},
		{"other", "ignored", 1},
	} {
		got := testutil.ToFloat64(m.Events().WithLabelValues("github", tc.event, tc.result))
		if got != tc.want {
			t.Fatalf("events{github,%s,%s} = %v, want %v", tc.event, tc.result, got, tc.want)
		}
	}
}

func TestFluxWebhookMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	h := hooks.NewFluxHandler(st, fluxTestSecret, nil, nil, m)

	// Ignored kind.
	body := []byte(`{"involvedObject":{"kind":"GitRepository","name":"x"},"reason":"Ready"}`)
	rr := fluxDeliverBody(t, h, body)
	if rr.Code != 200 {
		t.Fatalf("flux status = %d, want 200", rr.Code)
	}
	if got := testutil.ToFloat64(m.Events().WithLabelValues("flux", "flux", "ignored")); got != 1 {
		t.Fatalf("events{flux,flux,ignored} = %v, want 1", got)
	}
}
