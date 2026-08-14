package hooks_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	h := hooks.NewGitHubHandler(st, testSecret, nil, nil, nil, m)

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

	// Malformed JSON exits before any outcome is decided: the sentinel's
	// "error" default stands.
	rr = deliverBody(t, h, "issues", "d-4", []byte(`{not json`))
	if rr.Code != 400 {
		t.Fatalf("malformed-body status = %d, want 400", rr.Code)
	}

	for _, tc := range []struct {
		event, result string
		want          float64
	}{
		{"issues", "ignored", 1},
		{"issues", "ok", 1},
		{"issues", "rejected", 1},
		{"issues", "error", 1},
		{"other", "ignored", 1},
	} {
		got := testutil.ToFloat64(m.Events().WithLabelValues("github", tc.event, tc.result))
		if got != tc.want {
			t.Fatalf("events{github,%s,%s} = %v, want %v", tc.event, tc.result, got, tc.want)
		}
	}
}

// TestReleaseBranchResolveMetrics: outcome is recorded for every case
// resolveReleaseCommitish covers — resolved, unknown branch, resolver error,
// no App configured (skipped) — bounded to those four label values.
func TestReleaseBranchResolveMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	st := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", demoRepo); err != nil {
		t.Fatalf("add repo: %v", err)
	}

	resolve := func(_ context.Context, _, branch string) (string, error) {
		switch branch {
		case "release-1.2":
			return "9999999999999999999999999999999999999999", nil
		case "gone":
			return "", nil
		case "boom":
			return "", errors.New("api down")
		}
		t.Fatalf("unexpected branch %q", branch)
		return "", nil
	}
	h := hooks.NewGitHubHandlerWithResolver(st, testSecret, slog.Default(), nil, resolve, m)
	deliverBody(t, h, "release", "d-1", releaseBody("v1", "release-1.2")) // resolved
	deliverBody(t, h, "release", "d-2", releaseBody("v2", "gone"))        // unknown
	deliverBody(t, h, "release", "d-3", releaseBody("v3", "boom"))        // error

	noResolver := hooks.NewGitHubHandlerWithResolver(st, testSecret, slog.Default(), nil, nil, m)
	deliverBody(t, noResolver, "release", "d-4", releaseBody("v4", "release-1.2")) // skipped

	for _, tc := range []struct {
		outcome string
		want    float64
	}{
		{"resolved", 1}, {"unknown", 1}, {"error", 1}, {"skipped", 1},
	} {
		got := testutil.ToFloat64(m.BranchResolve().WithLabelValues(tc.outcome))
		if got != tc.want {
			t.Fatalf("branch_resolve{%s} = %v, want %v", tc.outcome, got, tc.want)
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
