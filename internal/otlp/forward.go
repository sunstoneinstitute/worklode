package otlp

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"
)

// queueSize bounds the in-memory forward queue (spec 071 §3): gateway
// latency never delays the agent's export, and a full queue drops rather
// than blocks.
const queueSize = 256

type payload struct {
	contentType string
	body        []byte
}

// Forwarder relays stored OTLP log batches to the cluster otel-gateway
// (spec 071 §3). A nil *Forwarder means forwarding is off: Enqueue returns
// false without blocking, Run returns at once.
//
// ponytail: no disk spool — a full queue or a dead process drops in-flight
// batches. Edge Agent already spools durably; add a spool here if loss
// becomes a problem worth the complexity.
type Forwarder struct {
	Upstream string
	Token    string

	// Client posts each payload; overridable so tests can point it at
	// httptest, or a caller can tune transport settings. Defaults to a
	// 10s-timeout client (spec 071 §3).
	Client *http.Client

	metrics *Metrics
	queue   chan payload
}

// NewForwarder builds a Forwarder posting to upstream with token. m may be
// nil.
func NewForwarder(upstream, token string, m *Metrics) *Forwarder {
	return &Forwarder{
		Upstream: upstream,
		Token:    token,
		Client:   &http.Client{Timeout: 10 * time.Second},
		metrics:  m,
		queue:    make(chan payload, queueSize),
	}
}

// Enqueue queues body for forwarding, non-blocking. It returns false — and
// counts queue_dropped — when the queue is full or f is nil.
func (f *Forwarder) Enqueue(contentType string, body []byte) bool {
	if f == nil {
		return false
	}
	select {
	case f.queue <- payload{contentType: contentType, body: body}:
		return true
	default:
		f.metrics.QueueDropped()
		return false
	}
}

// Run drains the queue, posting one payload at a time, until ctx is done.
// A nil *Forwarder returns at once.
func (f *Forwarder) Run(ctx context.Context) {
	if f == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-f.queue:
			f.post(ctx, p)
		}
	}
}

func (f *Forwarder) post(ctx context.Context, p payload) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Upstream+"/v1/logs", bytes.NewReader(p.body))
	if err != nil {
		slog.Warn("otlp forward: build request", "error", err)
		f.metrics.Forward("network")
		return
	}
	req.Header.Set("Content-Type", p.contentType)
	req.Header.Set("Authorization", "Bearer "+f.Token)

	resp, err := f.Client.Do(req)
	if err != nil {
		slog.Warn("otlp forward: request failed", "upstream", f.Upstream, "error", err)
		f.metrics.Forward("network")
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode < 300:
		f.metrics.Forward("ok")
	case resp.StatusCode < 500:
		f.metrics.Forward("client_error")
	default:
		slog.Warn("otlp forward: upstream error", "upstream", f.Upstream, "status", resp.StatusCode)
		f.metrics.Forward("server_error")
	}
}
