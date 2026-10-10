package iri_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// handBuilt matches a Go string literal that starts a wlid: IRI with a path,
// e.g. "wlid:actor/" or "wlid:doc/%s": a caller concatenating an IRI
// instead of calling this package (WL-REQ-249).
var handBuilt = regexp.MustCompile(`"wlid:[a-z]`)

func TestNoHandBuiltIRIs(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == ".worktrees" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // walking this repo's own source
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if handBuilt.MatchString(line) {
				offenders = append(offenders, path+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("wlid: IRI built by hand at %s; use internal/kg/iri and iri.CURIE", strings.Join(offenders, ", "))
	}
}
