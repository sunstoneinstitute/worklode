package hooks_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestGitHubWebhookMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	h := hooks.NewGitHubHandler(st, testSecret, nil, nil, nil, nil, m)

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
	h := hooks.NewGitHubHandlerWithResolver(st, testSecret, slog.Default(), nil, resolve, nil, m)
	deliverBody(t, h, "release", "d-1", releaseBody("v1", "release-1.2")) // resolved
	deliverBody(t, h, "release", "d-2", releaseBody("v2", "gone"))        // unknown
	deliverBody(t, h, "release", "d-3", releaseBody("v3", "boom"))        // error

	noResolver := hooks.NewGitHubHandlerWithResolver(st, testSecret, slog.Default(), nil, nil, nil, m)
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

// TestApprovalIngestMetrics: every approval write the GitHub ingest makes is
// counted under one of the bounded action values, impact_opened included —
// seedApprovedDependent puts one governed dependent behind PR 42, so the
// first synchronize designates a new head and fans one impact review out to
// it (029 §7.1). The second synchronize absorbs into that still-open row, so
// the count stays 1.
func TestApprovalIngestMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	e := newEnvWithMetrics(t, m)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	seedApprovedDependent(t, e, "sunstoneinstitute/demo#42")

	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened.json")
	deliverOK(t, e, "pull_request_review", "d-rev-1", "pull_request_review_changes_requested.json")
	deliverOK(t, e, "pull_request", "d-appr-2", "pull_request_review_requested.json")
	deliverOK(t, e, "pull_request", "d-sync-1", "pull_request_synchronize.json")            // rebind
	deliverOK(t, e, "pull_request_review", "d-rev-2", "pull_request_review_submitted.json") // resolves the rebound row
	deliverOK(t, e, "pull_request", "d-sync-2", "pull_request_synchronize_second.json")     // candidate

	want := map[string]float64{
		"opened": 1, "resolved": 2, "reopened": 1, "rebound": 1, "candidate": 1,
		"impact_opened": 1,
	}
	for action, wantN := range want {
		if got := testutil.ToFloat64(m.ApprovalsIngest().WithLabelValues(action)); got != wantN {
			t.Errorf("approvals_ingest{%s} = %v, want %v", action, got, wantN)
		}
	}
}

// seedApprovedDependent creates a second pull request that has already been
// approved and records that it references upstream, which is what makes a
// designation of upstream raise an impact review on it (029 §7.1).
func seedApprovedDependent(t *testing.T, e *env, upstream string) {
	t.Helper()
	now := time.Now().UTC()
	_, _, err := e.st.RecordEvent(context.Background(), "cli", "seed-dependent:"+t.Name(),
		"test.seed", []byte(`{}`), func(tx *sql.Tx, _ int64) error {
			const dep = "sunstoneinstitute/demo#99"
			if _, err := store.InsertAwaitingApproval(tx, now, "pr", dep, "dep-sha", "",
				nil, nil, nil); err != nil {
				return err
			}
			a, err := store.OpenApprovalForLane(tx, "pr", dep, "")
			if err != nil {
				return err
			}
			if err := store.ResolveApproval(tx, a.ID, "approved", nil, now); err != nil {
				return err
			}
			return store.InsertGovernedRefs(tx, now, nil, "pr", dep, "dep-sha",
				[]store.GovernedRef{{Kind: "pr", ID: upstream, Revision: "old-sha"}})
		})
	if err != nil {
		t.Fatalf("seed approved dependent: %v", err)
	}
}
