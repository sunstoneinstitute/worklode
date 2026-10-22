package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestFileFlagsReadStdin: every flag that takes a file reads standard input
// when given "-", so a caller with no local files (the MCP lode tool) can pipe
// the content in (WL-REQ-1863). A fake server records each request body; the
// stdin text must reach one of them, or, for a command that never calls the
// server, the command's output.
func TestFileFlagsReadStdin(t *testing.T) {
	const marker = "STDIN-MARKER-7f3a"
	docSrc := "---\nstatus: draft\n---\n\n# Piped\n\n" + marker + "\n"
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"doc add --file", docSrc, []string{"doc", "add", "--project", "p", "--kind", "spec", "--slug", "piped", "--file", "-"}},
		{"doc edit --file", marker, []string{"doc", "edit", "WL-SPEC-1", "--file", "-"}},
		{"doc revise --file", marker, []string{"doc", "revise", "WL-SPEC-1", "--file", "-"}},
		{"rule add --file", marker, []string{"rule", "add", "--project", "p", "--heading", "H", "--file", "-"}},
		{"rule edit --file", marker, []string{"rule", "edit", "WL-RULE-1", "--heading", "H", "--file", "-"}},
		{"rule supersede --map", "WL-RULE-1 -> " + marker + "\n", []string{"rule", "supersede", "--project", "p", "--map", "-"}},
		{"task add --body-file", marker, []string{"task", "add", "--project", "p", "--title", "t", "--body-file", "-"}},
		{"task edit --body-file", marker, []string{"task", "edit", "WL-1", "--body-file", "-"}},
		{"doc note --body-file", marker, []string{"doc", "note", "WL-SPEC-1#sec-1", "--body-file", "-"}},
		{"skill recommend --file", marker, []string{"skill", "recommend", "--file", "-"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, r.Method+" "+r.URL.Path+" "+string(b))
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/docs") {
					io.WriteString(w, `{"id":1,"ref":"WL-SPEC-1","project":"p","status":"draft","version":1,"kind":"spec","slug":"piped"}`)
					return
				}
				io.WriteString(w, `{"ref":"WL-RULE-1","id":"WL-1","status":"draft","version":1,"heading":"H","kind":"requirement"}`)
			}))
			defer srv.Close()
			t.Setenv("LODE_SERVER", srv.URL)
			t.Setenv("LODE_TOKEN", "wl_"+strings.Repeat("0", 40))
			t.Setenv("HOME", t.TempDir())

			out, err := runLodeIn(t, strings.NewReader(tc.stdin), tc.args...)
			mu.Lock()
			defer mu.Unlock()
			found := false
			for _, b := range bodies {
				if strings.Contains(b, marker) {
					found = true
				}
			}
			if !found {
				t.Fatalf("stdin text never reached the server (err=%v)\noutput: %s\nrequests: %q", err, out, bodies)
			}
		})
	}
}

// TestDocLintReadsStdin: `doc lint -` lints the document on stdin, and a file
// path still works.
func TestDocLintReadsStdin(t *testing.T) {
	good := "---\nstatus: draft\n---\n\n# S\n\nBody.\n"
	out, err := runLodeIn(t, strings.NewReader(good), "doc", "lint", "-", "--json")
	if err != nil {
		t.Fatalf("doc lint -: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"file":"-"`) {
		t.Errorf("report does not name stdin: %s", out)
	}
	// Input that cannot parse proves stdin, not a file named "-", was read.
	out, err = runLodeIn(t, strings.NewReader("---\nbogus: 1\n---\n# x\n"), "doc", "lint", "-")
	if err == nil || strings.Contains(err.Error(), "no such file") {
		t.Errorf("doc lint - on bad stdin: err=%v out=%s", err, out)
	}
	path := filepath.Join(t.TempDir(), "s.md")
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runLode(t, "doc", "lint", path)
	if err != nil {
		t.Fatalf("doc lint file: %v\n%s", err, out)
	}
}
