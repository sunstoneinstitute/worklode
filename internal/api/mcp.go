// mcp.go is the MCP server at /mcp (docs/research/mcp-server-scope.md): a
// Streamable HTTP endpoint whose tools manage tasks, documents and rules from
// Claude Code, claude.ai and Cowork without the lode CLI, plus the RFC 9728
// protected-resource metadata that points an MCP client at the login.
//
// A tool never reaches the store. It calls the server's own /api/v1 handlers
// in-process through a cli.Client carrying the caller's wl_ token
// (mcpLoopback), so route guards, Decide, validation, events and metrics
// apply exactly as they do to a CLI call. The tools themselves live in the
// feature-named siblings: mcpprojects.go, mcptasks.go, mcpdocs.go,
// mcprules.go; spoken-form parsing is mcpspoken.go.
package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// mcpLoopbackURL is the base URL the in-process client forms its request
// URLs from. The loopback transport never dials it; it only has to parse.
const mcpLoopbackURL = "http://mcp.loopback"

// mcpSessionTTL is how long an idle MCP session, and the project default
// project_use set on it, lives. A client whose session expired gets a 404
// and initializes a new one, then calls project_use again.
const mcpSessionTTL = 12 * time.Hour

// mcpMaxSessions bounds the project-default table, so a client that opens
// sessions without end cannot grow it without end.
const mcpMaxSessions = 10000

// mcpPageSize is the most items one list result carries; the rest are
// reached with the result's cursor.
const mcpPageSize = 10

// mcpProjectHint ends the description of every project-scoped tool.
const mcpProjectHint = " Every call works in one Worklode project: pass project when the user's " +
	"project instructions name one (look for a line 'Worklode project: <key>'), or omit it to use " +
	"the session's project set with project_use."

// mcpInstructions is what an MCP client is told about this server at
// initialize.
const mcpInstructions = "Worklode is the work tracker: tasks, design documents (specs and plans) " +
	"and the rules specs arrange. Each call works in one Worklode project. If the user's project " +
	"instructions contain a line 'Worklode project: <key>', pass that key as project. Otherwise call " +
	"project_use once per session; when a tool answers project_required, ask the user which Worklode " +
	"project to use, then call project_use. Refs can be given as spoken (\"spec seventy-seven\", " +
	"\"task five twelve\"). Every result starts with one sentence you can read out as it stands."

// mcpToolNames are the tools the server registers, and the only values the
// tool metric label takes.
var mcpToolNames = []string{
	"project_list", "project_resolve", "project_use",
	"search", "show", "file",
	"task_list", "task_add", "task_update", "task_set_state",
	"doc_list", "doc_add", "doc_edit", "doc_transition",
	"rule_list", "rule_add", "rule_edit", "rule_link", "rule_supersede",
}

// mcpOutcomes are the outcome label's values: ok, or why the call failed.
var mcpOutcomes = []string{"ok", "invalid", "project_required", "not_found", "refused", "conflict", "error"}

// mcpMetrics counts tool calls. Nil-safe, so a server built without it
// (none is, today) still serves.
type mcpMetrics struct {
	calls *prometheus.CounterVec
}

func newMCPMetrics(reg prometheus.Registerer) *mcpMetrics {
	m := &mcpMetrics{calls: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_mcp_tool_calls_total",
		Help: "MCP tool calls at /mcp, by tool and outcome (" + strings.Join(mcpOutcomes, ", ") +
			"). Labels are bounded: the project, the actor and the arguments are deliberately not among them.",
	}, []string{"tool", "outcome"})}
	for _, t := range mcpToolNames {
		for _, o := range mcpOutcomes {
			m.calls.WithLabelValues(t, o)
		}
	}
	reg.MustRegister(m.calls)
	return m
}

func (m *mcpMetrics) call(tool, outcome string) {
	if m != nil {
		m.calls.WithLabelValues(tool, outcome).Inc()
	}
}

// mcpState is the MCP server's share of *server.
type mcpState struct {
	// api is the server's own public handler, the one the loopback
	// transport dispatches tool calls into.
	api      http.Handler
	handler  http.Handler
	metrics  *mcpMetrics
	sessions *mcpSessions
}

// registerMCP registers /mcp and the protected-resource metadata, and builds
// the MCP server whose tools call back into r's mux.
func (s *server) registerMCP(r *router, reg prometheus.Registerer) {
	s.mcp = &mcpState{
		// The mux is the one every route is registered on, including those
		// registered after this call; observe gives a loopback call the same
		// request log and metrics a network call gets.
		api:      s.observe(r.mux),
		metrics:  newMCPMetrics(reg),
		sessions: newMCPSessions(mcpSessionTTL, mcpMaxSessions),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "worklode", Title: "Worklode", Version: "1"},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	s.addMCPProjectTools(srv)
	s.addMCPTaskTools(srv)
	s.addMCPDocTools(srv)
	s.addMCPRuleTools(srv)
	s.mcp.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{SessionTimeout: mcpSessionTTL, Logger: s.log})

	for _, p := range []string{"POST /mcp", "GET /mcp", "DELETE /mcp"} {
		r.publicFunc(p, s.serveMCP)
	}
	r.publicFunc("GET /.well-known/oauth-protected-resource", s.serveMCPResourceMetadata)
	r.publicFunc("GET /.well-known/oauth-protected-resource/mcp", s.serveMCPResourceMetadata)
}

// serveMCP authenticates the bearer token itself, then hands the request to
// the SDK's Streamable HTTP handler. The TokenInfo it attaches carries the
// actor id, which the SDK holds every later request on the session to: a
// session id presented with another actor's token is refused.
func (s *server) serveMCP(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		s.mcpUnauthorized(w, r)
		return
	}
	actor, _, err := s.st.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.mcpUnauthorized(w, r)
			return
		}
		s.mapStoreErr(w, err)
		return
	}
	info := &auth.TokenInfo{UserID: actor.ID}
	verified := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) { return info, nil }
	auth.RequireBearerToken(verified, &auth.RequireBearerTokenOptions{
		// Expiry was checked by Authenticate; a wl_ token need not carry one.
		AllowMissingExpiration: true,
	})(s.mcp.handler).ServeHTTP(w, r)
}

// mcpUnauthorized answers 401 with the RFC 9728 pointer to this origin's
// protected-resource metadata, which is how an MCP client finds the login.
// An instance with no MCP origin (OIDC unconfigured) has no login to point
// at, so the challenge is bare.
func (s *server) mcpUnauthorized(w http.ResponseWriter, r *http.Request) {
	challenge := "Bearer"
	if origin, ok := s.mcpOrigin(r); ok {
		challenge += fmt.Sprintf(` resource_metadata=%q`, origin+"/.well-known/oauth-protected-resource/mcp")
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeErr(w, http.StatusUnauthorized, "unauthorized")
}

// serveMCPResourceMetadata answers GET /.well-known/oauth-protected-resource
// and its /mcp suffixed form (RFC 9728 section 3.1) for the origin the request
// arrived on. 404 when the host is not one of mcpOrigins, which includes
// every request to an instance with OIDC unconfigured.
func (s *server) serveMCPResourceMetadata(w http.ResponseWriter, r *http.Request) {
	origin, ok := s.mcpOrigin(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "no MCP login on this host")
		return
	}
	// Public discovery data, read by clients on other origins.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, model.ProtectedResourceMetadata{
		Resource:               mcpResource(origin),
		AuthorizationServers:   []string{origin},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Worklode",
	})
}

// mcpLoopback is the http.RoundTripper a tool's cli.Client sends through: it
// serves each request with the server's own handler and records the answer,
// so nothing leaves the process.
type mcpLoopback struct{ h http.Handler }

func (l mcpLoopback) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.RemoteAddr = "mcp-loopback"
	req.RequestURI = req.URL.RequestURI()
	rec := &mcpRecorder{header: http.Header{}, code: http.StatusOK}
	l.h.ServeHTTP(rec, req)
	return &http.Response{
		Status:        strconv.Itoa(rec.code) + " " + http.StatusText(rec.code),
		StatusCode:    rec.code,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.header,
		Body:          io.NopCloser(bytes.NewReader(rec.body.Bytes())),
		ContentLength: int64(rec.body.Len()),
		Request:       req,
	}, nil
}

// mcpRecorder is the minimal http.ResponseWriter mcpLoopback records into.
type mcpRecorder struct {
	header      http.Header
	code        int
	wroteHeader bool
	body        bytes.Buffer
}

func (r *mcpRecorder) Header() http.Header { return r.header }

func (r *mcpRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.code, r.wroteHeader = code, true
	}
}

func (r *mcpRecorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}

// mcpSessions holds each MCP session's default project (project_use),
// keyed on the SDK's session id and bound to the actor that set it. It is
// in memory and bounded: an entry lives mcpSessionTTL past its last use, and
// past max entries the stalest is dropped.
type mcpSessions struct {
	mu  sync.Mutex
	ttl time.Duration
	max int
	now func() time.Time
	m   map[string]mcpSessionEntry
}

type mcpSessionEntry struct {
	actor, project string
	seen           time.Time
}

func newMCPSessions(ttl time.Duration, max int) *mcpSessions {
	return &mcpSessions{ttl: ttl, max: max, now: time.Now, m: map[string]mcpSessionEntry{}}
}

// get returns the project session set for actor, or "".
func (ss *mcpSessions) get(session, actor string) string {
	if session == "" {
		return ""
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	e, ok := ss.m[session]
	now := ss.now()
	if !ok || e.actor != actor || now.Sub(e.seen) > ss.ttl {
		return ""
	}
	e.seen = now
	ss.m[session] = e
	return e.project
}

// set records project as session's default for actor.
func (ss *mcpSessions) set(session, actor, project string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := ss.now()
	if _, ok := ss.m[session]; !ok && len(ss.m) >= ss.max {
		var oldest string
		for id, e := range ss.m {
			if now.Sub(e.seen) > ss.ttl {
				delete(ss.m, id)
				continue
			}
			if oldest == "" || e.seen.Before(ss.m[oldest].seen) {
				oldest = id
			}
		}
		if len(ss.m) >= ss.max && oldest != "" {
			delete(ss.m, oldest)
		}
	}
	ss.m[session] = mcpSessionEntry{actor: actor, project: project, seen: now}
}

// mcpCall is one tool call's context: the in-process client carrying the
// caller's token, and who and which session is calling.
type mcpCall struct {
	s       *server
	ctx     context.Context
	c       *cli.Client
	actor   string
	session string

	projects []model.Project // ListProjects, read once per call
}

// mcpToolError is a failed call phrased for Claude to act on, with the
// outcome the metric counts it under.
type mcpToolError struct {
	outcome string
	text    string
}

func (e *mcpToolError) Error() string { return e.text }

func mcpInvalid(format string, args ...any) error {
	return &mcpToolError{outcome: "invalid", text: fmt.Sprintf(format, args...)}
}

// mcpHandler is a tool body: it answers the speakable text and the
// structured result, or an error the wrapper turns into an isError result.
type mcpHandler[In, Out any] func(call *mcpCall, in In) (string, Out, error)

// addMCPTool registers one tool, wrapping h with the per-call client, the
// error phrasing and the metric.
func addMCPTool[In, Out any](s *server, srv *mcp.Server, name, title, desc string, readOnly bool, h mcpHandler[In, Out]) {
	tool := &mcp.Tool{
		Name: name, Title: title, Description: desc,
		Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: readOnly, OpenWorldHint: new(bool)},
	}
	mcp.AddTool(srv, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		call := s.newMCPCall(ctx, req)
		text, out, err := h(call, in)
		if err != nil {
			outcome, msg := mcpErrorText(err)
			s.mcp.metrics.call(name, outcome)
			var zero Out
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, zero, nil
		}
		s.mcp.metrics.call(name, "ok")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	})
}

// newMCPCall builds the call context: the bearer token this very request
// carried (the SDK hands each request's headers to the tool), and the actor
// serveMCP verified it as.
func (s *server) newMCPCall(ctx context.Context, req *mcp.CallToolRequest) *mcpCall {
	call := &mcpCall{s: s, ctx: ctx}
	var token string
	if req.Extra != nil {
		token, _ = strings.CutPrefix(req.Extra.Header.Get("Authorization"), "Bearer ")
		if req.Extra.TokenInfo != nil {
			call.actor = req.Extra.TokenInfo.UserID
		}
	}
	if req.Session != nil {
		call.session = req.Session.ID()
	}
	call.c = cli.NewClientTransport(mcpLoopbackURL, token, mcpLoopback{h: s.mcp.api})
	return call
}

// mcpErrorText phrases err for Claude: what happened and what to do next.
func mcpErrorText(err error) (outcome, text string) {
	var te *mcpToolError
	if errors.As(err, &te) {
		return te.outcome, te.text
	}
	var ce *cli.ClientError
	if errors.As(err, &ce) {
		switch {
		case ce.Status == http.StatusUnauthorized:
			return "refused", "Worklode did not accept this connector's token. Ask the user to reconnect the Worklode connector."
		case ce.Status == http.StatusForbidden:
			return "refused", "Worklode refused this: " + ce.Msg + ". The token this connector uses lacks the permission; tell the user rather than retrying."
		case ce.Status == http.StatusNotFound:
			return "not_found", "Worklode found nothing for that: " + ce.Msg + ". Check the ref (search or show can find it) and try again."
		case ce.Status == http.StatusConflict:
			return "conflict", "Worklode refused the write because it conflicts with the current state: " + ce.Msg + ". Read it again with show, then retry."
		case ce.Status == http.StatusBadRequest || ce.Status == http.StatusUnprocessableEntity:
			return "invalid", "Worklode rejected the request: " + ce.Msg + ". Fix the arguments and try again."
		default:
			return "error", fmt.Sprintf("Worklode failed (%d): %s. Try again later, or tell the user.", ce.Status, ce.Msg)
		}
	}
	return "error", "Worklode failed: " + err.Error() + ". Try again later, or tell the user."
}

// mcpPage cuts one page of at most mcpPageSize items from items at cursor,
// an offset a previous page returned. next is "" on the last page.
func mcpPage[T any](items []T, cursor string) (page []T, next string, err error) {
	off := 0
	if cursor != "" {
		off, err = strconv.Atoi(cursor)
		if err != nil || off < 0 {
			return nil, "", mcpInvalid("The cursor %q is not one this server returned; call again without it.", cursor)
		}
	}
	if off > len(items) {
		off = len(items)
	}
	end := min(off+mcpPageSize, len(items))
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[off:end], next, nil
}

// mcpMore is the line a paged list ends with when there is more.
func mcpMore(next string, total int) string {
	if next == "" {
		return ""
	}
	return fmt.Sprintf("\nShowing %s of %d. For more, call again with cursor %q.\n", next, total, next)
}

// mcpCount is n and noun, with an s unless n is one.
func mcpCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// mcpRender runs a cli renderer into a string.
func mcpRender(f func(io.Writer)) string {
	var b bytes.Buffer
	f(&b)
	return b.String()
}
