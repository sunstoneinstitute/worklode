package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestEventRetentionStartsOnlyWithArchive: the retention sweep runs only
// beside the archive subscriber, so with no OTLP endpoint its instruments
// are never registered and every event is kept (WL-REQ-1895).
func TestEventRetentionStartsOnlyWithArchive(t *testing.T) {
	for _, tc := range []struct {
		name string
		exp  sdklog.Exporter
		want bool
	}{
		{"no endpoint", nil, false},
		{"endpoint", nopExporter{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			reg := prometheus.NewRegistry()
			rules := []store.RetentionRule{{Pattern: "github:*", TTL: time.Hour}}
			if _, _, err := api.NewServer(st, api.Config{BackgroundCtx: ctx, ArchiveExporter: tc.exp, Metrics: reg, EventRetention: rules}); err != nil {
				t.Fatalf("new server: %v", err)
			}
			mfs, err := reg.Gather()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, mf := range mfs {
				for _, m := range mf.GetMetric() {
					for _, l := range m.GetLabel() {
						if l.GetName() == "pattern" && l.GetValue() == "github:*" {
							got[mf.GetName()] = true
						}
					}
				}
				if mf.GetName() == "worklode_events_retention_errors_total" {
					got[mf.GetName()] = true
				}
			}
			for _, name := range []string{"worklode_events_retention_deleted_total", "worklode_events_retention_errors_total"} {
				if got[name] != tc.want {
					t.Errorf("%s registered = %v, want %v", name, got[name], tc.want)
				}
			}
		})
	}
}
