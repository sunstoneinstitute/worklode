package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRenderingStaysInCLI guards the cmd/cli split CLAUDE.md states: a
// human-readable view of an internal/model shape is a cli.*Table/cli.*Render
// function, and internal/cmd only decides what to fetch and calls one. The two
// tells that a view has drifted back here are a hand-built tabwriter (whose
// column parameters cli.newTabwriter exists to hold) and a hand-formatted
// timestamp (cli.LocalTime). Both are cheap to check and were what WL-166
// found; neither is the whole rule, so read CLAUDE.md before working around a
// failure here.
func TestRenderingStaysInCLI(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(packageDir, "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no source files found; the glob is wrong, not the package")
	}
	fset := token.NewFileSet()
	for _, path := range files {
		file := filepath.Base(path)
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, imp := range f.Imports {
			imported, _ := strconv.Unquote(imp.Path.Value)
			if imported == "text/tabwriter" {
				t.Errorf("%s imports text/tabwriter: a tabbed view belongs in internal/cli "+
					"(cli.newTabwriter), with internal/cmd calling the renderer", file)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			// Match t.Local(), the first half of the localTime formatting this
			// package used to reimplement. A caller that wants a local
			// timestamp wants cli.LocalTime.
			sel, ok := n.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Local" {
				t.Errorf("%s:%d formats a timestamp by hand: use cli.LocalTime",
					file, fset.Position(sel.Pos()).Line)
			}
			return true
		})
	}
}
