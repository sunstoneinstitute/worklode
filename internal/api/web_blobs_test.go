package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

// getPage fetches a web page anonymously (newTestServerBlobs opts into
// WebOpen) and fails unless it renders.
func getPage(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body %s", path, rec.Code, rec.Body)
	}
	return rec
}

// TestTaskPageRendersMarkdown asserts the page renders a task body as
// markdown rather than dumping it in a <pre>, that an embedded blob becomes a
// real image, that a hostile body is neutered on the way, and that the page
// carries the Content-Security-Policy that backs all of it up (spec 021 §8).
func TestTaskPageRendersMarkdown(t *testing.T) {
	t.Parallel()
	st, h, token, _ := newTestServerBlobs(t)
	createProject(t, st, "proj")

	hash := uploadedHash(t, h, token, pngBytes)
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "shot", "priority": "medium", "kind": "bug",
		"body": "## Repro\n\n![shot](/blob/" + hash + ")\n\n" +
			"<script>alert(1)</script>\n\n" +
			"![offsite](https://evil.example/p.png)\n",
	})
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created task has no id: %v", created)
	}

	page := getPage(t, h, "/tasks/"+id)
	body := page.Body.String()

	if !strings.Contains(body, `<img src="/blob/`+hash+`"`) {
		t.Errorf("image not rendered:\n%s", body)
	}
	if !strings.Contains(body, "<h2") {
		t.Errorf("markdown not rendered as HTML:\n%s", body)
	}
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "alert(1)") {
		t.Errorf("script survived:\n%s", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Errorf("remote image source survived:\n%s", body)
	}

	// The blob the body embeds is a reference the task carries, so it is
	// listed as an attachment too — the same row GET
	// /api/v1/tasks/{id}/blobs answers with, addressed the same way.
	bodyContains(t, body, ">Attachments<", `href="/blob/`+hash+`"`, "image/png")

	// Every directive is load-bearing: without img-src/media-src the blob
	// redirect's origin is refused, and without object-src/base-uri a body
	// that slipped past the sanitiser would have somewhere to go.
	csp := page.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"img-src 'self'",
		"media-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %q", want, csp)
		}
	}
	// An unconfigured blob endpoint must not leave a dangling source list.
	if strings.Contains(csp, "'self' ;") || strings.Contains(csp, "'self';;") {
		t.Errorf("CSP has an empty source: %q", csp)
	}
}

// TestTaskPageCSPNamesTheBlobOrigin: GET /blob/{hash} redirects to presigned
// object storage, and a redirect target is matched against the source list by
// origin, so an embedded image loads only if the CSP names that origin.
func TestTaskPageCSPNamesTheBlobOrigin(t *testing.T) {
	t.Parallel()
	const origin = "https://hel1.your-objectstorage.com"
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen:          true,
		BlobStoreForTest: blobstore.NewFake(),
		// The path is deliberately present: only the origin belongs in a CSP.
		BlobEndpoint: origin + "/some/path",
	})
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "shot", "priority": "medium", "kind": "bug",
	})

	csp := getPage(t, h, "/tasks/"+created["id"].(string)).Header().Get("Content-Security-Policy")
	for _, want := range []string{"img-src 'self' " + origin + ";", "media-src 'self' " + origin + ";"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %q", want, csp)
		}
	}
	if strings.Contains(csp, origin+"/some/path") {
		t.Errorf("CSP carries the endpoint path, not just its origin: %q", csp)
	}
}

// TestTaskPageWithoutAttachmentsOmitsTheCard: the attachments card is a fact
// about the task, not page furniture — a task with no blobs must not render
// an empty one.
func TestTaskPageWithoutAttachmentsOmitsTheCard(t *testing.T) {
	t.Parallel()
	st, h, token, _ := newTestServerBlobs(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "plain", "priority": "medium", "kind": "bug",
		"body": "no attachments here",
	})

	body := getPage(t, h, "/tasks/"+created["id"].(string)).Body.String()
	if strings.Contains(body, ">Attachments<") {
		t.Errorf("attachments card rendered for a task with no blobs:\n%s", body)
	}
	assertShell(t, body)
}
