package api_test

// canonicalurl_test.go covers the S20 canonical URL scheme for tasks and
// documents: /projects/{proj}/{kind}/{n} serves the page, /{ver} serves a
// document version, and the root routes /tasks/{id}, /docs/{ref} and
// /docs/versions/{id}/{n} redirect there.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestCanonicalTaskAndDocURLs(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": projID, "title": "A bug", "priority": "medium", "kind": "bug",
	})
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1})

	base := "/projects/" + projID
	for path, want := range map[string]string{
		base + "/bug/1":  "A bug",
		base + "/spec/1": "Intro.",
		// A document version is a path segment on the canonical URL.
		base + "/spec/1/1": "Intro.",
	} {
		rr := doReq(t, h, http.MethodGet, path, "", nil)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("%s = %d, want 200 containing %q", path, rr.Code, want)
		}
	}

	for path, want := range map[string]string{
		// The root routes redirect to the canonical URL.
		"/tasks/WL-1":     base + "/bug/1",
		"/docs/WL-SPEC-1": base + "/spec/1",
		fmt.Sprintf("/docs/versions/%d/1", doc.ID): base + "/spec/1/1",
		// ?v=<n> is a 302 to the version path.
		"/docs/WL-SPEC-1?v=1": base + "/spec/1/1",
		base + "/spec/1?v=1":  base + "/spec/1/1",
		// Other query parameters survive the redirect.
		"/docs/WL-SPEC-1?body=source": base + "/spec/1?body=source",
		// A task under the wrong kind redirects to its own kind.
		base + "/feature/1": base + "/bug/1",
		// An uppercase project key redirects to the id form.
		"/projects/WL/bug/1": base + "/bug/1",
		// The bare-reference shortcut lands on the canonical URL in one hop.
		"/WL-1": base + "/bug/1",
	} {
		rr := doReq(t, h, http.MethodGet, path, "", nil)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
			t.Errorf("%s = %d %q, want 302 %q", path, rr.Code, rr.Header().Get("Location"), want)
		}
	}

	for _, path := range []string{
		base + "/bug/99",
		base + "/spec/99",
		base + "/spec/1/99",
		base + "/bug/1/1", // tasks have no versions
		base + "/widget/1",
		"/tasks/WL-99",
	} {
		if rr := doReq(t, h, http.MethodGet, path, "", nil); rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rr.Code)
		}
	}
}
