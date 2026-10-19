package mcp

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// AddScopedProbe registers scoped_probe on a handler from New: a stand-in for
// a part-2 project tool that runs requireProject first and answers with the
// key it got.
func AddScopedProbe(h http.Handler) {
	s := h.(*server)
	s.addTool("scoped_probe", "test", `{"type":"object"}`,
		func(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			a, err := argsOf[struct{ Project, Ref string }](req)
			if err != nil {
				return nil, err
			}
			key, err := s.requireProject(ctx, c, a.Project, a.Ref)
			if err != nil {
				return nil, err
			}
			return say("project "+key, nil), nil
		})
}
