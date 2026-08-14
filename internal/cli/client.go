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
// recognized keys are "server", "current_project", "project_key",
// "worktree_dir", "spec_corpus", and "plan_corpus", e.g.:
//
//	server = "https://wl.example.com"
//	current_project = "sunstone-web"
//	project_key = "WL"
//	worktree_dir = ".worktrees"
//
// A repo-local config file overrides current_project and project_key (and
// server) per checkout — see findRepoConfig — which is how both are normally
// set: one project per repository. worktree_dir is the one key this merge
// deliberately excludes: it is repo-scoped only (spec 008 §6), read via
// WorktreeDirFrom instead of through this struct — see WorktreeDir's doc.
//
// The token lives in the OS keychain, not the file. A legacy "token" key is
// still accepted on read (as a deprecated fallback) so older config files keep
// working until the next SaveConfig migrates the token into the keychain.
//
// The environment variables LODE_SERVER and LODE_TOKEN, when set, override
// the files and the keychain. LODE_WORKTREE_DIR is a separate override,
// applied only by WorktreeDirFrom (see its doc).
type Config struct {
	ServerURL      string
	Token          string
	CurrentProject string

	// CurrentProjectPath is the config file CurrentProject came from, so
	// commands can report which file set their scope. Empty when no file
	// set it.
	CurrentProjectPath string

	// ProjectKey is the current repo's design-doc project key ("WL"), used
	// to resolve shorthand refs like "WL-SPEC-25" (spec 026 §4.2). Empty
	// when unset, which degrades shorthand resolution to tier 3
	// (internal/designdoc.ResolveRef) rather than failing.
	ProjectKey string

	// WorktreeDir carries the worktree_dir key when Config is produced
	// directly by parseConfig — which is how WorktreeDirFrom reads it. It is
	// NOT populated by LoadConfig/loadConfigFrom: worktree_dir is scoped to
	// the repo-local config only (spec 008 §6, "the checkout owns it"), so a
	// user-level setting must never reach here — see WorktreeDirFrom, which
	// every consumer (the lifecycle commands, internal/hookrun's guard) uses
	// instead of this field.
	WorktreeDir string

	// SpecCorpus / PlanCorpus carry the spec_corpus / plan_corpus keys when
	// Config is produced directly by parseConfig — which is how CorporaFrom
	// reads them. Like WorktreeDir they are repo-scoped only (spec 025 §16.1)
	// and are NOT populated by LoadConfig/loadConfigFrom; CorporaFrom is the
	// sole reader.
	SpecCorpus string
	PlanCorpus string
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

// WorktreeDirFrom returns the worktree base directory configured for
// startDir's repo (spec 008 §5.1): a repo-local .worklode/config.toml's
// worktree_dir, with the LODE_WORKTREE_DIR env override applied on top, or ""
// when neither is set. Deliberately keychain-free and independent of
// LoadConfig/loadConfigFrom — it never touches the OS keychain or a token,
// only the repo-local config file (plus one env var), so it is cheap enough
// for a caller that must run on every hook event (internal/hookrun's guard).
// A missing or malformed repo config yields "" rather than an error: this
// never fails, it only ever degrades to "no override configured".
func WorktreeDirFrom(startDir string) string {
	var dir string
	if repoPath, ok := findRepoConfig(startDir); ok {
		if data, err := os.ReadFile(repoPath); err == nil {
			if cfg, err := parseConfig(string(data)); err == nil {
				dir = cfg.WorktreeDir
			}
		}
	}
	if v := os.Getenv("LODE_WORKTREE_DIR"); v != "" {
		dir = v
	}
	return dir
}

// Corpora is the repo-scoped corpus declaration (spec 025 §16.1): which
// directories `lode doc sync` reads, and as which document kind. A key's
// presence enables its corpus; the zero value means nothing is configured.
type Corpora struct {
	Root    string // absolute repo root — the directory holding .worklode/
	SpecDir string // absolute spec corpus dir; "" when spec_corpus is unset
	PlanDir string // absolute plan corpus dir; "" when plan_corpus is unset
}

// CorporaFrom reads startDir's repo-local config for spec_corpus/plan_corpus
// (spec 025 §16.1). Like WorktreeDirFrom it never consults the user-level config
// or the keychain, but unlike it a malformed repo config is an error here —
// sync must not silently degrade to "nothing configured" (025 §16.2).
func CorporaFrom(startDir string) (Corpora, error) {
	repoPath, ok := findRepoConfig(startDir)
	if !ok {
		return Corpora{}, nil
	}
	data, err := os.ReadFile(repoPath)
	if err != nil {
		return Corpora{}, fmt.Errorf("read %s: %w", repoPath, err)
	}
	cfg, err := parseConfig(string(data))
	if err != nil {
		return Corpora{}, fmt.Errorf("parse %s: %w", repoPath, err)
	}
	// repoPath is <root>/.worklode/config.toml (or .lode/): root is two up.
	root := filepath.Dir(filepath.Dir(repoPath))
	c := Corpora{Root: root}
	for _, k := range []struct {
		key, val string
		dst      *string
	}{
		{"spec_corpus", cfg.SpecCorpus, &c.SpecDir},
		{"plan_corpus", cfg.PlanCorpus, &c.PlanDir},
	} {
		if k.val == "" {
			continue
		}
		if filepath.IsAbs(k.val) {
			return Corpora{}, fmt.Errorf("%s: %s = %q must be a repo-relative directory", repoPath, k.key, k.val)
		}
		*k.dst = filepath.Join(root, filepath.FromSlash(k.val))
	}
	return c, nil
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
		// worktree_dir is repo-scoped only (spec 008 §6); a user-level file
		// setting it must not leak into the merged Config — WorktreeDirFrom
		// is the sole reader, and it never consults this path.
		cfg.WorktreeDir = ""
		// spec_corpus/plan_corpus are repo-scoped only (spec 025 §16.1);
		// CorporaFrom is the sole reader.
		cfg.SpecCorpus, cfg.PlanCorpus = "", ""
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
		case "project_key":
			cfg.ProjectKey = val
		case "worktree_dir":
			cfg.WorktreeDir = val
		case "spec_corpus":
			cfg.SpecCorpus = val
		case "plan_corpus":
			cfg.PlanCorpus = val
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
	if repo.ProjectKey != "" {
		cfg.ProjectKey = repo.ProjectKey
	}
	// worktree_dir, spec_corpus, and plan_corpus are deliberately NOT merged
	// here: they are repo-scoped only (specs 008 §6, 025 §16.1) and read
	// exclusively through WorktreeDirFrom / CorporaFrom, which never go
	// through loadConfigFrom/merge.
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
// existing current_project and project_key.
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
	if existing.ProjectKey != "" {
		fmt.Fprintf(&b, "project_key = %q\n", existing.ProjectKey)
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
	Assignee           string    `json:"assignee"`
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

// TaskParent is the one-hop-up projection of a task's parent.
type TaskParent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// TaskProgress is a parent's derived roll-up: closed of total direct children.
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
	// Parent, when set, files the new task under this parent in the same
	// request instead of a separate edge call.
	Parent string `json:"parent,omitempty"`
	// FollowUpTo, when set, records the task this one was spun out of in the
	// same request instead of a separate edge call.
	FollowUpTo string `json:"follow_up_to,omitempty"`
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
	// Assignee narrows to tasks assigned to this actor id.
	Assignee string
	// HasChildren narrows to containers — tasks with at least one child.
	HasChildren bool
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
	if f.Assignee != "" {
		q.Set("assignee", f.Assignee)
	}
	if f.HasChildren {
		q.Set("has_children", "true")
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
	// Parent is the task's parent, one hop up; nil for a root task.
	Parent *TaskParent `json:"parent"`
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
//
// Usage is the session's spend so far; nil leaves whatever the server has
// recorded alone. Reporting it on a heartbeat is what gets a crashed or
// swept session's tokens onto the books at all, since only a clean end
// reports them otherwise.
func (c *Client) TouchAgentSession(ctx context.Context, id, agent, agentVersion, sessionID string, usage []SessionUsageBucket) (AgentSession, []byte, error) {
	body := map[string]any{
		"agent":         agent,
		"agent_version": agentVersion,
		"session_id":    sessionID,
	}
	if usage != nil {
		body["usage"] = usage
	}
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session", body)
	if err != nil {
		return AgentSession{}, nil, err
	}
	var a AgentSession
	if err := json.Unmarshal(raw, &a); err != nil {
		return AgentSession{}, nil, fmt.Errorf("decode agent session: %w", err)
	}
	return a, raw, nil
}

// SessionUsageBucket is the tokens one model billed on one UTC day at one
// billing speed — the granularity a price can be applied at. Mirrors
// transcript.Bucket; Day is YYYY-MM-DD.
type SessionUsageBucket struct {
	Day          string `json:"day"`
	Model        string `json:"model"`
	Speed        string `json:"speed"`
	InputTokens  int64  `json:"input_tokens"`
	CacheWrite5m int64  `json:"cache_write_5m_tokens"`
	CacheWrite1h int64  `json:"cache_write_1h_tokens"`
	CacheRead    int64  `json:"cache_read_tokens"`
	OutputTokens int64  `json:"output_tokens"`
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
	// Usage replaces the session's stored per-model usage. No omitempty: nil
	// must reach the server as JSON null (leave stored usage alone), which an
	// empty slice — meaning "clear it" — would otherwise be indistinguishable
	// from.
	Usage []SessionUsageBucket `json:"usage"`
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
	body["usage"] = in.Usage // nil marshals as null: leave stored usage alone
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

// SubmitTask calls PATCH /api/v1/tasks/{id} with state "in_review": move the
// caller's in_progress task to review.
func (c *Client) SubmitTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.patchTaskState(ctx, id, "in_review")
}

// AssignTask calls POST /api/v1/tasks/{id}/assign: sets the task's assignee.
// An empty assignee assigns the task to the calling actor.
func (c *Client) AssignTask(ctx context.Context, id, assignee string) (Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/assign",
		map[string]string{"assignee": assignee})
	if err != nil {
		return Task{}, nil, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// UnassignTask calls POST /api/v1/tasks/{id}/unassign: clears the task's
// assignee.
func (c *Client) UnassignTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.taskAction(ctx, id, "unassign")
}

// StartTask calls POST /api/v1/tasks/{id}/start: moves the task to
// in_progress on behalf of the caller without taking a lease, assigning the
// caller when the task is unassigned.
func (c *Client) StartTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.taskAction(ctx, id, "start")
}

// StopTask calls POST /api/v1/tasks/{id}/stop: moves the caller's
// in_progress task back to ready, keeping the assignment.
func (c *Client) StopTask(ctx context.Context, id string) (Task, []byte, error) {
	return c.taskAction(ctx, id, "stop")
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

// Parent calls POST /api/v1/tasks/{id}/edges to file id under a parent.
func (c *Client) Parent(ctx context.Context, id, parent string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &parent, Type: "child_of"})
}

// Unparent calls DELETE /api/v1/tasks/{id}/edges to detach id from its parent.
func (c *Client) Unparent(ctx context.Context, id, parent string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &parent, Type: "child_of"})
}

// FollowUp calls POST /api/v1/tasks/{id}/edges to record that id was spun out
// of the work on origin.
func (c *Client) FollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &origin, Type: "follow_up_to"})
}

// UnfollowUp calls DELETE /api/v1/tasks/{id}/edges to drop the follow-up edge
// from id to origin.
func (c *Client) UnfollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &origin, Type: "follow_up_to"})
}

// DecomposeResponse is the wire form of POST /api/v1/tasks/{id}/decompose:
// the parent, keeping its id and kind, and the children it now tracks.
type DecomposeResponse struct {
	Parent   Task   `json:"parent"`
	Children []Task `json:"children"`
}

// Decompose calls POST /api/v1/tasks/{id}/decompose: converts id into an
// parent and files titles as new children under it.
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
	Draft             bool     `json:"draft,omitempty"`
	Parent            string   `json:"parent,omitempty"`
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

// LinkIssue calls POST /api/v1/inbox/link (204, no body): attach an inbox
// issue to a task that already exists.
func (c *Client) LinkIssue(ctx context.Context, repo string, number int64, taskID string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/link",
		map[string]any{"repo": repo, "number": number, "task_id": taskID})
}

// ImportInput is the request body for ImportInbox. An empty State means the
// server default, "open".
type ImportInput struct {
	Repo       string     `json:"repo"`
	State      string     `json:"state,omitempty"`
	IncludePRs bool       `json:"include_prs,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	DryRun     bool       `json:"dry_run,omitempty"`
}

// ImportCounts splits imported rows into ones that did not exist and ones
// that were refreshed. Truncated is this kind's own page-cap signal — issues
// and PRs page independently, so each has its own truncation state.
type ImportCounts struct {
	New       int  `json:"new"`
	Updated   int  `json:"updated"`
	Truncated bool `json:"truncated"`
}

// ImportResult is the response from ImportInbox. NewestUpdatedAt is set only
// when Issues.Truncated — it resumes the issues stream via --since. PRs have
// no such cursor; /pulls takes no since parameter.
type ImportResult struct {
	Repo            string       `json:"repo"`
	Issues          ImportCounts `json:"issues"`
	PRs             ImportCounts `json:"prs"`
	Truncated       bool         `json:"truncated"`
	DryRun          bool         `json:"dry_run"`
	NewestUpdatedAt *time.Time   `json:"newest_updated_at,omitempty"`
}

// ImportInbox calls POST /api/v1/inbox/import.
func (c *Client) ImportInbox(ctx context.Context, in ImportInput) (ImportResult, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/inbox/import", in)
	if err != nil {
		return ImportResult{}, nil, err
	}
	var out ImportResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return ImportResult{}, nil, fmt.Errorf("decode import response: %w", err)
	}
	return out, raw, nil
}

// --- docs ---------------------------------------------------------------

// DocSection is one heading extracted from a synced document's body.
type DocSection struct {
	Anchor   string `json:"anchor"`
	Heading  string `json:"heading"`
	Depth    int    `json:"depth"`
	Position int    `json:"position"`
}

// DocEdge is one cross-reference extracted from a synced document's body.
type DocEdge struct {
	SrcAnchor    string `json:"src_anchor"`
	Rel          string `json:"rel"`
	Target       string `json:"target"`
	TargetAnchor string `json:"target_anchor"`
}

// DocUpsert is one document in a SyncDocs request body.
type DocUpsert struct {
	Kind        string          `json:"kind"`
	Ordinal     string          `json:"ordinal"`
	Status      string          `json:"status"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Frontmatter json.RawMessage `json:"frontmatter"`
	Sections    []DocSection    `json:"sections,omitempty"`
	Edges       []DocEdge       `json:"edges,omitempty"`
}

// DocSyncInput is the request for SyncDocs.
type DocSyncInput struct {
	Project      string
	SourceBranch string
	Dirty        bool
	Force        bool
	DryRun       bool
	Docs         []DocUpsert
}

// DocSyncResult is one document's outcome in a SyncDocs response.
type DocSyncResult struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
}

// DocSyncReport is the response body of SyncDocs.
type DocSyncReport struct {
	DryRun    bool            `json:"dry_run"`
	Added     int             `json:"added"`
	Updated   int             `json:"updated"`
	Unchanged int             `json:"unchanged"`
	Results   []DocSyncResult `json:"results"`
}

// Doc is the wire form of a stored document (list rows have Body == "").
type Doc struct {
	ID           string          `json:"id"`
	Project      string          `json:"project"`
	Kind         string          `json:"kind"`
	Ordinal      string          `json:"ordinal"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	Version      int             `json:"version"`
	SourceBranch string          `json:"source_branch"`
	SourceDirty  bool            `json:"source_dirty"`
	SyncedAt     time.Time       `json:"synced_at"`
	Body         string          `json:"body,omitempty"`
	Frontmatter  json.RawMessage `json:"frontmatter,omitempty"`
	Sections     []DocSection    `json:"sections,omitempty"`
	Edges        []DocEdge       `json:"edges,omitempty"`
}

// DocListResponse is the response body of ListDocs.
type DocListResponse struct {
	Docs []Doc `json:"docs"`
}

// SyncDocs calls POST /api/v1/docs/sync — the git→backbone bulk upsert
// (spec 025 §16.2).
func (c *Client) SyncDocs(ctx context.Context, in DocSyncInput) (DocSyncReport, []byte, error) {
	body := map[string]any{
		"project":       in.Project,
		"source_branch": in.SourceBranch,
		"dirty":         in.Dirty,
		"force":         in.Force,
		"dry_run":       in.DryRun,
		"docs":          in.Docs,
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/docs/sync", body)
	if err != nil {
		return DocSyncReport{}, nil, err
	}
	var rep DocSyncReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return DocSyncReport{}, nil, fmt.Errorf("decode sync report: %w", err)
	}
	return rep, raw, nil
}

// ListDocs calls GET /api/v1/docs. Empty filter values do not filter.
func (c *Client) ListDocs(ctx context.Context, project, kind, status string) (DocListResponse, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if kind != "" {
		q.Set("kind", kind)
	}
	if status != "" {
		q.Set("status", status)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/docs", q), nil)
	if err != nil {
		return DocListResponse{}, nil, err
	}
	var resp DocListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return DocListResponse{}, nil, fmt.Errorf("decode doc list: %w", err)
	}
	return resp, raw, nil
}

// GetDoc calls GET /api/v1/docs/{id}.
func (c *Client) GetDoc(ctx context.Context, id string) (Doc, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/docs/"+url.PathEscape(id), nil)
	if err != nil {
		return Doc{}, nil, err
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return Doc{}, nil, fmt.Errorf("decode doc: %w", err)
	}
	return d, raw, nil
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

// PinProjectFocus calls PATCH /api/v1/projects/{id} to set (or clear) the
// curated pinned-focus card and returns the updated project. An empty note
// clears the card; pinnedBy is an actor id or a plain display name. The fields
// are always sent, so the server reads note:"" as an explicit clear.
func (c *Client) PinProjectFocus(ctx context.Context, id, note, pinnedBy string) (Project, []byte, error) {
	return c.patchProject(ctx, id, map[string]any{
		"focus_note":      note,
		"focus_pinned_by": pinnedBy,
	})
}

// SetProjectNextDecision calls PATCH /api/v1/projects/{id} to set (or clear)
// the curated next-decision card and returns the updated project. An empty
// title clears the card. The fields are always sent, so the server reads
// title:"" as an explicit clear.
func (c *Client) SetProjectNextDecision(ctx context.Context, id, title, accountable, readiness string) (Project, []byte, error) {
	return c.patchProject(ctx, id, map[string]any{
		"decision_title":       title,
		"decision_accountable": accountable,
		"decision_readiness":   readiness,
	})
}

// patchProject PATCHes body to /api/v1/projects/{id} and decodes the updated
// project it returns, shared by the project-mutation client methods.
func (c *Client) patchProject(ctx context.Context, id string, body map[string]any) (Project, []byte, error) {
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(id), body)
	if err != nil {
		return Project{}, nil, err
	}
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Project{}, nil, fmt.Errorf("decode project: %w", err)
	}
	return p, raw, nil
}

// CostTotals is the tokens and money one currency accounts for over a
// window. CostAmount is a decimal string, for the same reason
// EndAgentSessionInput.CostAmount is.
//
// UnpricedTokens counts tokens whose model had no price on file: the amount
// understates the bill by whatever they were worth, so it is reported rather
// than folded in at zero.
type CostTotals struct {
	Currency       string `json:"currency"`
	InputTokens    int64  `json:"input_tokens"`
	CacheWrite5m   int64  `json:"cache_write_5m_tokens"`
	CacheWrite1h   int64  `json:"cache_write_1h_tokens"`
	CacheRead      int64  `json:"cache_read_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	CostAmount     string `json:"cost_amount"`
	UnpricedTokens int64  `json:"unpriced_tokens"`
}

// CostDay is one UTC day's slice of CostTotals. Day is YYYY-MM-DD.
type CostDay struct {
	Day string `json:"day"`
	CostTotals
}

// ProjectCost is a project's cost over the requested window: one row per
// (day, currency) and one total per currency. Currencies are never summed
// together.
type ProjectCost struct {
	Days   []CostDay    `json:"days"`
	Totals []CostTotals `json:"totals"`
}

// ProjectDetail is the wire form of GET /api/v1/projects/{id}: a Project plus
// its cost.
type ProjectDetail struct {
	Project
	Cost ProjectCost `json:"cost"`
}

// ProjectDetail calls GET /api/v1/projects/{id}. A zero from or to leaves
// that end of the cost window unbounded.
func (c *Client) ProjectDetail(ctx context.Context, id string, from, to time.Time) (ProjectDetail, []byte, error) {
	q := url.Values{}
	if !from.IsZero() {
		q.Set("from", from.Format(time.DateOnly))
	}
	if !to.IsZero() {
		q.Set("to", to.Format(time.DateOnly))
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/projects/"+url.PathEscape(id), q), nil)
	if err != nil {
		return ProjectDetail{}, nil, err
	}
	var p ProjectDetail
	if err := json.Unmarshal(raw, &p); err != nil {
		return ProjectDetail{}, nil, fmt.Errorf("decode project detail: %w", err)
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

// AddRepoResult is the response from AddRepo. Warnings are non-fatal setup
// problems — the mapping was created regardless.
type AddRepoResult struct {
	ProjectID string   `json:"project_id"`
	Repo      string   `json:"repo"`
	DoneState string   `json:"done_state"`
	Warnings  []string `json:"warnings,omitempty"`
}

// AddRepo calls POST /api/v1/projects/{id}/repos. An empty doneState leaves
// the mapping at the server's default terminal delivery state.
func (c *Client) AddRepo(ctx context.Context, projectID, repo, doneState string) (AddRepoResult, []byte, error) {
	body := map[string]string{"repo": repo}
	if doneState != "" {
		body["done_state"] = doneState
	}
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/repos", body)
	if err != nil {
		return AddRepoResult{}, nil, err
	}
	var out AddRepoResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return AddRepoResult{}, nil, fmt.Errorf("decode add-repo response: %w", err)
	}
	return out, raw, nil
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
// in progress. Parent is its parent's id when it has one, so the board can
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
