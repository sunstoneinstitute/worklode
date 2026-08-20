// Package skillstore manages the local content-addressed skill cache, split
// across two directories (Dirs):
//
// Store/<hash>/ holds unpacked skill dirs and is the canonical location:
// immutable once extracted, one dir per version, so worktrees briefed
// against different versions of one skill never collide. That is the path
// Ensure returns and the path a brief should carry.
//
// Links/<name> is a symlink to the most recently installed version — a
// convenience for humans and the path every coding-agent harness walks to
// discover skills. It holds one name symlink per skill and nothing else: a
// hash dir living there too would surface every version as a duplicate
// skill (spec 008 §17.3). It holds one version at a time, so nothing that
// needs a specific version may depend on it.
//
// A pre-split layout (Store nested under Links, as <links>/.store/<hash>)
// migrates to the split layout silently and best-effort on first Ensure.
package skillstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/skillhash"
)

// maxExtracted caps the unpacked size of one skill version; maxEntries is
// skillhash.MaxEntries, the file-count cap the ingest side enforces too.
// Both are vars, not consts, so tests can lower them instead of building
// huge fixtures.
var (
	maxExtracted int64 = 8 << 20
	maxEntries         = skillhash.MaxEntries
)

// Root returns the local skill dir: $LODE_SKILLS_DIR or ~/.worklode/skills.
// Cleaned so a trailing slash can't make DefaultDirs derive the store as a
// child of the links dir instead of its sibling (spec 008 §17.3, acceptance
// 9: no store hash dir may be reachable by a harness walking the links dir).
func Root() (string, error) {
	if v := os.Getenv("LODE_SKILLS_DIR"); v != "" {
		return filepath.Clean(v), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("skill store root: %w", err)
	}
	return filepath.Join(home, ".worklode", "skills"), nil
}

// Dirs locates the two halves of the local skill cache. Links holds one
// symlink per skill name and nothing else — harnesses walk it, so a hash
// dir here would surface every version as a duplicate skill (spec 008
// §17.3). Store holds the immutable content-addressed version dirs.
type Dirs struct {
	Links string // ~/.worklode/skills
	Store string // ~/.worklode/store
}

// DefaultDirs resolves the cache location: $LODE_SKILLS_DIR (links; the
// store is its parent's "store" sibling) or ~/.worklode/{skills,store}. It
// is a pure path computation — neither directory is created here.
func DefaultDirs() (Dirs, error) {
	links, err := Root()
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{Links: links, Store: filepath.Join(filepath.Dir(links), "store")}, nil
}

// Path returns the by-name symlink dirs.Links/<name>, whether or not it
// exists yet. It points at whichever version was installed last, so it is
// for humans only; anything needing a particular version uses the path
// Ensure returns.
func Path(links, name string) string { return filepath.Join(links, name) }

// validHash requires lowercase hex only: uppercase would collide with
// lowercase store dirs on a case-insensitive filesystem (macOS default
// APFS), silently mixing two versions under one path.
func validHash(hash string) bool {
	if len(hash) < 6 || len(hash)%2 != 0 {
		return false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Ensure makes the version identified by hash available locally, calling
// fetch for the tar.gz only when it is not already in the store. It returns
// the canonical dirs.Store/<hash> path: spec 016 requires two worktrees
// briefed against different hashes of one skill to resolve valid paths
// simultaneously, and the single dirs.Links/<name> symlink cannot do that —
// the second install would repoint the first's path at the other version.
//
// dirs.Links/<name> is still repointed here, as the human-facing pointer to
// the most recent install.
func Ensure(dirs Dirs, name, hash string, fetch func() ([]byte, error)) (string, error) {
	if !skillhash.ValidName(name) {
		return "", fmt.Errorf("skill name %q: invalid", name)
	}
	if !validHash(hash) {
		return "", fmt.Errorf("skill hash %q: invalid", hash)
	}
	migrateLegacyStore(dirs)
	dst := filepath.Join(dirs.Store, hash)
	if info, err := os.Stat(dst); err != nil || !info.IsDir() {
		data, err := fetch()
		if err != nil {
			return "", fmt.Errorf("fetch skill %s@%s: %w", name, hash, err)
		}
		if err := extract(data, dst, hash); err != nil {
			return "", fmt.Errorf("extract skill %s@%s: %w", name, hash, err)
		}
	}
	link := Path(dirs.Links, name)
	if err := swapSymlink(relTarget(dirs, hash), link); err != nil {
		return "", fmt.Errorf("link skill %s: %w", name, err)
	}
	return dst, nil
}

// relTarget is the symlink target from the links dir to a store version,
// relative so the ~/.worklode tree stays relocatable as a unit.
func relTarget(dirs Dirs, hash string) string {
	rel, err := filepath.Rel(dirs.Links, filepath.Join(dirs.Store, hash))
	if err != nil {
		return filepath.Join(dirs.Store, hash) // disjoint roots: not necessarily absolute, just unrelocated
	}
	return rel
}

// rename is os.Rename, a var so a test can inject a failure (permission,
// EXDEV across a mount) to exercise migrateLegacyStore's per-hash fallback
// without needing a real broken filesystem.
var rename = os.Rename

// migrateLegacyStore moves a pre-split <links>/.store/ into dirs.Store
// (Q024.2: silent, by rename — content-addressed dirs are immutable, so a
// rename either fully succeeds or leaves the version in place) and repoints
// name symlinks that still target ".store/<hash>". Best-effort and silent,
// but never at the cost of content: a hash whose rename fails keeps its
// legacy copy and its working symlink untouched — exactly as if migration
// had not run for that version — rather than being repointed at a store
// entry that was never created or deleted out from under a live symlink.
func migrateLegacyStore(dirs Dirs) {
	legacy := filepath.Join(dirs.Links, ".store")
	entries, err := os.ReadDir(legacy)
	if err != nil {
		return // no legacy store — the common case, one cheap ReadDir
	}
	_ = os.MkdirAll(dirs.Store, 0o755)
	moved := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dst := filepath.Join(dirs.Store, e.Name())
		if _, err := os.Stat(dst); err == nil {
			// Already in the store (an earlier migration, or a fresh
			// Ensure that raced ahead of this one): the legacy copy is
			// redundant and safe to drop.
			_ = os.RemoveAll(filepath.Join(legacy, e.Name()))
			moved[e.Name()] = true
			continue
		}
		if err := rename(filepath.Join(legacy, e.Name()), dst); err == nil {
			moved[e.Name()] = true
		}
		// Rename failure leaves legacy/<hash> in place; its symlink is
		// left pointing at it below, and the next Ensure retries.
	}
	links, err := os.ReadDir(dirs.Links)
	if err != nil {
		return
	}
	for _, e := range links {
		p := filepath.Join(dirs.Links, e.Name())
		target, err := os.Readlink(p)
		if err != nil || !strings.HasPrefix(target, ".store"+string(filepath.Separator)) {
			continue
		}
		hash := filepath.Base(target)
		if !moved[hash] {
			continue // this hash's rename failed or never ran: leave the still-working symlink alone
		}
		_ = swapSymlink(relTarget(dirs, hash), p)
	}
	// Only clear the legacy dir once nothing is left in it: a leftover
	// entry means some hash's rename failed, and that content — and the
	// symlink still pointing at it — must survive for the next retry.
	if remaining, err := os.ReadDir(legacy); err == nil && len(remaining) == 0 {
		_ = os.RemoveAll(legacy)
	}
}

// extract unpacks tgz into a sibling tmp dir, verifies its content hashes to
// wantHash, then renames it into place at dst. The rename is the commit
// point: a half-extracted or hash-mismatched archive never becomes visible
// at dst, and dst is immutable once it exists.
func extract(tgz []byte, dst, wantHash string) error {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return err
	}
	suffix, err := randSuffix()
	if err != nil {
		return err
	}
	tmp := dst + ".tmp-" + suffix
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	tr := tar.NewReader(gz)
	var total int64
	var entries int
	// Keyed by path: a duplicate entry overwrites on disk (last write wins),
	// so the hash we verify must reflect the same collapse, not both writes.
	hashed := map[string]skillhash.File{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue // symlinks, dirs, etc: never materialized or followed
		}
		entries++
		if entries > maxEntries {
			return fmt.Errorf("archive exceeds %d entries", maxEntries)
		}
		rel := filepath.Clean(h.Name)
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q: escapes destination", h.Name)
		}
		if h.Size < 0 {
			return fmt.Errorf("archive entry %q: negative size", h.Name)
		}
		total += h.Size
		if total > maxExtracted {
			return fmt.Errorf("archive exceeds %d bytes", maxExtracted)
		}

		content, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("archive entry %q: %w", h.Name, err)
		}
		if int64(len(content)) != h.Size {
			return fmt.Errorf("archive entry %q: short read (%d of %d bytes)", h.Name, len(content), h.Size)
		}

		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		// Only the exec bit survives from the archive header; skillhash.Mode
		// masks everything else, so a hostile archive cannot request setuid,
		// setgid or sticky bits, and the mode we write is the mode the hash
		// below is computed over.
		exec := h.Mode&0o111 != 0
		if err := os.WriteFile(p, content, os.FileMode(skillhash.Mode(exec))); err != nil {
			return err
		}
		hashed[rel] = skillhash.File{Path: rel, Data: content, Exec: exec}
	}

	files := make([]skillhash.File, 0, len(hashed))
	for _, f := range hashed {
		files = append(files, f)
	}
	if got := skillhash.Sum(files); got != wantHash {
		return fmt.Errorf("content hash mismatch: want %s, got %s", wantHash, got)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// A concurrent Ensure won the race; its content is identical because
		// it verified against the same hash.
		if info, statErr := os.Stat(dst); statErr == nil && info.IsDir() {
			return nil
		}
		return err
	}
	return nil
}

func swapSymlink(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	suffix, err := randSuffix()
	if err != nil {
		return err
	}
	tmp := link + ".tmp-" + suffix
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op once rename consumes it; cleans up on failure
	return os.Rename(tmp, link)
}

func randSuffix() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand suffix: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
