package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// The remote cache saves a round-trip per command: resolving a git remote to
// a project needs the server, but repo→project mappings change rarely. A hit
// is trusted for a week; a miss only for an hour, so a repo mapped on the
// server just now starts working without any manual step.
const (
	cacheHitTTL  = 7 * 24 * time.Hour
	cacheMissTTL = time.Hour
)

// remoteEntry is a cached remote→project answer. An empty Project is a
// negative entry: the repo is not mapped to any project.
type remoteEntry struct {
	Project string    `json:"project"`
	At      time.Time `json:"at"`
}

// keyEntry is a cached project→task-id-key answer, for expanding bare task
// numbers.
type keyEntry struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// serverCache holds one server's answers.
type serverCache struct {
	Remotes map[string]remoteEntry `json:"remotes"`
	Keys    map[string]keyEntry    `json:"keys"`
}

// remoteCache is the on-disk cache at ~/.cache/worklode/remotes.json, viewed
// through one server: repo→project mappings belong to the server that answered
// them, and LODE_SERVER can point at a different one between commands. It is
// pure optimization: every read failure yields an empty cache and every write
// failure is survivable, so no caller ever fails a command over it.
type remoteCache struct {
	Servers map[string]serverCache `json:"servers"`

	server string // the server this view reads and writes; not serialized
}

// cachePath returns ~/.cache/worklode/remotes.json.
func cachePath() (string, error) {
	return homeFile(".cache", "worklode", "remotes.json")
}

// loadCache reads the cache and returns a view of the given server's section.
// A missing, unreadable, or corrupt file is an empty cache, never an error;
// so is a file in the older un-nested format, which simply yields no hits.
func loadCache(server string) *remoteCache {
	c := &remoteCache{Servers: map[string]serverCache{}, server: server}
	path, err := cachePath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var on remoteCache
	if err := json.Unmarshal(data, &on); err != nil {
		return c
	}
	if on.Servers != nil {
		c.Servers = on.Servers
	}
	return c
}

// remote returns the cached project for a raw remote URL. The second result
// reports whether a fresh entry exists; a fresh entry with an empty project
// means "known to be unmapped".
func (c *remoteCache) remote(rawURL string, now time.Time) (string, bool) {
	e, ok := c.Servers[c.server].Remotes[rawURL]
	if !ok || !fresh(e.At, e.Project != "", now) {
		return "", false
	}
	return e.Project, true
}

// key returns the cached task-id key for a project id.
func (c *remoteCache) key(project string, now time.Time) (string, bool) {
	e, ok := c.Servers[c.server].Keys[project]
	if !ok || !fresh(e.At, e.Key != "", now) {
		return "", false
	}
	return e.Key, true
}

// section returns this view's server section, creating it on first write. A
// missing key yields the zero serverCache, whose nil maps the two checks fill
// in.
func (c *remoteCache) section() serverCache {
	s := c.Servers[c.server]
	if s.Remotes == nil {
		s.Remotes = map[string]remoteEntry{}
	}
	if s.Keys == nil {
		s.Keys = map[string]keyEntry{}
	}
	c.Servers[c.server] = s
	return s
}

func (c *remoteCache) putRemote(rawURL, project string, now time.Time) {
	c.section().Remotes[rawURL] = remoteEntry{Project: project, At: now}
}

func (c *remoteCache) putKey(project, key string, now time.Time) {
	c.section().Keys[project] = keyEntry{Key: key, At: now}
}

// forgetRemote drops a cached remote answer, so the next resolution re-queries.
func (c *remoteCache) forgetRemote(rawURL string) {
	delete(c.Servers[c.server].Remotes, rawURL)
}

// fresh reports whether an entry recorded at "at" is still valid: hits live a
// week, misses an hour.
func fresh(at time.Time, hit bool, now time.Time) bool {
	ttl := cacheMissTTL
	if hit {
		ttl = cacheHitTTL
	}
	return now.Sub(at) < ttl
}

// save writes the cache through writeFileAtomic. Callers treat the error as
// advisory.
func (c *remoteCache) save() error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	return writeFileAtomic(path, "remotes-*.json", data)
}
