// Package archive is the `archive` event-bus subscriber (WL-REQ-1894): it
// sends every event in the log, in offset order, as one OpenTelemetry log
// record over OTLP/HTTP, so the event archive holds every event after the
// retention sweep (WL-REQ-1895) trims the Postgres working set.
package archive

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Subscriber is the event_subscribers row the archive consumes under. The
// retention sweep reads its acked offset, so it is an operator-facing name.
const Subscriber = "archive"

// EventName is the record's event name; the ClickHouse materialized view
// selects archive rows from otel_logs by it.
const EventName = "worklode.event"

// Enabled reports whether an OTLP logs endpoint is configured. With none the
// subscriber is not started (WL-REQ-1894).
func Enabled(getenv func(string) string) bool {
	return getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") != "" || getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

// NewExporter builds the OTLP/HTTP log exporter from the standard
// OTEL_EXPORTER_OTLP_* environment variables.
func NewExporter(ctx context.Context) (sdklog.Exporter, error) {
	return otlploghttp.New(ctx)
}

// Archiver turns events into log records and exports them one at a time.
// It is called from one eventbus loop goroutine, which the subscriber lock
// makes the only consumer, so it holds no lock of its own.
type Archiver struct {
	exp     sdklog.Exporter
	metrics *Metrics
	logger  log.Logger
	capture *capture
}

// New returns an Archiver exporting through exp. m may be nil.
//
// The SDK builds the record (resource, scope, attribute limits) through a
// LoggerProvider whose only processor captures it; Handle then calls
// exp.Export itself, so an export error reaches the loop instead of being
// swallowed by a processor.
func New(exp sdklog.Exporter, m *Metrics) *Archiver {
	c := &capture{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(c))
	return &Archiver{
		exp:     exp,
		metrics: m,
		logger:  lp.Logger("github.com/sunstoneinstitute/worklode/internal/archive"),
		capture: c,
	}
}

// Handle is the eventbus.Handler. An export error stops the batch, so the
// loop acks only what was exported and retries this event on its next poll.
func (a *Archiver) Handle(ctx context.Context, ev store.Event) (eventbus.Outcome, error) {
	var r log.Record
	r.SetEventName(EventName)
	r.SetTimestamp(ev.ReceivedAt)
	r.SetBody(log.StringValue(string(ev.Payload)))
	r.AddAttributes(
		log.Int64("worklode.event.id", ev.ID),
		log.String("worklode.event.source", ev.Source),
		log.String("worklode.event.type", ev.Type),
		log.String("worklode.event.external_id", ev.ExternalID),
		log.String("worklode.event.received_at", ev.ReceivedAt.UTC().Format(time.RFC3339Nano)),
	)
	a.logger.Emit(ctx, r)
	if err := a.exp.Export(ctx, []sdklog.Record{a.capture.rec}); err != nil {
		a.metrics.exportError()
		return eventbus.OutcomeApplied, fmt.Errorf("archive: export event %d: %w", ev.ID, err)
	}
	return eventbus.OutcomeApplied, nil
}

// Shutdown shuts the exporter down.
func (a *Archiver) Shutdown(ctx context.Context) error { return a.exp.Shutdown(ctx) }

// capture keeps a copy of the last record the SDK emitted.
type capture struct{ rec sdklog.Record }

func (c *capture) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (c *capture) OnEmit(_ context.Context, r *sdklog.Record) error       { c.rec = r.Clone(); return nil }
func (c *capture) Shutdown(context.Context) error                         { return nil }
func (c *capture) ForceFlush(context.Context) error                       { return nil }
