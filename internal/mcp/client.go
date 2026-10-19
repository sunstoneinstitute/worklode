package mcp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

var errNoBearer = errors.New("mcp: request carries no bearer token")

// bearer returns the request's bearer token, or "" when it has none.
func bearer(h http.Header) string {
	token, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// clientFor builds the cli.Client a tool call uses: it calls the API handler
// in-process with the caller's own bearer token (WL-RULE-1823).
func (s *server) clientFor(h http.Header) (*cli.Client, error) {
	token := bearer(h)
	if token == "" {
		return nil, errNoBearer
	}
	return cli.NewClientWithTransport(cli.Config{ServerURL: s.cfg.PublicURL, Token: token},
		handlerTransport{s.cfg.API}), nil
}

// handlerTransport is an http.RoundTripper that serves each request with h
// in the same process. Responses are buffered whole, which suits the JSON
// API; a streaming endpoint would not finish.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req = req.Clone(req.Context())
		req.Body = http.NoBody
	}
	rec := &recorder{header: http.Header{}}
	t.h.ServeHTTP(rec, req)
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	return &http.Response{
		Status:        http.StatusText(rec.code),
		StatusCode:    rec.code,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.header,
		Body:          io.NopCloser(&rec.body),
		ContentLength: int64(rec.body.Len()),
		Request:       req,
	}, nil
}

// recorder is the minimal http.ResponseWriter handlerTransport needs.
// net/http/httptest's recorder would do, but importing httptest registers a
// command-line flag in the server binary.
type recorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}
