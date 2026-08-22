package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

// pngBytes is a 1x1 PNG; http.DetectContentType sniffs it as image/png.
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89,
}

func postBlob(t *testing.T, h http.Handler, token, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blobs", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// uploadedHash posts body and returns the hash the server assigned it.
func uploadedHash(t *testing.T, h http.Handler, token string, body []byte) string {
	t.Helper()
	rec := postBlob(t, h, token, "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var up struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	return up.Hash
}

func TestUploadBlob(t *testing.T) {
	_, h, token, fake := newTestServerBlobs(t)

	rec := postBlob(t, h, token, "application/octet-stream", pngBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	var got struct {
		Hash      string `json:"hash"`
		MediaType string `json:"media_type"`
		Size      int64  `json:"size"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	sum := sha256.Sum256(pngBytes)
	wantHash := hex.EncodeToString(sum[:])
	if got.Hash != wantHash {
		t.Fatalf("hash = %s, want %s", got.Hash, wantHash)
	}
	// The client said octet-stream; the server must sniff and win.
	if got.MediaType != "image/png" {
		t.Fatalf("media_type = %q, want image/png (server must sniff)", got.MediaType)
	}
	if got.Size != int64(len(pngBytes)) {
		t.Fatalf("size = %d, want %d", got.Size, len(pngBytes))
	}
	if got.URL != "/blob/"+wantHash {
		t.Fatalf("url = %q, want /blob/%s", got.URL, wantHash)
	}

	stored, err := fake.Open(blobstore.Key(wantHash))
	if err != nil {
		t.Fatalf("object not stored: %v", err)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(stored); err != nil {
		t.Fatalf("read stored object: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), pngBytes) {
		t.Fatal("stored bytes differ from uploaded bytes")
	}
}

func TestUploadBlobDedup(t *testing.T) {
	_, h, token, fake := newTestServerBlobs(t)

	first := postBlob(t, h, token, "", pngBytes)
	if first.Code != http.StatusOK {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	second := postBlob(t, h, token, "", pngBytes)
	if second.Code != http.StatusOK {
		t.Fatalf("second: %d %s", second.Code, second.Body)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("dedup should return an identical body:\n%s\n%s", first.Body, second.Body)
	}
	objs, err := fake.List(t.Context(), "blobs/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("stored %d objects, want 1", len(objs))
	}
	// The object count above cannot tell the two paths apart: a second
	// PutObject of the same content-addressed key also leaves exactly one
	// object. Spec 021 AC1's "issues no second PutObject" is this assertion.
	if n := fake.Puts(); n != 1 {
		t.Fatalf("PutObject called %d times, want 1: the second upload must "+
			"dedup off the index and never reach the object store", n)
	}
}

// TestUploadBlobTooLarge pins the 413. It lowers the cap rather than
// uploading 100 MiB: the streaming path spools every byte the client sends
// before MaxBytesReader refuses it, so asserting at the real cap writes
// 100 MiB to the spool dir on every `make test`.
func TestUploadBlobTooLarge(t *testing.T) {
	fake := blobstore.NewFake()
	_, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen: true, BlobStoreForTest: fake, MaxBlobBytesForTest: 1 << 10,
	})

	rec := postBlob(t, h, token, "", bytes.Repeat([]byte("a"), (1<<10)+1))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body)
	}
	// A rejected upload stores nothing: the cap is enforced before the PUT.
	if n := fake.Puts(); n != 0 {
		t.Fatalf("PutObject called %d times for an over-cap upload, want 0", n)
	}
	// And a payload exactly at the cap is accepted, so the boundary is the
	// cap itself and not one byte under it.
	ok := postBlob(t, h, token, "", bytes.Repeat([]byte("b"), 1<<10))
	if ok.Code != http.StatusOK {
		t.Fatalf("at-cap status = %d, want 200; body = %s", ok.Code, ok.Body)
	}
}

func TestUploadBlobUnconfigured(t *testing.T) {
	_, h, token := newTestServer(t) // no blob store
	rec := postBlob(t, h, token, "", pngBytes)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when blob storage is unconfigured", rec.Code)
	}
}

func TestUploadBlobUnauthorized(t *testing.T) {
	_, h, _, _ := newTestServerBlobs(t)
	rec := postBlob(t, h, "", "", pngBytes)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUploadBlobEmpty(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	rec := postBlob(t, h, token, "", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for an empty payload", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "empty") {
		t.Fatalf("body = %s, want an 'empty' message", rec.Body)
	}
}

func TestServeBlobRedirect(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	hash := uploadedHash(t, h, token, pngBytes)

	req := httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)

	if got.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", got.Code, got.Body)
	}
	loc := got.Header().Get("Location")
	if !strings.Contains(loc, blobstore.Key(hash)) {
		t.Fatalf("Location = %q, want it to contain the object key", loc)
	}
	if !strings.Contains(loc, "response-content-type=image%2Fpng") {
		t.Fatalf("Location = %q, want a response-content-type override", loc)
	}
	// Embeddable type renders inline; anything else downloads.
	if !strings.Contains(loc, "response-content-disposition=inline") {
		t.Fatalf("Location = %q, want inline disposition for an image", loc)
	}
	if cc := got.Header().Get("Cache-Control"); cc != "private, max-age=60" {
		t.Fatalf("Cache-Control = %q, want private, max-age=60", cc)
	}
	if rp := got.Header().Get("Referrer-Policy"); rp != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", rp)
	}
}

func TestServeBlobAttachmentDisposition(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)

	// A text payload is not embeddable, so it must download.
	rec := postBlob(t, h, token, "", []byte("plain log line\n"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var up struct {
		Hash      string `json:"hash"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(up.MediaType, "text/plain") {
		t.Fatalf("media_type = %q, want text/plain...", up.MediaType)
	}

	req := httptest.NewRequest(http.MethodGet, "/blob/"+up.Hash, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if !strings.Contains(got.Header().Get("Location"), "attachment") {
		t.Fatalf("Location = %q, want attachment disposition for a non-embeddable type",
			got.Header().Get("Location"))
	}
}

// TestServeBlobOpenOnAnOptedInInstance pins half of spec 021 §4: the blob
// route inherits the UI's posture, so on an instance with no login provider
// that set LODE_WEB_OPEN, an anonymous fetch succeeds exactly as an anonymous
// page load does. A 401 here would render a task page fine and break every
// image on it.
func TestServeBlobOpenOnAnOptedInInstance(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	hash := uploadedHash(t, h, token, pngBytes)

	req := httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 on an instance that opted into an open UI", got.Code)
	}
}

// TestServeBlobRefusedOnClosedInstance is the other half of the same rule and
// the security-relevant one: no login provider and no LODE_WEB_OPEN means the
// instance serves no web surface at all, and the blob route is refused with
// it. It must not be the one anonymous read path into a closed deployment.
func TestServeBlobRefusedOnClosedInstance(t *testing.T) {
	fake := blobstore.NewFake()
	_, h, token := newTestServerWithConfig(t, api.Config{BlobStoreForTest: fake})
	hash := uploadedHash(t, h, token, pngBytes)

	req := httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401 on a closed instance; body = %s", got.Code, got.Body)
	}
	// And the same fetch with a bearer token still works: the refusal is
	// about having no identity, not about the route.
	req = httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	tok := httptest.NewRecorder()
	h.ServeHTTP(tok, req)
	if tok.Code != http.StatusFound {
		t.Fatalf("token status = %d, want 302; body = %s", tok.Code, tok.Body)
	}
}

// TestServeBlobRejectsInvalidToken is the regression eitherGuard's token
// branch exists to prevent, and the one case the other blob tests cannot
// reach: an *unknown* bearer token on an instance that opted into an open web
// UI. The guard must refuse it outright and must not fall through to
// webSubject, which on this instance yields the open subject and would serve
// the blob to a caller whose credential was just rejected. Every other test
// here sends either a valid token or none at all, so a refactor of the token
// branch into "if err != nil { sub = s.webSubject(...) }" would pass them all
// while opening exactly that hole.
func TestServeBlobRejectsInvalidToken(t *testing.T) {
	fake := blobstore.NewFake()
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	main, admin, err := api.NewServer(st, api.Config{WebOpen: true, BlobStoreForTest: fake})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	hash := uploadedHash(t, main, token, pngBytes)

	req := httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	req.Header.Set("Authorization", "Bearer wl_"+strings.Repeat("a", 40))
	got := httptest.NewRecorder()
	main.ServeHTTP(got, req)
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unknown token even on an open "+
			"instance; body = %s", got.Code, got.Body)
	}
	if loc := got.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want none: a rejected token must not be served", loc)
	}

	// The refusal is counted like any other denial, so a token-guessing
	// campaign against /blob/ is visible rather than silent.
	scrape := doReq(t, admin, "GET", "/metrics", "", nil)
	want := `worklode_authz_decisions_total{outcome="deny",permission="blob.read"} 1`
	if !strings.Contains(scrape.Body.String(), want) {
		t.Errorf("metrics body missing %q", want)
	}
}

// TestServeBlobUnconfigured covers serveBlob's 501: with no bucket wired up
// the route is registered and reachable but has nothing to presign against.
func TestServeBlobUnconfigured(t *testing.T) {
	_, h, token := newTestServer(t) // no blob store
	req := httptest.NewRequest(http.MethodGet, "/blob/"+strings.Repeat("e", 64), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when blob storage is unconfigured; body = %s",
			got.Code, got.Body)
	}
}

// TestServeBlobRequiresSessionWithProvider covers the configured-provider
// case: an anonymous fetch is refused and a valid session cookie is honoured.
func TestServeBlobRequiresSessionWithProvider(t *testing.T) {
	fake := blobstore.NewFake()
	st, h, _ := newOIDCServer(t, api.Config{BlobStoreForTest: fake})
	token := seedActor(t, st, "alice", "human", "Alice", true)
	hash := uploadedHash(t, h, token, pngBytes)

	anon := httptest.NewRecorder()
	h.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", anon.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	req.AddCookie(&http.Cookie{
		Name:  "wl_session",
		Value: api.SignSessionForTest("test-session-secret", "alice", st.Now().Add(time.Hour)),
	})
	sess := httptest.NewRecorder()
	h.ServeHTTP(sess, req)
	if sess.Code != http.StatusFound {
		t.Fatalf("session status = %d, want 302; body = %s", sess.Code, sess.Body)
	}
}

// TestNewServerRejectsUnwritableSpoolDir pins the wiring of the boot-time
// check: gated on blob storage being configured, and fatal when it is.
func TestNewServerRejectsUnwritableSpoolDir(t *testing.T) {
	st := newTestStore(t)
	missing := filepath.Join(t.TempDir(), "not-mounted")

	_, _, err := api.NewServer(st, api.Config{
		BlobStoreForTest: blobstore.NewFake(),
		BlobSpoolDir:     missing,
	})
	if err == nil {
		t.Fatal("NewServer succeeded with an unwritable blob spool directory")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name the offending directory %q", err, missing)
	}

	// A writable directory boots, and blob storage left unconfigured is not
	// checked at all: no bucket means no uploads to spool.
	if _, _, err := api.NewServer(st, api.Config{
		BlobStoreForTest: blobstore.NewFake(),
		BlobSpoolDir:     t.TempDir(),
	}); err != nil {
		t.Fatalf("writable spool dir: %v", err)
	}
	if _, _, err := api.NewServer(st, api.Config{BlobSpoolDir: missing}); err != nil {
		t.Fatalf("blob storage off should not check the spool dir: %v", err)
	}
}

func TestServeBlobNotFound(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	req := httptest.NewRequest(http.MethodGet, "/blob/"+strings.Repeat("e", 64), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got.Code)
	}
}

// TestBlobMetrics asserts both blob families reach the admin /metrics with
// the outcome the request actually had — the difference between a stored blob
// and a deduplicated one, and between a served blob and a missing one, is
// invisible in http_requests_total (all four are 200 or 404 alike).
func TestBlobMetrics(t *testing.T) {
	fake := blobstore.NewFake()
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	main, admin, err := api.NewServer(st, api.Config{WebOpen: true, BlobStoreForTest: fake})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	hash := uploadedHash(t, main, token, pngBytes)
	if rec := postBlob(t, main, token, "", pngBytes); rec.Code != http.StatusOK {
		t.Fatalf("second upload: %d %s", rec.Code, rec.Body)
	}
	if rec := postBlob(t, main, token, "", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty upload: %d %s", rec.Code, rec.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/blob/"+hash, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	main.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("serve: %d %s", rec.Code, rec.Body)
	}
	req = httptest.NewRequest(http.MethodGet, "/blob/"+strings.Repeat("e", 64), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	main.ServeHTTP(httptest.NewRecorder(), req)

	scrape := doReq(t, admin, "GET", "/metrics", "", nil)
	if scrape.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", scrape.Code)
	}
	body := scrape.Body.String()
	for _, want := range []string{
		`worklode_blob_uploads_total{outcome="stored"} 1`,
		`worklode_blob_uploads_total{outcome="deduplicated"} 1`,
		`worklode_blob_uploads_total{outcome="empty"} 1`,
		// Pre-initialised, so an outcome nothing hit still reports zero.
		`worklode_blob_uploads_total{outcome="unconfigured"} 0`,
		`worklode_blob_serves_total{outcome="redirect"} 1`,
		`worklode_blob_serves_total{outcome="not_found"} 1`,
		`worklode_blob_serves_total{outcome="storage_error"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

// servedDisposition drives GET /blob/{hash} with the given raw query and
// returns the Content-Disposition the presigned URL will carry.
func servedDisposition(t *testing.T, h http.Handler, token, hash, rawQuery string) string {
	t.Helper()
	target := "/blob/" + hash
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	if strings.ContainsAny(loc, "\r\n") {
		t.Fatalf("Location %q carries a line break", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	return u.Query().Get("response-content-disposition")
}

// TestServeBlobFilenameDisposition pins the promise of spec 021 §2: a
// reference that carries a name is served under it, encoded per RFC 6266.
// The name arrives as a query parameter because task_blobs.filename is
// per-reference while the route is per-blob.
func TestServeBlobFilenameDisposition(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	hash := uploadedHash(t, h, token, []byte("crash log line\n"))

	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"no name falls back to the bare token", "", "attachment"},
		{"a plain name is served verbatim", "filename=crash.log",
			"attachment; filename=crash.log"},
		{"spaces are quoted", "filename=" + url.QueryEscape("crash report (1).log"),
			`attachment; filename="crash report (1).log"`},
		{"unicode carries both halves", "filename=" + url.QueryEscape("crash-résumé.log"),
			`attachment; filename=crash-r_sum_.log; filename*=utf-8''crash-r%C3%A9sum%C3%A9.log`},
		// Percent-encoded CRLF in the query is the header-injection attempt.
		// It must not reach the header, and must not add one.
		{"CRLF cannot inject a header", "filename=crash.log%0d%0aX-Injected:%201",
			`attachment; filename="crash.logX-Injected: 1"`},
		{"quotes cannot end the parameter", "filename=" + url.QueryEscape(`"; filename="evil.exe`),
			`attachment; filename="\"; filename=\"evil.exe"`},
		{"a path is reduced to its last segment", "filename=" + url.QueryEscape("../../etc/passwd"),
			"attachment; filename=passwd"},
		{"an unparseable query is no name at all", "filename=%zz", "attachment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := servedDisposition(t, h, token, hash, tc.query); got != tc.want {
				t.Fatalf("Content-Disposition = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestServeBlobFilenameKeepsTheInlineToken: the name is cosmetic, the token
// is not. Spec 021 §6 rests the whole of the per-object hardening on
// inline-vs-attachment, so a caller-supplied name must not be able to move
// it — including by naming a text blob something executable-looking.
func TestServeBlobFilenameKeepsTheInlineToken(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)

	img := uploadedHash(t, h, token, pngBytes)
	if got := servedDisposition(t, h, token, img, "filename=shot.png"); got != "inline; filename=shot.png" {
		t.Fatalf("image disposition = %q, want it to stay inline", got)
	}
	txt := uploadedHash(t, h, token, []byte("plain log line\n"))
	if got := servedDisposition(t, h, token, txt, "filename=notes.png"); got != "attachment; filename=notes.png" {
		t.Fatalf("text disposition = %q, want it to stay an attachment", got)
	}
}
