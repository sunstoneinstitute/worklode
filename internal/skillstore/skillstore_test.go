package skillstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// tarEntry is a low-level archive entry builder, used where gzTar's
// plain-regular-file shorthand isn't enough (exec bit, symlinks).
type tarEntry struct {
	Name     string
	Content  string
	Mode     int64
	Typeflag byte
	Linkname string
}

func gzTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var entries []tarEntry
	for p, c := range files {
		entries = append(entries, tarEntry{Name: p, Content: c, Mode: 0o644, Typeflag: tar.TypeReg})
	}
	return buildTar(t, entries)
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Typeflag
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.Name, Mode: e.Mode, Typeflag: typ, Linkname: e.Linkname}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.Content))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatalf("write header %q: %v", e.Name, err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.Content)); err != nil {
				t.Fatalf("write content %q: %v", e.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestEnsure(t *testing.T) {
	root := t.TempDir()
	arch := gzTar(t, map[string]string{"SKILL.md": "body", "references/notes.md": "n"})
	fetches := 0
	fetch := func() ([]byte, error) { fetches++; return arch, nil }

	p, err := Ensure(root, "tdd", "aabb01", fetch)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(p, "SKILL.md")); string(got) != "body" {
		t.Fatalf("content: %q", got)
	}
	// Second ensure with the same hash: cache hit, no fetch.
	if _, err := Ensure(root, "tdd", "aabb01", fetch); err != nil || fetches != 1 {
		t.Fatalf("cache: fetches=%d err=%v", fetches, err)
	}
	// New hash: fetch again, symlink follows.
	arch2 := gzTar(t, map[string]string{"SKILL.md": "v2"})
	if _, err := Ensure(root, "tdd", "ccdd02", func() ([]byte, error) { return arch2, nil }); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "tdd", "SKILL.md")); string(got) != "v2" {
		t.Fatalf("after upgrade: %q", got)
	}
	// Old version still present in the store for concurrent worktrees.
	if _, err := os.Stat(filepath.Join(root, ".store", "aabb01", "SKILL.md")); err != nil {
		t.Fatalf("old version gone: %v", err)
	}

	// Hostile entries rejected.
	for _, bad := range []map[string]string{
		{"../escape.md": "x"},
		{"/abs.md": "x"},
	} {
		b := bad
		if _, err := Ensure(root, "evil", "eeff03", func() ([]byte, error) { return gzTar(t, b), nil }); err == nil {
			t.Fatalf("want traversal error for %v", b)
		}
	}
	// Bad identifiers rejected.
	if _, err := Ensure(root, "a/b", "aabb01", fetch); err == nil {
		t.Fatal("want name error")
	}
	if _, err := Ensure(root, "ok", "not hex!", fetch); err == nil {
		t.Fatal("want hash error")
	}
}

// TestExtractPreservesExecBit guards against silently dropping the
// executable bit: Task 6's buildArchive carries mode into the tar header
// and folds it into the content hash specifically so scripts stay runnable.
func TestExtractPreservesExecBit(t *testing.T) {
	root := t.TempDir()
	arch := buildTar(t, []tarEntry{
		{Name: "run.sh", Content: "#!/bin/sh\necho hi\n", Mode: 0o755, Typeflag: tar.TypeReg},
		{Name: "SKILL.md", Content: "body", Mode: 0o644, Typeflag: tar.TypeReg},
	})

	p, err := Ensure(root, "exec", "aabbcc", func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	runInfo, err := os.Stat(filepath.Join(p, "run.sh"))
	if err != nil {
		t.Fatalf("stat run.sh: %v", err)
	}
	if runInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("run.sh should be executable, got mode %v", runInfo.Mode())
	}

	skillInfo, err := os.Stat(filepath.Join(p, "SKILL.md"))
	if err != nil {
		t.Fatalf("stat SKILL.md: %v", err)
	}
	if skillInfo.Mode().Perm()&0o111 != 0 {
		t.Fatalf("SKILL.md should not be executable, got mode %v", skillInfo.Mode())
	}
}

// TestExtractDotDotPrefixedNameAccepted guards the traversal check against
// the false positive of rejecting a legitimate file merely starting with
// "..", as opposed to an actual "../" escape.
func TestExtractDotDotPrefixedNameAccepted(t *testing.T) {
	root := t.TempDir()
	arch := gzTar(t, map[string]string{"..notes.md": "hidden notes"})

	p, err := Ensure(root, "dotdot", "aa1122", func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(p, "..notes.md"))
	if err != nil {
		t.Fatalf("read ..notes.md: %v", err)
	}
	if string(got) != "hidden notes" {
		t.Fatalf("content: %q", got)
	}
}

// TestExtractGenuineTraversalRejected covers escape forms that must stay
// rejected even with the exact-match fix from
// TestExtractDotDotPrefixedNameAccepted.
func TestExtractGenuineTraversalRejected(t *testing.T) {
	root := t.TempDir()
	cases := map[string]map[string]string{
		"parent-relative": {"../escape.md": "x"},
		"nested-parent":   {"sub/../../escape.md": "x"},
		"literal-dotdot":  {"..": "x"},
		"absolute":        {"/abs.md": "x"},
	}
	for name, files := range cases {
		files := files
		t.Run(name, func(t *testing.T) {
			arch := gzTar(t, files)
			if _, err := Ensure(root, "evil-"+name, "aabbcc12", func() ([]byte, error) { return arch, nil }); err == nil {
				t.Fatalf("want traversal error for %v", files)
			}
		})
	}
}

// TestExtractOverCapRejected verifies an over-cap archive is a hard error,
// not a silently truncated file.
func TestExtractOverCapRejected(t *testing.T) {
	orig := maxExtracted
	maxExtracted = 16
	defer func() { maxExtracted = orig }()

	root := t.TempDir()
	arch := gzTar(t, map[string]string{"big.txt": strings.Repeat("x", 64)})

	if _, err := Ensure(root, "big", "aa00bb", func() ([]byte, error) { return arch, nil }); err == nil {
		t.Fatal("want error for over-cap archive")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".store", "aa00bb")); !os.IsNotExist(statErr) {
		t.Fatalf("store dir should not exist after failed extract: %v", statErr)
	}
}

// TestEnsureConcurrent exercises the extract race fallback and the
// symlink-swap rename: many goroutines racing to materialize the same
// (name, hash) must all succeed and land identical content. Run with
// -race.
func TestEnsureConcurrent(t *testing.T) {
	root := t.TempDir()
	arch := gzTar(t, map[string]string{"SKILL.md": "concurrent body", "helper.sh": "x"})
	var fetches int64
	fetch := func() ([]byte, error) {
		atomic.AddInt64(&fetches, 1)
		return arch, nil
	}

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Ensure(root, "race", "1234ab", fetch); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ensure: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "race", "SKILL.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "concurrent body" {
		t.Fatalf("content: %q", got)
	}
}

// TestExtractSkipsSymlink documents that the extractor never follows or
// materializes a symlink entry. Task 6 already drops symlinks server-side,
// but the extractor should not be the weak link if that ever changes.
func TestExtractSkipsSymlink(t *testing.T) {
	root := t.TempDir()
	arch := buildTar(t, []tarEntry{
		{Name: "SKILL.md", Content: "body", Mode: 0o644, Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	})

	p, err := Ensure(root, "symlink", "aa55bb", func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(p, "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink entry should not be materialized, lstat err=%v", err)
	}
}

// TestEnsureCreatesMissingRoot covers first run on a clean machine, where
// ~/.worklode/skills (or LODE_SKILLS_DIR) doesn't exist yet.
func TestEnsureCreatesMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "does-not-exist-yet")
	arch := gzTar(t, map[string]string{"SKILL.md": "fresh"})

	p, err := Ensure(root, "fresh", "aa77cc", func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(p, "SKILL.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "fresh" {
		t.Fatalf("content: %q", got)
	}
}
