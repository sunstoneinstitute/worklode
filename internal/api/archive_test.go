package api_test

import (
	"context"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/archive"
)

type nopExporter struct{}

func (nopExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (nopExporter) Shutdown(context.Context) error                { return nil }
func (nopExporter) ForceFlush(context.Context) error              { return nil }

// TestArchiveSubscriberStartsOnlyWithExporter: with no OTLP endpoint there is
// no exporter, and the archive subscriber is not started (WL-REQ-1894).
func TestArchiveSubscriberStartsOnlyWithExporter(t *testing.T) {
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
			if _, _, err := api.NewServer(st, api.Config{BackgroundCtx: ctx, ArchiveExporter: tc.exp}); err != nil {
				t.Fatalf("new server: %v", err)
			}
			subs, err := st.EventSubscribers(t.Context())
			if err != nil {
				t.Fatalf("EventSubscribers: %v", err)
			}
			got := false
			for _, s := range subs {
				got = got || s.Name == archive.Subscriber
			}
			if got != tc.want {
				t.Fatalf("archive subscriber registered = %v, want %v", got, tc.want)
			}
		})
	}
}
