// Package worklode holds no code. It exists so a guard that must see the
// whole tree — not one package's files — has somewhere to live.
package worklode

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// maxLines is where a Go file stops being readable in one pass. An agent's
// file read is chunked past this point, so every task that opens the file
// pays for the chunking, and a file that long has stopped being one feature.
// CLAUDE.md's "a file is named after the feature it serves" convention states
// the rule; this is the half of it a test can check.
const maxLines = 2000

// grandfathered records files that were already over maxLines when the rule
// landed, at the length they were. It is a ratchet, not an exemption: a listed
// file may not grow past its recorded length, and once it drops under
// maxLines its entry must be deleted (the test says so when that happens).
// Every entry here is a file waiting to be split along its feature seam.
//
// Empty on purpose: nothing is currently over the ceiling.
var grandfathered = map[string]int{}

// skipDirs are trees that are not this repo's own Go source. Every dotted
// directory is skipped alongside them: .worktrees and .claude/worktrees hold
// checkouts of this same repo, and walking those counts every file twice.
var skipDirs = map[string]bool{
	"node_modules": true,
	"graphify-out": true,
}

// TestNoOversizedGoFiles fails on a Go file past maxLines that is not on the
// ratchet, and on a ratchet entry that has grown or is no longer needed.
// Generated files are exempt: their length is the generator's business.
func TestNoOversizedGoFiles(t *testing.T) {
	root := repoRoot(t)
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // walking this repo's own source
		if readErr != nil {
			return readErr
		}
		if isGenerated(src) {
			return nil
		}
		counts[filepath.ToSlash(rel)] = strings.Count(string(src), "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(counts) == 0 {
		t.Fatal("no Go files found; the walk is wrong, not the repo")
	}

	var offenders []string
	for _, rel := range sortedKeys(counts) {
		n := counts[rel]
		ceiling, listed := grandfathered[rel]
		switch {
		case listed && n > ceiling:
			offenders = append(offenders, fmt.Sprintf(
				"%s grew to %d lines (ratcheted at %d) — split it, don't raise the entry", rel, n, ceiling))
		case listed && n <= maxLines:
			offenders = append(offenders, fmt.Sprintf(
				"%s is down to %d lines — delete its grandfathered entry", rel, n))
		case !listed && n > maxLines:
			offenders = append(offenders, fmt.Sprintf(
				"%s is %d lines, over the %d ceiling", rel, n, maxLines))
		}
	}
	for rel := range grandfathered {
		if _, found := counts[rel]; !found {
			offenders = append(offenders, fmt.Sprintf(
				"%s is grandfathered but no longer exists — delete its entry", rel))
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("file size ceiling:\n\t%s\n\n"+
			"Split the file along the seam its declarations already show, into per-feature\n"+
			"files in the same package (CLAUDE.md, Conventions). Adding a grandfathered\n"+
			"entry is for a file that predates the rule, not for one you just grew.",
			strings.Join(offenders, "\n\t"))
	}
}

// isGenerated reports the standard generated-code marker, which by convention
// appears before the package clause.
func isGenerated(src []byte) bool {
	for i, line := range strings.Split(string(src), "\n") {
		if i > 20 || strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "// Code generated ") && strings.HasSuffix(line, " DO NOT EDIT.") {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
