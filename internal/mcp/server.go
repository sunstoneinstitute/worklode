// Package mcp serves Worklode over the Model Context Protocol at /mcp
// (WL-REQ-1822). Every tool reaches data through a cli.Client calling the API
// handler in-process with the caller's own token (WL-RULE-1823), so this
// package never imports internal/api or internal/store.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// Config is what lode-server hands the MCP server.
type Config struct {
	// API is the server's own handler; tool calls go through it in-process.
	API http.Handler
	// PublicURL is the base URL the in-process client addresses.
	PublicURL string
	// SessionSecret keys the signed project handles and page cursors.
	SessionSecret string
	Metrics       *Metrics
	Now           func() time.Time
	// CLI is the lode command tree the lode tool runs (WL-REQ-1862).
	CLI CLI
}

// CLI is the command tree as the lode tool runs it: lode-server's wiring
// passes cmd.InProcess and cmd.ResolveCommand. It is injected, not imported,
// because internal/cmd's tests import internal/api, which imports this
// package. With Run nil the lode tool answers that it is unavailable.
type CLI struct {
	// Run runs args in process with c as the API client and project as the
	// current project ("" for none).
	Run func(ctx context.Context, args []string, stdin io.Reader, c *cli.Client, project string) (stdout, stderr []byte, exitCode int, err error)
	// Command names the command args resolve to, without the root, or
	// "unknown": a bounded metric label (WL-REQ-1865).
	Command func(args []string) string
}

// toolsHeader selects the tool set per request (WL-REQ-1864); toolsCLI is its
// one valid value. Without the header /mcp lists the full set.
const (
	toolsHeader = "Worklode-Tools"
	toolsCLI    = "cli"
)

// cliTools is the set listed under Worklode-Tools: cli (WL-REQ-1864).
var cliTools = map[string]bool{
	"lode": true, "project_list": true, "project_resolve": true, "project_use": true,
}

type server struct {
	cfg   Config
	srv   *mcp.Server // the full tool set
	cliS  *mcp.Server // the cli tool set
	h     http.Handler
	known map[string]bool // every tool name registered on srv, for bounded labels
}

// New returns the /mcp handler: Streamable HTTP in stateless mode, so no
// Mcp-Session-Id is read or written and GET answers 405. A request without
// a bearer token answers 401 before it reaches the protocol.
//
// Two servers hold the two tool sets of WL-REQ-1864. The Worklode-Tools
// header picks one per request, so nothing is kept per client (WL-REQ-1822).
func New(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	newSrv := func() *mcp.Server {
		return mcp.NewServer(&mcp.Implementation{Name: "worklode"}, &mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		})
	}
	full, cliSrv := newSrv(), newSrv()
	s := &server{cfg: cfg, srv: full, cliS: cliSrv, known: map[string]bool{}}
	s.addProjectTools()
	s.addFindTools()
	s.addTaskTools()
	s.addDocTools()
	s.addRuleTools()
	s.addFileTools()
	s.addReviewTools()
	s.addDecisionTools()
	s.addCLITools()

	// The cli set registers the same handlers on its own server, so the two
	// sets cannot differ in behavior. addTool writes to s.srv, so point it
	// at the cli server for these two groups and back.
	s.srv = cliSrv
	s.addProjectTools()
	s.addCLITools()
	s.srv = full
	cliSrv.AddReceivingMiddleware(s.cliOnly)

	s.h = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if r.Header.Get(toolsHeader) == toolsCLI {
			return cliSrv
		}
		return full
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	return s
}

// cliOnly answers a call to a tool outside the cli set with an invalid error
// result, counted like any other invalid call, instead of the SDK's unknown
// tool protocol error (WL-REQ-1864). The cli server does not hold those tools.
func (s *server) cliOnly(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/call" {
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && !cliTools[p.Name] {
				s.cfg.Metrics.toolCall(s.boundedTool(p.Name), outcomeInvalid)
				return errorResult("The tool " + p.Name + " is not available with Worklode-Tools: cli. Use the lode tool."), nil
			}
		}
		return next(ctx, method, req)
	}
}

// boundedTool keeps the tool label to names the server registers.
func (s *server) boundedTool(name string) string {
	if s.known[name] {
		return name
	}
	return "unknown"
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if bearer(r.Header) == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if v := r.Header.Values(toolsHeader); len(v) > 0 && (len(v) > 1 || v[0] != toolsCLI) {
		s.badToolsHeader(w, r)
		return
	}
	s.h.ServeHTTP(w, r)
}

// badToolsHeader answers a Worklode-Tools value other than cli (WL-REQ-1864)
// with HTTP 400 and a JSON-RPC invalid request error carrying the request's
// id. It is a transport-level refusal, so every method gets it, initialize
// included, and the client sees at once that its .mcp.json is wrong instead of
// getting a tool set it did not ask for. A tools/call is also counted as an
// invalid call of its tool.
func (s *server) badToolsHeader(w http.ResponseWriter, r *http.Request) {
	var rpc struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)); err == nil {
		_ = json.Unmarshal(body, &rpc) // a batch or junk leaves the zero value
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	if rpc.Method == "tools/call" {
		s.cfg.Metrics.toolCall(s.boundedTool(rpc.Params.Name), outcomeInvalid)
	}
	id := rpc.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32600, "message": "Worklode-Tools must be cli or absent."},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(out)
}
