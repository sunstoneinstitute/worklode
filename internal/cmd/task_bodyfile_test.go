package cmd

import (
	"os"
	"path/filepath"
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
	// "resolves outside", not "outside": the fixture is called outside.png,
	// so the bare word also matches a plain "no such file" failure.
	if !strings.Contains(err.Error()+out, "resolves outside") {
		t.Fatalf("error should name the traversal: %v\n%s", err, out)
	}
}

// TestBodyFileRejectsSymlinkEscape: --body-file is aimed at a markdown bundle
// plus its assets directory, which is exactly the kind of thing an author
// receives from someone else. os.Open follows symlinks, so containment has to
// be decided on resolved paths or the bundle can name any file on the box.
func TestBodyFileRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	secret := writeFile(t, dir, "elsewhere/secret.png", "\x89PNG\r\n\x1a\n secret")
	assets := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(assets, "shot.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	bodyFile := writeFile(t, assets, "bug.md", "![shot](./shot.png)\n")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	out, err := runLode(t, "task", "add", "--project", "p",
		"--title", "x", "--body-file", bodyFile)
	if err == nil {
		t.Fatalf("expected the symlink escape to be refused, got:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "resolves outside") {
		t.Fatalf("error should name the traversal: %v\n%s", err, out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.uploads) != 0 {
		t.Fatalf("uploaded %d file(s) through a symlink", len(srv.uploads))
	}
	if len(srv.created) != 0 {
		t.Fatalf("task was created despite the refusal: %v", srv.created)
	}
}

// TestBodyFilePercentEncodedName: a markdown destination is URL-escaped, so
// a file with a space in its name is written `./my%20shot.png` and has to be
// decoded before it is opened -- while the body is rewritten on the raw
// destination that is actually spelled there.
func TestBodyFilePercentEncodedName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "my shot.png", "\x89PNG\r\n\x1a\n one")
	bodyFile := writeFile(t, dir, "bug.md", "![shot](./my%20shot.png)\n")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	out, err := runLode(t, "task", "add", "--project", "p",
		"--title", "x", "--body-file", bodyFile)
	if err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(srv.uploads))
	}
	if len(srv.created) != 1 || !strings.Contains(srv.created[0], "](/blob/") {
		t.Fatalf("body not rewritten: %v", srv.created)
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
