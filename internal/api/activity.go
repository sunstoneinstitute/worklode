package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/otlp"
)

// maxOTLPBody caps an OTLP log batch at 4 MiB (spec 071 §1).
const maxOTLPBody = 4 << 20

// ingestOTLPLogs handles POST /otlp/v1/logs: an OTLP/JSON
// ExportLogsServiceRequest from a coding agent's log exporter (spec 071 §1).
// Attributed records are stored as task activity, the raw body is queued for
// the otel-gateway (§3), and the answer is an empty
// ExportLogsServiceResponse.
//
// The status codes are the exporter's retry contract: 400 on an undecodable
// body means "do not resend", 500 on a store failure means "resend".
func (s *server) ingestOTLPLogs(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		s.otlpMetrics.Ingest("unsupported_media")
		writeErr(w, http.StatusUnsupportedMediaType,
			"OTLP logs must be application/json (OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/json)")
		return
	}

	// Read the whole body rather than decode from the stream: the forwarder
	// needs the original bytes. io.ReadAll returns a buffer nothing else
	// holds, so it can be handed to Enqueue without copying.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOTLPBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.otlpMetrics.Ingest("too_large")
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		s.otlpMetrics.Ingest("bad_request")
		writeErr(w, http.StatusBadRequest, "read request body: "+err.Error())
		return
	}

	records, err := otlp.DecodeLogs(body)
	if err != nil {
		s.otlpMetrics.Ingest("bad_request")
		writeErr(w, http.StatusBadRequest, "invalid OTLP/JSON body: "+err.Error())
		return
	}

	sub := subjectFrom(r)
	rows := make([]model.TaskActivity, 0, len(records))
	unattributed := 0
	for _, rec := range records {
		if rec.Task == "" {
			unattributed++
			continue
		}
		// A task-scoped token speaks for its own task only (001 §2.1). The
		// whole batch is refused, since a mixed batch means the exporter is
		// stamping records it has no claim on.
		if sub.TaskID != "" && rec.Task != sub.TaskID {
			s.otlpMetrics.Ingest("forbidden")
			writeErr(w, http.StatusForbidden, "token is scoped to task "+sub.TaskID)
			return
		}
		rows = append(rows, model.TaskActivity{
			Task:    rec.Task,
			Actor:   sub.ActorID,
			Agent:   activityAgent(rec.Service),
			Session: rec.Session,
			At:      rec.At,
			Event:   rec.Event,
			Attrs:   rec.Attrs,
		})
	}

	stored, err := s.st.AppendTaskActivity(r.Context(), rows)
	if err != nil {
		s.otlpMetrics.Ingest("store_error")
		s.mapStoreErr(w, err)
		return
	}
	s.otlpMetrics.Records("stored", stored)
	s.otlpMetrics.Records("unattributed", unattributed)

	s.otlpForward.Enqueue(contentType, body)
	s.otlpMetrics.Ingest("ok")
	writeJSON(w, http.StatusOK, struct{}{})
}

// activityAgent maps an OTLP resource service.name onto spec 012's agent
// vocabulary (spec 071 §1). An absent name is the route's own default,
// claude-code; a name worklode has no id for is recorded as other, so an
// unfamiliar harness keeps its activity instead of losing the batch.
func activityAgent(service string) string {
	switch service {
	case "", "claude", "claude-code":
		return "claude-code"
	default:
		return model.NormalizeAgent(service)
	}
}
