---
status: accepted
task: WL-19
implements: docs/specs/021-images-in-task-bodies.md
---
# Blobs 3 — Rendering, GC, and import mirroring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish `docs/specs/021-images-in-task-bodies.md` §8 (web rendering and its sanitisation), §11 (two garbage-collection sweeps) and §12 (mirroring remote images on import with an SSRF guard). At the end, screenshots render in the web UI, unreferenced bytes are collectable, and an imported GitHub issue carries its images.

**Architecture:** A new `internal/mdrender` package renders a task body permissively with goldmark, then sanitises the **output HTML** with bluemonday — GitHub's own order of operations — with `img`/`video` `src` pinned to `^/blob/[0-9a-f]{64}$`. GC is two sweeps in `lode admin blob gc`: unreferenced `blobs` rows, and orphan objects in the bucket, both with a 24-hour grace period. Import mirroring reuses the plan-1 upload path behind a new `internal/safefetch` guard.

**Tech Stack:** Go 1.26, `github.com/yuin/goldmark` (direct as of plan 2), `github.com/microcosm-cc/bluemonday` (new). Builds on plans 1 and 2 — do not start until both are merged.

**Verification note:** store and api tests skip silently without a reachable Postgres unless `CI=1`. Run `docker compose up -d postgres` first and confirm `ok`, not `SKIP`.

---

### Task 1: mdrender — permissive render, strict sanitise

**Files:**
- Create: `internal/mdrender/mdrender.go`
- Create: `internal/mdrender/mdrender_test.go`
- Modify: `go.mod`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/microcosm-cc/bluemonday@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/mdrender/mdrender_test.go`:

```go
package mdrender_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
)

const validHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestHostileBodies is the load-bearing test. Task bodies are untrusted:
// spec 020's inbox import writes GitHub issue text straight into
// tasks.body, so anyone who can open an issue on a mapped repo controls
// this input.
func TestHostileBodies(t *testing.T) {
	cases := []struct {
		name, body string
		absent     []string
	}{
		{"script tag", "<script>alert(1)</script>", []string{"<script", "alert(1)"}},
		{"javascript href", "[x](javascript:alert(1))", []string{"javascript:"}},
		{"remote image", "![](https://evil.example/p.png)", []string{"evil.example"}},
		{"protocol-relative image", "![](//evil.example/p.png)", []string{"evil.example"}},
		{"traversal src", `<img src="/blob/../../etc/passwd">`, []string{"etc/passwd"}},
		{"onerror", `<img src="/blob/` + validHash + `" onerror="alert(1)">`, []string{"onerror"}},
		{"data uri", "![](data:text/html;base64,PHNjcmlwdD4=)", []string{"data:"}},
		{"iframe", `<iframe src="https://evil.example"></iframe>`, []string{"<iframe"}},
		{"svg script", `<svg><script>alert(1)</script></svg>`, []string{"<script"}},
		{"uppercase hash", "![](/blob/" + strings.ToUpper(validHash) + ")", []string{"/blob/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(mdrender.Body(tc.body))
			for _, bad := range tc.absent {
				if strings.Contains(got, bad) {
					t.Fatalf("output contains %q:\n%s", bad, got)
				}
			}
		})
	}
}

// TestSafeMarkupSurvives: sanitising must not gut ordinary formatting.
func TestSafeMarkupSurvives(t *testing.T) {
	body := "# Heading\n\n**bold** and `code`\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"- [ ] todo\n- [x] done\n\n" +
		"<b>raw bold</b>\n\n" +
		"[link](https://example.com)\n\n" +
		"![shot](/blob/" + validHash + ")\n"
	got := string(mdrender.Body(body))
	for _, want := range []string{
		"<h1", "<strong>", "<code>", "<table", "<b>raw bold</b>",
		`href="https://example.com"`, `src="/blob/` + validHash + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestVideoAllowed(t *testing.T) {
	body := `<video src="/blob/` + validHash + `" controls></video>`
	got := string(mdrender.Body(body))
	if !strings.Contains(got, "<video") || !strings.Contains(got, "controls") {
		t.Fatalf("video stripped:\n%s", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/mdrender/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Write the implementation**

Create `internal/mdrender/mdrender.go`:

```go
// Package mdrender turns an untrusted task body into safe HTML.
//
// The pipeline is GitHub's: render permissively, then sanitise the OUTPUT
// HTML. Escaping at the parser instead would forbid the limited inline HTML
// authors expect; sanitising after gives the same expressiveness with an
// allowlist that is easy to audit in one place.
package mdrender

import (
	"bytes"
	"html/template"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// blobSrc is the only image or video source a body may reference. Remote
// sources are dropped rather than proxied: an imported issue body must not
// turn every page view into a callback to a third party. Spec 021 section 12
// mirrors remote images into blobs at import time, so this costs nothing.
var blobSrc = regexp.MustCompile(`^/blob/[0-9a-f]{64}$`)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	// Unsafe here means "let raw HTML through to the sanitiser", not "trust
	// it". The bluemonday policy below is the actual boundary.
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

var policy = buildPolicy()

func buildPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// UGCPolicy already limits href to http/https/mailto and adds
	// rel=nofollow. Asserted in tests regardless: the CMS shipped exactly
	// this bug in "reject non-http(s) licence URLs before rendering as href".
	p.AllowAttrs("src").Matching(blobSrc).OnElements("img", "video", "source")
	p.AllowAttrs("alt", "title").OnElements("img")
	p.AllowElements("video", "source")
	p.AllowAttrs("controls", "preload", "poster").OnElements("video")
	p.AllowAttrs("type").OnElements("source")
	// GFM task lists render as disabled checkboxes.
	p.AllowAttrs("type", "checked", "disabled").
		Matching(regexp.MustCompile(`^(checkbox|checked|disabled)$`)).
		OnElements("input")
	return p
}

// Body renders an untrusted markdown body to sanitised HTML.
func Body(body string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(body), &buf); err != nil {
		// Rendering is a nicety; never lose the body over it.
		return template.HTML(template.HTMLEscapeString(body))
	}
	return template.HTML(policy.SanitizeBytes(buf.Bytes()))
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/mdrender/ -v`
Expected: PASS — twelve subtests plus two.

If `poster` on `<video>` fails the traversal case, constrain it with `.Matching(blobSrc)` too.

- [ ] **Step 6: Commit**

```bash
git add internal/mdrender/ go.mod go.sum
git commit -m "feat(mdrender): sanitised markdown rendering for untrusted task bodies"
```

---

### Task 2: Render the body in the web UI

**Files:**
- Modify: `internal/api/templates/task.html:15-16`
- Modify: `internal/api/templates/layout.html` (CSP, image styling)
- Modify: `internal/api/web.go`
- Create: `internal/api/web_blobs_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/web_blobs_test.go`:

```go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTaskPageRendersMarkdown asserts the page renders markdown rather than
// dumping it in a <pre>, and that a hostile body is neutered on the way.
func TestTaskPageRendersMarkdown(t *testing.T) {
	st, h, token, _ := newTestServerBlobs(t)
	if err := st.CreateProject(t.Context(), "p", "P", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	up := postBlob(t, h, token, "", pngBytes)
	var blob struct{ Hash string `json:"hash"` }
	json.Unmarshal(up.Body.Bytes(), &blob)

	rec := doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "p", "title": "shot", "priority": "medium", "kind": "bug",
		"body": "## Repro\n\n![shot](/blob/" + blob.Hash + ")\n\n" +
			"<script>alert(1)</script>",
	})
	var created struct{ ID string `json:"id"` }
	json.Unmarshal(rec.Body.Bytes(), &created)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID, nil)
	page := httptest.NewRecorder()
	h.ServeHTTP(page, req)
	if page.Code != http.StatusOK {
		t.Fatalf("status = %d", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, `<img src="/blob/`+blob.Hash+`"`) {
		t.Fatalf("image not rendered:\n%s", body)
	}
	if !strings.Contains(body, "<h2") {
		t.Fatalf("markdown not rendered:\n%s", body)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("script survived:\n%s", body)
	}
	if !strings.Contains(page.Header().Get("Content-Security-Policy"), "img-src") {
		t.Fatalf("missing CSP: %q", page.Header().Get("Content-Security-Policy"))
	}
}

// TestWebUIHasNoMutatingForms keeps the CSRF-free property a property rather
// than an assumption: every web route is GET and no template posts. The
// first mutating surface must add a per-session token and flip this test.
func TestWebUIHasNoMutatingForms(t *testing.T) {
	st, h, token, _ := newTestServerBlobs(t)
	if err := st.CreateProject(t.Context(), "p", "P", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, path := range []string{"/", "/projects/p"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		lower := strings.ToLower(rec.Body.String())
		if strings.Contains(lower, "<form") {
			t.Fatalf("%s contains a form; mutating web surfaces need CSRF protection", path)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run 'TestTaskPageRenders|TestWebUIHasNoMutating' -v`
Expected: FAIL — body is inside `<pre>`.

- [ ] **Step 3: Render in the template**

In `internal/api/templates/task.html`, replace lines 15–16:

```html
<h2>Body</h2>
<div class="body">{{.BodyHTML}}</div>
{{if .Blobs}}
<h2>Attachments</h2>
<ul class="attachments">
  {{range .Blobs}}
  <li><a href="{{.URL}}">{{if .Filename}}{{.Filename}}{{else}}{{.Hash}}{{end}}</a>
      <span class="meta">{{.MediaType}} · {{.Size}} bytes</span></li>
  {{end}}
</ul>
{{end}}
```

In `internal/api/web.go`'s task-page handler, add `BodyHTML: mdrender.Body(task.Body)` and `Blobs` (from `ListTaskBlobs`) to the template data struct. `BodyHTML` must be typed `template.HTML` — that is what makes `html/template` emit it unescaped, and it is safe only because `mdrender.Body` sanitised it.

- [ ] **Step 4: Add the CSP and image styling**

In `internal/api/web.go`, set on every web response:

```go
	// The blob route redirects to object storage, so that origin has to be
	// reachable for img and media. Everything else stays same-origin.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' "+s.blobOrigin()+
			"; media-src 'self' "+s.blobOrigin()+
			"; script-src 'none'; object-src 'none'; base-uri 'none'")
```

Add:

```go
// blobOrigin is the object-storage origin a blob redirect lands on, for the
// page CSP. Empty when blob storage is unconfigured.
func (s *server) blobOrigin() string {
	if s.cfg.BlobEndpoint == "" {
		return ""
	}
	u, err := url.Parse(s.cfg.BlobEndpoint)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
```

In `layout.html`'s style block, keep images inside the column:

```css
  .body img, .body video { max-width: 100%; height: auto; }
  .body table { border-collapse: collapse; }
  .body td, .body th { border: 1px solid #ccc; padding: 0.25rem 0.5rem; }
  .attachments .meta { color: #666; font-size: 0.9em; }
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestTaskPage|TestWebUIHasNoMutating' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/templates/ internal/api/web.go internal/api/web_blobs_test.go
git commit -m "feat(web): render task bodies as sanitised markdown with attachments"
```

---

### Task 3: GC sweep 1 — unreferenced blobs

**Files:**
- Modify: `internal/store/blobs.go`
- Modify: `internal/store/blobs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/blobs_test.go`:

```go
func TestUnreferencedBlobs(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := seedTask(t, s, "WL-9"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	referenced := "11" + strings.Repeat("0", 62)
	orphaned := "22" + strings.Repeat("0", 62)
	fresh := "33" + strings.Repeat("0", 62)

	old := time.Now().UTC().Add(-48 * time.Hour)
	s.SetNowFunc(func() time.Time { return old })
	for _, h := range []string{referenced, orphaned} {
		if _, err := s.InsertBlob(ctx, h, "image/png", 1); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.SetNowFunc(func() time.Time { return time.Now().UTC() })
	if _, err := s.InsertBlob(ctx, fresh, "image/png", 1); err != nil {
		t.Fatalf("insert fresh: %v", err)
	}
	if err := s.AttachBlob(ctx, "WL-9", referenced, "a.png", "alice"); err != nil {
		t.Fatalf("attach: %v", err)
	}

	got, err := s.UnreferencedBlobs(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("unreferenced: %v", err)
	}
	if len(got) != 1 || got[0].Hash != orphaned {
		t.Fatalf("got %+v, want only %s (referenced kept, fresh inside grace)", got, orphaned)
	}

	deleted, err := s.DeleteBlobIfUnreferenced(ctx, orphaned)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v; want true, nil", deleted, err)
	}
	deleted, err = s.DeleteBlobIfUnreferenced(ctx, referenced)
	if err != nil {
		t.Fatalf("delete referenced: %v", err)
	}
	if deleted {
		t.Fatal("a referenced blob must not be deletable")
	}
}
```

Add `"time"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUnreferencedBlobs -v`
Expected: FAIL — `s.UnreferencedBlobs undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/blobs.go`:

```go
// UnreferencedBlobs returns blobs no task_blobs row references and that are
// older than grace. The grace period exists because the upload path writes
// the object before the row (spec 021 section 5), so a blob seconds old may
// legitimately have no reference yet.
//
// When spec 014 adds section_blobs, this predicate grows a second NOT EXISTS
// clause. That is the one place a new reference table has to touch GC.
func (s *Store) UnreferencedBlobs(ctx context.Context, grace time.Duration) ([]Blob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.hash, b.media_type, b.size, b.created_at
		   FROM blobs b
		  WHERE NOT EXISTS (SELECT 1 FROM task_blobs tb WHERE tb.hash = b.hash)
		    AND b.created_at < $1
		  ORDER BY b.created_at`,
		s.nowFn().UTC().Add(-grace))
	if err != nil {
		return nil, fmt.Errorf("unreferenced blobs: %w", err)
	}
	defer rows.Close()
	var out []Blob
	for rows.Next() {
		var b Blob
		if err := rows.Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan blob: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBlobIfUnreferenced removes the index row, re-checking the
// zero-reference condition inside the statement so a reference added since
// the listing query cannot be raced away. Reports whether a row went.
//
// The caller deletes the object AFTER this returns true. That ordering is
// deliberate and mirrors the upload path: a failure between the two leaves an
// orphan object, which the second sweep collects, whereas the reverse order
// would leave a row pointing at deleted bytes -- a permanently broken image.
func (s *Store) DeleteBlobIfUnreferenced(ctx context.Context, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM blobs b
		  WHERE b.hash = $1
		    AND NOT EXISTS (SELECT 1 FROM task_blobs tb WHERE tb.hash = b.hash)`,
		hash)
	if err != nil {
		return false, fmt.Errorf("delete blob: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete blob rows: %w", err)
	}
	return n > 0, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestUnreferencedBlobs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/blobs.go internal/store/blobs_test.go
git commit -m "feat(store): unreferenced-blob GC queries"
```

---

### Task 4: GC sweeps in the API and CLI

**Files:**
- Create: `internal/api/blobgc.go`
- Create: `internal/api/blobgc_test.go`
- Modify: `internal/api/server.go`, `internal/cli/client.go`, `internal/cmd/admin.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/blobgc_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

func TestBlobGCDryRunDeletesNothing(t *testing.T) {
	_, h, token, fake := newTestServerBlobs(t)
	postBlob(t, h, token, "", pngBytes) // unreferenced, but fresh

	rec := doReq(t, h, http.MethodPost, "/api/v1/blobs/gc", token,
		map[string]any{"dry_run": true, "grace_hours": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("gc: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Unreferenced []string `json:"unreferenced"`
		Orphans      []string `json:"orphan_objects"`
		Deleted      int      `json:"deleted"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Unreferenced) != 1 {
		t.Fatalf("unreferenced = %v, want 1", got.Unreferenced)
	}
	if got.Deleted != 0 {
		t.Fatalf("dry run deleted %d, want 0", got.Deleted)
	}
	if objs, _ := fake.List(t.Context(), "blobs/"); len(objs) != 1 {
		t.Fatalf("dry run removed objects: %v", objs)
	}
}

func TestBlobGCCollects(t *testing.T) {
	_, h, token, fake := newTestServerBlobs(t)
	postBlob(t, h, token, "", pngBytes)

	// A key with no blobs row, aged past the grace period.
	orphanKey := blobstore.Key(strings.Repeat("9", 64))
	fake.PutAt(orphanKey, []byte("stray"), time.Now().Add(-48*time.Hour))

	rec := doReq(t, h, http.MethodPost, "/api/v1/blobs/gc", token,
		map[string]any{"dry_run": false, "grace_hours": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("gc: %d %s", rec.Code, rec.Body)
	}
	objs, _ := fake.List(t.Context(), "blobs/")
	if len(objs) != 0 {
		t.Fatalf("objects left after gc: %v", objs)
	}
}

func TestBlobGCRequiresAdmin(t *testing.T) {
	st, h, _, _ := newTestServerBlobs(t)
	if err := st.CreateActor(t.Context(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, err := st.CreateToken(t.Context(), "bob", "t", nil)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	rec := doReq(t, h, http.MethodPost, "/api/v1/blobs/gc", tok, map[string]any{"dry_run": true})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestBlobGC -v`
Expected: FAIL — 404.

- [ ] **Step 3: Write the handler**

Create `internal/api/blobgc.go`:

```go
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

// defaultGCGrace keeps both sweeps clear of uploads in flight.
const defaultGCGrace = 24 * time.Hour

type gcRequest struct {
	DryRun     bool `json:"dry_run"`
	GraceHours *int `json:"grace_hours"`
}

type gcResponse struct {
	Unreferenced  []string `json:"unreferenced"`
	OrphanObjects []string `json:"orphan_objects"`
	Deleted       int      `json:"deleted"`
	Errors        []string `json:"errors,omitempty"`
}

// blobGC runs both sweeps from spec 021 section 11.
func (s *server) blobGC(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		writeErr(w, http.StatusNotImplemented, "blob storage is not configured")
		return
	}
	var req gcRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	grace := defaultGCGrace
	if req.GraceHours != nil {
		grace = time.Duration(*req.GraceHours) * time.Hour
	}
	ctx := r.Context()
	out := gcResponse{Unreferenced: []string{}, OrphanObjects: []string{}}

	// Sweep 1: index rows nothing references.
	unref, err := s.st.UnreferencedBlobs(ctx, grace)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	known := map[string]bool{}
	for _, b := range unref {
		out.Unreferenced = append(out.Unreferenced, b.Hash)
		if req.DryRun {
			continue
		}
		// Row first, then object: a failure between the two leaves an
		// orphan object, which sweep 2 collects.
		deleted, err := s.st.DeleteBlobIfUnreferenced(ctx, b.Hash)
		if err != nil {
			out.Errors = append(out.Errors, "delete row "+b.Hash+": "+err.Error())
			continue
		}
		if !deleted {
			continue // referenced since the listing query; leave it alone
		}
		if err := s.blobs.Delete(ctx, blobstore.Key(b.Hash)); err != nil &&
			err != blobstore.ErrNotFound {
			out.Errors = append(out.Errors, "delete object "+b.Hash+": "+err.Error())
			continue
		}
		known[b.Hash] = true
		out.Deleted++
	}

	// Sweep 2: objects with no index row. Should find nothing, and
	// occasionally will, because the upload path deliberately creates
	// orphans on partial failure.
	objs, err := s.blobs.List(ctx, "blobs/")
	if err != nil {
		out.Errors = append(out.Errors, "list objects: "+err.Error())
		writeJSON(w, http.StatusOK, out)
		return
	}
	cutoff := s.st.Now().Add(-grace)
	for _, o := range objs {
		if o.LastModified.After(cutoff) {
			continue
		}
		hash := o.Key[strings.LastIndexByte(o.Key, '/')+1:]
		if known[hash] {
			continue // already counted by sweep 1
		}
		if _, err := s.st.GetBlob(ctx, hash); err == nil {
			continue // indexed; not an orphan
		}
		out.OrphanObjects = append(out.OrphanObjects, o.Key)
		if req.DryRun {
			continue
		}
		if err := s.blobs.Delete(ctx, o.Key); err != nil && err != blobstore.ErrNotFound {
			out.Errors = append(out.Errors, "delete orphan "+o.Key+": "+err.Error())
			continue
		}
		out.Deleted++
	}

	s.log.Info("blob gc", "dry_run", req.DryRun, "unreferenced", len(out.Unreferenced),
		"orphans", len(out.OrphanObjects), "deleted", out.Deleted, "errors", len(out.Errors))
	writeJSON(w, http.StatusOK, out)
}
```

Register: `mux.Handle("POST /api/v1/blobs/gc", s.auth(requireAdmin(s.blobGC)))`

- [ ] **Step 4: Add the CLI verb**

In `internal/cli/client.go`:

```go
// BlobGCResult is the gc endpoint's report.
type BlobGCResult struct {
	Unreferenced  []string `json:"unreferenced"`
	OrphanObjects []string `json:"orphan_objects"`
	Deleted       int      `json:"deleted"`
	Errors        []string `json:"errors"`
}

// BlobGC runs both garbage-collection sweeps.
func (c *Client) BlobGC(ctx context.Context, dryRun bool, graceHours int) (BlobGCResult, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/blobs/gc",
		map[string]any{"dry_run": dryRun, "grace_hours": graceHours})
	if err != nil {
		return BlobGCResult{}, err
	}
	var out BlobGCResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return BlobGCResult{}, fmt.Errorf("decode gc result: %w", err)
	}
	return out, nil
}
```

In `internal/cmd/admin.go`, add a `blob gc` subcommand. **`--dry-run` defaults to true** — running the real sweep should be a deliberate act until embedded reconciliation has been observed in production:

```go
func newAdminBlobGCCmd() *cobra.Command {
	var apply bool
	var graceHours int
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Collect unreferenced blobs and orphan objects",
		Long: "Reports by default. Pass --apply to delete. Grace period keeps both\n" +
			"sweeps clear of uploads in flight.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := clientFrom(cmd)
			if err != nil {
				return err
			}
			res, err := c.BlobGC(cmd.Context(), !apply, graceHours)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			verb := "would delete"
			if apply {
				verb = "deleted"
			}
			fmt.Fprintf(w, "%d unreferenced blob(s), %d orphan object(s); %s %d\n",
				len(res.Unreferenced), len(res.OrphanObjects), verb, res.Deleted)
			for _, e := range res.Errors {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", e)
			}
			if len(res.Errors) > 0 {
				return fmt.Errorf("%d gc error(s)", len(res.Errors))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually delete (default: report only)")
	cmd.Flags().IntVar(&graceHours, "grace-hours", 24, "ignore blobs and objects newer than this")
	return cmd
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run TestBlobGC -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/blobgc.go internal/api/blobgc_test.go internal/api/server.go internal/cli/client.go internal/cmd/admin.go
git commit -m "feat: blob garbage collection with dry-run default"
```

---

### Task 5: safefetch — the SSRF guard

**Files:**
- Create: `internal/safefetch/safefetch.go`
- Create: `internal/safefetch/safefetch_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/safefetch/safefetch_test.go`:

```go
package safefetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/safefetch"
)

func TestRejectsBadTargets(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	for _, url := range []string{
		"http://user-images.githubusercontent.com/x.png", // not https
		"https://evil.example/x.png",                     // host not allowed
		"https://169.254.169.254/latest/meta-data",        // metadata, and host not allowed
		"file:///etc/passwd",
		"https://localhost/x.png",
	} {
		if _, _, err := f.Get(context.Background(), url); err == nil {
			t.Fatalf("%s: expected rejection", url)
		}
	}
}

func TestAllowsSuffixMatchOnly(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	// A lookalike host must not pass: the suffix check has to be
	// label-aligned, not a substring test.
	if _, _, err := f.Get(context.Background(), "https://evilgithubusercontent.com/x.png"); err == nil {
		t.Fatal("lookalike host accepted")
	}
}

func TestFetchesAndCaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()

	// AllowLoopbackForTest lets the guard reach httptest; production never
	// sets it.
	f := safefetch.New(nil, 10)
	f.AllowLoopbackForTest = true
	f.AllowAnyHostForTest = true
	if _, _, err := f.Get(context.Background(), srv.URL); err == nil {
		t.Fatal("expected size cap to reject a 100-byte body with a 10-byte limit")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/safefetch/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/safefetch/safefetch.go`:

```go
// Package safefetch performs outbound HTTP GETs on attacker-influenced URLs.
// Mirroring remote images at import time (spec 021 section 12) means the
// server fetches URLs that came out of a GitHub issue body, so every request
// is guarded: https only, a host allowlist, and an IP check applied before
// connecting and again on every redirect hop.
package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxRedirects = 3
	fetchTimeout = 30 * time.Second
)

// Fetcher fetches remote content under the guard.
type Fetcher struct {
	allowedHosts []string
	maxBytes     int64

	// Test-only escapes; production never sets either.
	AllowLoopbackForTest bool
	AllowAnyHostForTest  bool
}

// New returns a Fetcher allowing the given host suffixes, capped at maxBytes.
func New(allowedHosts []string, maxBytes int64) *Fetcher {
	return &Fetcher{allowedHosts: allowedHosts, maxBytes: maxBytes}
}

// Get fetches url, returning its bytes and Content-Type.
func (f *Fetcher) Get(ctx context.Context, rawURL string) ([]byte, string, error) {
	if err := f.checkURL(rawURL); err != nil {
		return nil, "", err
	}
	client := &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			// Re-check every hop: a permitted host can redirect anywhere.
			return f.checkURL(req.URL.String())
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("fetch %s: %s", rawURL, resp.Status)
	}
	// Read one byte past the cap so an over-limit body is detected rather
	// than silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > f.maxBytes {
		return nil, "", fmt.Errorf("fetch %s: body exceeds %d bytes", rawURL, f.maxBytes)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (f *Fetcher) checkURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && !(f.AllowLoopbackForTest && u.Scheme == "http") {
		return fmt.Errorf("scheme %q not allowed", u.Scheme)
	}
	host := u.Hostname()
	if !f.AllowAnyHostForTest && !f.hostAllowed(host) {
		return fmt.Errorf("host %q not allowed", host)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if err := f.checkIP(ip.IP); err != nil {
			return err
		}
	}
	return nil
}

// hostAllowed matches on label boundaries: "githubusercontent.com" permits
// user-images.githubusercontent.com and rejects evilgithubusercontent.com.
func (f *Fetcher) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range f.allowedHosts {
		a = strings.ToLower(a)
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

var errBlockedIP = errors.New("resolved to a blocked address range")

func (f *Fetcher) checkIP(ip net.IP) error {
	if f.AllowLoopbackForTest && ip.IsLoopback() {
		return nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("%s: %w", ip, errBlockedIP)
	}
	// 169.254.169.254 is link-local and already caught; this covers the
	// IPv6 metadata address some clouds use.
	if ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return fmt.Errorf("%s: %w", ip, errBlockedIP)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/safefetch/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/safefetch/
git commit -m "feat(safefetch): SSRF-guarded fetcher for remote image mirroring"
```

---

### Task 6: Mirror remote images on import

**Files:**
- Modify: `internal/api/inbox_import.go`
- Create: `internal/api/inbox_mirror_test.go`

- [ ] **Step 1: Write the failing test**

Mirroring is a pure function of (body, fetcher, blob store), so test it directly rather than through the whole import path — the import wiring is covered by the existing `internal/api/inbox_import_test.go`. Create `internal/api/inbox_mirror_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// mirrorTestServer builds a server with a fake blob store and a stubbed
// image host, and returns the origin serving the image.
func mirrorTestServer(t *testing.T) (*server, *blobstore.Fake, string) {
	t.Helper()
	st := store.OpenTestStore(t)
	fake := blobstore.NewFake()
	s := &server{st: st, blobs: fake, log: testLogger(t)}

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG\r\n\x1a\n payload"))
	}))
	t.Cleanup(img.Close)
	return s, fake, img.URL
}

func TestMirrorRewritesAllowedHost(t *testing.T) {
	s, fake, origin := mirrorTestServer(t)
	// Point the guard at the stub instead of githubusercontent.com.
	s.mirrorFetcherForTest = testFetcher(origin)

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
}

// TestMirrorLeavesBlockedTarget: a body pointing at the metadata address is
// left exactly as written and the import still succeeds. A partially
// mirrored import beats a failed one, and the renderer drops the leftover
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

func TestMirrorNoImagesIsIdentity(t *testing.T) {
	s, _, _ := mirrorTestServer(t)
	body := "no images here\n\n```\n![fake](https://x.example/y.png)\n```\n"
	if got := s.mirrorRemoteImages(context.Background(), body); got != body {
		t.Fatalf("body changed:\n%s", got)
	}
}
```

Add `testLogger` if `internal/api` has none — `slog.New(slog.NewTextHandler(io.Discard, nil))` is sufficient. `testFetcher(origin)` returns a `*safefetch.Fetcher` with `AllowLoopbackForTest` and `AllowAnyHostForTest` set, which is why `mirrorRemoteImages` takes its fetcher from a field rather than constructing one inline (next step).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestInboxMirror -v`
Expected: FAIL — destinations unchanged.

- [ ] **Step 3: Write the mirroring pass**

Add to `internal/api/inbox_import.go`:

```go
// mirrorHosts are the only hosts import will fetch from. The import path
// knows exactly which hosts it expects, so the allowlist can be this narrow.
var mirrorHosts = []string{"githubusercontent.com", "github.com"}

// mirrorRemoteImages rewrites a body's remote image references to /blob/
// URLs, uploading each through the normal blob path. Everything becomes a
// blob, so nothing in a rendered body points off-site -- which is also what
// makes the renderer's hard restriction on remote img src cost nothing.
//
// Failure is per-image and never fatal: the original URL stays, the failure
// is logged, and the import proceeds. A partially-mirrored import beats a
// failed one, and the renderer drops the leftover rather than turning it
// into a tracking beacon.
func (s *server) mirrorRemoteImages(ctx context.Context, body string) string {
	if s.blobs == nil {
		return body
	}
	remotes := blobref.RemoteImages(body)
	if len(remotes) == 0 {
		return body
	}
	f := s.mirrorFetcherForTest
	if f == nil {
		f = safefetch.New(mirrorHosts, maxBlobBytes)
	}
	mapping := map[string]string{}
	for _, src := range remotes {
		data, _, err := f.Get(ctx, src)
		if err != nil {
			s.log.Warn("mirror image skipped", "url", src, "err", err)
			continue
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		mediaType := http.DetectContentType(data)
		if _, err := s.st.GetBlob(ctx, hash); err != nil {
			if err := s.blobs.Put(ctx, blobstore.Key(hash),
				bytes.NewReader(data), int64(len(data)), mediaType); err != nil {
				s.log.Warn("mirror image put failed", "url", src, "err", err)
				continue
			}
			if _, err := s.st.InsertBlob(ctx, hash, mediaType, int64(len(data))); err != nil {
				s.log.Warn("mirror image index failed", "url", src, "err", err)
				continue
			}
		}
		mapping[src] = "/blob/" + hash
	}
	return blobref.ReplaceDestination(body, mapping)
}
```

Add the test seam to the `server` struct in `internal/api/server.go`:

```go
	// mirrorFetcherForTest overrides the SSRF-guarded fetcher used by
	// mirrorRemoteImages. Tests only; production leaves it nil so the
	// host allowlist and IP checks apply.
	mirrorFetcherForTest *safefetch.Fetcher
```

Call `mirrorRemoteImages` on each issue body before `PromoteIssue` / `UpsertIssue` records it.

- [ ] **Step 4: Add RemoteImages to blobref**

Append to `internal/blobref/blobref.go`:

```go
// RemoteImages returns the body's http(s) image destinations, deduplicated
// in document order. These are what import mirrors into blobs.
func RemoteImages(body string) []string {
	var out []string
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if seen[dest] {
			return
		}
		if !strings.HasPrefix(dest, "http://") && !strings.HasPrefix(dest, "https://") {
			return
		}
		seen[dest] = true
		out = append(out, dest)
	})
	return out
}
```

Add a matching unit test in `internal/blobref/blobref_test.go` asserting it picks up `https://` destinations and skips `/blob/`, local, and `data:` ones.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ ./internal/blobref/ -run 'TestInboxMirror|TestRemoteImages' -v`
Expected: PASS

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`
Expected: PASS, no regressions.

- [ ] **Step 7: Commit**

```bash
git add internal/api/inbox_import.go internal/api/inbox_mirror_test.go internal/blobref/
git commit -m "feat(inbox): mirror remote issue images into blobs on import"
```

---

### Task 7: Close out the spec

**Files:**
- Modify: `docs/specs/021-images-in-task-bodies.md`
- Modify: `docs/follow-ups.md`
- Modify: `README.md`

- [ ] **Step 1: Walk the acceptance criteria**

Spec §"Acceptance criteria" lists twelve. Run each against a live stack (`docker compose up -d` plus a configured bucket) and tick them off. Any that fail become either a fix in this plan or a follow-up entry — not a silent pass.

- [ ] **Step 2: Record the deferred items**

Add to `docs/follow-ups.md`:

```markdown
- **Blob GC is opt-in and manual**: `lode admin blob gc` reports by default and
  deletes only with `--apply`. No scheduled sweep runs, so unreferenced bytes
  accumulate until someone runs it. Wire a CronJob once embedded reconciliation
  has been observed behaving in production (spec 021 §11).
- **Video poster frames**: an embedded `<video>` with no poster is a black
  rectangle until played, which is a poor answer to "show me the bug".
  Extracting frame 0 needs ffmpeg in the server image (spec 021 Q021.2).
- **Alt-text lint**: `lode task attach` defaults alt text to the file basename,
  which is not alt text. A `--alt` flag is cheap; a lint refusing `![](…)` on a
  `usability` task is more valuable (spec 021 Q021.1).
- **Bucket per environment**: dev and prod currently share whatever
  `LODE_BLOB_BUCKET` points at. Confirm against how the fleet provisions other
  buckets and split if needed (spec 021 Q021.3).
```

- [ ] **Step 3: Update the spec status**

Change the spec header to `**Status:** implemented` and replace §2's "Verify before building" paragraph with what the live bucket actually did.

- [ ] **Step 4: Commit**

```bash
git add docs/specs/021-images-in-task-bodies.md docs/follow-ups.md README.md
git commit -m "docs: close out spec 021 and record blob follow-ups"
```

---

## Done when

- A task page renders headings, tables, and inline screenshots; `<script>`, `javascript:` links, and remote images are all stripped.
- No web template contains a form, asserted by test.
- `lode admin blob gc` reports unreferenced blobs and orphan objects and deletes neither without `--apply`.
- An imported issue body's `githubusercontent.com` image ends up at `/blob/…`; one pointing at a metadata address is refused and left in place.
- `go test ./...` passes.
