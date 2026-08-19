package model_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// mode says how much of the ADR 036 rule set a scanned package is held to.
type mode int

const (
	// modeWire: no json-tagged struct may be declared here at all. Both ends
	// of these bodies are ours — internal/api encodes what internal/cli
	// decodes — so a declaration here is a second declaration of a shape that
	// can only be right once (§2).
	modeWire mode = iota
	// modeNamed: named json-tagged declarations are fine, anonymous ones are
	// not. internal/cmd's json-tagged types are `--json` stdout contracts:
	// they cross no HTTP boundary and have one declaration by construction,
	// so §2's test does not select them. They must still be named — a
	// contract nobody can grep for is one nobody reviews. The consequence is
	// deliberate: a *named* struct here that decodes an HTTP response is not
	// reported, and review is what catches it.
	modeNamed
	// modeBodies: struct declarations are out of scope, only outbound bodies
	// are checked. internal/hooks' json-tagged types are GitHub's and Flux's
	// inbound payload shapes — foreign schemas worklode does not own and
	// internal/model does not model. What it answers with is ours.
	modeBodies
)

// scanned are the packages the rules are checked against, as paths relative
// to internal/model.
var scanned = map[string]mode{
	"../api":   modeWire,
	"../cli":   modeWire,
	"../cmd":   modeNamed,
	"../hooks": modeBodies,
}

// allowed lists the json-tagged structs that legitimately stay outside
// internal/model in a strict package: transport internals that are serialized
// into a cookie, a state parameter, or a local file rather than into an HTTP
// body (ADR 036 §3). The value says where each one is serialized; a type that
// has no such answer is a wire shape and belongs in internal/model. Keys are
// package-scoped, so exempting internal/api's oauthState does not silently
// exempt a same-named type in internal/cli. An entry no declaration matches
// is reported as stale, the way router.go treats an unused route guard.
var allowed = map[string]string{
	"api.oauthState":  "internal/api/session.go — signed into the oauth-state cookie",
	"api.cliIntent":   "internal/api/session.go — signed into the CLI-intent cookie",
	"api.stateChange": "internal/api/web.go — decoded from a stored state_log row",
	// internal/cli/remotecache.go — the four shapes of the on-disk cache at
	// ~/.cache/worklode/remotes.json. Nothing sends or receives them.
	"cli.remoteEntry": "internal/cli/remotecache.go — on-disk remote cache",
	"cli.keyEntry":    "internal/cli/remotecache.go — on-disk remote cache",
	"cli.serverCache": "internal/cli/remotecache.go — on-disk remote cache",
	"cli.remoteCache": "internal/cli/remotecache.go — on-disk remote cache",
}

// bodyArg names the functions that hand a Go value to an HTTP body, and which
// argument carries it. A map literal there is a wire shape nobody declared —
// the loophole a struct-declaration check cannot see, since there is no
// struct to find.
//
// What this does not see: a body assembled by a helper that returns a map, or
// marshalled to bytes first. Those are deliberate work to arrange; the cases
// here are what a handler reaches for by accident.
var bodyArg = map[string]int{
	"writeJSON": 2, // internal/api: writeJSON(w, code, body)
	"do":        3, // internal/cli: (*Client).do(ctx, method, path, body)
}

// wireTagged reports whether any field of st carries a json tag.
func wireTagged(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		if f.Tag != nil && strings.Contains(f.Tag.Value, `json:"`) {
			return true
		}
	}
	return false
}

// TestNoWireStructsOutsideModel enforces ADR 036 §2: a value that crosses the
// HTTP boundary has exactly one declaration, in internal/model. It checks the
// three ways a second one gets in — a named struct, an anonymous struct, and
// a map literal used as a body.
func TestNoWireStructsOutsideModel(t *testing.T) {
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for dir, m := range scanned {
		pkg := filepath.Base(dir)
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		parsed := 0
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			parsed++
			for _, msg := range checkFile(fset, pkg, m, path, file, seen) {
				t.Error(msg)
			}
		}
		// Without this, renaming or splitting a package turns the guard green
		// while it inspects nothing at all.
		if parsed == 0 {
			t.Errorf("%s: no files parsed — the guard checked nothing; "+
				"did the package move? update scanned", dir)
		}
	}
	for key, why := range allowed {
		if !seen[key] {
			t.Errorf("allowed[%q] (%s) matches no json-tagged declaration — "+
				"drop the entry", key, why)
		}
	}
}

// checkFile returns one message per violation in file. It returns them
// rather than failing directly so the guard itself can be tested against
// known-bad source (TestGuardCatchesTheDodges) — a check nobody checks is how
// the previous version came to inspect nothing.
func checkFile(fset *token.FileSet, pkg string, m mode, path string, file *ast.File, seen map[string]bool) []string {
	var msgs []string
	if m != modeBodies {
		msgs = checkStructs(fset, pkg, m == modeWire, path, file, seen)
	}
	return append(msgs, checkBodies(fset, path, file)...)
}

// checkStructs reports json-tagged structs declared outside internal/model.
// Anonymous ones are reported in every scanned package: deleting the type
// name is the cheapest way to dodge a declaration check, and an undeclared
// body is exactly what ADR 036 exists to prevent.
func checkStructs(fset *token.FileSet, pkg string, strict bool, path string, file *ast.File, seen map[string]bool) (msgs []string) {
	named := map[*ast.StructType]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			if st, ok := ts.Type.(*ast.StructType); ok {
				named[st] = ts.Name.Name
			}
		}
		return true
	})
	base := filepath.Base(path)
	ast.Inspect(file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || !wireTagged(st) {
			return true
		}
		name, isNamed := named[st]
		if !isNamed {
			if strict {
				msgs = append(msgs, fmt.Sprintf(
					"%s:%d: anonymous struct with json tags (ADR 036 §2) — "+
						"a shape crossing the wire is declared once, in internal/model",
					base, fset.Position(st.Pos()).Line))
			} else {
				msgs = append(msgs, fmt.Sprintf(
					"%s:%d: anonymous struct with json tags — name it (a --json "+
						"contract nobody can grep for is one nobody reviews), or use "+
						"the internal/model type if it is an HTTP body",
					base, fset.Position(st.Pos()).Line))
			}
			return true
		}
		key := pkg + "." + name
		if _, ok := allowed[key]; ok {
			seen[key] = true
			// Exempt the whole type: a nested shape inside a cookie payload
			// is serialized by the same non-HTTP path.
			return false
		}
		if strict {
			msgs = append(msgs, fmt.Sprintf(
				"%s:%d: %s has json tags outside internal/model (ADR 036 §2) — "+
					"move it, or add %q to allowed with a reason",
				base, fset.Position(st.Pos()).Line, name, key))
			// One finding per type: a nested anonymous struct inside a type
			// already reported adds noise, not information.
			return false
		}
		return true
	})
	return msgs
}

// checkBodies reports map literals handed to an HTTP body argument, including
// one built up across a few statements — the shape of the conditional error
// body a handler writes by hand.
func checkBodies(fset *token.FileSet, path string, file *ast.File) (msgs []string) {
	base := filepath.Base(path)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		mapLocal := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, rhs := range as.Rhs {
				if !isMapValue(rhs) || i >= len(as.Lhs) {
					continue
				}
				if id, ok := as.Lhs[i].(*ast.Ident); ok {
					mapLocal[id.Name] = true
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var fname string
			switch f := call.Fun.(type) {
			case *ast.Ident:
				fname = f.Name
			case *ast.SelectorExpr:
				fname = f.Sel.Name
			}
			i, ok := bodyArg[fname]
			if !ok || i >= len(call.Args) {
				return true
			}
			arg := call.Args[i]
			id, isIdent := arg.(*ast.Ident)
			if !isMapValue(arg) && !(isIdent && mapLocal[id.Name]) {
				return true
			}
			msgs = append(msgs, fmt.Sprintf(
				"%s:%d: %s is handed a map as its HTTP body (ADR 036 §2) — "+
					"declare the shape in internal/model",
				base, fset.Position(call.Pos()).Line, fname))
			return true
		})
	}
	return msgs
}

// TestModelDeclaresNoUntypedMaps closes the loophole the checks above cannot
// see: a `map[string]any` nested inside a declared type. Moving a shape into
// internal/model and leaving it a map satisfies every other rule here while
// keeping exactly the problem ADR 036 exists to fix — an envelope with a name
// around entries with none. `model.TimelineResponse.Timeline` was that for one
// release; it is `[]TimelineEntry` now (§8).
//
// Only `any`-valued maps are reported. A `map[string]string` is a dictionary
// whose shape is fully stated; a `map[string]any` is a shape nobody wrote down.
func TestModelDeclaresNoUntypedMaps(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	parsed := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		parsed++
		for _, msg := range checkUntypedMaps(fset, path, file) {
			t.Error(msg)
		}
	}
	if parsed == 0 {
		t.Error("no files parsed — the guard checked nothing")
	}
}

// checkUntypedMaps reports json-tagged fields whose type is, or contains, a
// map with an `any` value.
func checkUntypedMaps(fset *token.FileSet, path string, file *ast.File) (msgs []string) {
	base := filepath.Base(path)
	ast.Inspect(file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil || !strings.Contains(f.Tag.Value, `json:"`) {
				continue
			}
			if !containsAnyMap(f.Type) {
				continue
			}
			name := "field"
			if len(f.Names) > 0 {
				name = f.Names[0].Name
			}
			msgs = append(msgs, fmt.Sprintf(
				"%s:%d: %s is a map[...]any on the wire (ADR 036 §8) — declare "+
					"the entry shape as a struct, or json.RawMessage if it is an "+
					"opaque stored payload passing through",
				base, fset.Position(f.Pos()).Line, name))
		}
		return true
	})
	return msgs
}

// containsAnyMap reports whether e is, or wraps, a map whose value type is
// `any` — `map[string]any`, `[]map[string]any`, `*map[string]any`,
// `map[string][]any`. It does not see a map behind a named type
// (`type payload map[string]any`); that is a declaration to review, not a
// field type to read.
func containsAnyMap(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.MapType:
		return containsAny(v.Value) || containsAnyMap(v.Value)
	case *ast.ArrayType:
		return containsAnyMap(v.Elt)
	case *ast.StarExpr:
		return containsAnyMap(v.X)
	}
	return false
}

// containsAny reports whether e is `any` (or the `interface{}` it spells),
// alone or under slices and pointers.
func containsAny(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name == "any"
	case *ast.InterfaceType:
		return v.Methods.NumFields() == 0
	case *ast.ArrayType:
		return containsAny(v.Elt)
	case *ast.StarExpr:
		return containsAny(v.X)
	}
	return false
}

// TestUntypedMapGuardCatchesTheDodges holds checkUntypedMaps to the same bar
// as the checks above: each shape it claims to catch must actually produce a
// finding, and the shapes that are fine must not.
func TestUntypedMapGuardCatchesTheDodges(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"map field", "type R struct {\n\tX map[string]any `json:\"x\"`\n}", true},
		{"slice of maps", "type R struct {\n\tX []map[string]any `json:\"x\"`\n}", true},
		{"pointer to map", "type R struct {\n\tX *map[string]any `json:\"x\"`\n}", true},
		{"empty interface value", "type R struct {\n\tX map[string]interface{} `json:\"x\"`\n}", true},
		{"map of slices of any", "type R struct {\n\tX map[string][]any `json:\"x\"`\n}", true},
		{"map of maps", "type R struct {\n\tX map[string]map[string]any `json:\"x\"`\n}", true},
		{"typed dictionary", "type R struct {\n\tX map[string]string `json:\"x\"`\n}", false},
		{"map of a named type", "type R struct {\n\tX map[string]Entry `json:\"x\"`\n}", false},
		{"raw payload", "type R struct {\n\tX json.RawMessage `json:\"x\"`\n}", false},
		{"untagged field", "type R struct {\n\tX map[string]any\n}", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "bad.go", "package model\n"+tc.src, 0)
			if err != nil {
				t.Fatalf("parse case source: %v", err)
			}
			got := checkUntypedMaps(fset, "bad.go", file)
			if tc.want != (len(got) > 0) {
				t.Errorf("finding = %v, want a finding: %v", got, tc.want)
			}
		})
	}
}

// isMapValue reports whether e builds a map: a composite literal or a make.
func isMapValue(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.CompositeLit:
		_, ok := v.Type.(*ast.MapType)
		return ok
	case *ast.CallExpr:
		id, ok := v.Fun.(*ast.Ident)
		if !ok || id.Name != "make" || len(v.Args) == 0 {
			return false
		}
		_, ok = v.Args[0].(*ast.MapType)
		return ok
	}
	return false
}

// TestGuardCatchesTheDodges runs the checks over known-bad source: every way
// of putting a wire shape outside internal/model must produce a finding, and
// the shapes that legitimately stay put must not. Without this, a refactor
// that quietly stops matching anything leaves a green test that checks
// nothing — which is what happened to the first version of this guard.
func TestGuardCatchesTheDodges(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		mode mode
		src  string
		want string // substring of the expected finding; "" means none
	}{{
		name: "named struct in a strict package", pkg: "api", mode: modeWire,
		src:  "type taskJSON struct {\n\tID string `json:\"id\"`\n}",
		want: "has json tags outside internal/model",
	}, {
		name: "anonymous struct in a strict package", pkg: "api", mode: modeWire,
		src:  "func h() {\n\tvar req struct {\n\t\tDoneState string `json:\"done_state\"`\n\t}\n\t_ = req\n}",
		want: "anonymous struct with json tags",
	}, {
		name: "anonymous struct in internal/cmd", pkg: "cmd", mode: modeNamed,
		src:  "func h() {\n\tout := struct {\n\t\tOK bool `json:\"ok\"`\n\t}{}\n\t_ = out\n}",
		want: "name it",
	}, {
		name: "named --json shape in internal/cmd", pkg: "cmd", mode: modeNamed,
		src:  "type statusResult struct {\n\tOK bool `json:\"ok\"`\n}",
		want: "",
	}, {
		name: "map literal written as a body", pkg: "api", mode: modeWire,
		src:  `func h() { writeJSON(w, 200, map[string]any{"status": "ok"}) }`,
		want: "handed a map as its HTTP body",
	}, {
		name: "map body built up over statements", pkg: "api", mode: modeWire,
		src: "func h() {\n\tbody := map[string]any{\"error\": \"nope\"}\n" +
			"\tbody[\"holder\"] = 1\n\twriteJSON(w, 409, body)\n}",
		want: "handed a map as its HTTP body",
	}, {
		name: "map body from make", pkg: "api", mode: modeWire,
		src: "func h() {\n\tbody := make(map[string]any)\n" +
			"\tbody[\"error\"] = \"nope\"\n\twriteJSON(w, 500, body)\n}",
		want: "handed a map as its HTTP body",
	}, {
		name: "map literal sent as a request body", pkg: "cli", mode: modeWire,
		src:  `func h() { c.do(ctx, "POST", "/api/v1/merges", map[string]any{"repo": r}) }`,
		want: "handed a map as its HTTP body",
	}, {
		name: "model type written as a body", pkg: "api", mode: modeWire,
		src:  `func h() { writeJSON(w, 200, model.HealthResponse{Status: "ok"}) }`,
		want: "",
	}, {
		name: "an allowed transport internal", pkg: "api", mode: modeWire,
		src:  "type oauthState struct {\n\tNonce string `json:\"nonce\"`\n}",
		want: "",
	}, {
		// allowed is keyed by package: api's exemption is not cli's.
		name: "an allowed name declared in another package", pkg: "cli", mode: modeWire,
		src:  "type oauthState struct {\n\tNonce string `json:\"nonce\"`\n}",
		want: "has json tags outside internal/model",
	}, {
		name: "a foreign inbound payload in internal/hooks", pkg: "hooks", mode: modeBodies,
		src:  "type pushEvent struct {\n\tRef string `json:\"ref\"`\n}",
		want: "",
	}, {
		name: "a map body answered by internal/hooks", pkg: "hooks", mode: modeBodies,
		src:  `func h() { writeJSON(w, 200, map[string]string{"status": "ok"}) }`,
		want: "handed a map as its HTTP body",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "bad.go", "package p\n"+tc.src, 0)
			if err != nil {
				t.Fatalf("parse case source: %v", err)
			}
			got := checkFile(fset, tc.pkg, tc.mode, "bad.go", file, map[string]bool{})
			if tc.want == "" {
				if len(got) > 0 {
					t.Errorf("want no finding, got %v", got)
				}
				return
			}
			for _, msg := range got {
				if strings.Contains(msg, tc.want) {
					return
				}
			}
			t.Errorf("want a finding containing %q, got %v", tc.want, got)
		})
	}
}
