// Package cli implements the lode command-line client: configuration, the HTTP
// client for the worklode API, and table rendering for its commands.
//
// Every typed client method returns (T, []byte, error): the decoded value for
// the renderers and the server's own bytes for --json, which is emitted
// verbatim rather than re-marshalled so the two can never drift.
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
// recognized keys are "server", "current_project", "project_key", and
// "worktree_dir", e.g.:
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
	// when unset, which degrades shorthand resolution to tier 3 rather than
	// failing.
	ProjectKey string

	// WorktreeDir carries the worktree_dir key when Config is produced
	// directly by parseConfig — which is how WorktreeDirFrom reads it. It is
	// NOT populated by LoadConfig/loadConfigFrom: worktree_dir is scoped to
	// the repo-local config only (spec 008 §6, "the checkout owns it"), so a
	// user-level setting must never reach here — see WorktreeDirFrom, which
	// every consumer (the lifecycle commands, internal/hookrun's guard) uses
	// instead of this field.
	WorktreeDir string
}

// tokenStore is the store the client reads/writes tokens through: the OS
// keychain, or a 0600 file on a machine that has none.
var tokenStore TokenStore = NewFallbackTokenStore()

// homeFile joins parts under the user's home directory. The three files the
// CLI keeps there — config.toml, the token fallback, the remote cache — all
// resolve through it.
func homeFile(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// writeFileAtomic writes data to path through a 0600 temp file in the same
// directory, renamed into place: rename is atomic, so a crash cannot leave a
// truncated file, and the mode comes from the temp file, so an existing file
// with looser permissions is replaced rather than written through. The
// directory is created 0700 when missing. Both local files the CLI owns — the
// token fallback and the remote cache — are written this way.
func writeFileAtomic(path, tmpPattern string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// configPath returns ~/.config/worklode/config.toml.
func configPath() (string, error) {
	return homeFile(".config", "worklode", "config.toml")
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

// ConfigOrigins reports where config loading would look from startDir: the
// user config path (and whether the file exists) and the repo-local
// .worklode/.lode config the walk-up found, if any. lode doctor reports
// these; LoadConfig remains the authority on what actually loads.
func ConfigOrigins(startDir string) (userPath string, userFound bool, repoPath string, repoFound bool) {
	if p, err := configPath(); err == nil {
		userPath = p
		if _, statErr := os.Stat(p); statErr == nil {
			userFound = true
		}
	}
	repoPath, repoFound = findRepoConfig(startDir)
	return userPath, userFound, repoPath, repoFound
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

// CurrentProjectFrom returns the project startDir's repo is scoped to, using
// only local config -- a repo-local config file, then the user config -- with
// no server round trip and no keychain access: the same cheap, dir-scoped
// contract WorktreeDirFrom has, for a caller (internal/hookrun) that must
// resolve a project ahead of a backbone call from a directory that is not
// necessarily the process's own cwd. Returns "" when neither config sets
// one. Does not attempt the git-remote tier of project resolution (spec 019
// §_, ResolveScope) -- a caller that also wants that tier still needs
// ResolveScope with a real client.
func CurrentProjectFrom(startDir string) string {
	if repoPath, ok := findRepoConfig(startDir); ok {
		if data, err := os.ReadFile(repoPath); err == nil {
			if cfg, err := parseConfig(string(data)); err == nil && cfg.CurrentProject != "" {
				return cfg.CurrentProject
			}
		}
	}
	if path, err := configPath(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			if cfg, err := parseConfig(string(data)); err == nil {
				return cfg.CurrentProject
			}
		}
	}
	return ""
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
		case "spec_corpus", "plan_corpus":
			// Retired with the file corpus: documents live in the backbone
			// (spec 025), and nothing reads these any more. Still accepted,
			// and ignored, so a checkout whose config.toml has not dropped
			// them yet does not fail every command on an unknown key.
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
	// worktree_dir is deliberately NOT merged here: it is repo-scoped only
	// (spec 008 §6) and read exclusively through WorktreeDirFrom, which never
	// goes through loadConfigFrom/merge.
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
		return nil, apiError(resp.StatusCode, data)
	}
	return data, nil
}

// apiError builds the *ClientError for a non-2xx response: the server's
// "error" field when the body decodes as our JSON envelope, the raw body
// otherwise.
func apiError(status int, body []byte) *ClientError {
	msg := strings.TrimSpace(string(body))
	var errBody model.ErrorResponse
	if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
		msg = errBody.Error
	}
	return &ClientError{Status: status, Msg: msg}
}

// doJSON sends one request through do and decodes its JSON response into T,
// returning the raw body too so callers can pass the server's bytes through
// to --json unchanged. what names the shape in the decode error ("task",
// "doc list"). A decode failure yields the zero T, never a half-filled one.
func doJSON[T any](ctx context.Context, c *Client, method, path string, body any, what string) (T, []byte, error) {
	var zero T
	raw, err := c.do(ctx, method, path, body)
	if err != nil {
		return zero, nil, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, nil, fmt.Errorf("decode %s: %w", what, err)
	}
	return v, raw, nil
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
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/tasks", in, "task")
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
	// PlanDoc narrows to the tasks minted from this plan document id (025
	// §9.2). 0 does not filter.
	PlanDoc int64
	// AboutDoc narrows to the tasks that reference this document id — the
	// review and planning tasks the doc-lifecycle watcher mints (025 §15.4).
	// 0 does not filter.
	AboutDoc int64
	// Deleted switches the list to tombstoned tasks (044 §5): they replace
	// the live ones rather than joining them, so a list never mixes the two.
	Deleted bool
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
	if f.PlanDoc != 0 {
		q.Set("plan_doc", strconv.FormatInt(f.PlanDoc, 10))
	}
	if f.AboutDoc != 0 {
		q.Set("about_doc", strconv.FormatInt(f.AboutDoc, 10))
	}
	if f.Deleted {
		q.Set("deleted", "true")
	}
	return doJSON[model.TaskListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/tasks", q), nil, "task list")
}

// TaskTreeFilter selects the hierarchy TaskTree returns. Root names a single
// container to report; Project and States narrow the whole-project form.
type TaskTreeFilter struct {
	Project string
	States  []string
	Root    string
}

// TaskTree calls GET /api/v1/tasks?tree=true: every container in scope with
// its progress and its direct children, in one request. The server assembles
// the tree so a client never fetches children per container.
func (c *Client) TaskTree(ctx context.Context, f TaskTreeFilter) (model.TaskTreeResponse, []byte, error) {
	q := url.Values{"tree": {"true"}}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	for _, s := range f.States {
		q.Add("state", s)
	}
	if f.Root != "" {
		q.Set("root", f.Root)
	}
	return doJSON[model.TaskTreeResponse](ctx, c, http.MethodGet, withQuery("/api/v1/tasks", q), nil, "task tree")
}

// GetTask calls GET /api/v1/tasks/{id}.
func (c *Client) GetTask(ctx context.Context, id string) (model.TaskDetail, []byte, error) {
	return doJSON[model.TaskDetail](ctx, c, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id), nil, "task")
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
	return doJSON[model.ClaimResponse](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/claim", in, "claim response")
}

// ClaimNext calls POST /api/v1/tasks/claim-next: rank the ready set
// server-side and atomically claim the top candidate. worktree is required
// unless DryRun is set. A "no ready task" or dry-run result is a normal
// (non-error) response — see model.ClaimNextResponse.
func (c *Client) ClaimNext(ctx context.Context, in model.ClaimNextInput) (model.ClaimNextResponse, []byte, error) {
	return doJSON[model.ClaimNextResponse](ctx, c, http.MethodPost, "/api/v1/tasks/claim-next", in, "claim-next response")
}

// Brief calls GET /api/v1/tasks/{id}/brief.
func (c *Client) Brief(ctx context.Context, id string) (model.Brief, []byte, error) {
	return c.brief(ctx, id, nil)
}

// BriefWithoutSkills is Brief with skills=false: the server skips pin
// resolution, the inlined pin bodies, and the embedding call. For callers
// that only read the task row or the lease, where a pinned brief is hundreds
// of kilobytes and up to a 2s round trip nobody reads.
func (c *Client) BriefWithoutSkills(ctx context.Context, id string) (model.Brief, []byte, error) {
	return c.brief(ctx, id, url.Values{"skills": {"false"}})
}

func (c *Client) brief(ctx context.Context, id string, q url.Values) (model.Brief, []byte, error) {
	return doJSON[model.Brief](ctx, c, http.MethodGet,
		withQuery("/api/v1/tasks/"+url.PathEscape(id)+"/brief", q), nil, "brief")
}

// RebindWorktree calls POST /api/v1/tasks/{id}/lease/worktree: move the
// caller's active lease on id to a new worktree identity. Returns the
// updated lease.
func (c *Client) RebindWorktree(ctx context.Context, id, worktree string) (model.Lease, []byte, error) {
	return doJSON[model.Lease](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/lease/worktree", model.RebindWorktreeInput{Worktree: worktree}, "lease")
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
	return doJSON[model.AgentSession](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/agent-session", in, "agent session")
}

// EndAgentSession calls POST /api/v1/tasks/{id}/agent-session/end.
func (c *Client) EndAgentSession(ctx context.Context, id string, in model.EndAgentSessionInput) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session/end", in)
	return err
}

// ReportProjectSessionUsage calls POST /api/v1/projects/{id}/session-usage:
// report one session's complete usage across the project, every task it
// billed plus the remainder, replaced together (spec 052 §2).
func (c *Client) ReportProjectSessionUsage(ctx context.Context, projectID string, in model.ProjectSessionUsageInput) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/session-usage", in)
	return err
}

// EditTask calls PATCH /api/v1/tasks/{id}, sending only the fields set on in.
func (c *Client) EditTask(ctx context.Context, id string, in model.EditTaskInput) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), in, "task")
}

// RenewLease calls POST /api/v1/tasks/{id}/renew.
func (c *Client) RenewLease(ctx context.Context, id string, ttl time.Duration) (model.Lease, []byte, error) {
	in := model.RenewInput{}
	if ttl > 0 {
		in.TTLSeconds = int(ttl.Seconds())
	}
	return doJSON[model.Lease](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/renew", in, "lease")
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
	return doJSON[model.MergeReport](ctx, c, http.MethodPost, "/api/v1/merges", model.MergeReportRequest{Repo: repo, SHA: sha, Tasks: tasks}, "merge report")
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

// DeleteTask calls DELETE /api/v1/tasks/{id}: tombstone the task (044 §2).
// The body is sent even when justification is empty — it marshals to `{}`,
// which the server reads as "none given". Whether that is acceptable depends
// on the instance environment and is the server's call alone (044 §3), so
// nothing is validated or prompted for here.
func (c *Client) DeleteTask(ctx context.Context, id, justification string) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id),
		model.DeleteInput{Justification: justification}, "task")
}

// UndeleteTask calls POST /api/v1/tasks/{id}/undelete: clear the tombstone.
// No justification on either instance environment (044 §3).
func (c *Client) UndeleteTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "undelete")
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
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/assign", model.AssignInput{Assignee: assignee}, "task")
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
	return doJSON[model.Task](ctx, c, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), model.EditTaskInput{State: &state}, "task")
}

func (c *Client) taskAction(ctx context.Context, id, action string) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/"+action, nil, "task")
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

// Duplicate calls POST /api/v1/tasks/{id}/edges to record that id is the same
// request as canonical, which is the one to work.
func (c *Client) Duplicate(ctx context.Context, id, canonical string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &canonical, Type: "duplicate_of"})
}

// Unduplicate calls DELETE /api/v1/tasks/{id}/edges to drop the duplicate
// edge from id to canonical.
func (c *Client) Unduplicate(ctx context.Context, id, canonical string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &canonical, Type: "duplicate_of"})
}

// Decompose calls POST /api/v1/tasks/{id}/decompose: converts id into an
// parent and files titles as new children under it.
func (c *Client) Decompose(ctx context.Context, id string, titles []string) (model.DecomposeResponse, []byte, error) {
	return doJSON[model.DecomposeResponse](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/decompose", model.DecomposeInput{Into: titles}, "decompose response")
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
	return doJSON[model.IssueListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/inbox", q), nil, "issue list")
}

// PromoteIssue calls POST /api/v1/inbox/promote.
func (c *Client) PromoteIssue(ctx context.Context, in model.PromoteInput) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/inbox/promote", in, "task")
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
	return doJSON[model.ImportResult](ctx, c, http.MethodPost, "/api/v1/inbox/import", in, "import response")
}

// --- docs ---------------------------------------------------------------

// CreateDoc calls POST /api/v1/docs. The whole markdown artifact goes in
// in.Body, frontmatter included: the server parses it, so the body is the
// authority for the document's title, issued date, sections and edges.
func (c *Client) CreateDoc(ctx context.Context, in model.CreateDocInput) (model.Doc, []byte, error) {
	return doJSON[model.Doc](ctx, c, http.MethodPost, "/api/v1/docs", in, "doc")
}

// DocListFilter narrows ListDocs. Zero-valued fields do not filter.
//
// NeedsPlanning, NeedsExecution and BareSuperseded are 026 §2's derived
// selectors, not plain filters: each implies a kind and a status, and the
// server refuses a Kind or Status that contradicts it, or more than one
// selector at once.
type DocListFilter struct {
	Project        string
	Kind           string // spec | adr | plan
	Status         string // draft | accepted | superseded
	NeedsPlanning  bool
	NeedsExecution bool
	BareSuperseded bool
	// Deleted switches the list to tombstoned documents (044 §5): they
	// replace the live ones rather than joining them, so a list never mixes
	// the two.
	Deleted bool
}

// ListDocs calls GET /api/v1/docs.
func (c *Client) ListDocs(ctx context.Context, f DocListFilter) (model.DocListResponse, []byte, error) {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.NeedsPlanning {
		q.Set("needs_planning", "true")
	}
	if f.NeedsExecution {
		q.Set("needs_execution", "true")
	}
	if f.BareSuperseded {
		q.Set("bare_superseded", "true")
	}
	if f.Deleted {
		q.Set("deleted", "true")
	}
	return doJSON[model.DocListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/docs", q), nil, "doc list")
}

// ResolveDoc calls GET /api/v1/docs/resolve?ref=, returning the document a
// reference names — an id or an exact slug (025 §14.3). The server owns the
// grammar and its ambiguity rule, so a ref costs one indexed lookup rather
// than a listing of the whole corpus, and a grammar extension needs no client
// upgrade. A *ClientError with Status 404 means no document holds that ref;
// 422 means a slug that names more than one. The response carries no body
// text — fetch the document with GetDoc when the text is wanted.
func (c *Client) ResolveDoc(ctx context.Context, ref string) (model.Doc, error) {
	q := url.Values{}
	q.Set("ref", ref)
	d, _, err := doJSON[model.Doc](ctx, c, http.MethodGet, withQuery("/api/v1/docs/resolve", q), nil, "doc")
	return d, err
}

// GetDoc calls GET /api/v1/docs/{id}: the document plus its sections, its
// edges in both directions, and its open candidate revision if it has one.
func (c *Client) GetDoc(ctx context.Context, id int64) (model.DocDetail, []byte, error) {
	return doJSON[model.DocDetail](ctx, c, http.MethodGet, docPath(id, ""), nil, "doc")
}

// UpdateDocBody calls PUT /api/v1/docs/{id}/body: an in-place edit, which the
// server allows on a draft and on a plan at any status. An accepted spec or
// ADR is revised instead (see ReviseDoc).
func (c *Client) UpdateDocBody(ctx context.Context, id int64, body string) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPut, docPath(id, "/body"), model.UpdateDocBodyInput{Body: body})
}

// ReplaceDocEdges calls PUT /api/v1/docs/{id}/edges: re-resolve the document's
// frontmatter references against the documents that exist now. It is the
// corpus import's second pass — the first cannot resolve a reference to a
// document it has not created yet — and needs the admin-only doc.import
// permission. Nothing else about the document changes; the response is the
// same detail GET serves, so the caller reads back the resolved edge set.
func (c *Client) ReplaceDocEdges(ctx context.Context, id int64) (model.DocDetail, []byte, error) {
	return doJSON[model.DocDetail](ctx, c, http.MethodPut, docPath(id, "/edges"), nil, "doc")
}

// SubmitDoc calls POST /api/v1/docs/{id}/submit: the document enters review.
// Submission is an event, not a status (025 §15.4), so nothing about the
// document changes and the response is the document as it stands. Submitting
// the same version twice records one event and still answers 200.
func (c *Client) SubmitDoc(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/submit"), nil)
}

// AcceptDoc calls POST /api/v1/docs/{id}/accept. Only the document's assignee
// may accept it (025 §7); anyone else gets 403. The response also carries the
// tasks a plan's acceptance minted (025 §9.2); Tasks is empty for a spec or
// ADR.
func (c *Client) AcceptDoc(ctx context.Context, id int64) (model.AcceptDocResponse, []byte, error) {
	return doJSON[model.AcceptDocResponse](ctx, c, http.MethodPost, docPath(id, "/accept"), nil, "doc accept")
}

// ReviseDoc calls POST /api/v1/docs/{id}/revise, opening the one candidate
// revision an accepted spec or ADR may carry, and returns it.
func (c *Client) ReviseDoc(ctx context.Context, id int64) (model.DocRevision, []byte, error) {
	return c.docRevisionWrite(ctx, http.MethodPost, docPath(id, "/revise"), nil)
}

// UpdateDocRevision calls PUT /api/v1/docs/{id}/revision, replacing the open
// candidate's body.
func (c *Client) UpdateDocRevision(ctx context.Context, id int64, body string) (model.DocRevision, []byte, error) {
	return c.docRevisionWrite(ctx, http.MethodPut, docPath(id, "/revision"),
		model.UpdateDocBodyInput{Body: body})
}

// DiscardDocRevision calls DELETE /api/v1/docs/{id}/revision, withdrawing the
// open candidate without landing it and freeing the document's one candidate
// slot. Either the document's assignee or the revision's author may (025
// §7.2); anyone else gets 403. The document itself is unchanged, and is what
// the response carries.
func (c *Client) DiscardDocRevision(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodDelete, docPath(id, "/revision"), nil)
}

// AcceptDocRevision calls POST /api/v1/docs/{id}/revision/accept, landing the
// open candidate as the document's next version. A candidate that breaks the
// 025 §6 anchor rules is refused with the violations named.
func (c *Client) AcceptDocRevision(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/revision/accept"), nil)
}

// DeleteDoc calls DELETE /api/v1/docs/{id}: tombstone the document (044 §2).
// Like DeleteTask, the body goes out even with an empty justification; the
// server owns the instance-environment rule (044 §3).
func (c *Client) DeleteDoc(ctx context.Context, id int64, justification string) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodDelete, docPath(id, ""),
		model.DeleteInput{Justification: justification})
}

// UndeleteDoc calls POST /api/v1/docs/{id}/undelete: clear the tombstone. No
// justification on either instance environment (044 §3).
func (c *Client) UndeleteDoc(ctx context.Context, id int64) (model.Doc, []byte, error) {
	return c.docWrite(ctx, http.MethodPost, docPath(id, "/undelete"), nil)
}

// docPath builds a document endpoint path.
func docPath(id int64, suffix string) string {
	return "/api/v1/docs/" + strconv.FormatInt(id, 10) + suffix
}

// docWrite is the shared decode for the document endpoints answering with the
// document itself.
func (c *Client) docWrite(ctx context.Context, method, path string, body any) (model.Doc, []byte, error) {
	return doJSON[model.Doc](ctx, c, method, path, body, "doc")
}

// docRevisionWrite is the same for the two endpoints answering with the open
// candidate revision.
func (c *Client) docRevisionWrite(ctx context.Context, method, path string, body any) (model.DocRevision, []byte, error) {
	return doJSON[model.DocRevision](ctx, c, method, path, body, "doc revision")
}

// --- projects ---------------------------------------------------------

// CreateProject calls POST /api/v1/projects.
func (c *Client) CreateProject(ctx context.Context, in model.CreateProjectInput) (model.Project, []byte, error) {
	return doJSON[model.Project](ctx, c, http.MethodPost, "/api/v1/projects", in, "project")
}

// ListProjects calls GET /api/v1/projects.
func (c *Client) ListProjects(ctx context.Context) (model.ProjectListResponse, []byte, error) {
	return doJSON[model.ProjectListResponse](ctx, c, http.MethodGet, "/api/v1/projects", nil, "project list")
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
	return doJSON[model.Project](ctx, c, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(id), in, "project")
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
	return doJSON[model.ProjectDetail](ctx, c, http.MethodGet, withQuery("/api/v1/projects/"+url.PathEscape(id), q), nil, "project detail")
}

// TaskCost calls GET /api/v1/tasks/{id}/cost. A zero from or to leaves that
// end of the window unbounded.
func (c *Client) TaskCost(ctx context.Context, id string, children bool,
	from, to time.Time) (model.TaskCost, []byte, error) {

	q := url.Values{}
	if children {
		q.Set("children", "true")
	}
	if !from.IsZero() {
		q.Set("from", from.Format(time.DateOnly))
	}
	if !to.IsZero() {
		q.Set("to", to.Format(time.DateOnly))
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/tasks/"+url.PathEscape(id)+"/cost", q), nil)
	if err != nil {
		return model.TaskCost{}, nil, err
	}
	var tc model.TaskCost
	if err := json.Unmarshal(raw, &tc); err != nil {
		return model.TaskCost{}, nil, fmt.Errorf("decode task cost: %w", err)
	}
	return tc, raw, nil
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
	p, _, err := doJSON[model.Project](ctx, c, http.MethodGet, withQuery("/api/v1/projects/resolve", q), nil, "project")
	return p, err
}

// AddRepo calls POST /api/v1/projects/{id}/repos. An empty doneState leaves
// the mapping at the server's default terminal delivery state.
func (c *Client) AddRepo(ctx context.Context, projectID, repo, doneState string) (model.AddRepoResult, []byte, error) {
	return doJSON[model.AddRepoResult](ctx, c, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/repos", model.AddRepoInput{Repo: repo, DoneState: doneState}, "add-repo response")
}

// ReposDoctor calls GET /api/v1/repos/doctor. An empty repo reports every
// mapped repo. Admin-only on the server.
func (c *Client) ReposDoctor(ctx context.Context, repo string) (model.ReposDoctorResponse, []byte, error) {
	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	return doJSON[model.ReposDoctorResponse](ctx, c, http.MethodGet, withQuery("/api/v1/repos/doctor", q), nil, "repos doctor")
}

// Reconcile calls POST /api/v1/reconcile and returns the run report.
// Admin-only on the server; synchronous.
func (c *Client) Reconcile(ctx context.Context, in model.ReconcileInput) (model.ReconcileResponse, []byte, error) {
	return doJSON[model.ReconcileResponse](ctx, c, http.MethodPost, "/api/v1/reconcile", in, "reconcile")
}

// AddCrewMember calls POST /api/v1/projects/{id}/participants, adding one
// role-labelled Crew member (spec 029 §6.1). An empty role means "member";
// the returned member carries every role that actor holds on the project,
// not just the one just added. Deputy marks the member as the project's one
// deputy; it is mutually exclusive with lead.
func (c *Client) AddCrewMember(ctx context.Context, project, actor, role string, lead, deputy bool) (model.CrewMember, []byte, error) {
	return doJSON[model.CrewMember](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/participants",
		model.AddCrewMemberInput{Actor: actor, Role: role, Lead: lead, Deputy: deputy}, "crew member")
}

// ListCrew calls GET /api/v1/projects/{id}/participants: every member of a
// project's Crew (spec 029 §6.1), lead-first then by when they were added.
// An empty roster is an empty slice, not nil.
func (c *Client) ListCrew(ctx context.Context, project string) ([]model.CrewMember, []byte, error) {
	resp, raw, err := doJSON[model.ParticipantListResponse](ctx, c, http.MethodGet,
		"/api/v1/projects/"+url.PathEscape(project)+"/participants", nil, "participant list")
	if err != nil {
		return nil, nil, err
	}
	return resp.Participants, raw, nil
}

// RemoveCrewMember calls DELETE /api/v1/projects/{id}/participants/{actor},
// removing every role that actor holds on the project in one act (spec 029
// §6.1). The server answers 204 with no body, so the returned raw bytes are
// always empty; it is returned anyway to match AddCrewMember/ListCrew's
// shape, letting the caller's --json path print via printRaw the same way.
// A removal refused because the member still owns open work comes back as a
// *ClientError whose message names each item, so the caller can print the
// responsibility list as the server wrote it.
func (c *Client) RemoveCrewMember(ctx context.Context, project, actor string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete,
		"/api/v1/projects/"+url.PathEscape(project)+"/participants/"+url.PathEscape(actor), nil)
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
	return doJSON[model.Actor](ctx, c, http.MethodPost, "/api/v1/actors", in, "actor")
}

// CreateToken calls POST /api/v1/actors/{id}/tokens. A nil expiresAt means
// the token never expires.
func (c *Client) CreateToken(ctx context.Context, actorID, description string, expiresAt *time.Time) (model.TokenResponse, []byte, error) {
	in := model.CreateTokenInput{Description: description}
	if expiresAt != nil {
		exp := expiresAt.UTC().Format(time.RFC3339)
		in.ExpiresAt = &exp
	}
	return doJSON[model.TokenResponse](ctx, c, http.MethodPost, "/api/v1/actors/"+url.PathEscape(actorID)+"/tokens", in, "token response")
}

// MintTaskToken calls POST /api/v1/tasks/{id}/tokens: a task-scoped token
// (001 §2.1). Zero values take the server defaults (actor "sandbox", the
// lease TTL).
func (c *Client) MintTaskToken(ctx context.Context, taskID string, in model.TaskTokenInput) (model.TaskTokenResponse, []byte, error) {
	return doJSON[model.TaskTokenResponse](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(taskID)+"/tokens", in, "task token")
}

// RevokeToken calls DELETE /api/v1/tokens (204, no body). token may be
// either the plaintext or its stored hash.
func (c *Client) RevokeToken(ctx context.Context, token string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tokens", model.RevokeTokenInput{Token: token})
}

// --- skills -----------------------------------------------------------

// Skills calls GET /api/v1/skills.
func (c *Client) Skills(ctx context.Context) ([]model.Skill, []byte, error) {
	resp, raw, err := doJSON[model.SkillsListResponse](ctx, c, http.MethodGet, "/api/v1/skills", nil, "skills")
	if err != nil {
		return nil, nil, err
	}
	return resp.Skills, raw, nil
}

// Skill calls GET /api/v1/skills/{name}.
func (c *Client) Skill(ctx context.Context, name string) (model.Skill, []byte, error) {
	return doJSON[model.Skill](ctx, c, http.MethodGet, "/api/v1/skills/"+url.PathEscape(name), nil, "skill")
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
	return doJSON[model.SkillRecommendation](ctx, c, http.MethodPost, "/api/v1/skills/recommend", in, "skill recommendation")
}

// SyncSkills calls POST /api/v1/skills/sync (admin-only).
func (c *Client) SyncSkills(ctx context.Context) (model.SkillSyncReport, []byte, error) {
	return doJSON[model.SkillSyncReport](ctx, c, http.MethodPost, "/api/v1/skills/sync", nil, "skill sync report")
}

// --- board and timeline -------------------------------------------------

// Board calls GET /api/v1/board. An empty project fetches every project.
func (c *Client) Board(ctx context.Context, project string) (model.BoardResponse, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.BoardResponse](ctx, c, http.MethodGet, withQuery("/api/v1/board", q), nil, "board")
}

// Timeline calls GET /api/v1/tasks/{id}/timeline.
func (c *Client) Timeline(ctx context.Context, taskID string) (model.TimelineResponse, []byte, error) {
	return doJSON[model.TimelineResponse](ctx, c, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(taskID)+"/timeline", nil, "timeline")
}

// SecretsCatalog calls GET /api/v1/secrets/catalog (authenticated; 404 when
// the server has no catalog configured).
func (c *Client) SecretsCatalog(ctx context.Context) (model.SecretCatalogResponse, []byte, error) {
	return doJSON[model.SecretCatalogResponse](ctx, c, http.MethodGet, "/api/v1/secrets/catalog", nil, "secrets catalog")
}

// RecordSecretsMaterialized calls POST /api/v1/tasks/{id}/secrets-materialized
// with the materialized name list — the names-only audit event of spec 017.
func (c *Client) RecordSecretsMaterialized(ctx context.Context, id string, names []string) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/secrets-materialized",
		model.SecretsMaterializedInput{Names: names})
	return err
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
	return doJSON[model.EventListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/events", q), nil, "event list")
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
		return apiError(resp.StatusCode, body)
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
	return doJSON[model.EventSubscriberListResponse](ctx, c, http.MethodGet, "/api/v1/event-subscribers", nil, "event subscriber list")
}

// SeekEventSubscriber calls POST /api/v1/event-subscribers/{name}/seek,
// moving both of the subscriber's offsets to to — an admin correction of
// consumer state (025 §18), safe only because handlers are idempotent.
func (c *Client) SeekEventSubscriber(ctx context.Context, name string, to int64) (model.EventSubscriberStatus, []byte, error) {
	return doJSON[model.EventSubscriberStatus](ctx, c, http.MethodPost, "/api/v1/event-subscribers/"+url.PathEscape(name)+"/seek", model.EventSubscriberSeekRequest{To: to}, "event subscriber status")
}

// ProjectionFailures calls GET /api/v1/graph/projection/failures: the
// projects the knowledge-graph projector has quarantined, oldest failure
// first (006 §11).
func (c *Client) ProjectionFailures(ctx context.Context) (model.ProjectionFailureListResponse, []byte, error) {
	return doJSON[model.ProjectionFailureListResponse](ctx, c, http.MethodGet, "/api/v1/graph/projection/failures", nil, "projection failure list")
}

// --- blobs ------------------------------------------------------------

// UploadBlob streams r to POST /api/v1/blobs. The body is raw bytes, not
// JSON, so this bypasses do() and its JSON encoding.
func (c *Client) UploadBlob(ctx context.Context, r io.Reader, size int64) (model.BlobResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/blobs", r)
	if err != nil {
		return model.BlobResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	if size > 0 {
		req.ContentLength = size
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return model.BlobResponse{}, fmt.Errorf("upload blob: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return model.BlobResponse{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return model.BlobResponse{}, apiError(resp.StatusCode, data)
	}
	var b model.BlobResponse
	if err := json.Unmarshal(data, &b); err != nil {
		return model.BlobResponse{}, fmt.Errorf("decode blob: %w", err)
	}
	return b, nil
}

// UploadFile uploads one local file, returning its blob.
func (c *Client) UploadFile(ctx context.Context, path string) (model.BlobResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.BlobResponse{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return model.BlobResponse{}, err
	}
	return c.UploadBlob(ctx, f, fi.Size())
}

// ListTaskBlobs returns a task's blob references.
func (c *Client) ListTaskBlobs(ctx context.Context, id string) ([]model.TaskBlob, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/blobs", nil)
	if err != nil {
		return nil, err
	}
	var out model.TaskBlobsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode task blobs: %w", err)
	}
	return out.Blobs, nil
}

// AttachBlob records an explicit reference from a task to an uploaded blob.
func (c *Client) AttachBlob(ctx context.Context, id, hash, filename string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/blobs",
		model.AttachBlobInput{Hash: hash, Filename: filename})
	return err
}

// DetachBlob removes an explicit reference.
func (c *Client) DetachBlob(ctx context.Context, id, hash string) error {
	_, err := c.do(ctx, http.MethodDelete,
		"/api/v1/tasks/"+url.PathEscape(id)+"/blobs/"+url.PathEscape(hash), nil)
	return err
}

// BlobGC runs both garbage-collection sweeps (spec 021 §11). graceHours is
// pointer-typed on the wire so an admin can pass 0 deliberately (tests, or a
// deployment confident nothing is mid-upload) without it reading as "use the
// server default".
func (c *Client) BlobGC(ctx context.Context, dryRun bool, graceHours *int) (model.BlobGCResponse, []byte, error) {
	return doJSON[model.BlobGCResponse](ctx, c, http.MethodPost, "/api/v1/blobs/gc",
		model.BlobGCRequest{DryRun: dryRun, GraceHours: graceHours}, "blob gc result")
}

// WhoAmI calls GET /api/v1/whoami: which actor the configured token belongs
// to. A *ClientError with Status 401 means the token is not accepted; a
// transport error means the server is unreachable — lode doctor tells those
// two failures apart.
func (c *Client) WhoAmI(ctx context.Context) (model.WhoAmI, []byte, error) {
	return doJSON[model.WhoAmI](ctx, c, http.MethodGet, "/api/v1/whoami", nil, "whoami")
}

// --- drift & overview (spec 007) -------------------------------------------

// Overview calls GET /api/v1/overview: the one-screen roll-up. An empty
// project rolls up every project.
func (c *Client) Overview(ctx context.Context, project string) (model.Overview, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.Overview](ctx, c, http.MethodGet, withQuery("/api/v1/overview", q), nil, "overview")
}

// Drift calls GET /api/v1/drift. With acknowledged the response also carries
// the accepted deviations, active and expired.
func (c *Client) Drift(ctx context.Context, acknowledged bool) (model.Drift, []byte, error) {
	q := url.Values{}
	if acknowledged {
		q.Set("acknowledged", "1")
	}
	return doJSON[model.Drift](ctx, c, http.MethodGet, withQuery("/api/v1/drift", q), nil, "drift")
}

// Gaps calls GET /api/v1/gaps: components with no governing doc, and repo
// paths no component claims.
func (c *Client) Gaps(ctx context.Context) (model.GapList, []byte, error) {
	return doJSON[model.GapList](ctx, c, http.MethodGet, "/api/v1/gaps", nil, "gaps")
}

// Frontier calls GET /api/v1/frontier: the ready set in pickup order,
// annotated with the overview-only criticality measures.
func (c *Client) Frontier(ctx context.Context, project string) (model.FrontierList, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.FrontierList](ctx, c, http.MethodGet, withQuery("/api/v1/frontier", q), nil, "frontier")
}

// CriticalPath calls GET /api/v1/critical-path: the estimate-free critical
// path over blocks + requires, plus any cycles found on the way.
func (c *Client) CriticalPath(ctx context.Context) (model.CriticalPath, []byte, error) {
	return doJSON[model.CriticalPath](ctx, c, http.MethodGet, "/api/v1/critical-path", nil, "critical path")
}

// RunDerive calls POST /api/v1/derive: run the server-side derivers
// (pr-affects, deploy). The repo-local derivers run from a checkout instead,
// through `lode derive` without --server.
func (c *Client) RunDerive(ctx context.Context) (model.DeriveResponse, []byte, error) {
	return doJSON[model.DeriveResponse](ctx, c, http.MethodPost, "/api/v1/derive", nil, "derive results")
}
