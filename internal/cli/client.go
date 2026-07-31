// Package cli implements the lode command-line client: configuration, the HTTP
// client for the worklode API, and table rendering for its commands.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config holds the client's server URL, bearer token, and the project the user
// is currently working in.
//
// It is loaded from ~/.config/worklode/config.toml, a minimal hand-rolled format
// (there is no TOML dependency in this module): one `key = "value"`
// assignment per line, blank lines and lines starting with '#' ignored. The
// recognized keys are "server" and "current_project", e.g.:
//
//	server = "https://wl.example.com"
//	current_project = "sunstone-web"
//
// A repo-local config file overrides those per checkout — see
// findRepoConfig — which is how current_project is normally set: one project
// per repository.
//
// The token lives in the OS keychain, not the file. A legacy "token" key is
// still accepted on read (as a deprecated fallback) so older config files keep
// working until the next SaveConfig migrates the token into the keychain.
//
// The environment variables LODE_SERVER and LODE_TOKEN, when set, override the
// files and the keychain.
type Config struct {
	ServerURL      string
	Token          string
	CurrentProject string

	// CurrentProjectPath is the config file CurrentProject came from, so
	// commands can report which file set their scope. Empty when no file
	// set it.
	CurrentProjectPath string
}

// tokenStore is the keychain the client reads/writes tokens through.
var tokenStore TokenStore = NewKeychainTokenStore()

// configPath returns ~/.config/worklode/config.toml.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "worklode", "config.toml"), nil
}

// repoConfigDirs are the per-repo config directory names, in the order they
// are probed at each level of the walk.
var repoConfigDirs = []string{".worklode", ".lode"}

// findRepoConfig walks up from startDir looking for a repo-local
// .worklode/config.toml (or .lode/config.toml) and returns the first hit. The
// walk stops just before $HOME — a config there is the user's, not a repo's —
// and at the filesystem root.
func findRepoConfig(startDir string) (string, bool) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.Abs(home)
		if err != nil {
			home = ""
		}
	}
	for {
		if home != "" && dir == home {
			return "", false
		}
		for _, name := range repoConfigDirs {
			p := filepath.Join(dir, name, "config.toml")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// LoadConfig reads the config files (a missing file is not an error — its
// fields are just left empty), merges the repo-local config found from the
// working directory on top of the user config, and applies the
// LODE_SERVER/LODE_TOKEN environment overrides on top of that.
func LoadConfig() (Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	return loadConfigFrom(wd)
}

// loadConfigFrom is LoadConfig with an explicit directory to search for a
// repo-local config from. An empty startDir skips the repo-local lookup.
func loadConfigFrom(startDir string) (Config, error) {
	var cfg Config

	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		cfg, err = parseConfig(string(data))
		if err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if cfg.CurrentProject != "" {
			cfg.CurrentProjectPath = path
		}
	case os.IsNotExist(err):
		// No config file: fine, env vars (or flags) may still supply everything.
	default:
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	if startDir != "" {
		if repoPath, ok := findRepoConfig(startDir); ok {
			repoCfg, err := readRepoConfig(repoPath)
			if err != nil {
				return Config{}, err
			}
			cfg.merge(repoCfg, repoPath)
		}
	}

	// Server + explicit env token first.
	if v := os.Getenv("LODE_SERVER"); v != "" {
		// A legacy cleartext token in the file belongs to the file's server; if
		// LODE_SERVER points elsewhere, it must not leak onto the new server. Only
		// a keychain hit (or LODE_TOKEN) may supply a token for the override.
		if v != cfg.ServerURL {
			cfg.Token = ""
		}
		cfg.ServerURL = v
	}
	if v := os.Getenv("LODE_TOKEN"); v != "" {
		cfg.Token = v
		return cfg, nil
	}
	// Keychain wins over any legacy cleartext token in the file. The legacy
	// cfg.Token from the file remains the fallback when the keychain has nothing.
	if cfg.ServerURL != "" {
		if tok, err := tokenStore.Get(cfg.ServerURL); err == nil && tok != "" {
			cfg.Token = tok
		}
	}
	return cfg, nil
}

// parseConfig parses the config.toml format described on Config.
func parseConfig(data string) (Config, error) {
	var cfg Config
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("line %d: expected key = \"value\"", i+1)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"`)
		switch key {
		case "server":
			cfg.ServerURL = val
		case "token":
			cfg.Token = val
		case "current_project":
			cfg.CurrentProject = val
		default:
			return Config{}, fmt.Errorf("line %d: unknown key %q", i+1, key)
		}
	}
	return cfg, nil
}

// readRepoConfig parses a repo-local config file. A token there is refused
// rather than honoured: repo configs tend to be committed, and the token
// belongs in the OS keychain (or LODE_TOKEN).
func readRepoConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := parseConfig(string(data))
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Token != "" {
		return Config{}, fmt.Errorf("%s: a repo config must not set a token; it lives in the OS keychain (or LODE_TOKEN)", path)
	}
	return cfg, nil
}

// merge applies the non-empty values of a repo-local config (read from path)
// on top of cfg.
func (cfg *Config) merge(repo Config, path string) {
	if repo.ServerURL != "" && repo.ServerURL != cfg.ServerURL {
		// Same reasoning as the LODE_SERVER override in loadConfigFrom: a
		// legacy cleartext token in the user config belongs to that config's
		// server and must not leak onto a different one.
		cfg.Token = ""
		cfg.ServerURL = repo.ServerURL
	}
	if repo.CurrentProject != "" {
		cfg.CurrentProject = repo.CurrentProject
		cfg.CurrentProjectPath = path
	}
}

// SaveConfig stores the token in the OS keychain and writes only the server URL
// to ~/.config/worklode/config.toml. Any legacy cleartext token in the file is
// dropped. Returns an error (without writing the file) if the keychain write
// fails, so the token is never silently left only in cleartext.
func SaveConfig(cfg Config) error {
	if cfg.Token != "" {
		if err := tokenStore.Set(cfg.ServerURL, cfg.Token); err != nil {
			return fmt.Errorf("store token in keychain (set LODE_TOKEN to use a token without the keychain): %w", err)
		}
	}
	return SaveServerOnly(cfg.ServerURL)
}

// SaveServerOnly writes just the server key to config.toml (0600), creating the
// directory (0700) as needed. It never writes the token, and preserves an
// existing current_project.
func SaveServerOnly(server string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	var existing Config
	if data, err := os.ReadFile(path); err == nil {
		// A malformed existing file is not worth failing the write over; it is
		// about to be replaced with a well-formed one.
		existing, _ = parseConfig(string(data))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "server = %q\n", server)
	if existing.CurrentProject != "" {
		fmt.Fprintf(&b, "current_project = %q\n", existing.CurrentProject)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ClientError is returned for any non-2xx response from the server. Its
// message is the server's "error" field when the body decodes as JSON with
// one, or the raw body otherwise.
type ClientError struct {
	Status int
	Msg    string
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("server error (%d): %s", e.Status, e.Msg)
}

// Client is a thin, typed wrapper over the worklode HTTP API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a Client from cfg. It does not validate cfg.ServerURL —
// callers should do that (e.g. with a clear "server not configured" message)
// before use.
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: strings.TrimRight(cfg.ServerURL, "/"),
		token:   cfg.Token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ServerURL returns the base URL this client talks to. Callers key
// server-specific local state (such as the remote cache) by it.
func (c *Client) ServerURL() string { return c.baseURL }

// do sends one request and returns the raw response body. body, if non-nil,
// is JSON-encoded as the request body. A non-2xx response is returned as a
// *ClientError, never masked by a generic error.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		var errBody map[string]string
		if json.Unmarshal(data, &errBody) == nil && errBody["error"] != "" {
			msg = errBody["error"]
		}
		return nil, &ClientError{Status: resp.StatusCode, Msg: msg}
	}
	return data, nil
}

// withQuery appends q to path as a query string, or returns path unchanged
// if q is empty.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// --- tasks ----------------------------------------------------------------

// Task is the wire form of a task, matching internal/api's taskJSON.
type Task struct {
	ID                 string    `json:"id"`
	Project            string    `json:"project"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	Priority           string    `json:"priority"`
	Kind               string    `json:"kind"`
	State              string    `json:"state"`
	Concern            string    `json:"concern"`
	NeedsDecomposition bool      `json:"needs_decomposition"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Skills             []string  `json:"skills"`
}

// Lease is the wire form of a lease.
type Lease struct {
	TaskID     string    `json:"task_id"`
	ActorID    string    `json:"actor_id"`
	Worktree   string    `json:"worktree"`
	AcquiredAt time.Time `json:"acquired_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// TaskEdgeOut and TaskEdgeIn are the two halves of a TaskDetail's edge list.
type TaskEdgeOut struct {
	To   string `json:"to"`
	Type string `json:"type"`
}
type TaskEdgeIn struct {
	From string `json:"from"`
	Type string `json:"type"`
}

// TaskParent is the one-hop-up projection of a task's parent epic.
type TaskParent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// TaskProgress is an epic's derived roll-up: closed of total direct children.
type TaskProgress struct {
	Closed int `json:"closed"`
	Total  int `json:"total"`
}

// TaskHierarchy is the hierarchy block on a task detail: the parent (nil for a
// root task) and the derived child progress.
type TaskHierarchy struct {
	Parent   *TaskParent  `json:"parent"`
	Progress TaskProgress `json:"progress"`
}

// TaskDetail is the wire form of GET /api/v1/tasks/{id}: a Task plus its
// blocked status, edges, hierarchy, and (when active) lease.
type TaskDetail struct {
	Task
	Blocked bool `json:"blocked"`
	Edges   struct {
		Out []TaskEdgeOut `json:"out"`
		In  []TaskEdgeIn  `json:"in"`
	} `json:"edges"`
	Lease     *Lease        `json:"lease,omitempty"`
	Hierarchy TaskHierarchy `json:"hierarchy"`
}

// CreateTaskInput is the request body for CreateTask.
type CreateTaskInput struct {
	Project  string   `json:"project"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Priority string   `json:"priority"`
	Kind     string   `json:"kind"`
	Concern  string   `json:"concern,omitempty"`
	Draft    bool     `json:"draft"`
	Skills   []string `json:"skills,omitempty"`
}

// CreateTask calls POST /api/v1/tasks.
func (c *Client) CreateTask(ctx context.Context, in CreateTaskInput) (Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks", in)
	if err != nil {
		return Task{}, nil, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// TaskListFilter narrows ListTasks. Zero-valued fields do not filter.
type TaskListFilter struct {
	Project  string
	States   []string
	Priority string
	Kind     string
	// Parent narrows to the direct children of this task id.
	Parent string
}

// TaskListResponse is the response body of ListTasks.
type TaskListResponse struct {
	Tasks []Task `json:"tasks"`
}

// ListTasks calls GET /api/v1/tasks.
func (c *Client) ListTasks(ctx context.Context, f TaskListFilter) (TaskListResponse, []byte, error) {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	for _, s := range f.States {
		q.Add("state", s)
	}
	if f.Priority != "" {
		q.Set("priority", f.Priority)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Parent != "" {
		q.Set("parent", f.Parent)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/tasks", q), nil)
	if err != nil {
		return TaskListResponse{}, nil, err
	}
	var resp TaskListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return TaskListResponse{}, nil, fmt.Errorf("decode task list: %w", err)
	}
	return resp, raw, nil
}

// GetTask calls GET /api/v1/tasks/{id}.
func (c *Client) GetTask(ctx context.Context, id string) (TaskDetail, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return TaskDetail{}, nil, err
	}
	var t TaskDetail
	if err := json.Unmarshal(raw, &t); err != nil {
		return TaskDetail{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// SetTaskSkills calls PUT /api/v1/tasks/{id}/skills, replacing the task's
// pinned skill names. A nil or empty skills clears existing pins.
func (c *Client) SetTaskSkills(ctx context.Context, id string, skills []string) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/api/v1/tasks/"+url.PathEscape(id)+"/skills",
		map[string]any{"skills": skills})
}

// ClaimResponse is the response body of ClaimTask.
type ClaimResponse struct {
	Lease  Lease  `json:"lease"`
	Branch string `json:"branch"`
}

// ClaimTask calls POST /api/v1/tasks/{id}/claim. worktree is the caller's
// worktree identity (required by the server); ttl <= 0 means the server
// default (2h).
func (c *Client) ClaimTask(ctx context.Context, id, worktree string, ttl time.Duration) (ClaimResponse, []byte, error) {
	body := map[string]any{"worktree": worktree}
	if ttl > 0 {
		body["ttl_seconds"] = int(ttl.Seconds())
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/claim", body)
	if err != nil {
		return ClaimResponse{}, nil, err
	}
	var resp ClaimResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ClaimResponse{}, nil, fmt.Errorf("decode claim response: %w", err)
	}
	return resp, raw, nil
}

// ClaimNextPickLease is the lease shard of a ClaimNextPick, present only when
// the pick was actually claimed (not a dry run or a no-ready-task response).
type ClaimNextPickLease struct {
	Worktree  string    `json:"worktree"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ClaimNextPick is the wire form of a claim-next candidate/claimed task: a
// slimmer projection than Task, matching the ranking-relevant fields (spec
// 02) rather than the full task record.
type ClaimNextPick struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	// Branch is the server-authoritative task branch (<prefix><id>-<slug>).
	Branch   string              `json:"branch"`
	Concern  string              `json:"concern"`
	Priority string              `json:"priority"`
	FanOut   int                 `json:"fan_out"`
	Project  string              `json:"project"`
	Lease    *ClaimNextPickLease `json:"lease,omitempty"`
}

// ClaimNextResponse is the response body of ClaimNext. Task is nil only when
// no ready task exists (Claimed is false and Reason is "no-ready-task"). A
// dry-run hit sets DryRun and Task but leaves Claimed false and Task.Lease
// nil.
type ClaimNextResponse struct {
	Claimed bool           `json:"claimed"`
	Reason  string         `json:"reason,omitempty"`
	DryRun  bool           `json:"dry_run,omitempty"`
	Task    *ClaimNextPick `json:"task,omitempty"`
}

// ClaimNextInput is the request body for ClaimNext.
type ClaimNextInput struct {
	Project     string
	StrictFocus bool
	DryRun      bool
	Worktree    string
	TTL         time.Duration
}

// ClaimNext calls POST /api/v1/tasks/claim-next: rank the ready set
// server-side and atomically claim the top candidate. worktree is required
// unless DryRun is set. A "no ready task" or dry-run result is a normal
// (non-error) response — see ClaimNextResponse.
func (c *Client) ClaimNext(ctx context.Context, in ClaimNextInput) (ClaimNextResponse, []byte, error) {
	body := map[string]any{
		"strict_focus": in.StrictFocus,
		"dry_run":      in.DryRun,
	}
	if in.Project != "" {
		body["project"] = in.Project
	}
	if in.Worktree != "" {
		body["worktree"] = in.Worktree
	}
	if in.TTL > 0 {
		body["ttl_seconds"] = int(in.TTL.Seconds())
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/claim-next", body)
	if err != nil {
		return ClaimNextResponse{}, nil, err
	}
	var resp ClaimNextResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ClaimNextResponse{}, nil, fmt.Errorf("decode claim-next response: %w", err)
	}
	return resp, raw, nil
}

// BriefBlocker is the slim projection of an open blocker in a Brief.
type BriefBlocker struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// Brief is the wire form of GET /api/v1/tasks/{id}/brief: the bounded
// start-of-work payload for a task. Lease is nil when the task has no active
// lease. GoverningDesign, AffectedComponents, and DefinitionOfDone are
// reserved fields that serialize as JSON null in v1. Skills carries the
// task's pinned skills (content inline) plus embedding-matched suggestions.
type Brief struct {
	Task               Task                `json:"task"`
	Body               string              `json:"body"`
	Branch             string              `json:"branch"`
	OpenBlockers       []BriefBlocker      `json:"open_blockers"`
	Lease              *Lease              `json:"lease"`
	GoverningDesign    *string             `json:"governing_design"`
	AffectedComponents []string            `json:"affected_components"`
	DefinitionOfDone   *string             `json:"definition_of_done"`
	Skills             SkillRecommendation `json:"skills"`
}

// Brief calls GET /api/v1/tasks/{id}/brief.
func (c *Client) Brief(ctx context.Context, id string) (Brief, []byte, error) {
	return c.brief(ctx, id, "")
}

// BriefWithoutSkills is Brief with skills=false: the server skips pin
// resolution, the inlined pin bodies, and the embedding call. For callers
// that only read the task row or the lease, where a pinned brief is hundreds
// of kilobytes and up to a 2s round trip nobody reads.
func (c *Client) BriefWithoutSkills(ctx context.Context, id string) (Brief, []byte, error) {
	return c.brief(ctx, id, "?skills=false")
}

func (c *Client) brief(ctx context.Context, id, query string) (Brief, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/brief"+query, nil)
	if err != nil {
		return Brief{}, nil, err
	}
	var b Brief
	if err := json.Unmarshal(raw, &b); err != nil {
		return Brief{}, nil, fmt.Errorf("decode brief: %w", err)
	}
	return b, raw, nil
}

// RebindWorktree calls POST /api/v1/tasks/{id}/lease/worktree: move the
// caller's active lease on id to a new worktree identity. Returns the
// updated lease.
func (c *Client) RebindWorktree(ctx context.Context, id, worktree string) (Lease, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/lease/worktree",
		map[string]string{"worktree": worktree})
	if err != nil {
		return Lease{}, nil, err
	}
	var l Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return Lease{}, nil, fmt.Errorf("decode lease: %w", err)
	}
	return l, raw, nil
}

// AgentSession is the wire form of an agent session on a task's lease.
type AgentSession struct {
	LeaseID      int64      `json:"lease_id"`
	Agent        string     `json:"agent"`
	AgentVersion string     `json:"agent_version,omitempty"`
	SessionID    string     `json:"session_id"`
	StartedAt    time.Time  `json:"started_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	InputTokens  *int64     `json:"input_tokens"`
	OutputTokens *int64     `json:"output_tokens"`
	CostAmount   *string    `json:"cost_amount"`
	CostCurrency string     `json:"cost_currency"`
}

// TouchAgentSession calls POST /api/v1/tasks/{id}/agent-session: report that
// this agent session is working id, or heartbeat an already-reported one.
func (c *Client) TouchAgentSession(ctx context.Context, id, agent, agentVersion, sessionID string) (AgentSession, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session",
		map[string]string{
			"agent":         agent,
			"agent_version": agentVersion,
			"session_id":    sessionID,
		})
	if err != nil {
		return AgentSession{}, nil, err
	}
	var a AgentSession
	if err := json.Unmarshal(raw, &a); err != nil {
		return AgentSession{}, nil, fmt.Errorf("decode agent session: %w", err)
	}
	return a, raw, nil
}

// EndAgentSessionInput carries the required identity plus optional accounting
// for ending a session. A nil usage field leaves the stored value untouched.
type EndAgentSessionInput struct {
	Agent        string
	SessionID    string
	InputTokens  *int64
	OutputTokens *int64
	// CostAmount is a decimal string, not a float, so it round-trips through
	// the server's numeric(12,6) column exactly (see agentsessions.go).
	CostAmount   *string
	CostCurrency string
}

// EndAgentSession calls POST /api/v1/tasks/{id}/agent-session/end.
func (c *Client) EndAgentSession(ctx context.Context, id string, in EndAgentSessionInput) error {
	body := map[string]any{"agent": in.Agent, "session_id": in.SessionID}
	if in.InputTokens != nil {
		body["input_tokens"] = *in.InputTokens
	}
	if in.OutputTokens != nil {
		body["output_tokens"] = *in.OutputTokens
	}
	if in.CostAmount != nil {
		body["cost_amount"] = *in.CostAmount
	}
	if in.CostCurrency != "" {
		body["cost_currency"] = in.CostCurrency
	}
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session/end", body)
	return err
}

// EditTaskInput carries the optional fields of a task edit; nil means leave
// the field unchanged. Concern "" or "none" clears the concern.
type EditTaskInput struct {
	Title              *string
	Body               *string
	Concern            *string
	Priority           *string
	NeedsDecomposition *bool
}

// EditTask calls PATCH /api/v1/tasks/{id}, sending only the fields set on in.
func (c *Client) EditTask(ctx context.Context, id string, in EditTaskInput) (Task, []byte, error) {
	body := map[string]any{}
	if in.Title != nil {
		body["title"] = *in.Title
	}
	if in.Body != nil {
		body["body"] = *in.Body
	}
	if in.Concern != nil {
		body["concern"] = *in.Concern
	}
	if in.Priority != nil {
		body["priority"] = *in.Priority
	}
	if in.NeedsDecomposition != nil {
		body["needs_decomposition"] = *in.NeedsDecomposition
	}
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), body)
	if err != nil {
		return Task{}, nil, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// RenewLease calls POST /api/v1/tasks/{id}/renew.
func (c *Client) RenewLease(ctx context.Context, id string, ttl time.Duration) (Lease, []byte, error) {
	body := map[string]any{}
	if ttl > 0 {
		body["ttl_seconds"] = int(ttl.Seconds())
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/renew", body)
	if err != nil {
		return Lease{}, nil, err
	}
	var l Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return Lease{}, nil, fmt.Errorf("decode lease: %w", err)
	}
	return l, raw, nil
}

// ReleaseLease calls POST /api/v1/tasks/{id}/release (204, no body).
func (c *Client) ReleaseLease(ctx context.Context, id string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/release", nil)
}

// ReacquireOrRenew re-acquires the lease on taskID for the worktree identity:
// renew when this worktree already holds it (including an expired lease still
// nominally ours), re-claim when no lease exists (the sweeper reclaimed it),
// and error when it is actively leased to a different worktree. lease is the
// current lease from a freshly-fetched brief (nil ⇒ none). This is the shared
// resume/auto-resume core used by both `lode resume` and the hook handlers.
func ReacquireOrRenew(ctx context.Context, c *Client, taskID, identity string, lease *Lease) error {
	switch {
	case lease == nil:
		if _, _, err := c.ClaimTask(ctx, taskID, identity, 0); err != nil {
			return fmt.Errorf("re-claim %s: %w", taskID, err)
		}
	case lease.Worktree == identity:
		if _, _, err := c.RenewLease(ctx, taskID, 0); err != nil {
			return fmt.Errorf("renew lease on %s: %w", taskID, err)
		}
	default:
		return fmt.Errorf("%s is actively leased to a different worktree (%s); refusing to resume", taskID, lease.Worktree)
	}
	return nil
}

// DoneTask calls POST /api/v1/tasks/{id}/done.
func (c *Client) DoneTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.taskAction(ctx, id, "done")
}

// AbandonTask calls POST /api/v1/tasks/{id}/abandon.
func (c *Client) AbandonTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.taskAction(ctx, id, "abandon")
}

// ReopenTask calls POST /api/v1/tasks/{id}/reopen: move a delivered or
// abandoned task back to ready (a fresh claim is then required).
func (c *Client) ReopenTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.taskAction(ctx, id, "reopen")
}

// ReadyTask calls PATCH /api/v1/tasks/{id} with state "ready": publish a
// draft task so it becomes claimable.
func (c *Client) ReadyTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.patchTaskState(ctx, id, "ready")
}

// ReworkTask calls PATCH /api/v1/tasks/{id} with state "in_progress": move a
// task under review back to in_progress after a review requested changes.
func (c *Client) ReworkTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.patchTaskState(ctx, id, "in_progress")
}

func (c *Client) patchTaskState(ctx context.Context, id, state string) (Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id),
		map[string]string{"state": state})
	if err != nil {
		return Task{}, nil, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

func (c *Client) taskAction(ctx context.Context, id, action string) (Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/"+action, nil)
	if err != nil {
		return Task{}, nil, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

type edgeBody struct {
	To   *string `json:"to,omitempty"`
	From *string `json:"from,omitempty"`
	Type string  `json:"type"`
}

// Block calls POST /api/v1/tasks/{id}/edges to record that by blocks id.
func (c *Client) Block(ctx context.Context, id, by string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges", edgeBody{From: &by, Type: "blocks"})
}

// Unblock calls DELETE /api/v1/tasks/{id}/edges to remove the "by blocks id" edge.
func (c *Client) Unblock(ctx context.Context, id, by string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges", edgeBody{From: &by, Type: "blocks"})
}

// Parent calls POST /api/v1/tasks/{id}/edges to file id under an epic.
func (c *Client) Parent(ctx context.Context, id, epic string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &epic, Type: "child_of"})
}

// Unparent calls DELETE /api/v1/tasks/{id}/edges to detach id from its epic.
func (c *Client) Unparent(ctx context.Context, id, epic string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &epic, Type: "child_of"})
}

// DecomposeResponse is the wire form of POST /api/v1/tasks/{id}/decompose:
// the converted epic, keeping its id, and the children it now tracks.
type DecomposeResponse struct {
	Epic     Task   `json:"epic"`
	Children []Task `json:"children"`
}

// Decompose calls POST /api/v1/tasks/{id}/decompose: converts id into an
// epic and files titles as new children under it.
func (c *Client) Decompose(ctx context.Context, id string, titles []string) (DecomposeResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/decompose",
		map[string]any{"into": titles})
	if err != nil {
		return DecomposeResponse{}, nil, err
	}
	var resp DecomposeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return DecomposeResponse{}, nil, fmt.Errorf("decode decompose response: %w", err)
	}
	return resp, raw, nil
}

// --- inbox ------------------------------------------------------------

// Issue is the wire form of an inbox issue.
type Issue struct {
	Repo              string   `json:"repo"`
	Number            int64    `json:"number"`
	Title             string   `json:"title"`
	State             string   `json:"state"`
	TriageState       string   `json:"triage_state"`
	TaskID            string   `json:"task_id,omitempty"`
	AppliesToVersions []string `json:"applies_to_versions,omitempty"`
	URL               string   `json:"url"`
}

// IssueListResponse is the response body of ListIssues.
type IssueListResponse struct {
	Issues []Issue `json:"issues"`
}

// ListIssues calls GET /api/v1/inbox. An empty state lists every triage
// state; an empty project lists every project's issues.
func (c *Client) ListIssues(ctx context.Context, state, project string) (IssueListResponse, []byte, error) {
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if project != "" {
		q.Set("project", project)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/inbox", q), nil)
	if err != nil {
		return IssueListResponse{}, nil, err
	}
	var resp IssueListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return IssueListResponse{}, nil, fmt.Errorf("decode issue list: %w", err)
	}
	return resp, raw, nil
}

// PromoteInput is the request body for PromoteIssue. Title is optional — the
// server defaults it to the issue's own title.
type PromoteInput struct {
	Repo              string   `json:"repo"`
	Number            int64    `json:"number"`
	Title             string   `json:"title,omitempty"`
	Body              string   `json:"body,omitempty"`
	Priority          string   `json:"priority"`
	Kind              string   `json:"kind"`
	AppliesToVersions []string `json:"applies_to_versions,omitempty"`
}

// PromoteIssue calls POST /api/v1/inbox/promote.
func (c *Client) PromoteIssue(ctx context.Context, in PromoteInput) (Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/inbox/promote", in)
	if err != nil {
		return Task{}, nil, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// DismissIssue calls POST /api/v1/inbox/dismiss (204, no body).
func (c *Client) DismissIssue(ctx context.Context, repo string, number int64) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/dismiss", map[string]any{"repo": repo, "number": number})
}

// --- projects ---------------------------------------------------------

// Project is the wire form of a project, including its mapped repos and
// ranking focus (the ordered list of concerns claim-next should prioritize).
type Project struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Key   string        `json:"key"`
	Repos []RepoMapping `json:"repos"`
	Focus []string      `json:"focus"`
}

// RepoMapping is a repo mapped to a project, with the terminal delivery state
// that counts as fully delivered for it (merged, deployed_prod, or released).
type RepoMapping struct {
	Repo      string `json:"repo"`
	DoneState string `json:"done_state"`
}

// CreateProjectInput is the request body for CreateProject.
type CreateProjectInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// CreateProject calls POST /api/v1/projects.
func (c *Client) CreateProject(ctx context.Context, in CreateProjectInput) (Project, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/projects", in)
	if err != nil {
		return Project{}, nil, err
	}
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Project{}, nil, fmt.Errorf("decode project: %w", err)
	}
	return p, raw, nil
}

// ProjectListResponse is the response body of ListProjects.
type ProjectListResponse struct {
	Projects []Project `json:"projects"`
}

// ListProjects calls GET /api/v1/projects.
func (c *Client) ListProjects(ctx context.Context) (ProjectListResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/projects", nil)
	if err != nil {
		return ProjectListResponse{}, nil, err
	}
	var resp ProjectListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ProjectListResponse{}, nil, fmt.Errorf("decode project list: %w", err)
	}
	return resp, raw, nil
}

// SetProjectFocus calls PATCH /api/v1/projects/{id} with the ordered focus
// list and returns the updated project. focus is always sent non-nil (an
// empty slice clears the focus) since the server rejects a missing/null
// focus with 422.
func (c *Client) SetProjectFocus(ctx context.Context, id string, focus []string) (Project, []byte, error) {
	if focus == nil {
		focus = []string{}
	}
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(id),
		map[string]any{"focus": focus})
	if err != nil {
		return Project{}, nil, err
	}
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Project{}, nil, fmt.Errorf("decode project: %w", err)
	}
	return p, raw, nil
}

// GetProject returns one project by id, or a *ClientError with Status 404 if
// no such project exists. There is no single-project GET endpoint, so this
// filters the project list.
func (c *Client) GetProject(ctx context.Context, id string) (Project, error) {
	resp, _, err := c.ListProjects(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, p := range resp.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return Project{}, &ClientError{Status: http.StatusNotFound, Msg: "project not found: " + id}
}

// ResolveRemote calls GET /api/v1/projects/resolve, returning the project the
// given git remote URL maps to. The URL is sent exactly as git reported it —
// the server owns normalization — and a *ClientError with Status 404 means
// the repo is not mapped to any project.
func (c *Client) ResolveRemote(ctx context.Context, remote string) (Project, error) {
	q := url.Values{}
	q.Set("remote", remote)
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/projects/resolve", q), nil)
	if err != nil {
		return Project{}, err
	}
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Project{}, fmt.Errorf("decode project: %w", err)
	}
	return p, nil
}

// AddRepo calls POST /api/v1/projects/{id}/repos. An empty doneState leaves
// the mapping at the server's default terminal delivery state.
func (c *Client) AddRepo(ctx context.Context, projectID, repo, doneState string) ([]byte, error) {
	body := map[string]string{"repo": repo}
	if doneState != "" {
		body["done_state"] = doneState
	}
	return c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/repos", body)
}

// SetRepoDoneState calls PATCH /api/v1/repos/{owner}/{name} (204, no body),
// setting the terminal delivery state for an already-mapped repo.
func (c *Client) SetRepoDoneState(ctx context.Context, repo, doneState string) ([]byte, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	return c.do(ctx, http.MethodPatch,
		"/api/v1/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name),
		map[string]string{"done_state": doneState})
}

// --- actors and tokens --------------------------------------------------

// Actor is the wire form of an actor.
type Actor struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

// CreateActorInput is the request body for CreateActor. Admin grants the
// actor the right to manage projects, actors, and tokens.
type CreateActorInput struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

// CreateActor calls POST /api/v1/actors.
func (c *Client) CreateActor(ctx context.Context, in CreateActorInput) (Actor, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/actors", in)
	if err != nil {
		return Actor{}, nil, err
	}
	var a Actor
	if err := json.Unmarshal(raw, &a); err != nil {
		return Actor{}, nil, fmt.Errorf("decode actor: %w", err)
	}
	return a, raw, nil
}

// TokenResponse is the response body of CreateToken: the plaintext token,
// returned exactly once.
type TokenResponse struct {
	Token string `json:"token"`
}

// CreateToken calls POST /api/v1/actors/{id}/tokens. A nil expiresAt means
// the token never expires.
func (c *Client) CreateToken(ctx context.Context, actorID, description string, expiresAt *time.Time) (TokenResponse, []byte, error) {
	body := map[string]any{"description": description}
	if expiresAt != nil {
		body["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/actors/"+url.PathEscape(actorID)+"/tokens", body)
	if err != nil {
		return TokenResponse{}, nil, err
	}
	var resp TokenResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return TokenResponse{}, nil, fmt.Errorf("decode token response: %w", err)
	}
	return resp, raw, nil
}

// RevokeToken calls DELETE /api/v1/tokens (204, no body). token may be
// either the plaintext or its stored hash.
func (c *Client) RevokeToken(ctx context.Context, token string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tokens", map[string]string{"token": token})
}

// --- board and timeline -------------------------------------------------

// Holder is the actor currently holding a lease on a board task.
type Holder struct {
	ActorID   string    `json:"actor_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BoardTask is a Task as it appears on the board, with its lease holder when
// in progress. Parent is its epic's id when it has one, so the board can
// group children under it.
type BoardTask struct {
	Task
	Parent string  `json:"parent,omitempty"`
	Holder *Holder `json:"holder,omitempty"`
}

// BoardProject is one project's slice of the board.
type BoardProject struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	InProgress []BoardTask `json:"in_progress"`
	InReview   []BoardTask `json:"in_review"`
	Ready      []BoardTask `json:"ready"`
	Blocked    []BoardTask `json:"blocked"`
}

// --- skills -----------------------------------------------------------

// Skill mirrors internal/api's skillJSON.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourceRepo  string `json:"source_repo"`
	Hash        string `json:"hash"`
	Deleted     bool   `json:"deleted"`
}

// SkillMatch mirrors internal/api's skillMatchJSON: one embedding-recommend
// hit.
type SkillMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Hash        string  `json:"hash"`
	Score       float64 `json:"score"`
}

// PinnedSkill mirrors internal/api's pinnedSkillJSON: a task-pinned skill
// with its content inlined, so a caller never needs a second round trip to
// read it.
type PinnedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hash        string `json:"hash"`
	Content     string `json:"content"`
}

// SkillRecommendation mirrors internal/api's recommendationJSON.
type SkillRecommendation struct {
	Pinned   []PinnedSkill `json:"pinned"`
	Matches  []SkillMatch  `json:"matches"`
	Warnings []string      `json:"warnings"`
	Provider string        `json:"provider"`
}

// skillsListResponse is the response body of Skills.
type skillsListResponse struct {
	Skills []Skill `json:"skills"`
}

// Skills calls GET /api/v1/skills.
func (c *Client) Skills(ctx context.Context) ([]Skill, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/skills", nil)
	if err != nil {
		return nil, nil, err
	}
	var resp skillsListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode skills: %w", err)
	}
	return resp.Skills, raw, nil
}

// Skill calls GET /api/v1/skills/{name}.
func (c *Client) Skill(ctx context.Context, name string) (Skill, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/skills/"+url.PathEscape(name), nil)
	if err != nil {
		return Skill{}, nil, err
	}
	var sk Skill
	if err := json.Unmarshal(raw, &sk); err != nil {
		return Skill{}, nil, fmt.Errorf("decode skill: %w", err)
	}
	return sk, raw, nil
}

// SkillArchive calls GET /api/v1/skills/{name}/archive/{hash} and returns the
// raw tar.gz bytes. Unlike every other client method, the response body is
// not JSON — c.do returns the response body untouched regardless of content
// type, so no decode step is needed or possible here.
func (c *Client) SkillArchive(ctx context.Context, name, hash string) ([]byte, error) {
	return c.do(ctx, http.MethodGet,
		"/api/v1/skills/"+url.PathEscape(name)+"/archive/"+url.PathEscape(hash), nil)
}

// RecommendSkills calls POST /api/v1/skills/recommend. Exactly one of taskID
// or text is required by the server.
func (c *Client) RecommendSkills(ctx context.Context, taskID, text string, limit int) (SkillRecommendation, []byte, error) {
	body := map[string]any{"task_id": taskID, "text": text, "limit": limit}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/skills/recommend", body)
	if err != nil {
		return SkillRecommendation{}, nil, err
	}
	var rec SkillRecommendation
	if err := json.Unmarshal(raw, &rec); err != nil {
		return SkillRecommendation{}, nil, fmt.Errorf("decode skill recommendation: %w", err)
	}
	return rec, raw, nil
}

// SyncSkills calls POST /api/v1/skills/sync (admin-only).
func (c *Client) SyncSkills(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/skills/sync", nil)
}

// RuntimeEvent is a recent runtime event as shown on the board.
type RuntimeEvent struct {
	ID         int64     `json:"id"`
	Cluster    string    `json:"cluster"`
	Kind       string    `json:"kind"`
	Workload   string    `json:"workload"`
	Image      string    `json:"image"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

// BoardResponse is the response body of Board. RecentFailures is nil when
// project narrows the response to one project (it is not project-scoped),
// non-nil (possibly empty) otherwise.
type BoardResponse struct {
	Projects       []BoardProject `json:"projects"`
	RecentFailures []RuntimeEvent `json:"recent_failures"`
}

// Board calls GET /api/v1/board. An empty project fetches every project.
func (c *Client) Board(ctx context.Context, project string) (BoardResponse, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/board", q), nil)
	if err != nil {
		return BoardResponse{}, nil, err
	}
	var resp BoardResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return BoardResponse{}, nil, fmt.Errorf("decode board: %w", err)
	}
	return resp, raw, nil
}

// TimelineResponse is the response body of Timeline. Each entry always has
// "at" (RFC3339 string) and "type" fields; the remaining fields vary by
// type — see internal/api/timeline.go for the full set per type.
type TimelineResponse struct {
	Task     Task             `json:"task"`
	Timeline []map[string]any `json:"timeline"`
}

// Timeline calls GET /api/v1/tasks/{id}/timeline.
func (c *Client) Timeline(ctx context.Context, taskID string) (TimelineResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(taskID)+"/timeline", nil)
	if err != nil {
		return TimelineResponse{}, nil, err
	}
	var resp TimelineResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return TimelineResponse{}, nil, fmt.Errorf("decode timeline: %w", err)
	}
	return resp, raw, nil
}
