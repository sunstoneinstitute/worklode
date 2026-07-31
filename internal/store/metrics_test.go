package store

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestWithMetricsRegisters asserts WithMetrics registers the store's
// instruments: the DB pool collector and the active-lease collector.
func TestWithMetricsRegisters(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := OpenTestStore(t, WithMetrics(reg))
	if s.metrics == nil {
		t.Fatal("WithMetrics did not set s.metrics")
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}
	joined := strings.Join(names, "\n")
	for _, want := range []string{"go_sql_open_connections", "worklode_leases_active"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("registry missing %s; got:\n%s", want, joined)
		}
	}
}

// TestClaimOutcomeMapping asserts the sentinel-error → label mapping.
func TestClaimOutcomeMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{ErrLeased, "leased"},
		{ErrBlocked, "blocked"},
		{ErrNotFound, "not_found"},
		{ErrBadTransition, "error"},
	} {
		if got := claimOutcome(tc.err); got != tc.want {
			t.Fatalf("claimOutcome(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestStoreMetricsNilSafe asserts a store opened without WithMetrics (nil
// storeMetrics) records nothing and does not panic.
func TestStoreMetricsNilSafe(t *testing.T) {
	var m *storeMetrics
	m.claim("claim", "ok")
	m.renew("ok")
	m.release("ok")
	m.expire(3)
}

// keep testutil imported before Task 2 fills in behavioral assertions
var _ = testutil.ToFloat64
