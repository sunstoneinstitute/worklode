package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestListProjectionFailures covers GET /api/v1/graph/projection/failures:
// the quarantine rows an operator can otherwise only reach with psql, and
// that any authenticated actor may read them (permProjectionRead).
func TestListProjectionFailures(t *testing.T) {
	st, h, token := newTestServer(t)
	ctx := context.Background()

	// Empty is an empty array, never null: a client rendering a table should
	// not have to distinguish "none" from "no field".
	rr := doReq(t, h, "GET", "/api/v1/graph/projection/failures", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list failures status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got, ok := decodeMap(t, rr)["failures"].([]any); !ok || len(got) != 0 {
		t.Fatalf("failures on a healthy instance = %v, want []", decodeMap(t, rr)["failures"])
	}

	t0 := time.Now().UTC().Truncate(time.Second)
	if err := st.RecordProjectionFailure(ctx, model.ProjectionFailure{
		Project: "alpha", Attempts: 3,
		FirstFailedAt: t0.Add(-time.Hour), LastFailedAt: t0,
		NextAttemptAt: t0.Add(2 * time.Minute),
		LastError:     "put graph: 503",
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	rr = doReq(t, h, "GET", "/api/v1/graph/projection/failures", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list failures status = %d, body %s", rr.Code, rr.Body.String())
	}
	failures, ok := decodeMap(t, rr)["failures"].([]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("failures = %v, want one row", decodeMap(t, rr)["failures"])
	}
	row := failures[0].(map[string]any)
	if row["project"] != "alpha" {
		t.Errorf("project = %v, want alpha", row["project"])
	}
	if row["attempts"] != float64(3) {
		t.Errorf("attempts = %v, want 3", row["attempts"])
	}
	if row["last_error"] != "put graph: 503" {
		t.Errorf("last_error = %v, want the recorded error", row["last_error"])
	}
	for _, k := range []string{"first_failed_at", "last_failed_at", "next_attempt_at"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row missing field %q: %v", k, row)
		}
	}
}
