//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// blobPublicURL is what the brief's absolute blob URLs are built from
// (spec 021 §10). It has to be fixed at NewServer time, before httptest
// hands out a port, so it is a name rather than the test server's own URL.
const blobPublicURL = "https://worklode.e2e.test"

// pngA and pngB are two distinct 1x1-ish PNGs: same signature, so
// http.DetectContentType sniffs both as image/png, different bytes, so they
// hash differently and the "one survives, one is collected" half of spec 021
// §15 criterion 5 has two blobs to tell apart.
var (
	pngA = []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89,
	}
	pngB = append(append([]byte{}, pngA...), 0x42, 0x42)

	crashLog = []byte("panic: e2e\n\ngoroutine 1 [running]:\nmain.main()\n")
)

// noRedirect follows nothing: GET /blob/{hash} 302s to a presigned URL on a
// host that does not resolve, and the redirect itself is what the test is
// about.
var noRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// getBlob issues GET /blob/{hash}, optionally with a bearer token, without
// following the redirect.
func getBlob(t *testing.T, baseURL, hash, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/blob/"+hash, nil)
	if err != nil {
		t.Fatalf("build blob request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET /blob/%s: %v", hash, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// objectExists reports whether the object store still holds a blob's bytes.
func objectExists(t *testing.T, fake *blobstore.Fake, hash string) bool {
	t.Helper()
	_, err := fake.Open(blobstore.Key(hash))
	if err == nil {
		return true
	}
	if errors.Is(err, blobstore.ErrNotFound) {
		return false
	}
	t.Fatalf("open object %s: %v", hash, err)
	return false
}

// taskBlobsByHash indexes a task's reference rows for assertion.
func taskBlobsByHash(t *testing.T, c *cli.Client, id string) map[string]model.TaskBlob {
	t.Helper()
	refs, err := c.ListTaskBlobs(context.Background(), id)
	if err != nil {
		t.Fatalf("list task blobs: %v", err)
	}
	out := make(map[string]model.TaskBlob, len(refs))
	for _, r := range refs {
		out[r.Hash] = r
	}
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestBlobLifecycle drives spec 021 end to end through the real stack: two
// image uploads and a log upload over POST /api/v1/blobs, a task body that
// cites the images, an explicit attachment, the redirect GET /blob/{hash}
// answers with for a bearer token and for a browser, the rendered task page,
// the brief's blobs array, and then both garbage-collection sweeps — the row
// sweep and the orphan-object sweep — including the 24-hour grace period that
// every package-level GC test skips by passing grace_hours: 0.
//
// The object store is the one substituted dependency (blobstore.Fake stands
// in for Hetzner Object Storage, as store.OpenTestStore stands in for the
// production database). Everything else goes through the public surfaces:
// the JSON API, the web pages, and no direct store writes.
func TestBlobLifecycle(t *testing.T) {
	ctx := context.Background()

	fake := blobstore.NewFake()
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:   bootstrapToken,
		BlobStoreForTest: fake,
		PublicURL:        blobPublicURL,
		// The task page is fetched anonymously below, and a blob a browser
		// loads is a subresource of it.
		WebOpen: true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "blobs", Name: "Blobs", Key: "BLOB",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "reporter", Kind: "human", DisplayName: "Reporter",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "reporter", "e2e blobs", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	// An ordinary user uploads and authors; GC is admin-only (permBlobAdmin).
	user := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// 1. Upload. The server sniffs the media type and content-addresses the
	// bytes; re-posting identical bytes must return the same hash and must
	// not create a second object.
	upA, err := user.UploadBlob(ctx, bytes.NewReader(pngA), int64(len(pngA)))
	if err != nil {
		t.Fatalf("upload pngA: %v", err)
	}
	if upA.Hash != sha256Hex(pngA) {
		t.Fatalf("hash = %q, want sha256 of the bytes %q", upA.Hash, sha256Hex(pngA))
	}
	if upA.MediaType != "image/png" || upA.Size != int64(len(pngA)) || upA.URL != "/blob/"+upA.Hash {
		t.Fatalf("upload response = %+v", upA)
	}
	again, err := user.UploadBlob(ctx, bytes.NewReader(pngA), int64(len(pngA)))
	if err != nil {
		t.Fatalf("re-upload pngA: %v", err)
	}
	if again.Hash != upA.Hash {
		t.Fatalf("re-upload hash = %q, want %q", again.Hash, upA.Hash)
	}
	upB, err := user.UploadBlob(ctx, bytes.NewReader(pngB), int64(len(pngB)))
	if err != nil {
		t.Fatalf("upload pngB: %v", err)
	}
	upLog, err := user.UploadBlob(ctx, bytes.NewReader(crashLog), int64(len(crashLog)))
	if err != nil {
		t.Fatalf("upload crash log: %v", err)
	}
	if !strings.HasPrefix(upLog.MediaType, "text/plain") {
		t.Fatalf("log media type = %q, want text/plain", upLog.MediaType)
	}
	objs, err := fake.List(ctx, "blobs/")
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("object store holds %d objects after 4 uploads, want 3 (dedup)", len(objs))
	}

	// 2. A body that cites both images. The reference graph is derived from
	// the body in the same transaction as the write (spec 021 §1).
	task, _, err := user.CreateTask(ctx, model.CreateTaskInput{
		Project:  "blobs",
		Title:    "Map flashes narrow at 390px",
		Priority: "high",
		Kind:     "bug",
		Body: "## Repro\n\nScroll back up.\n\n" +
			"![before](" + upA.URL + ")\n\n" +
			"![after](" + upB.URL + ")\n",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	refs := taskBlobsByHash(t, user, task.ID)
	if len(refs) != 2 {
		t.Fatalf("task has %d blob refs, want 2: %+v", len(refs), refs)
	}
	for _, h := range []string{upA.Hash, upB.Hash} {
		r, ok := refs[h]
		if !ok {
			t.Fatalf("no reference row for %s: %+v", h, refs)
		}
		if !r.Embedded || r.Attached {
			t.Fatalf("ref %s: embedded = %v, attached = %v, want embedded only", h, r.Embedded, r.Attached)
		}
		if r.MediaType != "image/png" || r.URL != "/blob/"+h {
			t.Fatalf("ref %s = %+v", h, r)
		}
	}

	// 3. An attachment is declared, not derived: it appends nothing to the
	// body and survives a body rewrite (step 6).
	if err := user.AttachBlob(ctx, task.ID, upLog.Hash, "crash.log"); err != nil {
		t.Fatalf("attach log: %v", err)
	}
	refs = taskBlobsByHash(t, user, task.ID)
	logRef, ok := refs[upLog.Hash]
	if !ok {
		t.Fatalf("no reference row for the attached log: %+v", refs)
	}
	if logRef.Attached == false || logRef.Embedded {
		t.Fatalf("log ref: attached = %v, embedded = %v, want attached only", logRef.Attached, logRef.Embedded)
	}
	if logRef.Filename != "crash.log" {
		t.Fatalf("log filename = %q", logRef.Filename)
	}
	detail, _, err := user.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if strings.Contains(detail.Body, "crash.log") {
		t.Fatalf("attach appended to the body:\n%s", detail.Body)
	}

	// 4. Serving. Both an agent's bearer token and a browser (no credential,
	// WebOpen) get a 302 to a presigned URL carrying the sniffed type; the
	// disposition is inline for an image and attachment for a log.
	for _, tc := range []struct {
		name, hash, token, wantType, wantDisposition string
	}{
		{"bearer image", upA.Hash, tok.Token, "image/png", "inline"},
		{"browser image", upA.Hash, "", "image/png", "inline"},
		{"bearer log", upLog.Hash, tok.Token, "text/plain", "attachment"},
	} {
		resp := getBlob(t, srv.URL, tc.hash, tc.token)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("%s: GET /blob/%s = %d, want 302", tc.name, tc.hash, resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if !strings.Contains(loc, blobstore.Key(tc.hash)) {
			t.Errorf("%s: Location %q does not name the object key", tc.name, loc)
		}
		if !strings.Contains(loc, "response-content-type="+strings.ReplaceAll(tc.wantType, "/", "%2F")) {
			t.Errorf("%s: Location %q does not carry content type %q", tc.name, loc, tc.wantType)
		}
		if !strings.Contains(loc, "response-content-disposition="+tc.wantDisposition) {
			t.Errorf("%s: Location %q does not carry disposition %q", tc.name, loc, tc.wantDisposition)
		}
	}

	// 5. The rendered page: images become real <img> elements pointing at the
	// permanent /blob/ reference, and the attachment is listed for download.
	code, page := getPage(t, srv.URL+"/tasks/"+task.ID)
	if code != http.StatusOK {
		t.Fatalf("task page: %d", code)
	}
	for _, want := range []string{
		`<img src="/blob/` + upA.Hash + `"`,
		`<img src="/blob/` + upB.Hash + `"`,
		">Attachments<",
		`href="/blob/` + upLog.Hash + `"`,
		"crash.log",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("task page missing %q", want)
		}
	}

	// 6. The brief carries the same graph as absolute URLs, so an agent never
	// has to parse markdown to find the picture (spec 021 §10).
	brief, _, err := user.BriefWithoutSkills(ctx, task.ID)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	if len(brief.Blobs) != 3 {
		t.Fatalf("brief lists %d blobs, want 3: %+v", len(brief.Blobs), brief.Blobs)
	}
	for _, b := range brief.Blobs {
		if b.URL != blobPublicURL+"/blob/"+b.Hash {
			t.Errorf("brief blob %s: url = %q, want absolute", b.Hash, b.URL)
		}
		switch b.Hash {
		case upA.Hash, upB.Hash:
			if b.MediaType != "image/png" || !b.Embedded {
				t.Errorf("brief blob %s = %+v, want embedded image/png", b.Hash, b)
			}
		case upLog.Hash:
			if !strings.HasPrefix(b.MediaType, "text/plain") || b.Embedded {
				t.Errorf("brief blob %s = %+v, want non-embedded text/plain", b.Hash, b)
			}
		default:
			t.Errorf("brief lists an unexpected blob %+v", b)
		}
	}

	// 7. Edit the second image out of the body. The derived half of the graph
	// follows the body: the row goes, the attachment does not.
	newBody := "## Repro\n\nScroll back up.\n\n![before](" + upA.URL + ")\n"
	if _, _, err := user.EditTask(ctx, task.ID, model.EditTaskInput{Body: &newBody}); err != nil {
		t.Fatalf("edit task: %v", err)
	}
	refs = taskBlobsByHash(t, user, task.ID)
	if _, still := refs[upB.Hash]; still {
		t.Fatalf("dropped image still referenced: %+v", refs)
	}
	if r, ok := refs[upA.Hash]; !ok || !r.Embedded {
		t.Fatalf("surviving image ref = %+v (present %v)", r, ok)
	}
	if r, ok := refs[upLog.Hash]; !ok || !r.Attached {
		t.Fatalf("attachment did not survive the body rewrite: %+v (present %v)", r, ok)
	}

	// 8. Two orphan objects, one on each side of the grace period. The write
	// path deliberately creates orphans on partial failure (object before
	// row), which is the only way one can exist — so the test places them the
	// way that failure would.
	staleOrphan := blobstore.Key(sha256Hex([]byte("stale orphan")))
	freshOrphan := blobstore.Key(sha256Hex([]byte("fresh orphan")))
	fake.PutAt(staleOrphan, []byte("stale orphan"), time.Now().Add(-48*time.Hour))
	fake.PutAt(freshOrphan, []byte("fresh orphan"), time.Now())

	// 9. Dry run is the default and deletes nothing: it names the dropped
	// image's row and the aged orphan object and leaves both in place.
	zero := 0
	dry, _, err := admin.BlobGC(ctx, true, &zero)
	if err != nil {
		t.Fatalf("blob gc dry run: %v", err)
	}
	if len(dry.Unreferenced) != 1 || dry.Unreferenced[0] != upB.Hash {
		t.Fatalf("dry run unreferenced = %v, want [%s]", dry.Unreferenced, upB.Hash)
	}
	if dry.Deleted != 0 {
		t.Fatalf("dry run deleted %d objects", dry.Deleted)
	}
	if !objectExists(t, fake, upB.Hash) {
		t.Fatal("dry run deleted the dropped image's object")
	}

	// 10. Apply at the default 24-hour grace. Nothing uploaded during this
	// test is old enough for either sweep, so only the aged orphan goes —
	// which is the grace period doing its job in both directions.
	graced, _, err := admin.BlobGC(ctx, false, nil)
	if err != nil {
		t.Fatalf("blob gc at default grace: %v", err)
	}
	if len(graced.Unreferenced) != 0 {
		t.Errorf("default grace collected a row younger than 24h: %v", graced.Unreferenced)
	}
	if len(graced.OrphanObjects) != 1 || graced.OrphanObjects[0] != staleOrphan {
		t.Fatalf("default grace orphans = %v, want [%s]", graced.OrphanObjects, staleOrphan)
	}
	if graced.Deleted != 1 {
		t.Errorf("default grace deleted %d, want 1", graced.Deleted)
	}
	if len(graced.Errors) != 0 {
		t.Errorf("default grace reported errors: %v", graced.Errors)
	}
	if _, err := fake.Open(staleOrphan); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("aged orphan survived the sweep: %v", err)
	}
	if _, err := fake.Open(freshOrphan); err != nil {
		t.Errorf("orphan younger than the grace period was deleted: %v", err)
	}
	if !objectExists(t, fake, upB.Hash) {
		t.Error("a blob row younger than the grace period was collected")
	}

	// 11. Apply at zero grace: both sweeps now bite. The dropped image's row
	// and object go together, the fresh orphan goes, and everything the task
	// still references is left alone.
	swept, _, err := admin.BlobGC(ctx, false, &zero)
	if err != nil {
		t.Fatalf("blob gc apply: %v", err)
	}
	if len(swept.Unreferenced) != 1 || swept.Unreferenced[0] != upB.Hash {
		t.Fatalf("apply unreferenced = %v, want [%s]", swept.Unreferenced, upB.Hash)
	}
	if len(swept.OrphanObjects) != 1 || swept.OrphanObjects[0] != freshOrphan {
		t.Fatalf("apply orphans = %v, want [%s]", swept.OrphanObjects, freshOrphan)
	}
	if swept.Deleted != 2 {
		t.Errorf("apply deleted %d, want 2", swept.Deleted)
	}
	if len(swept.Errors) != 0 {
		t.Errorf("apply reported errors: %v", swept.Errors)
	}
	if objectExists(t, fake, upB.Hash) {
		t.Error("collected blob's object survived")
	}
	if resp := getBlob(t, srv.URL, upB.Hash, tok.Token); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /blob/%s after collection = %d, want 404", upB.Hash, resp.StatusCode)
	}
	if !objectExists(t, fake, upA.Hash) || !objectExists(t, fake, upLog.Hash) {
		t.Fatal("a still-referenced blob was collected")
	}
	if resp := getBlob(t, srv.URL, upA.Hash, tok.Token); resp.StatusCode != http.StatusFound {
		t.Errorf("GET /blob/%s after the sweep = %d, want 302", upA.Hash, resp.StatusCode)
	}
	refs = taskBlobsByHash(t, user, task.ID)
	if len(refs) != 2 {
		t.Fatalf("task has %d refs after GC, want 2: %+v", len(refs), refs)
	}
	code, page = getPage(t, srv.URL+"/tasks/"+task.ID)
	if code != http.StatusOK {
		t.Fatalf("task page after GC: %d", code)
	}
	if !strings.Contains(page, `<img src="/blob/`+upA.Hash+`"`) {
		t.Error("surviving image no longer renders on the task page")
	}
	if strings.Contains(page, upB.Hash) {
		t.Error("collected image still referenced by the task page")
	}
}
