package skillstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/skillhash"
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

// hashEntries computes the content hash Ensure will require of an archive
// built from entries, using the same rule extract applies: only regular
// files count, keyed by their cleaned path. Tests that expect Ensure to
// succeed must pass this value, now that fetched content is verified
// against it.
func hashEntries(entries []tarEntry) string {
	var files []skillhash.File
	for _, e := range entries {
		typ := e.Typeflag
		if typ == 0 {
			typ = tar.TypeReg
		}
		if typ != tar.TypeReg {
			continue
		}
		files = append(files, skillhash.File{
			Path: filepath.Clean(e.Name),
			Data: []byte(e.Content),
			Exec: e.Mode&0o111 != 0,
		})
	}
	return skillhash.Sum(files)
}

func hashFiles(files map[string]string) string {
	entries := make([]tarEntry, 0, len(files))
	for p, c := range files {
		entries = append(entries, tarEntry{Name: p, Content: c, Mode: 0o644, Typeflag: tar.TypeReg})
	}
	return hashEntries(entries)
}

func manyEntries(n int) []tarEntry {
	entries := make([]tarEntry, n)
	for i := range entries {
		entries[i] = tarEntry{Name: fmt.Sprintf("f%04d", i), Mode: 0o644, Typeflag: tar.TypeReg}
	}
	return entries
}

// testDirs builds a links/store pair under a fresh temp dir, mirroring the
// sibling relationship DefaultDirs computes for the real ~/.worklode tree.
func testDirs(t *testing.T) Dirs {
	t.Helper()
	root := t.TempDir()
	return Dirs{Links: filepath.Join(root, "skills"), Store: filepath.Join(root, "store")}
}

func TestDefaultDirs(t *testing.T) {
	t.Setenv("LODE_SKILLS_DIR", "/x/skills")
	d, err := DefaultDirs()
	if err != nil || d.Links != "/x/skills" || d.Store != "/x/store" {
		t.Fatalf("dirs = %+v, %v", d, err)
	}
}

func TestEnsure(t *testing.T) {
	dirs := testDirs(t)
	files1 := map[string]string{"SKILL.md": "body", "references/notes.md": "n"}
	arch := gzTar(t, files1)
	hash1 := hashFiles(files1)
	fetches := 0
	fetch := func() ([]byte, error) { fetches++; return arch, nil }

	p, err := Ensure(dirs, "tdd", hash1, fetch)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(p, "SKILL.md")); string(got) != "body" {
		t.Fatalf("content: %q", got)
	}
	// Second ensure with the same hash: cache hit, no fetch.
	if _, err := Ensure(dirs, "tdd", hash1, fetch); err != nil || fetches != 1 {
		t.Fatalf("cache: fetches=%d err=%v", fetches, err)
	}
	// New hash: fetch again, symlink follows.
	files2 := map[string]string{"SKILL.md": "v2"}
	arch2 := gzTar(t, files2)
	hash2 := hashFiles(files2)
	if _, err := Ensure(dirs, "tdd", hash2, func() ([]byte, error) { return arch2, nil }); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dirs.Links, "tdd", "SKILL.md")); string(got) != "v2" {
		t.Fatalf("after upgrade: %q", got)
	}
	// Old version still present in the store for concurrent worktrees.
	if _, err := os.Stat(filepath.Join(dirs.Store, hash1, "SKILL.md")); err != nil {
		t.Fatalf("old version gone: %v", err)
	}

	// Hostile entries rejected. The traversal check fires before content is
	// ever hashed, so the hash argument here doesn't need to be correct.
	for _, bad := range []map[string]string{
		{"../escape.md": "x"},
		{"/abs.md": "x"},
	} {
		b := bad
		if _, err := Ensure(dirs, "evil", "eeff03", func() ([]byte, error) { return gzTar(t, b), nil }); err == nil {
			t.Fatalf("want traversal error for %v", b)
		}
	}
	// Bad identifiers rejected.
	if _, err := Ensure(dirs, "a/b", hash1, fetch); err == nil {
		t.Fatal("want name error")
	}
	if _, err := Ensure(dirs, "ok", "not hex!", fetch); err == nil {
		t.Fatal("want hash error")
	}
}

// TestEnsurePlacesVersionsInStoreDir is the split's point: harnesses walk the
// links dir looking for skill names, so it must hold name symlinks and
// nothing else — no ".store", no hash-named dirs (spec 024 acceptance 2).
func TestEnsurePlacesVersionsInStoreDir(t *testing.T) {
	dirs := testDirs(t)
	files := map[string]string{"SKILL.md": "body"}
	arch := gzTar(t, files)
	hash := hashFiles(files)

	p, err := Ensure(dirs, "tdd", hash, func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	wantPath := filepath.Join(dirs.Store, hash)
	if p != wantPath {
		t.Fatalf("returned path = %q, want %q", p, wantPath)
	}

	target, err := os.Readlink(filepath.Join(dirs.Links, "tdd"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	wantTarget := filepath.Join("..", "store", hash)
	if target != wantTarget {
		t.Fatalf("symlink target = %q, want %q", target, wantTarget)
	}

	entries, err := os.ReadDir(dirs.Links)
	if err != nil {
		t.Fatalf("read links dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == ".store" {
			t.Fatalf("links dir still holds a .store entry")
		}
		if e.Type()&os.ModeSymlink == 0 {
			t.Fatalf("links dir holds a non-symlink entry %q", e.Name())
		}
	}
}

// TestEnsureMigratesLegacyStore covers the pre-spec-024 layout: hash dirs and
// name symlinks under <links>/.store. Ensure must migrate it, silently, on
// its way to installing an unrelated skill.
func TestEnsureMigratesLegacyStore(t *testing.T) {
	base := t.TempDir()
	links := filepath.Join(base, "skills")
	dirs := Dirs{Links: links, Store: filepath.Join(base, "store")}

	// Build the legacy layout by hand: links/.store/<hash>/SKILL.md plus
	// links/<name> -> .store/<hash>.
	legacyFiles := map[string]string{"SKILL.md": "legacy body"}
	legacyHash := hashFiles(legacyFiles)
	legacyDir := filepath.Join(links, ".store", legacyHash)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("setup legacy store dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "SKILL.md"), []byte("legacy body"), 0o644); err != nil {
		t.Fatalf("setup legacy skill file: %v", err)
	}
	if err := os.Symlink(filepath.Join(".store", legacyHash), filepath.Join(links, "legacy")); err != nil {
		t.Fatalf("setup legacy symlink: %v", err)
	}

	// Run Ensure for a DIFFERENT skill.
	otherFiles := map[string]string{"SKILL.md": "other body"}
	otherArch := gzTar(t, otherFiles)
	otherHash := hashFiles(otherFiles)
	if _, err := Ensure(dirs, "other", otherHash, func() ([]byte, error) { return otherArch, nil }); err != nil {
		t.Fatalf("ensure other: %v", err)
	}

	if _, err := os.Stat(filepath.Join(links, ".store")); !os.IsNotExist(err) {
		t.Fatalf("links/.store should be gone, stat err=%v", err)
	}

	migrated, err := os.ReadFile(filepath.Join(dirs.Store, legacyHash, "SKILL.md"))
	if err != nil || string(migrated) != "legacy body" {
		t.Fatalf("migrated content = %q, %v", migrated, err)
	}

	target, err := os.Readlink(filepath.Join(links, "legacy"))
	if err != nil {
		t.Fatalf("readlink legacy: %v", err)
	}
	wantTarget := filepath.Join("..", "store", legacyHash)
	if target != wantTarget {
		t.Fatalf("legacy symlink target = %q, want %q", target, wantTarget)
	}
	resolved, err := os.Readlink(filepath.Join(links, "legacy"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(links, filepath.Dir(resolved))); err != nil {
		t.Fatalf("resolved legacy symlink target missing: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(links, "legacy", "SKILL.md")); err != nil || string(got) != "legacy body" {
		t.Fatalf("legacy symlink does not resolve: %q, %v", got, err)
	}

	// A second Ensure run finds nothing left to migrate (idempotent).
	migrateLegacyStore(dirs)
	if _, err := os.Stat(filepath.Join(links, ".store")); !os.IsNotExist(err) {
		t.Fatalf("second migration should be a no-op, stat err=%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(links, "legacy", "SKILL.md")); err != nil || string(got) != "legacy body" {
		t.Fatalf("legacy symlink broken after second migration: %q, %v", got, err)
	}
}

// TestEnsureTwoVersionsResolveSimultaneously is spec 016 acceptance
// criterion 5: two worktrees briefed against different hashes of one skill
// must both resolve valid local paths at the same time. The by-name symlink
// holds one version, so the path Ensure returns has to be the content-
// addressed one; returning the symlink made the second install silently
// repoint the first worktree's path at the other version.
func TestEnsureTwoVersionsResolveSimultaneously(t *testing.T) {
	dirs := testDirs(t)
	v1 := map[string]string{"SKILL.md": "version one"}
	v2 := map[string]string{"SKILL.md": "version two"}

	p1, err := Ensure(dirs, "tdd", hashFiles(v1), func() ([]byte, error) { return gzTar(t, v1), nil })
	if err != nil {
		t.Fatalf("ensure v1: %v", err)
	}
	p2, err := Ensure(dirs, "tdd", hashFiles(v2), func() ([]byte, error) { return gzTar(t, v2), nil })
	if err != nil {
		t.Fatalf("ensure v2: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("both versions resolved to one path: %s", p1)
	}
	for _, tc := range []struct{ path, want string }{{p1, "version one"}, {p2, "version two"}} {
		got, err := os.ReadFile(filepath.Join(tc.path, "SKILL.md"))
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s holds %q, want %q", tc.path, got, tc.want)
		}
	}
	// The by-name symlink is the convenience pointer: last install wins.
	if got, err := os.ReadFile(filepath.Join(Path(dirs.Links, "tdd"), "SKILL.md")); err != nil || string(got) != "version two" {
		t.Fatalf("by-name symlink = %q err=%v, want the most recent install", got, err)
	}
}

// TestExtractPreservesExecBit guards against silently dropping the
// executable bit: skillsync.buildArchive carries mode into the tar header
// and folds it into the content hash specifically so scripts stay runnable.
func TestExtractPreservesExecBit(t *testing.T) {
	dirs := testDirs(t)
	entries := []tarEntry{
		{Name: "run.sh", Content: "#!/bin/sh\necho hi\n", Mode: 0o755, Typeflag: tar.TypeReg},
		{Name: "SKILL.md", Content: "body", Mode: 0o644, Typeflag: tar.TypeReg},
	}
	arch := buildTar(t, entries)
	hash := hashEntries(entries)

	p, err := Ensure(dirs, "exec", hash, func() ([]byte, error) { return arch, nil })
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
	dirs := testDirs(t)
	files := map[string]string{"..notes.md": "hidden notes"}
	arch := gzTar(t, files)
	hash := hashFiles(files)

	p, err := Ensure(dirs, "dotdot", hash, func() ([]byte, error) { return arch, nil })
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
// TestExtractDotDotPrefixedNameAccepted. The traversal check fires before
// content is hashed, so an arbitrary well-formed hash is fine here.
func TestExtractGenuineTraversalRejected(t *testing.T) {
	dirs := testDirs(t)
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
			if _, err := Ensure(dirs, "evil-"+name, "aabbcc12", func() ([]byte, error) { return arch, nil }); err == nil {
				t.Fatalf("want traversal error for %v", files)
			}
		})
	}
}

// TestExtractOverCapRejected verifies an over-cap archive is a hard error,
// not a silently truncated file. It mutates the package-level maxExtracted
// var (not a const, so tests can avoid an 8 MiB fixture); this package must
// not adopt t.Parallel() while any test relies on that pattern.
func TestExtractOverCapRejected(t *testing.T) {
	orig := maxExtracted
	maxExtracted = 16
	defer func() { maxExtracted = orig }()

	dirs := testDirs(t)
	arch := gzTar(t, map[string]string{"big.txt": strings.Repeat("x", 64)})

	// The byte cap trips mid-archive, before content is ever hashed, so an
	// arbitrary well-formed hash is fine here.
	if _, err := Ensure(dirs, "big", "aa00bb", func() ([]byte, error) { return arch, nil }); err == nil {
		t.Fatal("want error for over-cap archive")
	}
	if _, statErr := os.Stat(filepath.Join(dirs.Store, "aa00bb")); !os.IsNotExist(statErr) {
		t.Fatalf("store dir should not exist after failed extract: %v", statErr)
	}
}

// TestExtractEntryCountCapped verifies the cap also bounds file count, not
// just bytes: an archive of many zero-byte entries would otherwise exhaust
// inodes without ever tripping the byte cap. Mutates the package-level
// maxEntries var; see TestExtractOverCapRejected's note on t.Parallel().
func TestExtractEntryCountCapped(t *testing.T) {
	orig := maxEntries
	maxEntries = 5
	defer func() { maxEntries = orig }()

	entries := manyEntries(6)
	arch := buildTar(t, entries)
	hash := hashEntries(entries)

	dirs := testDirs(t)
	if _, err := Ensure(dirs, "toomany", hash, func() ([]byte, error) { return arch, nil }); err == nil {
		t.Fatal("want error for archive exceeding the entry cap")
	}
	if _, statErr := os.Stat(filepath.Join(dirs.Store, hash)); !os.IsNotExist(statErr) {
		t.Fatalf("store dir should not exist after rejected extract: %v", statErr)
	}
}

// TestEnsureConcurrent exercises the extract race fallback and the
// symlink-swap rename: many goroutines racing to materialize the same
// (name, hash) must all succeed and land identical content. Run with
// -race.
func TestEnsureConcurrent(t *testing.T) {
	dirs := testDirs(t)
	files := map[string]string{"SKILL.md": "concurrent body", "helper.sh": "x"}
	arch := gzTar(t, files)
	hash := hashFiles(files)
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
			if _, err := Ensure(dirs, "race", hash, fetch); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ensure: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dirs.Links, "race", "SKILL.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "concurrent body" {
		t.Fatalf("content: %q", got)
	}
}

// TestExtractSkipsSymlink documents that the extractor never follows or
// materializes a symlink entry. skillsync.skillDirs drops symlinks at ingest,
// but the extractor should not be the weak link if that ever changes.
func TestExtractSkipsSymlink(t *testing.T) {
	dirs := testDirs(t)
	entries := []tarEntry{
		{Name: "SKILL.md", Content: "body", Mode: 0o644, Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	}
	arch := buildTar(t, entries)
	hash := hashEntries(entries) // the symlink entry does not contribute to the hash

	p, err := Ensure(dirs, "symlink", hash, func() ([]byte, error) { return arch, nil })
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
	base := filepath.Join(t.TempDir(), "nested", "does-not-exist-yet")
	dirs := Dirs{Links: filepath.Join(base, "skills"), Store: filepath.Join(base, "store")}
	files := map[string]string{"SKILL.md": "fresh"}
	arch := gzTar(t, files)
	hash := hashFiles(files)

	p, err := Ensure(dirs, "fresh", hash, func() ([]byte, error) { return arch, nil })
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

// TestEnsureRelativeRoot guards against a symlink target computed relative
// to the wrong base. A symlink's relative target resolves against the
// link's own directory, which is always dirs.Links — so the stored target
// must be relative to the links dir ("../store/<hash>"), never
// links-prefixed, or a relative links dir produces a dangling link.
func TestEnsureRelativeRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	files := map[string]string{"SKILL.md": "relative body"}
	arch := gzTar(t, files)
	hash := hashFiles(files)

	// Both relative to the test's cwd, not absolute — mirrors DefaultDirs'
	// Links/Store sibling relationship for a relative LODE_SKILLS_DIR.
	dirs := Dirs{Links: "skills", Store: "store"}
	p, err := Ensure(dirs, "tdd", hash, func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(p, "SKILL.md"))
	if err != nil {
		t.Fatalf("read via returned path: %v", err)
	}
	if string(got) != "relative body" {
		t.Fatalf("content: %q", got)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "SKILL.md")); err != nil {
		t.Fatalf("resolved symlink target missing content: %v", err)
	}
}

// TestExtractHashRoundTrip is the decisive check that extract's own hash
// accumulation agrees, byte for byte, with skillhash.Sum's encoding: an
// executable file, a non-executable file, and a nested path. If mode
// normalization disagrees between the two, this is what catches it.
func TestExtractHashRoundTrip(t *testing.T) {
	entries := []tarEntry{
		{Name: "SKILL.md", Content: "body", Mode: 0o644, Typeflag: tar.TypeReg},
		{Name: "scripts/check.sh", Content: "#!/bin/sh\necho ok\n", Mode: 0o755, Typeflag: tar.TypeReg},
		{Name: "references/nested/notes.md", Content: "nested notes", Mode: 0o644, Typeflag: tar.TypeReg},
	}
	arch := buildTar(t, entries)
	want := hashEntries(entries)

	dirs := testDirs(t)
	p, err := Ensure(dirs, "roundtrip", want, func() ([]byte, error) { return arch, nil })
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, e := range entries {
		got, err := os.ReadFile(filepath.Join(p, e.Name))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name, err)
		}
		if string(got) != e.Content {
			t.Fatalf("%s content: %q", e.Name, got)
		}
	}
}

// TestEnsureRejectsContentHashMismatch is the direct regression test for
// verifying fetched bytes against the requested hash: a truncated or
// proxied response, a stale cache, or server/client skew must not be
// stored under a hash it doesn't match.
func TestEnsureRejectsContentHashMismatch(t *testing.T) {
	dirs := testDirs(t)
	arch := gzTar(t, map[string]string{"SKILL.md": "authentic"})
	wrong := hashFiles(map[string]string{"SKILL.md": "tampered"})

	if _, err := Ensure(dirs, "evil-fetch", wrong, func() ([]byte, error) { return arch, nil }); err == nil {
		t.Fatal("want error: fetched content does not hash to the requested version")
	}
	if _, statErr := os.Stat(filepath.Join(dirs.Store, wrong)); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched content should not be committed to the store: %v", statErr)
	}
}

// TestSwapSymlinkCleansUpTmpOnFailure covers the case where <root>/<name>
// is a real, non-empty directory: the rename fails, and the .tmp-* symlink
// must not be left behind for every retry to accumulate another one of.
func TestSwapSymlinkCleansUpTmpOnFailure(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "tdd")
	if err := os.MkdirAll(filepath.Join(link, "sub"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := swapSymlink(filepath.Join(".store", "aabbcc"), link); err == nil {
		t.Fatal("want error: link is an existing non-empty directory")
	}

	matches, err := filepath.Glob(link + ".tmp-*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("tmp symlink leaked: %v", matches)
	}
}

// TestEnsureRejectsNonDirStoreEntry covers a <store>/<hash> that exists but
// is a plain file, not a directory — e.g. a partial write from an
// incompatible process. Ensure must neither treat it as a cache hit (skip
// the fetch) nor let extract's concurrent-loser fallback swallow the rename
// failure and report success having committed nothing.
func TestEnsureRejectsNonDirStoreEntry(t *testing.T) {
	dirs := testDirs(t)
	files := map[string]string{"SKILL.md": "x"}
	hash := hashFiles(files)

	storePath := filepath.Join(dirs.Store, hash)
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(storePath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	fetches := 0
	fetch := func() ([]byte, error) {
		fetches++
		return gzTar(t, files), nil
	}
	if _, err := Ensure(dirs, "conflict", hash, fetch); err == nil {
		t.Fatal("want error: store entry is a file, not a directory")
	}
	if fetches != 1 {
		t.Fatalf("a non-dir store entry should not be treated as a cache hit: fetches=%d", fetches)
	}
}

// TestValidHashLowercaseOnly pins hash validation to lowercase hex.
// Uppercase would collide with a lowercase store dir on a case-insensitive
// filesystem (macOS default APFS), silently mixing two versions.
func TestValidHashLowercaseOnly(t *testing.T) {
	cases := map[string]bool{
		"aabbcc": true,
		"AABBCC": false,
		"AaBbCc": false,
		"abcxyz": false, // not hex at all
		"abc":    false, // too short
		"aabbc":  false, // odd length
	}
	for h, want := range cases {
		if got := validHash(h); got != want {
			t.Errorf("validHash(%q) = %v, want %v", h, got, want)
		}
	}
}
