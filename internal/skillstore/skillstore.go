// Package skillstore manages the local content-addressed skill cache.
//
// <root>/.store/<hash>/ holds unpacked skill dirs and is the canonical
// location: immutable once extracted, one dir per version, so worktrees
// briefed against different versions of one skill never collide. That is the
// path Ensure returns and the path a brief should carry.
//
// <root>/<name> is a symlink to the most recently installed version — a
// convenience for humans browsing the cache. It holds one version at a time,
// so nothing that needs a specific version may depend on it.
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

// maxExtracted caps the unpacked size of one skill version, and maxEntries
// caps the file count — bytes alone don't stop an archive of many
// zero-byte files from exhausting inodes. Both are vars, not consts, so
// tests can lower them instead of building huge fixtures.
var (
	maxExtracted int64 = 8 << 20
	maxEntries         = 2000
)

// Root returns the local skill dir: $LODE_SKILLS_DIR or ~/.worklode/skills.
func Root() (string, error) {
	if v := os.Getenv("LODE_SKILLS_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("skill store root: %w", err)
	}
	return filepath.Join(home, ".worklode", "skills"), nil
}

// Path returns the by-name symlink <root>/<name>, whether or not it exists
// yet. It points at whichever version was installed last, so it is for humans
// only; anything needing a particular version uses the path Ensure returns.
func Path(root, name string) string { return filepath.Join(root, name) }

func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".." && !strings.HasPrefix(name, ".")
}

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
// the canonical <root>/.store/<hash> path: spec 016 requires two worktrees
// briefed against different hashes of one skill to resolve valid paths
// simultaneously, and the single <root>/<name> symlink cannot do that — the
// second install would repoint the first's path at the other version.
//
// <root>/<name> is still repointed here, as the human-facing pointer to the
// most recent install.
func Ensure(root, name, hash string, fetch func() ([]byte, error)) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("skill name %q: invalid", name)
	}
	if !validHash(hash) {
		return "", fmt.Errorf("skill hash %q: invalid", hash)
	}
	dst := filepath.Join(root, ".store", hash)
	if info, err := os.Stat(dst); err != nil || !info.IsDir() {
		data, err := fetch()
		if err != nil {
			return "", fmt.Errorf("fetch skill %s@%s: %w", name, hash, err)
		}
		if err := extract(data, dst, hash); err != nil {
			return "", fmt.Errorf("extract skill %s@%s: %w", name, hash, err)
		}
	}
	link := Path(root, name)
	// The symlink target is store-relative (".store/<hash>", not the
	// absolute or root-prefixed dst): a symlink resolves relative to its
	// own directory, which is always root — so this works whether root
	// itself is relative or absolute, and keeps the store relocatable.
	if err := swapSymlink(filepath.Join(".store", hash), link); err != nil {
		return "", fmt.Errorf("link skill %s: %w", name, err)
	}
	return dst, nil
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
		// Only the exec bit survives from the archive header; mask everything
		// else so a hostile archive can't request setuid/setgid/sticky bits.
		perm := os.FileMode(0o644)
		exec := h.Mode&0o111 != 0
		if exec {
			perm = 0o755
		}
		if err := os.WriteFile(p, content, perm); err != nil {
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
