// Package probe polls declared artifact addresses and reports observed
// state to the worklode server (spec 029 §3.2). It runs as lode-watch
// -mode artifacts: its own deployment, holding whatever read credentials
// the addresses need, so the server's blast radius stays the database it
// owns. It verifies existence and change, never a human claim — probing as
// verification of reported state is 007 v2 and deliberately absent.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// defaultInterval is Run's full-sweep period when Options.Interval is zero.
const defaultInterval = 15 * time.Minute

// httpTimeout bounds one checker call so a hanging origin cannot stall a
// sweep forever.
const httpTimeout = 10 * time.Second

// Options configures Run and SweepOnce.
type Options struct {
	Server   string        // worklode base URL
	Token    string        // bearer token (LODE_TOKEN)
	Interval time.Duration // full-sweep period, default 15m
	Log      *slog.Logger  // optional, defaults to slog.Default()
}

// checker probes one address and reports what it found. ok is false when
// the result is inconclusive (5xx, timeout) — not evidence of state, so the
// caller reports nothing for it.
type checker func(ctx context.Context, address string) (state, fingerprint string, ok bool)

// checkers is keyed by URL scheme. v1 registers http/https only; gs:// and
// bigquery:// arrive here later.
var checkers = map[string]checker{
	"http":  httpCheck,
	"https": httpCheck,
}

var httpClient = &http.Client{Timeout: httpTimeout}

// httpCheck HEADs address, falling back to GET on 405/501 (some origins
// don't implement HEAD). 2xx is published, fingerprinted by ETag, then
// Last-Modified, then the status code; 404/410 is removed. Anything else —
// including a transport error or timeout — is inconclusive.
func httpCheck(ctx context.Context, address string) (state, fingerprint string, ok bool) {
	resp, err := doHTTPRequest(ctx, http.MethodHead, address)
	if err == nil && (resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented) {
		resp.Body.Close()
		resp, err = doHTTPRequest(ctx, http.MethodGet, address)
	}
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		fp := resp.Header.Get("ETag")
		if fp == "" {
			fp = resp.Header.Get("Last-Modified")
		}
		if fp == "" {
			fp = strconv.Itoa(resp.StatusCode)
		}
		return "published", fp, true
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return "removed", strconv.Itoa(resp.StatusCode), true
	default:
		return "", "", false
	}
}

func doHTTPRequest(ctx context.Context, method, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, err
	}
	return httpClient.Do(req)
}

// client talks to the worklode API: fetching probe targets and posting
// artifact reports, mirroring internal/watch's HTTPReporter.
type client struct {
	serverURL string
	token     string
	http      *http.Client
}

func newClient(serverURL, token string) *client {
	return &client{
		serverURL: strings.TrimRight(serverURL, "/"),
		token:     token,
		http:      &http.Client{Timeout: httpTimeout},
	}
}

func (c *client) probeTargets(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/api/v1/probe-targets", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get probe targets: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("get probe targets: server returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var out model.ProbeTargetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode probe targets: %w", err)
	}
	return out.Artifacts, nil
}

func (c *client) reportArtifact(ctx context.Context, in model.ArtifactReportInput) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal artifact report: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.serverURL+"/api/v1/artifact-reports", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("post artifact report: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("post artifact report: server returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// SweepOnce runs one full sweep: GET /api/v1/probe-targets, check each
// address with the checker registered for its scheme, POST one
// artifact-report per conclusive finding. An unsupported scheme is logged
// once per sweep and skipped.
func SweepOnce(ctx context.Context, opts Options) error {
	if opts.Server == "" {
		return errors.New("--server (or LODE_SERVER) is required")
	}
	if opts.Token == "" {
		return errors.New("--token (or LODE_TOKEN) is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	c := newClient(opts.Server, opts.Token)
	targets, err := c.probeTargets(ctx)
	if err != nil {
		return err
	}

	warned := map[string]bool{}
	for _, address := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}

		scheme := ""
		if u, err := url.Parse(address); err == nil {
			scheme = u.Scheme
		}
		check, supported := checkers[scheme]
		if !supported {
			if !warned[scheme] {
				log.Warn("unsupported probe scheme", "scheme", scheme, "address", address)
				warned[scheme] = true
			}
			continue
		}

		state, fingerprint, ok := check(ctx, address)
		if !ok {
			continue
		}

		in := model.ArtifactReportInput{
			Artifact:   address,
			State:      state,
			URL:        address,
			DedupeKey:  "probe:" + address + ":" + state + ":" + fingerprint,
			OccurredAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := c.reportArtifact(ctx, in); err != nil {
			log.Error("report artifact state", "address", address, "err", err)
		}
	}
	return nil
}

// Run sweeps immediately, then again every Options.Interval (default 15m)
// until ctx ends. A failed sweep is logged and does not stop the loop —
// the next tick retries, and the server's dedupe makes repeats safe.
func Run(ctx context.Context, opts Options) error {
	if opts.Server == "" {
		return errors.New("--server (or LODE_SERVER) is required")
	}
	if opts.Token == "" {
		return errors.New("--token (or LODE_TOKEN) is required")
	}
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	sweep := func() {
		if err := SweepOnce(ctx, opts); err != nil {
			log.Error("probe sweep failed", "err", err)
		}
	}

	log.Info("starting prober", "server", opts.Server, "interval", opts.Interval)
	sweep()
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}
