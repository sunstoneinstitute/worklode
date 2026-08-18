// Package cli implements the lode command-line client: configuration, the HTTP
// client for the worklode API, and table rendering for its commands.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
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

// tokenStore is the store the client reads/writes tokens through: the OS
// keychain, or a 0600 file on a machine that has none.
var tokenStore TokenStore = NewFallbackTokenStore()

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

// SaveConfig stores the token through tokenStore — the OS keychain, or the 0600
// token file on a machine with no keychain (spec 001 §8.5) — and writes only the
// server URL to ~/.config/worklode/config.toml. Any legacy cleartext token in the
// file is dropped. A failed store write is still returned as an error — the token
// is never silently left only in config.toml — but the server is recorded anyway:
// it is not a secret, and the `export LODE_TOKEN=…` guidance that error carries
// only works on a machine that knows which server the token is for. The legacy
// cleartext token survives that path, since nothing migrated it into the
// keychain and stripping it would destroy the only copy.
//
// Since the file fallback landed, reaching that error means a keychain that
// exists and refused; absence takes the file instead and succeeds.
func SaveConfig(cfg Config) error {
	if cfg.Token != "" {
		if err := tokenStore.Set(cfg.ServerURL, cfg.Token); err != nil {
			if serr := saveServer(cfg.ServerURL, true); serr != nil {
				err = errors.Join(err, serr)
			}
			return fmt.Errorf("store token in keychain (set LODE_TOKEN to use a token without the keychain): %w", err)
		}
	}
	return SaveServerOnly(cfg.ServerURL)
}

// SaveServerOnly writes just the server key to config.toml (0600), creating the
// directory (0700) as needed. It never writes the token, and preserves an
// existing current_project and project_key.
func SaveServerOnly(server string) error { return saveServer(server, false) }

// saveServer is SaveServerOnly with control over the legacy cleartext token:
// keepLegacyToken carries an existing one through instead of stripping it, for
// the one caller whose keychain write failed.
func saveServer(server string, keepLegacyToken bool) error {
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
	if keepLegacyToken && existing.Token != "" && existing.ServerURL == server {
		fmt.Fprintf(&b, "token = %q\n", existing.Token)
	}
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
		var errBody model.ErrorResponse
		if json.Unmarshal(data, &errBody) == nil && errBody.Error != "" {
			msg = errBody.Error
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

// CreateTask calls POST /api/v1/tasks.
func (c *Client) CreateTask(ctx context.Context, in model.CreateTaskInput) (model.Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks", in)
	if err != nil {
		return model.Task{}, nil, err
	}
	var t model.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.Task{}, nil, fmt.Errorf("decode task: %w", err)
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
	// Repo narrows to the project owning this repo. Any git remote URL form
	// works as well as owner/name; the server normalizes it.
	Repo string
}

// ListTasks calls GET /api/v1/tasks.
func (c *Client) ListTasks(ctx context.Context, f TaskListFilter) (model.TaskListResponse, []byte, error) {
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
	if f.Repo != "" {
		q.Set("repo", f.Repo)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/tasks", q), nil)
	if err != nil {
		return model.TaskListResponse{}, nil, err
	}
	var resp model.TaskListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.TaskListResponse{}, nil, fmt.Errorf("decode task list: %w", err)
	}
	return resp, raw, nil
}

// GetTask calls GET /api/v1/tasks/{id}.
func (c *Client) GetTask(ctx context.Context, id string) (model.TaskDetail, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return model.TaskDetail{}, nil, err
	}
	var t model.TaskDetail
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.TaskDetail{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// SetTaskSkills calls PUT /api/v1/tasks/{id}/skills, replacing the task's
// pinned skill names. A nil or empty skills clears existing pins.
func (c *Client) SetTaskSkills(ctx context.Context, id string, skills []string) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/api/v1/tasks/"+url.PathEscape(id)+"/skills",
		model.SetSkillsInput{Skills: skills})
}

// ClaimTask calls POST /api/v1/tasks/{id}/claim. worktree is the caller's
// worktree identity (required by the server); ttl <= 0 means the server
// default (2h).
func (c *Client) ClaimTask(ctx context.Context, id, worktree string, ttl time.Duration) (model.ClaimResponse, []byte, error) {
	in := model.ClaimInput{Worktree: worktree}
	if ttl > 0 {
		in.TTLSeconds = int(ttl.Seconds())
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/claim", in)
	if err != nil {
		return model.ClaimResponse{}, nil, err
	}
	var resp model.ClaimResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.ClaimResponse{}, nil, fmt.Errorf("decode claim response: %w", err)
	}
	return resp, raw, nil
}

// ClaimNext calls POST /api/v1/tasks/claim-next: rank the ready set
// server-side and atomically claim the top candidate. worktree is required
// unless DryRun is set. A "no ready task" or dry-run result is a normal
// (non-error) response — see model.ClaimNextResponse.
func (c *Client) ClaimNext(ctx context.Context, in model.ClaimNextInput) (model.ClaimNextResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/claim-next", in)
	if err != nil {
		return model.ClaimNextResponse{}, nil, err
	}
	var resp model.ClaimNextResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.ClaimNextResponse{}, nil, fmt.Errorf("decode claim-next response: %w", err)
	}
	return resp, raw, nil
}

// Brief calls GET /api/v1/tasks/{id}/brief.
func (c *Client) Brief(ctx context.Context, id string) (model.Brief, []byte, error) {
	return c.brief(ctx, id, "")
}

// BriefWithoutSkills is Brief with skills=false: the server skips pin
// resolution, the inlined pin bodies, and the embedding call. For callers
// that only read the task row or the lease, where a pinned brief is hundreds
// of kilobytes and up to a 2s round trip nobody reads.
func (c *Client) BriefWithoutSkills(ctx context.Context, id string) (model.Brief, []byte, error) {
	return c.brief(ctx, id, "?skills=false")
}

func (c *Client) brief(ctx context.Context, id, query string) (model.Brief, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/brief"+query, nil)
	if err != nil {
		return model.Brief{}, nil, err
	}
	var b model.Brief
	if err := json.Unmarshal(raw, &b); err != nil {
		return model.Brief{}, nil, fmt.Errorf("decode brief: %w", err)
	}
	return b, raw, nil
}

// RebindWorktree calls POST /api/v1/tasks/{id}/lease/worktree: move the
// caller's active lease on id to a new worktree identity. Returns the
// updated lease.
func (c *Client) RebindWorktree(ctx context.Context, id, worktree string) (model.Lease, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/lease/worktree",
		model.RebindWorktreeInput{Worktree: worktree})
	if err != nil {
		return model.Lease{}, nil, err
	}
	var l model.Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return model.Lease{}, nil, fmt.Errorf("decode lease: %w", err)
	}
	return l, raw, nil
}

// TouchAgentSession calls POST /api/v1/tasks/{id}/agent-session: report that
// this agent session is working id, or heartbeat an already-reported one.
//
// Usage is the session's spend so far; nil leaves whatever the server has
// recorded alone. Reporting it on a heartbeat is what gets a crashed or
// swept session's tokens onto the books at all, since only a clean end
// reports them otherwise.
func (c *Client) TouchAgentSession(ctx context.Context, id, agent, agentVersion, sessionID string, usage []model.SessionUsageBucket) (model.AgentSession, []byte, error) {
	in := model.AgentSessionInput{Agent: agent, AgentVersion: agentVersion, SessionID: sessionID, Usage: usage}
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session", in)
	if err != nil {
		return model.AgentSession{}, nil, err
	}
	var a model.AgentSession
	if err := json.Unmarshal(raw, &a); err != nil {
		return model.AgentSession{}, nil, fmt.Errorf("decode agent session: %w", err)
	}
	return a, raw, nil
}

// EndAgentSession calls POST /api/v1/tasks/{id}/agent-session/end.
func (c *Client) EndAgentSession(ctx context.Context, id string, in model.EndAgentSessionInput) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session/end", in)
	return err
}

// EditTask calls PATCH /api/v1/tasks/{id}, sending only the fields set on in.
func (c *Client) EditTask(ctx context.Context, id string, in model.EditTaskInput) (model.Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), in)
	if err != nil {
		return model.Task{}, nil, err
	}
	var t model.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// RenewLease calls POST /api/v1/tasks/{id}/renew.
func (c *Client) RenewLease(ctx context.Context, id string, ttl time.Duration) (model.Lease, []byte, error) {
	in := model.RenewInput{}
	if ttl > 0 {
		in.TTLSeconds = int(ttl.Seconds())
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/renew", in)
	if err != nil {
		return model.Lease{}, nil, err
	}
	var l model.Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return model.Lease{}, nil, fmt.Errorf("decode lease: %w", err)
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
func ReacquireOrRenew(ctx context.Context, c *Client, taskID, identity string, lease *model.Lease) error {
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
func (c *Client) DoneTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "done")
}

// ReportMerge calls POST /api/v1/merges: tell the backbone that sha landed on
// repo's default branch carrying the work of these tasks. repo may be any git
// remote URL form; the server normalizes it.
func (c *Client) ReportMerge(ctx context.Context, repo, sha string, tasks []string) (model.MergeReport, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/merges",
		model.MergeReportRequest{Repo: repo, SHA: sha, Tasks: tasks})
	if err != nil {
		return model.MergeReport{}, nil, err
	}
	var out model.MergeReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return model.MergeReport{}, nil, fmt.Errorf("decode merge report: %w", err)
	}
	return out, raw, nil
}

// AbandonTask calls POST /api/v1/tasks/{id}/abandon.
func (c *Client) AbandonTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "abandon")
}

// ReopenTask calls POST /api/v1/tasks/{id}/reopen: move a delivered or
// abandoned task back to ready (a fresh claim is then required).
func (c *Client) ReopenTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "reopen")
}

// ReadyTask calls PATCH /api/v1/tasks/{id} with state "ready": publish a
// draft task so it becomes claimable.
func (c *Client) ReadyTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.patchTaskState(ctx, id, "ready")
}

// ReworkTask calls PATCH /api/v1/tasks/{id} with state "in_progress": move a
// task under review back to in_progress after a review requested changes.
func (c *Client) ReworkTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.patchTaskState(ctx, id, "in_progress")
}

// SubmitTask calls PATCH /api/v1/tasks/{id} with state "in_review": move the
// caller's in_progress task to review.
func (c *Client) SubmitTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.patchTaskState(ctx, id, "in_review")
}

// AssignTask calls POST /api/v1/tasks/{id}/assign: sets the task's assignee.
// An empty assignee assigns the task to the calling actor.
func (c *Client) AssignTask(ctx context.Context, id, assignee string) (model.Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/assign",
		model.AssignInput{Assignee: assignee})
	if err != nil {
		return model.Task{}, nil, err
	}
	var t model.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// UnassignTask calls POST /api/v1/tasks/{id}/unassign: clears the task's
// assignee.
func (c *Client) UnassignTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "unassign")
}

// StartTask calls POST /api/v1/tasks/{id}/start: moves the task to
// in_progress on behalf of the caller without taking a lease, assigning the
// caller when the task is unassigned.
func (c *Client) StartTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "start")
}

// StopTask calls POST /api/v1/tasks/{id}/stop: moves the caller's
// in_progress task back to ready, keeping the assignment.
func (c *Client) StopTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "stop")
}

func (c *Client) patchTaskState(ctx context.Context, id, state string) (model.Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id),
		model.EditTaskInput{State: &state})
	if err != nil {
		return model.Task{}, nil, err
	}
	var t model.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

func (c *Client) taskAction(ctx context.Context, id, action string) (model.Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/"+action, nil)
	if err != nil {
		return model.Task{}, nil, err
	}
	var t model.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// Block calls POST /api/v1/tasks/{id}/edges to record that by blocks id.
func (c *Client) Block(ctx context.Context, id, by string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges", model.EdgeInput{From: &by, Type: "blocks"})
}

// Unblock calls DELETE /api/v1/tasks/{id}/edges to remove the "by blocks id" edge.
func (c *Client) Unblock(ctx context.Context, id, by string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges", model.EdgeInput{From: &by, Type: "blocks"})
}

// Parent calls POST /api/v1/tasks/{id}/edges to file id under a parent.
func (c *Client) Parent(ctx context.Context, id, parent string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &parent, Type: "child_of"})
}

// Unparent calls DELETE /api/v1/tasks/{id}/edges to detach id from its parent.
func (c *Client) Unparent(ctx context.Context, id, parent string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &parent, Type: "child_of"})
}

// FollowUp calls POST /api/v1/tasks/{id}/edges to record that id was spun out
// of the work on origin.
func (c *Client) FollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &origin, Type: "follow_up_to"})
}

// UnfollowUp calls DELETE /api/v1/tasks/{id}/edges to drop the follow-up edge
// from id to origin.
func (c *Client) UnfollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &origin, Type: "follow_up_to"})
}

// Decompose calls POST /api/v1/tasks/{id}/decompose: converts id into an
// parent and files titles as new children under it.
func (c *Client) Decompose(ctx context.Context, id string, titles []string) (model.DecomposeResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/decompose",
		model.DecomposeInput{Into: titles})
	if err != nil {
		return model.DecomposeResponse{}, nil, err
	}
	var resp model.DecomposeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.DecomposeResponse{}, nil, fmt.Errorf("decode decompose response: %w", err)
	}
	return resp, raw, nil
}

// --- inbox ------------------------------------------------------------

// ListIssues calls GET /api/v1/inbox. An empty state lists every triage
// state; an empty project lists every project's issues.
func (c *Client) ListIssues(ctx context.Context, state, project string) (model.IssueListResponse, []byte, error) {
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if project != "" {
		q.Set("project", project)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/inbox", q), nil)
	if err != nil {
		return model.IssueListResponse{}, nil, err
	}
	var resp model.IssueListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.IssueListResponse{}, nil, fmt.Errorf("decode issue list: %w", err)
	}
	return resp, raw, nil
}

// PromoteIssue calls POST /api/v1/inbox/promote.
func (c *Client) PromoteIssue(ctx context.Context, in model.PromoteInput) (model.Task, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/inbox/promote", in)
	if err != nil {
		return model.Task{}, nil, err
	}
	var t model.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return model.Task{}, nil, fmt.Errorf("decode task: %w", err)
	}
	return t, raw, nil
}

// DismissIssue calls POST /api/v1/inbox/dismiss (204, no body).
func (c *Client) DismissIssue(ctx context.Context, repo string, number int64) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/dismiss", model.DismissInput{Repo: repo, Number: number})
}

// LinkIssue calls POST /api/v1/inbox/link (204, no body): attach an inbox
// issue to a task that already exists.
func (c *Client) LinkIssue(ctx context.Context, repo string, number int64, taskID string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/link",
		model.LinkInput{Repo: repo, Number: number, TaskID: taskID})
}

// ImportInbox calls POST /api/v1/inbox/import.
func (c *Client) ImportInbox(ctx context.Context, in model.ImportInput) (model.ImportResult, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/inbox/import", in)
	if err != nil {
		return model.ImportResult{}, nil, err
	}
	var out model.ImportResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return model.ImportResult{}, nil, fmt.Errorf("decode import response: %w", err)
	}
	return out, raw, nil
}

// --- docs ---------------------------------------------------------------

// --- projects ---------------------------------------------------------

// CreateProject calls POST /api/v1/projects.
func (c *Client) CreateProject(ctx context.Context, in model.CreateProjectInput) (model.Project, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/projects", in)
	if err != nil {
		return model.Project{}, nil, err
	}
	var p model.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return model.Project{}, nil, fmt.Errorf("decode project: %w", err)
	}
	return p, raw, nil
}

// ListProjects calls GET /api/v1/projects.
func (c *Client) ListProjects(ctx context.Context) (model.ProjectListResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/projects", nil)
	if err != nil {
		return model.ProjectListResponse{}, nil, err
	}
	var resp model.ProjectListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.ProjectListResponse{}, nil, fmt.Errorf("decode project list: %w", err)
	}
	return resp, raw, nil
}

// SetProjectFocus calls PATCH /api/v1/projects/{id} with the ordered focus
// list and returns the updated project. focus is always sent non-nil (an
// empty slice clears the focus) since the server rejects a missing/null
// focus with 422.
func (c *Client) SetProjectFocus(ctx context.Context, id string, focus []string) (model.Project, []byte, error) {
	if focus == nil {
		focus = []string{}
	}
	return c.patchProject(ctx, id, model.PatchProjectInput{Focus: &focus})
}

// PinProjectFocus calls PATCH /api/v1/projects/{id} to set (or clear) the
// curated pinned-focus card and returns the updated project. An empty note
// clears the card; pinnedBy is an actor id or a plain display name. The fields
// are always sent, so the server reads note:"" as an explicit clear.
func (c *Client) PinProjectFocus(ctx context.Context, id, note, pinnedBy string) (model.Project, []byte, error) {
	return c.patchProject(ctx, id, model.PatchProjectInput{
		FocusNote:     &note,
		FocusPinnedBy: &pinnedBy,
	})
}

// SetProjectNextDecision calls PATCH /api/v1/projects/{id} to set (or clear)
// the curated next-decision card and returns the updated project. An empty
// title clears the card. The fields are always sent, so the server reads
// title:"" as an explicit clear.
func (c *Client) SetProjectNextDecision(ctx context.Context, id, title, accountable, readiness string) (model.Project, []byte, error) {
	return c.patchProject(ctx, id, model.PatchProjectInput{
		DecisionTitle:       &title,
		DecisionAccountable: &accountable,
		DecisionReadiness:   &readiness,
	})
}

// patchProject PATCHes in to /api/v1/projects/{id} and decodes the updated
// project it returns, shared by the project-mutation client methods.
func (c *Client) patchProject(ctx context.Context, id string, in model.PatchProjectInput) (model.Project, []byte, error) {
	raw, err := c.do(ctx, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(id), in)
	if err != nil {
		return model.Project{}, nil, err
	}
	var p model.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return model.Project{}, nil, fmt.Errorf("decode project: %w", err)
	}
	return p, raw, nil
}

// ProjectDetail calls GET /api/v1/projects/{id}. A zero from or to leaves
// that end of the cost window unbounded.
func (c *Client) ProjectDetail(ctx context.Context, id string, from, to time.Time) (model.ProjectDetail, []byte, error) {
	q := url.Values{}
	if !from.IsZero() {
		q.Set("from", from.Format(time.DateOnly))
	}
	if !to.IsZero() {
		q.Set("to", to.Format(time.DateOnly))
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/projects/"+url.PathEscape(id), q), nil)
	if err != nil {
		return model.ProjectDetail{}, nil, err
	}
	var p model.ProjectDetail
	if err := json.Unmarshal(raw, &p); err != nil {
		return model.ProjectDetail{}, nil, fmt.Errorf("decode project detail: %w", err)
	}
	return p, raw, nil
}

// GetProject returns one project by id, or a *ClientError with Status 404 if
// no such project exists. There is no single-project GET endpoint, so this
// filters the project list.
func (c *Client) GetProject(ctx context.Context, id string) (model.Project, error) {
	resp, _, err := c.ListProjects(ctx)
	if err != nil {
		return model.Project{}, err
	}
	for _, p := range resp.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Project{}, &ClientError{Status: http.StatusNotFound, Msg: "project not found: " + id}
}

// ResolveRemote calls GET /api/v1/projects/resolve, returning the project the
// given git remote URL maps to. The URL is sent exactly as git reported it —
// the server owns normalization — and a *ClientError with Status 404 means
// the repo is not mapped to any project.
func (c *Client) ResolveRemote(ctx context.Context, remote string) (model.Project, error) {
	q := url.Values{}
	q.Set("remote", remote)
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/projects/resolve", q), nil)
	if err != nil {
		return model.Project{}, err
	}
	var p model.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return model.Project{}, fmt.Errorf("decode project: %w", err)
	}
	return p, nil
}

// AddRepo calls POST /api/v1/projects/{id}/repos. An empty doneState leaves
// the mapping at the server's default terminal delivery state.
func (c *Client) AddRepo(ctx context.Context, projectID, repo, doneState string) (model.AddRepoResult, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/repos",
		model.AddRepoInput{Repo: repo, DoneState: doneState})
	if err != nil {
		return model.AddRepoResult{}, nil, err
	}
	var out model.AddRepoResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return model.AddRepoResult{}, nil, fmt.Errorf("decode add-repo response: %w", err)
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
		model.SetRepoDoneStateInput{DoneState: doneState})
}

// --- actors and tokens --------------------------------------------------

// CreateActor calls POST /api/v1/actors.
func (c *Client) CreateActor(ctx context.Context, in model.CreateActorInput) (model.Actor, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/actors", in)
	if err != nil {
		return model.Actor{}, nil, err
	}
	var a model.Actor
	if err := json.Unmarshal(raw, &a); err != nil {
		return model.Actor{}, nil, fmt.Errorf("decode actor: %w", err)
	}
	return a, raw, nil
}

// CreateToken calls POST /api/v1/actors/{id}/tokens. A nil expiresAt means
// the token never expires.
func (c *Client) CreateToken(ctx context.Context, actorID, description string, expiresAt *time.Time) (model.TokenResponse, []byte, error) {
	in := model.CreateTokenInput{Description: description}
	if expiresAt != nil {
		exp := expiresAt.UTC().Format(time.RFC3339)
		in.ExpiresAt = &exp
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/actors/"+url.PathEscape(actorID)+"/tokens", in)
	if err != nil {
		return model.TokenResponse{}, nil, err
	}
	var resp model.TokenResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.TokenResponse{}, nil, fmt.Errorf("decode token response: %w", err)
	}
	return resp, raw, nil
}

// RevokeToken calls DELETE /api/v1/tokens (204, no body). token may be
// either the plaintext or its stored hash.
func (c *Client) RevokeToken(ctx context.Context, token string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tokens", model.RevokeTokenInput{Token: token})
}

// --- board and timeline -------------------------------------------------

// --- skills -----------------------------------------------------------

// Skills calls GET /api/v1/skills.
func (c *Client) Skills(ctx context.Context) ([]model.Skill, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/skills", nil)
	if err != nil {
		return nil, nil, err
	}
	var resp model.SkillsListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode skills: %w", err)
	}
	return resp.Skills, raw, nil
}

// Skill calls GET /api/v1/skills/{name}.
func (c *Client) Skill(ctx context.Context, name string) (model.Skill, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/skills/"+url.PathEscape(name), nil)
	if err != nil {
		return model.Skill{}, nil, err
	}
	var sk model.Skill
	if err := json.Unmarshal(raw, &sk); err != nil {
		return model.Skill{}, nil, fmt.Errorf("decode skill: %w", err)
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
func (c *Client) RecommendSkills(ctx context.Context, taskID, text string, limit int) (model.SkillRecommendation, []byte, error) {
	in := model.RecommendInput{TaskID: taskID, Text: text, Limit: limit}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/skills/recommend", in)
	if err != nil {
		return model.SkillRecommendation{}, nil, err
	}
	var rec model.SkillRecommendation
	if err := json.Unmarshal(raw, &rec); err != nil {
		return model.SkillRecommendation{}, nil, fmt.Errorf("decode skill recommendation: %w", err)
	}
	return rec, raw, nil
}

// SyncSkills calls POST /api/v1/skills/sync (admin-only).
func (c *Client) SyncSkills(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/skills/sync", nil)
}

// Board calls GET /api/v1/board. An empty project fetches every project.
func (c *Client) Board(ctx context.Context, project string) (model.BoardResponse, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/board", q), nil)
	if err != nil {
		return model.BoardResponse{}, nil, err
	}
	var resp model.BoardResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.BoardResponse{}, nil, fmt.Errorf("decode board: %w", err)
	}
	return resp, raw, nil
}

// Timeline calls GET /api/v1/tasks/{id}/timeline.
func (c *Client) Timeline(ctx context.Context, taskID string) (model.TimelineResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(taskID)+"/timeline", nil)
	if err != nil {
		return model.TimelineResponse{}, nil, err
	}
	var resp model.TimelineResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.TimelineResponse{}, nil, fmt.Errorf("decode timeline: %w", err)
	}
	return resp, raw, nil
}

// --- events (spec 025 §15/§18) ---------------------------------------------

// EventListFilter narrows ListEvents. Zero-valued fields do not filter.
type EventListFilter struct {
	Type  string
	Since time.Time
	After int64 // exclusive id cursor
	Limit int
}

// ListEvents calls GET /api/v1/events.
func (c *Client) ListEvents(ctx context.Context, f EventListFilter) (model.EventListResponse, []byte, error) {
	q := url.Values{}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if f.After != 0 {
		q.Set("after", strconv.FormatInt(f.After, 10))
	}
	if f.Limit != 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/events", q), nil)
	if err != nil {
		return model.EventListResponse{}, nil, err
	}
	var resp model.EventListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.EventListResponse{}, nil, fmt.Errorf("decode event list: %w", err)
	}
	return resp, raw, nil
}

// EventStreamFilter narrows StreamEvents. After is where the stream resumes
// (exclusive); zero means the server picks the current head, so a bare follow
// shows only what happens next.
type EventStreamFilter struct {
	Type  string
	After int64
}

// maxSSELine caps one line of the stream. An event's payload is a webhook
// body, which can legitimately be large, so this is generous — but it is a
// bound, because a server that never sends a newline must not grow the
// client's buffer without limit.
const maxSSELine = 4 << 20

// maxAPIErrBody caps how much of a refused stream's body is read for its
// error message. do reads whole bodies because they are bounded responses;
// this one is a stream, and a refusal is a small JSON object.
const maxAPIErrBody = 64 << 10

// ErrStreamEnded reports that the server closed the stream. A follow is meant
// to last, so this is never success — and because reconnecting is not
// implemented yet, it is the only thing that tells a caller its view of the
// log has stopped advancing. Without it a server restart is indistinguishable
// from a clean stop.
var ErrStreamEnded = errors.New("event stream closed by the server")

// StreamEvents follows GET /api/v1/events/stream, calling fn once per event
// until the context is cancelled (returning the context's error), the server
// closes the stream (ErrStreamEnded), or fn returns an error (returned
// unchanged, so a caller can stop cleanly).
//
// A dropped connection is returned, not retried: reconnecting means deciding
// what to do about the gap, and the server already has the mechanism for that
// (Last-Event-ID). docs/follow-ups.md records it.
func (c *Client) StreamEvents(ctx context.Context, f EventStreamFilter, fn func(model.Event) error) error {
	q := url.Values{}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if f.After != 0 {
		q.Set("after", strconv.FormatInt(f.After, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+withQuery("/api/v1/events/stream", q), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// c.http caps every request at 30s, which is a correct default for a
	// request/response call and fatal for one meant to stay open. The context
	// is what ends this one.
	resp, err := (&http.Client{Transport: c.http.Transport}).Do(req)
	if err != nil {
		return fmt.Errorf("GET /api/v1/events/stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIErrBody))
		msg := strings.TrimSpace(string(body))
		var errBody model.ErrorResponse
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			msg = errBody.Error
		}
		return &ClientError{Status: resp.StatusCode, Msg: msg}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	var data []byte
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			// End of one message. id: and event: are deliberately not kept:
			// both are also fields of the JSON, and one parse is better than
			// two that can disagree.
			if len(data) == 0 {
				continue
			}
			var e model.Event
			if err := json.Unmarshal(data, &e); err != nil {
				return fmt.Errorf("decode streamed event %q: %w", data, err)
			}
			data = data[:0]
			if err := fn(e); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// Comment line: the server's heartbeat.
		case strings.HasPrefix(line, "data:"):
			// The spec joins repeated data: lines with a newline, which is
			// how a value containing one is transmitted at all. The server
			// emits a single line per message today, so this is latent —
			// but a parser that concatenates instead would corrupt the first
			// message that isn't, silently.
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read event stream: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrStreamEnded
}

// EventSubscribers calls GET /api/v1/event-subscribers.
func (c *Client) EventSubscribers(ctx context.Context) (model.EventSubscriberListResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/event-subscribers", nil)
	if err != nil {
		return model.EventSubscriberListResponse{}, nil, err
	}
	var resp model.EventSubscriberListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.EventSubscriberListResponse{}, nil, fmt.Errorf("decode event subscriber list: %w", err)
	}
	return resp, raw, nil
}

// SeekEventSubscriber calls POST /api/v1/event-subscribers/{name}/seek,
// moving both of the subscriber's offsets to to — an admin correction of
// consumer state (025 §18), safe only because handlers are idempotent.
func (c *Client) SeekEventSubscriber(ctx context.Context, name string, to int64) (model.EventSubscriberStatus, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/event-subscribers/"+url.PathEscape(name)+"/seek",
		model.EventSubscriberSeekRequest{To: to})
	if err != nil {
		return model.EventSubscriberStatus{}, nil, err
	}
	var st model.EventSubscriberStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return model.EventSubscriberStatus{}, nil, fmt.Errorf("decode event subscriber status: %w", err)
	}
	return st, raw, nil
}
