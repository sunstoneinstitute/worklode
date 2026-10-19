//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

type mcpBearerRT string

func (b mcpBearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// mcpDetail decodes a tool result's second content block (WL-RULE-1819).
func mcpDetail(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	if len(res.Content) < 2 {
		t.Fatalf("result has no detail block: %+v", res.Content)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Content[1].(*sdk.TextContent).Text), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestMCPProjectTools drives /mcp over HTTP with a minted token: tools list,
// project_use handle and confirm_project paths, project_resolve ranking, and
// the tool-call counter on /metrics (WL-REQ-1822).
func TestMCPProjectTools(t *testing.T) {
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, admin, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		SessionSecret:  "e2e-mcp-secret",
		PublicURL:      "http://lode.test",
		Metrics:        reg,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(main)
	defer srv.Close()

	root := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	for _, p := range []model.CreateProjectInput{
		{ID: "worklode", Name: "Worklode", Key: "WL"},
		{ID: "rollout", Name: "Rollout", Key: "RO"},
	} {
		if _, _, err := root.CreateProject(ctx, p); err != nil {
			t.Fatalf("create project %s: %v", p.Key, err)
		}
	}
	if _, _, err := root.CreateActor(ctx, model.CreateActorInput{ID: "mcp-user", Kind: "human", DisplayName: "MCP User"}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := root.CreateToken(ctx, "mcp-user", "e2e mcp", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// No elicitation handler: the client cannot answer a form.
	c := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil)
	cs, err := c.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerRT(tok.Token)}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	call := func(tool string, args map[string]any) *sdk.CallToolResult {
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return res
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, want := range []string{"project_list", "project_resolve", "project_use"} {
		if !have[want] {
			t.Errorf("tool %s not listed; have %v", want, have)
		}
	}

	res := call("project_use", map[string]any{"project": "WL", "confirmed": true})
	if res.IsError {
		t.Fatalf("confirmed project_use is an error: %+v", res.Content)
	}
	if h, _ := mcpDetail(t, res)["project"].(string); !strings.HasPrefix(h, "WL.") {
		t.Fatalf("handle = %q, want WL.<...>", h)
	}

	// The bare key, unconfirmed, on a client without elicitation.
	res = call("project_use", map[string]any{"project": "WL"})
	d := mcpDetail(t, res)
	if _, ok := d["project"]; !res.IsError || ok {
		t.Fatalf("bare key: IsError=%v detail=%v, want confirm_project with no handle", res.IsError, d)
	}
	if cands, _ := d["candidates"].([]any); len(cands) == 0 || cands[0].(map[string]any)["key"] != "WL" {
		t.Fatalf("bare key candidates = %v", d["candidates"])
	}

	res = call("project_resolve", map[string]any{"spoken": "work load"})
	if cands, _ := mcpDetail(t, res)["candidates"].([]any); len(cands) == 0 || cands[0].(map[string]any)["key"] != "WL" {
		t.Fatalf("project_resolve candidates = %v, want WL first", mcpDetail(t, res)["candidates"])
	}

	body := scrapeMetrics(t, admin)
	if !strings.Contains(body, "worklode_mcp_tool_calls_total") {
		t.Fatalf("/metrics missing worklode_mcp_tool_calls_total:\n%s", body)
	}
}
