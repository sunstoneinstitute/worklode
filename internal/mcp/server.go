// Package mcp serves Worklode over the Model Context Protocol at /mcp
// (WL-REQ-1822). Every tool reaches data through a cli.Client calling the API
// handler in-process with the caller's own token (WL-RULE-1823), so this
// package never imports internal/api or internal/store.
package mcp

import (
	"context"
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

type server struct {
	cfg Config
	srv *mcp.Server
	h   http.Handler
}

// New returns the /mcp handler: Streamable HTTP in stateless mode, so no
// Mcp-Session-Id is read or written and GET answers 405. A request without
// a bearer token answers 401 before it reaches the protocol.
func New(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "worklode"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	s := &server{cfg: cfg, srv: srv}
	s.addProjectTools()
	s.addFindTools()
	s.addTaskTools()
	s.addDocTools()
	s.addRuleTools()
	s.addFileTools()
	s.addReviewTools()
	s.addDecisionTools()
	s.addCLITools()
	s.h = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true})
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if bearer(r.Header) == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.h.ServeHTTP(w, r)
}
