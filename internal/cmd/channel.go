package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newChannelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel",
		Short: "The stdio MCP channel Claude Code spawns to receive steering instructions",
	}
	cmd.AddCommand(newChannelServeCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newChannelCmd()) }

func newChannelServeCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the stdio JSON-RPC channel that delivers steering instructions as MCP notifications",
		Long: `serve speaks just enough of the legacy MCP stdio protocol to stay a
"channel": it answers initialize (echoing the caller's protocolVersion
verbatim, which is what keeps Claude Code's unsolicited-notification path
open), notifications/initialized (no reply — it's a notification), and
tools/list, and rejects every other method, including server/discover, with
JSON-RPC error -32601. Answering server/discover at all would negotiate
Claude Code into a newer protocol era that closes off channel delivery, so
it must stay unimplemented.

Every --interval, it polls for steering instructions queued against tasks
this actor's token currently leases and delivers each as an unsolicited
notifications/claude/channel notification. Addressing is actor-scoped, so
serve takes no task or worktree argument.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runChannel(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), interval)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 3*time.Second, "poll interval for claiming pending steering instructions")
	return cmd
}

// runChannel drives the stdio JSON-RPC loop until in reaches EOF or ctx is
// canceled: one JSON-RPC message per input line, one JSON object per output
// line. The poll goroutine and the request/response loop share writeLine
// (mutex-guarded) as the only path to out, so a claimed-instruction
// notification can never land mid-write with a response. c is a *cli.Client
// in production; channel_test.go points it at an httptest.Server instead of
// a real worklode server, the same fake-the-HTTP-side pattern the rest of
// this package's tests use.
//
// The poll goroutine waits on ready before claiming anything: an MCP client
// is expected to ignore any server-initiated message that arrives before its
// initialize response completes, but a claimed steering instruction is
// stamped delivered_at in the same claim query, so an early notification
// would be a permanently lost instruction, not a delayed one. markReady
// closes ready right after handleChannelRequest writes the initialize
// response.
func runChannel(ctx context.Context, c *cli.Client, in io.Reader, out io.Writer, stderr io.Writer, interval time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)

	var mu sync.Mutex
	writeLine := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			fmt.Fprintf(stderr, "channel: encode: %v\n", err)
			return
		}
		b = append(b, '\n')
		mu.Lock()
		defer mu.Unlock()
		out.Write(b)
	}

	ready := make(chan struct{})
	var readyOnce sync.Once
	markReady := func() { readyOnce.Do(func() { close(ready) }) }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ready:
		case <-ctx.Done():
			return
		}
		pollInstructions(ctx, c, interval, writeLine, stderr)
	}()

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		handleChannelRequest(line, writeLine, stderr, markReady)
	}
	// Stop the poll goroutine and wait for it before returning, so no write
	// to out can land after the caller stops reading from it.
	cancel()
	wg.Wait()
	return scanner.Err()
}

// jsonrpcCapabilities is the fixed shape of an initialize response's
// capabilities: the empty tools object plus the claude/channel experimental
// flag Claude Code checks to keep this process addressable as a channel.
type jsonrpcCapabilities struct {
	Tools        map[string]any `json:"tools"`
	Experimental map[string]any `json:"experimental"`
}

type jsonrpcServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type jsonrpcInitializeResult struct {
	ProtocolVersion string              `json:"protocolVersion"`
	Capabilities    jsonrpcCapabilities `json:"capabilities"`
	ServerInfo      jsonrpcServerInfo   `json:"serverInfo"`
}

type jsonrpcToolsListResult struct {
	Tools []any `json:"tools"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// handleChannelRequest decodes one input line and writes the reply, if any,
// through writeLine. A malformed line (not even valid JSON, or missing
// "method") is logged to stderr and dropped — there is no id to reply
// against safely. markReady is called once the initialize response has been
// written, unblocking the poll goroutine.
func handleChannelRequest(line []byte, writeLine func(any), stderr io.Writer, markReady func()) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		fmt.Fprintf(stderr, "channel: malformed request: %v\n", err)
		return
	}
	var method string
	if err := json.Unmarshal(raw["method"], &method); err != nil || method == "" {
		fmt.Fprintf(stderr, "channel: request has no method: %s\n", line)
		return
	}
	id, hasID := raw["id"]

	switch method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(raw["params"], &params)
		writeLine(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: jsonrpcInitializeResult{
				// Echoed verbatim, never hardcoded: this is what keeps
				// Claude Code's unsolicited-notification delivery path
				// open. See the package doc comment on runChannel.
				ProtocolVersion: params.ProtocolVersion,
				Capabilities: jsonrpcCapabilities{
					Tools:        map[string]any{},
					Experimental: map[string]any{"claude/channel": map[string]any{}},
				},
				ServerInfo: jsonrpcServerInfo{Name: "lode-channel", Version: buildinfo.Version},
			},
		})
		markReady()
	case "notifications/initialized":
		// A notification: no id, no reply.
	case "tools/list":
		writeLine(jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: jsonrpcToolsListResult{Tools: []any{}}})
	default:
		// Every other method, including server/discover, is deliberately
		// left unimplemented: answering it at all is what would negotiate
		// Claude Code into a newer protocol era that closes off channel
		// delivery. A request without an id is itself a notification —
		// there's nothing to reply to.
		if !hasID {
			return
		}
		writeLine(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &jsonrpcError{Code: -32601, Message: "method not found: " + method},
		})
	}
}

// channelNotification is one unsolicited notifications/claude/channel line.
type channelNotification struct {
	JSONRPC string                    `json:"jsonrpc"`
	Method  string                    `json:"method"`
	Params  channelNotificationParams `json:"params"`
}

type channelNotificationParams struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta"`
}

// pollInstructions claims pending steering instructions every interval and
// delivers each as a notification. A claim error is logged to stderr and
// otherwise ignored: the instruction row is still held server-side, so
// nothing is lost, and the next tick tries again.
func pollInstructions(ctx context.Context, c *cli.Client, interval time.Duration, writeLine func(any), stderr io.Writer) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		resp, _, err := c.ClaimInstructions(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(stderr, "channel: claim instructions: %v\n", err)
			continue
		}
		for _, ins := range resp.Instructions {
			// Meta keys must match [A-Za-z0-9_]+ — Claude Code silently
			// drops any that don't (e.g. instruction_id, not
			// instruction-id).
			writeLine(channelNotification{
				JSONRPC: "2.0",
				Method:  "notifications/claude/channel",
				Params: channelNotificationParams{
					Content: ins.Body,
					Meta: map[string]string{
						"task":           ins.Task,
						"instruction_id": strconv.FormatInt(ins.ID, 10),
						"from":           ins.CreatedBy,
					},
				},
			})
		}
	}
}
