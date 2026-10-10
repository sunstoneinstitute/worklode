package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// gateRepo builds a repo with one base commit and one head commit that
// changes internal/cmd/x.go, and returns the dir with the two revisions.
func gateRepo(t *testing.T, gateTable string, headMessage string) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if err := gitexec.Run(dir, append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, ".worklode"), 0o755)
	os.MkdirAll(filepath.Join(dir, "internal", "cmd"), 0o755)
	os.WriteFile(filepath.Join(dir, ".worklode", "config.toml"), []byte("current_project = \"p\"\n"+gateTable), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base, _ = gitexec.Line(dir, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "internal", "cmd", "x.go"), []byte("package cmd\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", headMessage)
	head, _ = gitexec.Line(dir, "rev-parse", "HEAD")
	return dir, base, head
}

const gateTable = "\n[gate]\npaths = [\"internal/cmd/**\"]\n"

func TestGateCheckRefusesWithoutTrailer(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command")
	_, err := runGateCheck(dir, base, head, "")
	if err == nil {
		t.Fatal("guarded change without a trailer must fail")
	}
}

func TestGateCheckPassesWithTrailerInCommit(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command\n\nSpec: WL-RULE-7\n")
	out, err := runGateCheck(dir, base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WL-RULE-7") || !strings.Contains(out, "internal/cmd/x.go") {
		t.Errorf("verdict should name the declaration and the guarded file: %q", out)
	}
}

func TestGateCheckPassesWithTrailerInBodyFile(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command")
	body := filepath.Join(dir, "body.md")
	os.WriteFile(body, []byte("Summary\n\nSpec: none refactor\n"), 0o644)
	if _, err := runGateCheck(dir, base, head, body); err != nil {
		t.Fatal(err)
	}
}

func TestGateCheckIsANoOpWithoutTable(t *testing.T) {
	dir, base, head := gateRepo(t, "", "change a command")
	out, err := runGateCheck(dir, base, head, "")
	if err != nil || !strings.Contains(out, "no [gate] table") {
		t.Fatalf("no table: out=%q err=%v", out, err)
	}
}

// A section-valued trailer fails and names `lode show <doc>#sec-N` when no
// server is configured (WL-REQ-7).
func TestGateCheckRefusesSectionOffline(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command\n\nSpec: WL-SPEC-72 sec-4\n")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LODE_SERVER", "")
	_, err := runGateCheck(dir, base, head, "")
	if err == nil || !strings.Contains(err.Error(), "lode show WL-SPEC-72#sec-4") {
		t.Fatalf("offline section refusal must name lode show: %v", err)
	}
}

// With a reachable server the refusal names the rule ref the section
// resolves to (WL-REQ-7).
func TestGateCheckRefusesSectionNamingItsRule(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command\n\nSpec: WL-SPEC-72#sec-4\n")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rules" || r.URL.Query().Get("doc") != "WL-SPEC-72" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"ref":"WL-REQ-6","arranged_in":[{"doc_ref":"WL-SPEC-72","anchor":"sec-3"}]},
			{"ref":"WL-REQ-7","arranged_in":[{"doc_ref":"WL-SPEC-72","anchor":"sec-4"}]}]`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "wl_"+strings.Repeat("0", 40))
	_, err := runGateCheck(dir, base, head, "")
	if err == nil || !strings.Contains(err.Error(), "Spec: WL-REQ-7") || strings.Contains(err.Error(), "lode show") {
		t.Fatalf("section refusal must name the rule ref: %v", err)
	}
}

// addCommit commits files on top of dir's HEAD and returns the new head.
func addCommit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	if err := gitexec.Run(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := gitexec.Run(dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "more\n\nSpec: WL-RULE-7"); err != nil {
		t.Fatal(err)
	}
	head, _ := gitexec.Line(dir, "rev-parse", "HEAD")
	return head
}

// The citation test of WL-REQ-1791 runs over the lines the diff adds: a
// string literal passes, a comment citing a section fails naming it.
func TestGateCheckCitationsInAddedLines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LODE_SERVER", "")
	dir, base, _ := gateRepo(t, gateTable, "change a command\n\nSpec: WL-RULE-7\n")
	head := addCommit(t, dir, map[string]string{"internal/cmd/y.go": "package cmd\n\nvar anchor = \"#sec-\"\n"})
	if _, err := runGateCheck(dir, base, head, ""); err != nil {
		t.Fatalf("a Go string literal is not prose: %v", err)
	}
	head = addCommit(t, dir, map[string]string{"notes.md": "see WL-SPEC-77 §4\n"})
	_, err := runGateCheck(dir, base, head, "")
	if err == nil || !strings.Contains(err.Error(), "notes.md:1") || !strings.Contains(err.Error(), "lode show WL-SPEC-77#sec-4") {
		t.Fatalf("an added prose line citing a section must fail naming it: %v", err)
	}
}

func TestGateCheckCitationsInBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LODE_SERVER", "")
	dir, base, head := gateRepo(t, gateTable, "change a command")
	body := filepath.Join(dir, "body.md")
	os.WriteFile(body, []byte("Implements WL-SPEC-77 §4.\n\nSpec: WL-REQ-165\n"), 0o644)
	_, err := runGateCheck(dir, base, head, body)
	if err == nil || !strings.Contains(err.Error(), "WL-SPEC-77 §4") {
		t.Fatalf("a body citing a section must fail: %v", err)
	}
}
