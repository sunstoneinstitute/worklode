package api

import (
	"mime"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSpoolWritable covers the probe without a store. The error must name
// the path: it is all an operator gets from the crash loop.
func TestCheckSpoolWritable(t *testing.T) {
	t.Parallel()
	if err := checkSpoolWritable(t.TempDir()); err != nil {
		t.Fatalf("writable dir: %v", err)
	}
	// Empty means os.TempDir(), which is writable in a test environment.
	if err := checkSpoolWritable(""); err != nil {
		t.Fatalf("default dir: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "not-mounted")
	err := checkSpoolWritable(missing)
	if err == nil {
		t.Fatal("no error for a directory that does not exist")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name %q", err, missing)
	}
}

// TestCheckSpoolWritableLeavesNothingBehind: the probe runs on every boot, so
// a crash-looping pod must not fill its own spool volume.
func TestCheckSpoolWritableLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for range 3 {
		if err := checkSpoolWritable(dir); err != nil {
			t.Fatalf("probe: %v", err)
		}
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left %v behind", entries)
	}
}

// TestContentDisposition covers the header spec 021 §2 promises. The name is
// caller-controlled, so the interesting rows are the hostile ones: what
// matters is that every one of them still parses back to exactly the name we
// meant to serve, and that none of them can add a header.
func TestContentDisposition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		kind     string
		filename string
		want     string // full header value
		wantName string // what a client decodes it back to; "" means no filename
	}{
		{"no name keeps the bare token", "attachment", "", "attachment", ""},
		{"inline keeps its token", "inline", "shot.png", "inline; filename=shot.png", "shot.png"},
		{"spaces force quoting", "attachment", "my report (1).log",
			`attachment; filename="my report (1).log"`, "my report (1).log"},
		{"unicode gets both RFC 6266 halves", "attachment", "naïve résumé.pdf",
			`attachment; filename="na_ve r_sum_.pdf"; filename*=utf-8''na%C3%AFve%20r%C3%A9sum%C3%A9.pdf`,
			"naïve résumé.pdf"},
		{"quotes are escaped, not honoured", "attachment", `"; filename="evil.exe`,
			`attachment; filename="\"; filename=\"evil.exe"`, `"; filename="evil.exe`},
		{"CRLF is stripped before it reaches the header", "attachment",
			"crash.log\r\nX-Injected: 1", `attachment; filename="crash.logX-Injected: 1"`,
			"crash.logX-Injected: 1"},
		{"only the last path segment survives", "attachment", "../../etc/passwd",
			"attachment; filename=passwd", "passwd"},
		{"a windows path is a name too", "attachment", `C:\Users\bob\secret.txt`,
			"attachment; filename=secret.txt", "secret.txt"},
		{"a bare dot is not a name", "attachment", ".", "attachment", ""},
		{"a parent reference is not a name", "attachment", "..", "attachment", ""},
		{"whitespace is not a name", "attachment", "   ", "attachment", ""},
		{"an overlong name is refused", "attachment", strings.Repeat("a", 201) + ".log",
			"attachment", ""},
		{"invalid UTF-8 is refused", "attachment", "bad\xffname.log", "attachment", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := contentDisposition(tc.kind, tc.filename)
			if got != tc.want {
				t.Fatalf("contentDisposition(%q, %q) = %q, want %q",
					tc.kind, tc.filename, got, tc.want)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("header value %q carries a line break", got)
			}
			kind, params, err := mime.ParseMediaType(got)
			if err != nil {
				t.Fatalf("ParseMediaType(%q): %v", got, err)
			}
			if kind != tc.kind {
				t.Fatalf("disposition token = %q, want %q", kind, tc.kind)
			}
			// ParseMediaType prefers the RFC 8187 half when both are present,
			// which is what a browser does, so this reads the exact name.
			if params["filename"] != tc.wantName {
				t.Fatalf("decoded filename = %q, want %q", params["filename"], tc.wantName)
			}
		})
	}
}

// TestBlobURL: a reference addresses its blob by hash and carries its own
// name, since the name is per-reference and the route is per-blob (021 §2).
func TestBlobURL(t *testing.T) {
	t.Parallel()
	const hash = "9f2ac1"
	if got := blobURL(hash, ""); got != "/blob/"+hash {
		t.Fatalf("unnamed reference = %q, want the bare URL", got)
	}
	got := blobURL(hash, "crash report.log")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse %q: %v", got, err)
	}
	if u.Path != "/blob/"+hash {
		t.Fatalf("path = %q, want /blob/%s", u.Path, hash)
	}
	if name := u.Query().Get("filename"); name != "crash report.log" {
		t.Fatalf("filename = %q, want it to round-trip", name)
	}
	// The name is sanitised on the way out as well as on the way in, so a
	// stored path can never become a query parameter that suggests one.
	if got := blobURL(hash, "../../etc/passwd"); !strings.HasSuffix(got, "?filename=passwd") {
		t.Fatalf("blobURL with a path = %q", got)
	}
}
