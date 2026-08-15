package model_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// wireTagged reports whether any field of st carries a json tag.
func wireTagged(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		if f.Tag != nil && strings.Contains(f.Tag.Value, `json:"`) {
			return true
		}
	}
	return false
}

// allowed lists the json-tagged structs that legitimately stay outside
// internal/model: transport internals that are serialized into a cookie, a
// state parameter, or a local file rather than into an HTTP body
// (ADR 036 §3). Each entry names where it is serialized; a type that has no
// such answer is a wire shape and belongs in internal/model.
var allowed = map[string]bool{
	// internal/api/session.go — signed into the oauth-state cookie.
	"oauthState": true,
	// internal/api/session.go — signed into the CLI-intent cookie.
	"cliIntent": true,
	// internal/cli/remotecache.go — the four shapes of the on-disk cache at
	// ~/.cache/worklode/remotes.json. Nothing sends or receives them.
	"remoteEntry": true,
	"keyEntry":    true,
	"serverCache": true,
	"remoteCache": true,
}

// TestNoWireStructsOutsideModel enforces ADR 036 §2: a struct with json tags
// in internal/api or internal/cli is a wire shape, and wire shapes have
// exactly one declaration, in internal/model.
func TestNoWireStructsOutsideModel(t *testing.T) {
	fset := token.NewFileSet()
	for _, pkg := range []string{"../api", "../cli"} {
		paths, err := filepath.Glob(filepath.Join(pkg, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", pkg, err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || allowed[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if ok && wireTagged(st) {
					t.Errorf("%s: %s has json tags outside internal/model "+
						"(ADR 036 §2) — move it, or add it to allowed with a reason",
						filepath.Base(path), ts.Name.Name)
				}
				return true
			})
		}
	}
}
