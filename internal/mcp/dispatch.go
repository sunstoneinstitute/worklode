package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// toolFunc is a tool body. It calls the API through c (WL-RULE-1823). A
// returned error becomes an error result; *toolError picks the metric
// outcome, a *cli.ClientError maps from its status, anything else is "error".
type toolFunc func(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error)

// toolError reports a tool failure that did not come from an API status,
// such as project_required or confirm_project from the handle check.
type toolError struct {
	outcome string
	msg     string
}

func (e *toolError) Error() string { return e.msg }

// statusOutcome maps an API status to a tool outcome.
func statusOutcome(status int) string {
	switch {
	case status >= 200 && status < 300:
		return outcomeOK
	case status == http.StatusBadRequest, status == http.StatusUnprocessableEntity:
		return outcomeInvalid
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return outcomeRefused
	case status == http.StatusNotFound:
		return outcomeNotFound
	case status == http.StatusConflict:
		return outcomeConflict
	}
	return outcomeError
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// tool wraps fn with the caller's in-process client, panic recovery and the
// outcome counter (WL-REQ-1825).
func (s *server) tool(name string, fn toolFunc) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, _ error) {
		outcome := outcomeOK
		defer func() {
			if p := recover(); p != nil {
				outcome = outcomeError
				res = errorResult("The tool failed unexpectedly.")
			}
			s.cfg.Metrics.toolCall(name, outcome)
		}()
		var h http.Header
		if extra := req.GetExtra(); extra != nil {
			h = extra.Header
		}
		c, err := s.clientFor(h)
		if err == nil {
			res, err = fn(ctx, c, req)
		}
		if err == nil {
			return res, nil
		}
		var oe *toolError
		var ce *cli.ClientError
		switch {
		case errors.Is(err, errInvalidCursor):
			outcome = outcomeInvalid
		case errors.As(err, &oe):
			outcome = oe.outcome
		case errors.As(err, &ce):
			outcome = statusOutcome(ce.Status)
			err = fmt.Errorf("%s", ce.Msg)
		default:
			outcome = outcomeError
		}
		return errorResult(err.Error()), nil
	}
}
