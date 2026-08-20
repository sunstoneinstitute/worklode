package cmd

import (
	"strings"
	"testing"
)

func TestBodyFileUploadsAndRewrites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shots/a.png", "\x89PNG\r\n\x1a\n one")
	writeFile(t, dir, "shots/b.png", "\x89PNG\r\n\x1a\n two")
	bodyFile := writeFile(t, dir, "bug.md",
		"Flashes at 390px:\n\n"+
			"![before](./shots/a.png)\n\n"+
			"![expected](./shots/b.png)\n\n"+
			"![remote](https://x.example/y.png)\n\n"+
			"![abs](/etc/passwd)\n")

	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	out, err := runLode(t, "task", "add", "--project", "p",
		"--title", "map flash", "--body-file", bodyFile)
	if err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.uploads) != 2 {
		t.Fatalf("uploads = %d, want 2 (locals only)", len(srv.uploads))
	}
	if len(srv.created) != 1 {
		t.Fatalf("creates = %d, want 1", len(srv.created))
	}
	body := srv.created[0]
	if strings.Contains(body, "./shots/") {
		t.Fatalf("local paths survived:\n%s", body)
	}
	if n := strings.Count(body, "](/blob/"); n != 2 {
		t.Fatalf("blob references = %d, want 2:\n%s", n, body)
	}
	// Remote and absolute destinations are left exactly as written.
	if !strings.Contains(body, "https://x.example/y.png") ||
		!strings.Contains(body, "(/etc/passwd)") {
		t.Fatalf("non-local destination was rewritten:\n%s", body)
	}
}

// TestBodyFileMissingImageFailsBeforeCreate: the whole command must fail
// before the task is written, so an author never gets a body pointing at
// images that were never uploaded.
func TestBodyFileMissingImageFailsBeforeCreate(t *testing.T) {
	dir := t.TempDir()
	bodyFile := writeFile(t, dir, "bug.md", "![gone](./missing.png)\n")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	out, err := runLode(t, "task", "add", "--project", "p",
		"--title", "x", "--body-file", bodyFile)
	if err == nil {
		t.Fatalf("expected failure, got:\n%s", out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.created) != 0 {
		t.Fatalf("task was created despite a missing image: %v", srv.created)
	}
}

func TestBodyFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "outside.png", "\x89PNG\r\n\x1a\n x")
	sub := writeFile(t, dir, "sub/bug.md", "![up](../outside.png)\n")
	startBlobSrv(t, func([]byte) string { return "image/png" })

	out, err := runLode(t, "task", "add", "--project", "p",
		"--title", "x", "--body-file", sub)
	if err == nil {
		t.Fatalf("expected traversal rejection, got:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "outside") {
		t.Fatalf("error should name the traversal: %v\n%s", err, out)
	}
}

func TestBodyFileNoUpload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.png", "\x89PNG\r\n\x1a\n one")
	bodyFile := writeFile(t, dir, "bug.md", "![a](./a.png)\n")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	if out, err := runLode(t, "task", "add", "--project", "p",
		"--title", "x", "--body-file", bodyFile, "--no-upload"); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.uploads) != 0 {
		t.Fatalf("--no-upload still uploaded %d file(s)", len(srv.uploads))
	}
	if !strings.Contains(srv.created[0], "./a.png") {
		t.Fatalf("--no-upload rewrote the body:\n%s", srv.created[0])
	}
}
