// Package cli implements the lode command-line client: configuration, the HTTP
// client for the worklode API, and table rendering for its commands.
//
// Every typed client method returns (T, []byte, error): the decoded value for
// the renderers and the server's own bytes for --json, which is emitted
// verbatim rather than re-marshalled so the two can never drift.
//
// This file holds only shared transport and config plumbing; each feature's
// client methods live in that feature's own file (tasks.go, docs.go, ...).
package cli

import (
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
