package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()

	c := loadCache()
	c.putRemote("git@github.com:acme/app.git", "acme-app", now)
	c.putKey("acme-app", "AA", now)
	if err := c.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := loadCache()
	project, ok := got.remote("git@github.com:acme/app.git", now)
	if !ok || project != "acme-app" {
		t.Fatalf("remote = %q, %v; want acme-app, true", project, ok)
	}
	if key, ok := got.key("acme-app", now); !ok || key != "AA" {
		t.Fatalf("key = %q, %v; want AA, true", key, ok)
	}
}

func TestCacheHitExpires(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := time.Now()

	c := loadCache()
	c.putRemote("r", "p", base)
	if _, ok := c.remote("r", base.Add(cacheHitTTL-time.Minute)); !ok {
		t.Fatal("entry expired before its TTL")
	}
	if _, ok := c.remote("r", base.Add(cacheHitTTL+time.Minute)); ok {
		t.Fatal("entry survived past its TTL")
	}
}

func TestCacheMissExpiresSooner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := time.Now()

	c := loadCache()
	c.putRemote("r", "", base) // negative entry: repo not mapped
	project, ok := c.remote("r", base.Add(30*time.Minute))
	if !ok || project != "" {
		t.Fatalf("negative entry = %q, %v; want \"\", true", project, ok)
	}
	if _, ok := c.remote("r", base.Add(cacheMissTTL+time.Minute)); ok {
		t.Fatal("negative entry survived past the miss TTL")
	}
}

func TestCacheForget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	c := loadCache()
	c.putRemote("r", "p", now)
	c.forgetRemote("r")
	if _, ok := c.remote("r", now); ok {
		t.Fatal("forgotten entry still present")
	}
}

func TestCacheCorruptFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".cache", "worklode", "remotes.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := loadCache()
	if _, ok := c.remote("r", time.Now()); ok {
		t.Fatal("corrupt cache returned an entry")
	}
	// A corrupt file must not block a later write.
	c.putRemote("r", "p", time.Now())
	if err := c.save(); err != nil {
		t.Fatalf("save over corrupt file: %v", err)
	}
	if _, ok := loadCache().remote("r", time.Now()); !ok {
		t.Fatal("entry not persisted over a corrupt file")
	}
}

func TestCacheUnwritableDirIsNotFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A regular file where the cache directory should be: mkdir will fail.
	if err := os.MkdirAll(filepath.Join(home, ".cache"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cache", "worklode"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := loadCache()
	c.putRemote("r", "p", time.Now())
	if err := c.save(); err == nil {
		t.Fatal("save into an unwritable location returned nil error")
	}
	// Reading must still work (as an empty cache), not panic.
	if _, ok := loadCache().remote("r", time.Now()); ok {
		t.Fatal("unwritable cache returned an entry")
	}
}
