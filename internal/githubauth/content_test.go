package githubauth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTarball(t *testing.T) {
	want := []byte("\x1f\x8b\x08 not really gzip, but exact bytes matter")
	f := &appFixture{tarball: want}

	got, err := f.start(t).Tarball(context.Background(), "acme/app", "main")
	if err != nil {
		t.Fatalf("tarball: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tarball = %q, want %q", got, want)
	}
	f.assertTokenAuth(t, "/repos/acme/app/tarball/main")
}

func TestTarballErrorStatus(t *testing.T) {
	f := &appFixture{tarballCode: http.StatusNotFound}
	_, err := f.start(t).Tarball(context.Background(), "acme/app", "main")
	if err == nil {
		t.Fatal("want error for a 404 from the tarball endpoint")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should name the status: %v", err)
	}
	// A 404 could be a bad ref, a missing repo, or a revoked installation;
	// GitHub's message is what tells them apart, so the error carries it.
	if !strings.Contains(err.Error(), "No commit found") {
		t.Errorf("error should carry GitHub's message: %v", err)
	}
}

// A transport failure must not print the URL it was reaching for: GitHub
// redirects to a signed codeload link, and for a private repo that link
// carries a token in its query string.
func TestTarballTransportErrorHidesSignedURL(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL + "/repos/acme/app/tar.gz/refs/heads/main?token=SUPERSECRET123"
	dead.Close()

	f := &appFixture{tarballTo: deadURL}
	_, err := f.start(t).Tarball(context.Background(), "acme/app", "main")
	if err == nil {
		t.Fatal("want error when the redirect target refuses the connection")
	}
	if strings.Contains(err.Error(), "SUPERSECRET123") {
		t.Errorf("error leaks the signed codeload URL: %v", err)
	}
	if !strings.Contains(err.Error(), "acme/app@main") {
		t.Errorf("error should still name the repo and ref: %v", err)
	}
}

// The ref is escaped whole, so a slashed branch reaches GitHub as one %2F
// segment. GitHub decodes it and resolves the branch, and escaping this way
// keeps a ref like "../../x" inert instead of emitting dot segments that a
// server would normalize back out of the endpoint path.
func TestTarballEscapesRefAsOneSegment(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"release/v1", "/repos/acme/app/tarball/release%2Fv1"},
		{"../../x", "/repos/acme/app/tarball/..%2F..%2Fx"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			f := &appFixture{tarball: []byte("archive")}
			if _, err := f.start(t).Tarball(context.Background(), "acme/app", tc.ref); err != nil {
				t.Fatalf("tarball: %v", err)
			}
			if got := f.lastEscapedPath(); got != tc.want {
				t.Errorf("requested %q, want %q", got, tc.want)
			}
		})
	}
}

// The tarball is walked in memory, so a source repo far larger than a skill
// collection must be refused rather than read onto the heap.
func TestTarballRejectsOversize(t *testing.T) {
	orig := maxTarball
	maxTarball = 16
	t.Cleanup(func() { maxTarball = orig })

	f := &appFixture{tarball: bytes.Repeat([]byte("x"), maxTarball+1)}
	got, err := f.start(t).Tarball(context.Background(), "acme/app", "main")
	if err == nil {
		t.Fatalf("want error for an over-size tarball, got %d bytes", len(got))
	}
	if got != nil {
		t.Errorf("over-size tarball returned %d bytes alongside the error", len(got))
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should say the tarball is too large: %v", err)
	}
}

// The cap is inclusive: reading one byte past it must not reject a body that
// is exactly maxTarball bytes.
func TestTarballAcceptsExactlyMaxSize(t *testing.T) {
	orig := maxTarball
	maxTarball = 16
	t.Cleanup(func() { maxTarball = orig })

	f := &appFixture{tarball: bytes.Repeat([]byte("x"), maxTarball)}
	got, err := f.start(t).Tarball(context.Background(), "acme/app", "main")
	if err != nil {
		t.Fatalf("tarball of exactly the cap was rejected: %v", err)
	}
	if len(got) != maxTarball {
		t.Errorf("got %d bytes, want %d", len(got), maxTarball)
	}
}

// A malformed repo is rejected before it can reshape a request URL, so no call
// reaches GitHub at all.
func TestTarballRejectsMalformedRepo(t *testing.T) {
	for _, repo := range []string{"", "app", "acme/app/extra", "/app", "acme/", "acme/../../x"} {
		t.Run(repo, func(t *testing.T) {
			f := &appFixture{}
			_, err := f.start(t).Tarball(context.Background(), repo, "main")
			if err == nil {
				t.Fatalf("Tarball(%q) = nil error, want a rejection", repo)
			}
			if !strings.Contains(err.Error(), "owner/name") {
				t.Errorf("error should name the expected shape: %v", err)
			}
			if n := f.callCount(); n != 0 {
				t.Errorf("%d request(s) reached GitHub for a malformed repo", n)
			}
		})
	}
}
