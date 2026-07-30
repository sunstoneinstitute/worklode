package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ScopeSource names the step of the resolution chain that produced a scope.
type ScopeSource string

const (
	ScopeFlag       ScopeSource = "flag"
	ScopeRepoConfig ScopeSource = "repo config"
	ScopeUserConfig ScopeSource = "user config"
	ScopeGitRemote  ScopeSource = "git remote"
	ScopeNone       ScopeSource = "none"
)

// Scope is a resolved project scope. An empty Project means "every project":
// nothing narrowed the command down, which is not an error.
type Scope struct {
	Project string
	Key     string // task-id key, e.g. "WL"; "" when not looked up
	Source  ScopeSource
	Path    string // config file, when Source is a config
	Remote  string // raw git remote URL, when Source is ScopeGitRemote
	Cached  bool   // the answer came from the local cache
}

// ResolveScope returns the project a command run in dir should act on, per
// docs/specs/019-project-scoping.md: repo config, then user config, then the
// git remote, then unscoped.
//
// It never fails. A missing remote, an unreachable server, an unmapped repo,
// or a malformed response all yield an unscoped result — scoping is a
// convenience, and losing it must not stop a command from running. A nil
// client skips the remote step.
func ResolveScope(ctx context.Context, c *Client, cfg Config, dir string) Scope {
	if cfg.CurrentProject != "" {
		return Scope{
			Project: cfg.CurrentProject,
			Source:  configSource(cfg.CurrentProjectPath),
			Path:    cfg.CurrentProjectPath,
		}
	}
	if c == nil || dir == "" {
		return Scope{Source: ScopeNone}
	}
	remote := gitRemoteURL(dir)
	if remote == "" {
		return Scope{Source: ScopeNone}
	}

	now := time.Now()
	cache := loadCache()
	if project, ok := cache.remote(remote, now); ok {
		if project == "" {
			return Scope{Source: ScopeNone, Remote: remote, Cached: true}
		}
		key, _ := cache.key(project, now)
		return Scope{
			Project: project, Key: key,
			Source: ScopeGitRemote, Remote: remote, Cached: true,
		}
	}

	p, err := c.ResolveRemote(ctx, remote)
	if err != nil {
		// Only a definite "no such mapping" is worth remembering. A transient
		// failure must not pin this repo to unscoped for the next hour.
		var ce *ClientError
		if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
			cache.putRemote(remote, "", now)
			_ = cache.save()
		}
		return Scope{Source: ScopeNone, Remote: remote}
	}
	if p.ID == "" {
		return Scope{Source: ScopeNone, Remote: remote}
	}

	cache.putRemote(remote, p.ID, now)
	if p.Key != "" {
		cache.putKey(p.ID, p.Key, now)
	}
	_ = cache.save()

	return Scope{Project: p.ID, Key: p.Key, Source: ScopeGitRemote, Remote: remote}
}

// ForgetRemote drops the cached answer for the repo containing dir, so the
// next resolution re-queries the server. Backs `lode project resolve --refresh`.
func ForgetRemote(dir string) {
	remote := gitRemoteURL(dir)
	if remote == "" {
		return
	}
	cache := loadCache()
	cache.forgetRemote(remote)
	_ = cache.save()
}

// ProjectKey returns the task-id key for a project ("WL" for worklode),
// consulting the cache before the server. Returns "" when the project is
// empty or unknown — callers treat that as "cannot expand a bare task number".
func ProjectKey(ctx context.Context, c *Client, project string) string {
	if project == "" || c == nil {
		return ""
	}
	now := time.Now()
	cache := loadCache()
	if key, ok := cache.key(project, now); ok {
		return key
	}
	p, err := c.GetProject(ctx, project)
	if err != nil || p.Key == "" {
		return ""
	}
	cache.putKey(project, p.Key, now)
	_ = cache.save()
	return p.Key
}

// configSource classifies the file that set current_project. The user config
// lives under ~/.config/worklode; anything else is a repo-local config.
func configSource(path string) ScopeSource {
	if path == "" {
		return ScopeUserConfig
	}
	if strings.Contains(path, "/.config/worklode/") {
		return ScopeUserConfig
	}
	return ScopeRepoConfig
}
