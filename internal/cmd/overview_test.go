package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

func TestDeriveDryRunPrintsTriples(t *testing.T) {
	// A minimal repo: manifest + one Go file per component is not needed for
	// layout; imports are skipped when go list fails (reported, not fatal).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	man := `repo: github.com/acme/app
components:
  - iri: https://worklode.io/ns/id/component/github.com/acme/app
    name: app
    paths: ["**"]
`
	if err := os.WriteFile(filepath.Join(root, ".worklode", "components.yaml"),
		[]byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", true, nil, derive.Options{})
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if !strings.Contains(out, "id/repo/github.com/acme/app") ||
		!strings.Contains(out, "dc/terms/hasPart") {
		t.Fatalf("dry-run output missing layout triples:\n%s", out)
	}
}

func TestDeriveRequiresManifest(t *testing.T) {
	_, err := runDeriveLocal(t.Context(), t.TempDir(), "github.com", "acme", "app", true, nil, derive.Options{})
	if err == nil || !strings.Contains(err.Error(), "components.yaml") {
		t.Fatalf("err = %v; want a missing-manifest error naming the file", err)
	}
}

// TestDeriveDryRunNamesAnEmptyDocument: a whole-repo component drops every
// import edge as intra-component, so go-imports comes out empty — worklode's
// own case. The dry run must say so rather than print a bare graph header
// with nothing beneath it, which reads as truncated output.
func TestDeriveDryRunNamesAnEmptyDocument(t *testing.T) {
	root := goRepoWithEmptyImports(t)

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", true, nil, derive.Options{})
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if !strings.Contains(out, "observed/go-imports") || !strings.Contains(out, "# (empty:") {
		t.Fatalf("dry-run output must name the empty go-imports document:\n%s", out)
	}
}

// goRepoWithEmptyImports writes a minimal Go module whose single whole-repo
// component drops every import edge as intra-component, so go-imports derives
// to nothing while repo-layout derives normally — worklode's own shape.
func goRepoWithEmptyImports(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(".worklode", "components.yaml"): `repo: github.com/acme/app
components:
  - iri: https://worklode.io/ns/id/component/github.com/acme/app
    name: app
    paths: ["**"]
`,
		"go.mod":  "module example.com/app\n\ngo 1.24\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println() }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// stubGraphServer answers the stored-hash SELECT with hash and accepts
// branch-scoped GSP PUTs, counting them.
func stubGraphServer(t *testing.T, hash string, puts *int32) *graphserver.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sparql":
			bindings := ""
			if hash != "" {
				bindings = `{"h": {"type": "literal", "value": "` + hash + `"}}`
			}
			w.Header().Set("Content-Type", "application/sparql-results+json")
			io.WriteString(w, `{"head":{"vars":["h"]},"results":{"bindings":[`+bindings+`]}}`)
		case r.URL.Path == "/branches/main/graphs" && r.Method == http.MethodPut:
			atomic.AddInt32(puts, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return graphserver.New(srv.URL, nil)
}

// TestDeriveRefusesToEmptyAGraphAndNamesTheFlag: the guard reaching the CLI —
// an empty go-imports document against a graph that already holds content
// fails the run, writes nothing, and says which flag overrides it.
func TestDeriveRefusesToEmptyAGraphAndNamesTheFlag(t *testing.T) {
	root := goRepoWithEmptyImports(t)
	var puts int32
	c := stubGraphServer(t, "sha256:stale", &puts)

	_, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", false, c, derive.Options{})
	if err == nil {
		t.Fatal("runDeriveLocal = nil error; an empty document must not silently replace a stored one")
	}
	if !errors.Is(err, derive.ErrWouldEmptyGraph) || !strings.Contains(err.Error(), "--allow-empty") {
		t.Fatalf("err = %v; want ErrWouldEmptyGraph naming --allow-empty", err)
	}
	if n := atomic.LoadInt32(&puts); n != 0 {
		t.Fatalf("puts=%d; a refused run must write nothing at all", n)
	}
}

// TestDeriveAllowEmptyReportsTheEmptyDocument: with the opt-in the run
// proceeds and the result line says which document was empty.
func TestDeriveAllowEmptyReportsTheEmptyDocument(t *testing.T) {
	root := goRepoWithEmptyImports(t)
	var puts int32
	c := stubGraphServer(t, "sha256:stale", &puts)

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", false, c,
		derive.Options{AllowEmpty: true})
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if !strings.Contains(out, "observed/go-imports/github.com/acme/app: hash=") ||
		!strings.Contains(out, "empty=true") {
		t.Fatalf("output must report the empty document:\n%s", out)
	}
	if !strings.Contains(out, "observed/repo-layout/github.com/acme/app: hash=") ||
		!strings.Contains(out, "empty=false") {
		t.Fatalf("output must report the non-empty document too:\n%s", out)
	}
	if n := atomic.LoadInt32(&puts); n != 2 {
		t.Fatalf("puts=%d; want both documents written", n)
	}
}

// fakeOverviewServer stands up an HTTP server answering every request through
// h, and points LODE_SERVER/LODE_TOKEN/HOME at it so the spec 007 commands
// build a client against it rather than the developer's own config.
func fakeOverviewServer(t *testing.T, h func(*http.Request) (int, string)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := h(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())
}

func TestDriftCommandJSON(t *testing.T) {
	fakeOverviewServer(t, func(*http.Request) (int, string) {
		return http.StatusOK, `{"violations":[{"from":"urn:a","to":"urn:b"}],"stale_intent":[]}`
	})
	out, err := runLode(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift --json: %v", err)
	}
	if !strings.Contains(out, `"from": "urn:a"`) && !strings.Contains(out, `"from":"urn:a"`) {
		t.Fatalf("drift --json output missing the violation:\n%s", out)
	}
}

func TestFrontierCommandPassesProject(t *testing.T) {
	var gotQuery string
	fakeOverviewServer(t, func(r *http.Request) (int, string) {
		gotQuery = r.URL.RawQuery
		return http.StatusOK, `{"tasks":[]}`
	})
	if _, err := runLode(t, "frontier", "--project", "worklode", "--json"); err != nil {
		t.Fatalf("frontier: %v", err)
	}
	if !strings.Contains(gotQuery, "project=worklode") {
		t.Fatalf("query = %q; want project=worklode", gotQuery)
	}
}

func TestDriftAcknowledgedFlag(t *testing.T) {
	var gotQuery string
	fakeOverviewServer(t, func(r *http.Request) (int, string) {
		gotQuery = r.URL.RawQuery
		return http.StatusOK, `{"violations":[],"stale_intent":[],"acknowledged":[]}`
	})
	if _, err := runLode(t, "drift", "--acknowledged", "--json"); err != nil {
		t.Fatalf("drift --acknowledged: %v", err)
	}
	if !strings.Contains(gotQuery, "acknowledged=1") {
		t.Fatalf("query = %q; want acknowledged=1", gotQuery)
	}
}

// TestDeriveServerFlagPostsToTheAPI: --server is the one caller of
// POST /api/v1/derive; without it the command derives from the checkout, so
// the method and path are what the flag is for.
func TestDeriveServerFlagPostsToTheAPI(t *testing.T) {
	var gotMethod, gotPath string
	fakeOverviewServer(t, func(r *http.Request) (int, string) {
		gotMethod, gotPath = r.Method, r.URL.Path
		return http.StatusOK, `{"results":[{"graph":"urn:g","hash":"sha256:abc","skipped":false,"empty":false,"bytes":42}]}`
	})
	out, err := runLode(t, "derive", "--server")
	if err != nil {
		t.Fatalf("derive --server: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/derive" {
		t.Fatalf("request = %s %s; want POST /api/v1/derive", gotMethod, gotPath)
	}
	if !strings.Contains(out, "urn:g") || !strings.Contains(out, "sha256:abc") || !strings.Contains(out, "42") {
		t.Fatalf("derive --server output missing the result row:\n%s", out)
	}
}

// TestDeriveServerRejectsRepoLocalFlags: --dry-run, --graph-url and
// --allow-empty describe a checkout the server does not have; the combination
// must fail rather than silently ignore them.
func TestDeriveServerRejectsRepoLocalFlags(t *testing.T) {
	for _, args := range [][]string{
		{"derive", "--server", "--dry-run"},
		{"derive", "--server", "--graph-url", "http://example.invalid"},
		{"derive", "--server", "--allow-empty"},
	} {
		_, err := runLode(t, args...)
		if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
			t.Errorf("%v: err = %v; want a mutual-exclusion error", args, err)
		}
	}
}
