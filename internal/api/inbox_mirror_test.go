package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/safefetch"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// pngBytes sniffs as image/png, which is what makes it mirrorable.
const pngBytes = "\x89PNG\r\n\x1a\n payload"

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testFetcher relaxes the SSRF guard enough to reach an httptest origin:
// plain http, a random port, and a loopback address, all of which production
// refuses.
func testFetcher() *safefetch.Fetcher {
	f := safefetch.New(mirrorHosts, maxBlobBytes)
	f.AllowLoopbackForTest = true
	f.AllowAnyHostForTest = true
	return f
}

// mirrorTestServer builds a server with a fake blob store and a stubbed image
// host, and returns the origin serving the image. Metrics are left nil, which
// every observe* helper tolerates.
func mirrorTestServer(t *testing.T) (*server, *blobstore.Fake, string) {
	t.Helper()
	st := store.OpenTestStore(t)
	fake := blobstore.NewFake()
	s := &server{st: st, blobs: fake, log: testLogger(t)}

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(pngBytes))
	}))
	t.Cleanup(img.Close)
	return s, fake, img.URL
}

func TestMirrorRewritesAllowedHost(t *testing.T) {
	s, fake, origin := mirrorTestServer(t)
	// Point the guard at the stub instead of githubusercontent.com.
	s.mirrorFetcherForTest = testFetcher()

	body := "repro:\n\n![shot](" + origin + "/a.png)\n"
	got := s.mirrorRemoteImages(context.Background(), body)

	if strings.Contains(got, origin) {
		t.Fatalf("remote URL survived:\n%s", got)
	}
	if !strings.Contains(got, "](/blob/") {
		t.Fatalf("not rewritten to a blob:\n%s", got)
	}
	objs, _ := fake.List(context.Background(), "blobs/")
	if len(objs) != 1 {
		t.Fatalf("stored %d objects, want 1", len(objs))
	}
	// The index row must exist too: a body pointing at a hash serveBlob
	// cannot look up is a permanently broken image.
	hash := strings.TrimSuffix(strings.SplitN(got, "](/blob/", 2)[1], ")\n")
	if _, err := s.st.GetBlob(context.Background(), hash); err != nil {
		t.Fatalf("GetBlob(%s): %v", hash, err)
	}
}

// TestMirrorLeavesBlockedTarget: a body pointing at the metadata address is
// left exactly as written and the promote still succeeds. A partially
// mirrored body beats a failed promote, and the renderer drops the leftover
// rather than turning it into a beacon.
func TestMirrorLeavesBlockedTarget(t *testing.T) {
	s, fake, _ := mirrorTestServer(t)
	body := "![x](http://169.254.169.254/latest/meta-data)\n"
	if got := s.mirrorRemoteImages(context.Background(), body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
	if objs, _ := fake.List(context.Background(), "blobs/"); len(objs) != 0 {
		t.Fatalf("stored %d objects, want 0", len(objs))
	}
}

// TestMirrorLeavesDisallowedHost uses the production fetcher — no test
// escapes — so the host allowlist is what refuses. It is checked before any
// DNS lookup, so this makes no network call.
func TestMirrorLeavesDisallowedHost(t *testing.T) {
	s, fake, _ := mirrorTestServer(t)
	body := "![x](https://evil.example/p.png)\n"
	if got := s.mirrorRemoteImages(context.Background(), body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
	if objs, _ := fake.List(context.Background(), "blobs/"); len(objs) != 0 {
		t.Fatalf("stored %d objects, want 0", len(objs))
	}
}

func TestMirrorNoImagesIsIdentity(t *testing.T) {
	s, _, _ := mirrorTestServer(t)
	body := "no images here\n\n```\n![fake](https://x.example/y.png)\n```\n"
	if got := s.mirrorRemoteImages(context.Background(), body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
}

// TestMirrorDedupesIdenticalBytes: content addressing means the same
// screenshot referenced twice, or served from two URLs, is one object. Both
// references still get rewritten.
func TestMirrorDedupesIdenticalBytes(t *testing.T) {
	s, fake, origin := mirrorTestServer(t)
	s.mirrorFetcherForTest = testFetcher()

	body := "![a](" + origin + "/a.png)\n\n![again](" + origin + "/a.png)\n\n" +
		"![b](" + origin + "/b.png)\n"
	got := s.mirrorRemoteImages(context.Background(), body)

	if strings.Contains(got, origin) {
		t.Fatalf("a remote URL survived:\n%s", got)
	}
	if n := strings.Count(got, "](/blob/"); n != 3 {
		t.Fatalf("rewrote %d references, want 3:\n%s", n, got)
	}
	objs, _ := fake.List(context.Background(), "blobs/")
	if len(objs) != 1 {
		t.Fatalf("stored %d objects, want 1 — identical bytes are one blob", len(objs))
	}
}

// TestMirrorSkipsNonImage: the URL is chosen by whoever filed the issue and
// its bytes end up behind an <img src>, so anything that cannot render in
// place is not stored. Mirroring it would buy a hosting primitive for
// attacker-supplied content and still render as a broken image; left alone,
// the remote reference renders as nothing (spec 021 §8).
func TestMirrorSkipsNonImage(t *testing.T) {
	st := store.OpenTestStore(t)
	fake := blobstore.NewFake()
	s := &server{st: st, blobs: fake, log: testLogger(t)}
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png") // claimed, not true
		_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
	}))
	t.Cleanup(html.Close)
	s.mirrorFetcherForTest = testFetcher()

	body := "![x](" + html.URL + "/a.png)\n"
	if got := s.mirrorRemoteImages(context.Background(), body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
	if objs, _ := fake.List(context.Background(), "blobs/"); len(objs) != 0 {
		t.Fatalf("stored %d objects, want 0", len(objs))
	}
}

// TestMirrorUnconfiguredIsIdentity: an instance with no bucket promotes
// bodies unchanged rather than failing the promote.
func TestMirrorUnconfiguredIsIdentity(t *testing.T) {
	s := &server{log: testLogger(t)}
	body := "![x](https://user-images.githubusercontent.com/1/a.png)\n"
	if got := s.mirrorRemoteImages(context.Background(), body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
}

// TestMirrorMetrics pins one outcome per remote reference across the mixed
// case: two references to the same bytes (one stored, one deduplicated) and
// one the guard refuses.
func TestMirrorMetrics(t *testing.T) {
	s, _, origin := mirrorTestServer(t)
	s.initMetrics(prometheus.NewRegistry())
	s.mirrorFetcherForTest = testFetcher()

	body := "![a](" + origin + "/a.png)\n\n![b](" + origin + "/b.png)\n\n" +
		"![c](http://169.254.169.254/latest/meta-data)\n"
	s.mirrorRemoteImages(context.Background(), body)

	for _, tc := range []struct {
		outcome string
		want    float64
	}{
		{mirrorStored, 1},
		{mirrorDeduplicated, 1},
		{mirrorFetchFailed, 1},
		{mirrorNotEmbeddable, 0},
		{mirrorStoreFailed, 0},
		{mirrorRewriteFailed, 0},
	} {
		if got := testutil.ToFloat64(s.imageMirrors.WithLabelValues(tc.outcome)); got != tc.want {
			t.Fatalf("imageMirrors{%s} = %v, want %v", tc.outcome, got, tc.want)
		}
	}
}
