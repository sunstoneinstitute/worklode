// Package mcp serves Worklode over the Model Context Protocol at /mcp
// (WL-REQ-1822). Every tool reaches data through a cli.Client calling the API
// handler in-process with the caller's own token (WL-RULE-1823), so this
// package never imports internal/api or internal/store.
package mcp

import (
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
