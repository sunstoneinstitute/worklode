package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/githubauth"
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
	return safefetch.NewForTest(mirrorHosts, maxBlobBytes, safefetch.TestEscapes{Loopback: true, AnyHost: true})
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
	got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body)

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
	if got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body); got != body {
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
	if got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
	if objs, _ := fake.List(context.Background(), "blobs/"); len(objs) != 0 {
		t.Fatalf("stored %d objects, want 0", len(objs))
	}
}

func TestMirrorNoImagesIsIdentity(t *testing.T) {
	s, _, _ := mirrorTestServer(t)
	body := "no images here\n\n```\n![fake](https://x.example/y.png)\n```\n"
	if got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body); got != body {
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
	got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body)

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
	if got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body); got != body {
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
	if got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body); got != body {
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
	s.mirrorRemoteImages(context.Background(), "acme/widgets", body)

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
		{mirrorCapped, 0},
	} {
		if got := testutil.ToFloat64(s.imageMirrors.WithLabelValues(tc.outcome)); got != tc.want {
			t.Fatalf("imageMirrors{%s} = %v, want %v", tc.outcome, got, tc.want)
		}
	}
}

// TestMirrorCapsReferencesPerBody: a body with more remote references than
// maxMirroredImages mirrors exactly the cap's worth; the rest keep their
// original URL, the same fate as any other per-image failure.
func TestMirrorCapsReferencesPerBody(t *testing.T) {
	s, fake, origin := mirrorTestServer(t)
	s.initMetrics(prometheus.NewRegistry())
	s.mirrorFetcherForTest = testFetcher()

	const total = maxMirroredImages + 5
	var body strings.Builder
	for i := 0; i < total; i++ {
		fmt.Fprintf(&body, "![x](%s/%d.png)\n\n", origin, i)
	}
	got := s.mirrorRemoteImages(context.Background(), "acme/widgets", body.String())

	if n := strings.Count(got, "](/blob/"); n != maxMirroredImages {
		t.Fatalf("rewrote %d references, want %d", n, maxMirroredImages)
	}
	if n := strings.Count(got, origin); n != total-maxMirroredImages {
		t.Fatalf("%d original URLs survived, want %d", n, total-maxMirroredImages)
	}
	// All references resolve to the same bytes, so the cap still leaves
	// exactly one stored object regardless of how many references were
	// mirrored.
	objs, _ := fake.List(context.Background(), "blobs/")
	if len(objs) != 1 {
		t.Fatalf("stored %d objects, want 1", len(objs))
	}
	if got := testutil.ToFloat64(s.imageMirrors.WithLabelValues(mirrorCapped)); got != float64(total-maxMirroredImages) {
		t.Fatalf("imageMirrors{capped} = %v, want %d", got, total-maxMirroredImages)
	}
}

// --- the installation token (§12) ---

// mirrorTokenApp serves the two calls minting an installation token for
// acme/widgets takes. fail makes the installation lookup 500, which is how a
// GitHub outage or a lost App installation reaches mirrorToken.
func mirrorTokenApp(t *testing.T, fail bool) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_mirror"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// mirrorAuthOrigin is an image host that remembers the Authorization header of
// every request it served. Mutex-guarded: the handler runs on the server's
// goroutine, the assertion on the test's.
type mirrorAuthOrigin struct {
	mu   sync.Mutex
	seen []string
	url  string // the "localhost" spelling, which is what a host scope matches
}

func newMirrorAuthOrigin(t *testing.T) *mirrorAuthOrigin {
	t.Helper()
	o := &mirrorAuthOrigin{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.seen = append(o.seen, r.Header.Get("Authorization"))
		o.mu.Unlock()
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(pngBytes))
	}))
	t.Cleanup(srv.Close)
	o.url = strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if o.url == srv.URL {
		t.Fatalf("unexpected httptest URL %q", srv.URL)
	}
	return o
}

func (o *mirrorAuthOrigin) headers() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.seen...)
}

// mirrorTokenServer builds a mirroring server wired to a GitHub App, with
// metrics initialised so the token outcome is observable.
func mirrorTokenServer(t *testing.T, app *githubauth.AppAuth) *server {
	t.Helper()
	s := &server{st: store.OpenTestStore(t), blobs: blobstore.NewFake(), log: testLogger(t)}
	s.initMetrics(prometheus.NewRegistry())
	s.appAuth = app
	s.mirrorFetcherForTest = testFetcher()
	return s
}

// aimAtTestHost points the token's host scope at the httptest origin for one
// test. Production's scope is mirrorTokenScopes' own value, which
// TestMirrorTokenStaysOffOtherHosts exercises unchanged.
//
// It writes a package-level var, so a caller must not be parallel; no test in
// this package is. Restoring it in Cleanup is what keeps the tests that assert
// against the production value honest.
func aimAtTestHost(t *testing.T) {
	t.Helper()
	saved := mirrorTokenScopes
	mirrorTokenScopes = []string{"localhost"}
	t.Cleanup(func() { mirrorTokenScopes = saved })
}

// A private repo's image is exactly a fetch that 404s without the token, so
// the header has to reach the image host, and the image still has to mirror.
func TestMirrorSendsInstallationToken(t *testing.T) {
	aimAtTestHost(t)
	s := mirrorTokenServer(t, mirrorTokenApp(t, false))
	origin := newMirrorAuthOrigin(t)

	got := s.mirrorRemoteImages(context.Background(), "acme/widgets", "![shot]("+origin.url+"/a.png)\n")

	if h := origin.headers(); len(h) != 1 || h[0] != "Bearer ghs_mirror" {
		t.Fatalf("image host saw Authorization %q, want one \"Bearer ghs_mirror\"", h)
	}
	if !strings.Contains(got, "](/blob/") {
		t.Fatalf("not rewritten to a blob:\n%s", got)
	}
	if v := testutil.ToFloat64(s.mirrorTokens.WithLabelValues(mirrorTokenMinted)); v != 1 {
		t.Fatalf("mirrorTokens{minted} = %v, want 1", v)
	}
}

// The token's host scope is githubusercontent.com, not the whole fetch
// allowlist: an image on any other host mirroring will fetch gets no
// credential. Deliberately runs against the production value of
// mirrorTokenScopes.
func TestMirrorTokenStaysOffOtherHosts(t *testing.T) {
	s := mirrorTokenServer(t, mirrorTokenApp(t, false))
	origin := newMirrorAuthOrigin(t)

	s.mirrorRemoteImages(context.Background(), "acme/widgets", "![shot]("+origin.url+"/a.png)\n")

	// The mint has to have succeeded for the empty header to mean "withheld"
	// rather than "there was never a token to send".
	if v := testutil.ToFloat64(s.mirrorTokens.WithLabelValues(mirrorTokenMinted)); v != 1 {
		t.Fatalf("mirrorTokens{minted} = %v, want 1", v)
	}
	if h := origin.headers(); len(h) != 1 || h[0] != "" {
		t.Fatalf("unnamed host saw Authorization %q, want one empty", h)
	}
}

// A token that cannot be minted is not fatal: the fetch goes out bare, a
// public repo's images still mirror, and the failure is on the meter rather
// than only in the log.
func TestMirrorProceedsWhenTokenMintFails(t *testing.T) {
	aimAtTestHost(t)
	s := mirrorTokenServer(t, mirrorTokenApp(t, true))
	origin := newMirrorAuthOrigin(t)

	got := s.mirrorRemoteImages(context.Background(), "acme/widgets", "![shot]("+origin.url+"/a.png)\n")

	if h := origin.headers(); len(h) != 1 || h[0] != "" {
		t.Fatalf("image host saw Authorization %q, want one empty", h)
	}
	if !strings.Contains(got, "](/blob/") {
		t.Fatalf("a failed token mint blocked the mirror:\n%s", got)
	}
	if v := testutil.ToFloat64(s.mirrorTokens.WithLabelValues(mirrorTokenFailed)); v != 1 {
		t.Fatalf("mirrorTokens{failed} = %v, want 1", v)
	}
}

// No App configured is not a mint that failed: nothing is counted, and the
// pass runs unauthenticated as it always did.
func TestMirrorWithoutGitHubAppCountsNoToken(t *testing.T) {
	aimAtTestHost(t)
	s := mirrorTokenServer(t, nil)
	origin := newMirrorAuthOrigin(t)

	s.mirrorRemoteImages(context.Background(), "acme/widgets", "![shot]("+origin.url+"/a.png)\n")

	if h := origin.headers(); len(h) != 1 || h[0] != "" {
		t.Fatalf("image host saw Authorization %q, want one empty", h)
	}
	for _, outcome := range mirrorTokenOutcomes {
		if v := testutil.ToFloat64(s.mirrorTokens.WithLabelValues(outcome)); v != 0 {
			t.Fatalf("mirrorTokens{%s} = %v, want 0", outcome, v)
		}
	}
}
