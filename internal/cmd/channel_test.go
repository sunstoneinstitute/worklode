package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// readLines polls buf until it holds at least want newline-terminated
// lines, or fails the test after timeout. The poll loop under test runs on
// its own goroutine, so there is no other signal to block on.
func readLines(t *testing.T, buf *syncBuffer, want int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := strings.TrimRight(buf.String(), "\n")
		var lines []string
		if s != "" {
			lines = strings.Split(s, "\n")
		}
		if len(lines) >= want {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d line(s), got %d: %q", want, len(lines), buf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestChannelHandshakeAndUnknownMethod drives the request/response loop over
// an io.Pipe and checks the three load-bearing protocol details from the
// task brief: initialize echoes the caller's protocolVersion verbatim (never
// a hardcoded one) and declares the claude/channel experimental capability,
// notifications/initialized gets no reply, tools/list replies with an empty
// list, and every other method — including server/discover — is rejected
// with JSON-RPC error -32601 rather than answered.
func TestChannelHandshakeAndUnknownMethod(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.InstructionsResponse{})
	}))
	defer ts.Close()
	c := cli.NewClient(cli.Config{ServerURL: ts.URL})

	stdinR, stdinW := io.Pipe()
	var out, stderr syncBuffer

	done := make(chan error, 1)
	go func() {
		done <- runChannel(context.Background(), c, stdinR, &out, &stderr, time.Hour)
	}()

	const protocolVersion = "weird-2099-01-01" // deliberately not a real one, to prove it's echoed, not hardcoded
	for _, req := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + protocolVersion + `"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"server/discover"}`,
	} {
		if _, err := stdinW.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("write request: %v", err)
		}
	}

	lines := readLines(t, &out, 3, 2*time.Second)
	stdinW.Close()
	if err := <-done; err != nil {
		t.Fatalf("runChannel: %v", err)
	}

	var initResp, toolsResp, discoverResp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("decode initialize response: %v (%q)", err, lines[0])
	}
	if err := json.Unmarshal([]byte(lines[1]), &toolsResp); err != nil {
		t.Fatalf("decode tools/list response: %v (%q)", err, lines[1])
	}
	if err := json.Unmarshal([]byte(lines[2]), &discoverResp); err != nil {
		t.Fatalf("decode server/discover response: %v (%q)", err, lines[2])
	}

	if id, _ := initResp["id"].(float64); id != 1 {
		t.Errorf("initialize response id = %v, want 1", initResp["id"])
	}
	result, _ := initResp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize response has no result: %v", initResp)
	}
	if got := result["protocolVersion"]; got != protocolVersion {
		t.Errorf("protocolVersion = %v, want %q (must be echoed verbatim, not hardcoded)", got, protocolVersion)
	}
	caps, _ := result["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatalf("initialize result has no capabilities: %v", result)
	}
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities.tools missing: %v", caps)
	}
	experimental, _ := caps["experimental"].(map[string]any)
	if _, ok := experimental["claude/channel"]; !ok {
		t.Errorf("capabilities.experimental[\"claude/channel\"] missing: %v", caps)
	}

	if id, _ := toolsResp["id"].(float64); id != 2 {
		t.Errorf("tools/list response id = %v, want 2", toolsResp["id"])
	}
	toolsResult, _ := toolsResp["result"].(map[string]any)
	tools, ok := toolsResult["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Errorf("tools/list result = %v, want {\"tools\":[]}", toolsResp["result"])
	}

	if id, _ := discoverResp["id"].(float64); id != 3 {
		t.Errorf("server/discover response id = %v, want 3", discoverResp["id"])
	}
	if _, hasResult := discoverResp["result"]; hasResult {
		t.Errorf("server/discover must not be answered with a result, got: %v", discoverResp)
	}
	rpcErr, _ := discoverResp["error"].(map[string]any)
	if rpcErr == nil {
		t.Fatalf("server/discover response has no error: %v", discoverResp)
	}
	if code, _ := rpcErr["code"].(float64); code != -32601 {
		t.Errorf("server/discover error code = %v, want -32601", rpcErr["code"])
	}
}

// TestChannelDeliversClaimedInstruction stubs the claim step with an
// httptest server (this package's usual way of faking the client's HTTP
// side, no real network or Postgres involved) that returns one instruction
// on its first call and none after, and checks the poll loop turns that
// into exactly one well-formed notifications/claude/channel line with
// meta keys the [A-Za-z0-9_]+ pattern Claude Code requires.
func TestChannelDeliversClaimedInstruction(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&calls, 1) == 1 {
			json.NewEncoder(w).Encode(model.InstructionsResponse{Instructions: []model.Instruction{
				{ID: 42, Task: "WL-7", Body: "steer this way", CreatedBy: "stig"},
			}})
			return
		}
		json.NewEncoder(w).Encode(model.InstructionsResponse{})
	}))
	defer ts.Close()
	c := cli.NewClient(cli.Config{ServerURL: ts.URL})

	stdinR, stdinW := io.Pipe()
	var out, stderr syncBuffer

	done := make(chan error, 1)
	go func() {
		done <- runChannel(context.Background(), c, stdinR, &out, &stderr, 5*time.Millisecond)
	}()

	readLines(t, &out, 1, 2*time.Second)
	stdinW.Close()
	if err := <-done; err != nil {
		t.Fatalf("runChannel: %v", err)
	}
	// runChannel only returns after its poll goroutine has stopped, so the
	// buffer is final here: no later tick can still be in flight.

	got := strings.TrimRight(out.String(), "\n")
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 notification line, got %d: %q", len(lines), got)
	}

	var msg map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &msg); err != nil {
		t.Fatalf("decode notification: %v (%q)", err, lines[0])
	}
	if msg["method"] != "notifications/claude/channel" {
		t.Errorf("method = %v, want notifications/claude/channel", msg["method"])
	}
	if _, hasID := msg["id"]; hasID {
		t.Errorf("notification must not carry an id: %v", msg)
	}
	params, _ := msg["params"].(map[string]any)
	if params["content"] != "steer this way" {
		t.Errorf("content = %v, want %q", params["content"], "steer this way")
	}
	meta, _ := params["meta"].(map[string]any)
	want := map[string]string{"task": "WL-7", "instruction_id": "42", "from": "stig"}
	for k, v := range want {
		if got := meta[k]; got != v {
			t.Errorf("meta[%q] = %v, want %q", k, got, v)
		}
	}
	metaKey := regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	for k := range meta {
		if !metaKey.MatchString(k) {
			t.Errorf("meta key %q does not match [A-Za-z0-9_]+ — Claude Code would silently drop it", k)
		}
	}
}
