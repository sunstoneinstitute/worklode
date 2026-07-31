// Package skillstore manages the local content-addressed skill cache:
// <root>/.store/<hash>/ holds unpacked skill dirs; <root>/<name> is a
// symlink to the current version. Concurrent worktrees can hold different
// versions because store dirs are immutable once extracted.
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
)

// maxExtracted caps the unpacked size of one skill version. A var, not a
// const, so tests can lower it instead of building an 8 MiB fixture.
var maxExtracted int64 = 8 << 20

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

// Path returns the stable per-name path (the symlink), whether or not it
// exists yet. Callers print this in briefs.
func Path(root, name string) string { return filepath.Join(root, name) }

func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".." && !strings.HasPrefix(name, ".")
}

func validHash(hash string) bool {
	if len(hash) < 6 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// Ensure makes <root>/<name> point at the unpacked version identified by
// hash, calling fetch for the tar.gz only when that version is not already
// in the store. Returns the symlink path.
func Ensure(root, name, hash string, fetch func() ([]byte, error)) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("skill name %q: invalid", name)
	}
	if !validHash(hash) {
		return "", fmt.Errorf("skill hash %q: invalid", hash)
	}
	dst := filepath.Join(root, ".store", hash)
	if _, err := os.Stat(dst); err != nil {
		data, err := fetch()
		if err != nil {
			return "", fmt.Errorf("fetch skill %s@%s: %w", name, hash, err)
		}
		if err := extract(data, dst); err != nil {
			return "", fmt.Errorf("extract skill %s@%s: %w", name, hash, err)
		}
	}
	link := Path(root, name)
	if err := swapSymlink(dst, link); err != nil {
		return "", fmt.Errorf("link skill %s: %w", name, err)
	}
	return link, nil
}

// extract unpacks tgz into a sibling tmp dir, then renames it into place at
// dst. The rename is the commit point: a half-extracted archive never
// becomes visible at dst, and dst is immutable once it exists.
func extract(tgz []byte, dst string) error {
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

		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		// Only the exec bit survives from the archive header; mask everything
		// else so a hostile archive can't request setuid/setgid/sticky bits.
		perm := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			perm = 0o755
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			return err
		}
		n, cerr := io.Copy(f, tr)
		cerr2 := f.Close()
		if cerr != nil {
			return cerr
		}
		if cerr2 != nil {
			return cerr2
		}
		if n != h.Size {
			return fmt.Errorf("archive entry %q: short write (%d of %d bytes)", h.Name, n, h.Size)
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// A concurrent Ensure won the race; its content is identical.
		if _, statErr := os.Stat(dst); statErr == nil {
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
	return os.Rename(tmp, link)
}

func randSuffix() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand suffix: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
