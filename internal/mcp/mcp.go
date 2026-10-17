// Package mcp is the MCP tool server lode-server mounts at /mcp
// (docs/research/mcp-server-scope.md): a Streamable HTTP endpoint whose
// tools manage tasks, documents and rules from Claude Code, claude.ai and
// Cowork without the lode CLI.
//
// A tool never reaches the store. It is a client of /api/v1, like
// internal/cli: it calls the server's own /api/v1 handler (Config.API)
// in-process through a cli.Client carrying the caller's wl_ token
// (mcpLoopback), so route guards, Decide, validation, events and metrics
// apply exactly as they do to a CLI call. internal/disttest holds this
// package to that: it may not import internal/api, internal/store or
// internal/cmd. The OAuth login that issues the token, the RFC 9728
// protected-resource metadata and the 401 challenge belong to the server
// and live in internal/api (mcpauth.go, mcpbase.go).
//
// The tools themselves live in the feature-named siblings: mcpprojects.go,
// mcptasks.go, mcpdocs.go, mcprules.go; spoken-form parsing is mcpspoken.go.
package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
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
const mcpProjectHint = " Every call works in the session's Worklode project, set with project_use; omit " +
	"project to use it. A project argument naming any other project answers confirm_project and does " +
	"nothing. " + mcpProjectRule

// mcpRefProjectHint ends the description of a tool that takes a ref: a full
// ref carries its project's key.
const mcpRefProjectHint = mcpProjectHint + " A full ref (WL-12, WL-SPEC-3, WL-REQ-9) names its own " +
	"project, so project can then be omitted."

// mcpInstructions is what an MCP client is told about this server at
// initialize.
const mcpInstructions = "Worklode is the work tracker: tasks, design documents (specs and plans) " +
	"and the rules specs arrange. Each call works in one Worklode project, which is never picked " +
	"silently. If the user's Claude project instructions contain 'Worklode project: <name>', call " +
	"project_use with that name and from_instructions=true at the start, without asking. Otherwise " +
	"confirm the project with the user first (your question tool in text or a GUI; in voice, read back " +
	"the full project name and wait for a yes), then call project_use with confirmed=true. A tool that " +
	"answers project_required or confirm_project needs that confirmation. Refs can be given as spoken " +
	"(\"spec seventy-seven\", \"task five twelve\"). Every result starts with one sentence you can read " +
	"out as it stands."

// mcpToolNames are the tools the server registers, and the only values the
// tool metric label takes.
var mcpToolNames = []string{
	"project_list", "project_resolve", "project_use",
	"search", "show", "file",
	"task_list", "task_add", "task_update", "task_set_state",
	"doc_list", "doc_add", "doc_edit", "doc_transition",
	"rule_list", "rule_add", "rule_edit", "rule_link", "rule_supersede",
}

// Config is what the MCP tool server needs from the server that mounts it.
type Config struct {
	// API is the /api/v1 handler every tool calls in-process (mcpLoopback),
	// so route guards, Decide, validation, events and metrics apply to a
	// tool call exactly as they do to a CLI call.
	API http.Handler
	// Authenticate resolves a request's bearer token to the actor it
	// belongs to. On false it has already answered w: the Unauthorized
	// challenge for a token it does not know, or the error the lookup
	// failed with.
	Authenticate func(w http.ResponseWriter, r *http.Request, token string) (actorID string, ok bool)
	// Unauthorized answers a request that carries no bearer token: a 401
	// with the challenge that points an MCP client at the login.
	Unauthorized func(w http.ResponseWriter, r *http.Request)
	// PublicURL is the base URL a task's web link is formed from.
	PublicURL string
	// Log receives tool and loopback panics and the SDK's own log. Nil
	// discards.
	Log *slog.Logger
	// Registerer receives worklode_mcp_tool_calls_total. Nil registers
	// nothing, and the calls go uncounted.
	Registerer prometheus.Registerer
}

// server is the MCP tool server New builds: the SDK's Streamable HTTP
// handler behind serveHTTP's authentication.
type server struct {
	cfg Config
	log *slog.Logger
	// api is the handler the loopback transport dispatches tool calls into.
	api      http.Handler
	handler  http.Handler
	metrics  *mcpMetrics
	sessions *mcpSessions
}

// New builds the MCP tool server. The returned handler serves POST, GET and
// DELETE on the MCP endpoint; the caller mounts it.
func New(cfg Config) (http.Handler, error) {
	if cfg.API == nil || cfg.Authenticate == nil || cfg.Unauthorized == nil {
		return nil, errors.New("mcp: Config needs API, Authenticate and Unauthorized")
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	metrics, err := newMCPMetrics(cfg.Registerer)
	if err != nil {
		return nil, err
	}
	s := &server{
		cfg:      cfg,
		log:      log,
		api:      cfg.API,
		metrics:  metrics,
		sessions: newMCPSessions(mcpSessionTTL, mcpMaxSessions),
	}
	srv := sdk.NewServer(&sdk.Implementation{Name: "worklode", Title: "Worklode", Version: "1"},
		&sdk.ServerOptions{Instructions: mcpInstructions})
	s.addMCPProjectTools(srv)
	s.addMCPTaskTools(srv)
	s.addMCPDocTools(srv)
	s.addMCPRuleTools(srv)
	s.handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{SessionTimeout: mcpSessionTTL, Logger: log})
	return http.HandlerFunc(s.serveHTTP), nil
}

// serveHTTP authenticates the bearer token itself, then hands the request
// to the SDK's Streamable HTTP handler. The TokenInfo it attaches carries
// the actor id, which the SDK holds every later request on the session to:
// a session id presented with another actor's token is refused.
func (s *server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r.Header)
	if !ok {
		s.cfg.Unauthorized(w, r)
		return
	}
	actor, ok := s.cfg.Authenticate(w, r, token)
	if !ok {
		return
	}
	info := &auth.TokenInfo{UserID: actor}
	verified := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) { return info, nil }
	auth.RequireBearerToken(verified, &auth.RequireBearerTokenOptions{
		// Expiry was checked by Authenticate; a wl_ token need not carry one.
		AllowMissingExpiration: true,
	})(s.handler).ServeHTTP(w, r)
}

// bearerToken is the token in h's Authorization header. The scheme is
// matched without regard to case (RFC 7235 section 2.1).
func bearerToken(h http.Header) (string, bool) {
	scheme, token, ok := strings.Cut(h.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// mcpLoopback is the http.RoundTripper a tool's cli.Client sends through: it
// serves each request with the server's own handler and records the answer,
// so nothing leaves the process.
type mcpLoopback struct {
	h   http.Handler
	log *slog.Logger
}

func (l mcpLoopback) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.RemoteAddr = "mcp-loopback"
	req.RequestURI = req.URL.RequestURI()
	rec := &mcpRecorder{header: http.Header{}, code: http.StatusOK}
	l.serve(rec, req)
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

// serve runs the handler on the tool's goroutine, which the SDK starts with
// no recover of its own. net/http recovers a handler panic for a network
// request; here nothing would, and lode-server would die, so a panic is
// logged and answered 500 as net/http would answer it.
func (l mcpLoopback) serve(rec *mcpRecorder, req *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			if l.log != nil {
				l.log.Error("panic serving mcp loopback request", "method", req.Method, "path", req.URL.Path,
					"panic", v, "stack", string(debug.Stack()))
			}
			rec.header = http.Header{"Content-Type": {"application/json"}}
			rec.body.Reset()
			rec.code, rec.wroteHeader = http.StatusInternalServerError, true
			rec.body.WriteString(`{"error":"internal error"}` + "\n")
		}
	}()
	l.h.ServeHTTP(rec, req)
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
	ss      *sdk.ServerSession // for elicitation; nil outside a session

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
func addMCPTool[In, Out any](s *server, srv *sdk.Server, name, title, desc string, readOnly bool, h mcpHandler[In, Out]) {
	tool := &sdk.Tool{
		Name: name, Title: title, Description: desc,
		Annotations: &sdk.ToolAnnotations{Title: title, ReadOnlyHint: readOnly, OpenWorldHint: new(bool)},
	}
	sdk.AddTool(srv, tool, func(ctx context.Context, req *sdk.CallToolRequest, in In) (res *sdk.CallToolResult, out Out, err error) {
		// The SDK runs each call on a goroutine of its own with no recover,
		// so a panic here would take lode-server down with it.
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic in mcp tool", "tool", name, "panic", v, "stack", string(debug.Stack()))
				s.metrics.call(name, "error")
				var zero Out
				res, out, err = &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{
					Text: "Worklode failed while running " + name + ". Try again later, or tell the user.",
				}}}, zero, nil
			}
		}()
		call := s.newMCPCall(ctx, req)
		text, out, err := h(call, in)
		if err != nil {
			outcome, msg := mcpErrorText(err)
			s.metrics.call(name, outcome)
			var zero Out
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: msg}}}, zero, nil
		}
		s.metrics.call(name, "ok")
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, out, nil
	})
}

// newMCPCall builds the call context: the bearer token this very request
// carried (the SDK hands each request's headers to the tool), and the actor
// serveMCP verified it as.
func (s *server) newMCPCall(ctx context.Context, req *sdk.CallToolRequest) *mcpCall {
	call := &mcpCall{s: s, ctx: ctx}
	var token string
	if req.Extra != nil {
		token, _ = bearerToken(req.Extra.Header)
		if req.Extra.TokenInfo != nil {
			call.actor = req.Extra.TokenInfo.UserID
		}
	}
	if req.Session != nil {
		call.session = req.Session.ID()
		call.ss = req.Session
	}
	call.c = cli.NewClientTransport(mcpLoopbackURL, token, mcpLoopback{h: s.api, log: s.log})
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
