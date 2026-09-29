package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestQueryParamsStayTyped guards the convention CLAUDE.md states: a route's
// query parameters are one internal/model *Params struct, decoded by
// readQuery on the server and encoded by withParams on the client, so the
// two sides cannot disagree. For every /api/v1 route registered in
// server.go with r.api, this walks the handler's own body (not functions it
// calls) and fails if the body reads the query string any other way
// (URL.Query, RawQuery, ParseForm, Form, FormValue, queryBool, queryFlag),
// if a readQuery call's struct type disagrees with routeDocs' params, or if
// routeDocs documents params the handler never reads. It also fails when a
// guarded /api/v1 route is not registered with r.api, since the walk would
// never see it.
//
// It is a tripwire, not a proof: a handler that delegates its whole body to
// a shared helper is outside what this test can see — beginEventStream's
// own r.URL.Query() call (shared by the SSE routes) never shows up here,
// same as renderrule_test.go's checks are tripwires for the cmd/cli split.
func TestQueryParamsStayTyped(t *testing.T) {
	fset := token.NewFileSet()

	server, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	routes := apiRoutes(t, server)
	if len(routes) == 0 {
		t.Fatal("no /api/v1 routes found in server.go; the parse is wrong, not the router")
	}

	// Every guarded /api/v1 route must be one this walk saw; a route
	// registered some other way would escape the check.
	var missing, extra []string
	for pattern, g := range routeGuards {
		if _, ok := routes[pattern]; isAPIPattern(pattern) && g.perm != permPublic && !ok {
			missing = append(missing, pattern)
		}
	}
	for pattern := range routes {
		if g, ok := routeGuards[pattern]; !ok || g.perm == permPublic {
			extra = append(extra, pattern)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		slices.Sort(missing)
		slices.Sort(extra)
		t.Errorf("r.api routes and guarded /api/v1 routeGuards differ: guarded but not registered with r.api: %v; registered with r.api but not a guarded route: %v",
			missing, extra)
	}

	handlers := handlerFuncs(t, fset)
	if len(handlers) == 0 {
		t.Fatal("no *server handler methods found; the glob or parse is wrong, not the package")
	}

	for pattern, name := range routes {
		fn, ok := handlers[name]
		if !ok {
			t.Errorf("%s: handler %s not found among *server methods", pattern, name)
			continue
		}

		paramsType := ""
		violations := 0
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "queryBool" || x.Name == "queryFlag" {
					t.Errorf("%s:%d %s reads the query string with %s; use readQuery so the params struct documents it",
						name, fset.Position(x.Pos()).Line, name, x.Name)
					violations++
				}
			case *ast.SelectorExpr:
				switch x.Sel.Name {
				case "FormValue", "RawQuery", "ParseForm", "Form":
					t.Errorf("%s:%d %s reads the query string with %s; use readQuery so the params struct documents it",
						name, fset.Position(x.Pos()).Line, name, x.Sel.Name)
					violations++
				}
				if x.Sel.Name == "Query" {
					if inner, ok := x.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "URL" {
						t.Errorf("%s:%d %s reads the query string with URL.Query; use readQuery so the params struct documents it",
							name, fset.Position(x.Pos()).Line, name)
						violations++
					}
				}
			case *ast.CallExpr:
				if fun, ok := x.Fun.(*ast.Ident); ok && fun.Name == "readQuery" && len(x.Args) == 2 {
					if u, ok := x.Args[1].(*ast.UnaryExpr); ok && u.Op == token.AND {
						if id, ok := u.X.(*ast.Ident); ok {
							paramsType = declaredModelType(fn.Body, id.Name)
						}
					}
				}
			}
			return true
		})
		if violations > 0 {
			continue
		}

		doc, documented := routeDocs[pattern]
		var wantType string
		if documented && doc.params != nil {
			wantType = reflect.TypeOf(doc.params).Name()
		}

		switch {
		case wantType != "" && paramsType == "":
			t.Errorf("%s: routeDocs documents params %s but %s never calls readQuery", pattern, wantType, name)
		case wantType == "" && paramsType != "":
			t.Errorf("%s: %s calls readQuery with %s but routeDocs documents no params for this route", pattern, name, paramsType)
		case wantType != "" && paramsType != "" && wantType != paramsType:
			t.Errorf("%s: %s calls readQuery with %s but routeDocs documents params %s", pattern, name, paramsType, wantType)
		}
	}
}

// apiRoutes returns every /api/v1 pattern registered with r.api in server.go,
// mapped to the *server method name it routes to.
func apiRoutes(t *testing.T, f *ast.File) map[string]string {
	t.Helper()
	routes := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "api" || len(call.Args) != 2 {
			return true
		}
		if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "r" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.Contains(pattern, "/api/v1") {
			return true
		}
		handlerSel, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok {
			t.Errorf("r.api(%q, ...): handler is not a plain s.<method> selector", pattern)
			return true
		}
		if recv, ok := handlerSel.X.(*ast.Ident); !ok || recv.Name != "s" {
			t.Errorf("r.api(%q, ...): handler receiver is not s", pattern)
			return true
		}
		routes[pattern] = handlerSel.Sel.Name
		return true
	})
	return routes
}

// handlerFuncs parses every non-test source file in the package directory
// and returns each *server method by name.
func handlerFuncs(t *testing.T, fset *token.FileSet) map[string]*ast.FuncDecl {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	handlers := map[string]*ast.FuncDecl{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "server" {
				handlers[fn.Name.Name] = fn
			}
		}
	}
	return handlers
}

// declaredModelType looks inside body for ident's declaration as an
// internal/model type — `var <ident> model.T` or `<ident> := model.T{}` —
// and returns "T", or "" if neither form is found.
func declaredModelType(body *ast.BlockStmt, ident string) string {
	found := ""
	ast.Inspect(body, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.ValueSpec:
			for _, name := range decl.Names {
				if name.Name == ident {
					if sel, ok := decl.Type.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "model" {
							found = sel.Sel.Name
						}
					}
				}
			}
		case *ast.AssignStmt:
			if decl.Tok != token.DEFINE || len(decl.Lhs) != len(decl.Rhs) {
				return true
			}
			for i, lhs := range decl.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != ident {
					continue
				}
				lit, ok := decl.Rhs[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if sel, ok := lit.Type.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "model" {
						found = sel.Sel.Name
					}
				}
			}
		}
		return true
	})
	return found
}
