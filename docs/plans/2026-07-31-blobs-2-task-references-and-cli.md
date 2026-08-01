---
implements: docs/specs/021-images-in-task-bodies.md
---
# Blobs 2 — Task references and CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect blobs to tasks per `docs/specs/021-images-in-task-bodies.md` §1 (reference graph), §3, §7, §9 and §10. At the end, `lode task add --body-file bug.md` with local `![](./shot.png)` references uploads the images, rewrites the body, and records the references — and `lode task attach WL-42 crash.log` works for non-embeddable files.

**Architecture:** A new `internal/blobref` package parses a markdown body's `/blob/<hash>` image destinations (goldmark AST walk, not a regex — a hash inside a code fence is not a reference). The server reconciles the `embedded` flag from that set on every task write; `attached` is set explicitly by the CLI and survives body edits. The CLI's `--body-file` path walks the same AST for *local relative* image paths, uploads each, and rewrites destinations before the create/update call.

**Tech Stack:** Go 1.26, `github.com/yuin/goldmark` (already indirect via glamour; this plan promotes it to a direct dependency). Builds on `2026-07-31-blobs-1-store-and-serving.md` — do not start until that plan is merged.

**Verification note:** store and api tests skip silently without a reachable Postgres unless `CI=1`. Run `docker compose up -d postgres` first and confirm `ok`, not `SKIP`.

---

### Task 1: blobref — extract and rewrite markdown image destinations

**Files:**
- Create: `internal/blobref/blobref.go`
- Create: `internal/blobref/blobref_test.go`
- Modify: `go.mod`

- [ ] **Step 1: Promote goldmark to a direct dependency**

```bash
go get github.com/yuin/goldmark@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/blobref/blobref_test.go`:

```go
package blobref_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
)

func TestExtract(t *testing.T) {
	h1 := strings.Repeat("a", 64)
	h2 := strings.Repeat("b", 64)
	body := "before\n\n![one](/blob/" + h1 + ")\n\n![two](/blob/" + h2 + ")\n\n" +
		"![dup](/blob/" + h1 + ")\n"

	got := blobref.Extract(body)
	want := []string{h1, h2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract = %v, want %v (sorted, deduped)", got, want)
	}
}

// TestExtractIgnoresNonImages is the reason this is an AST walk and not a
// regex: a hash in a code fence or a plain link is not a reference, and
// treating it as one would keep bytes alive forever.
func TestExtractIgnoresNonImages(t *testing.T) {
	h := strings.Repeat("c", 64)
	body := "```\n![x](/blob/" + h + ")\n```\n\n" +
		"[a link](/blob/" + h + ")\n\n" +
		"`/blob/" + h + "` inline code\n"
	if got := blobref.Extract(body); len(got) != 0 {
		t.Fatalf("Extract = %v, want none", got)
	}
}

func TestExtractIgnoresMalformed(t *testing.T) {
	body := "![short](/blob/abc)\n\n![remote](https://evil.example/x.png)\n\n" +
		"![upper](/blob/" + strings.Repeat("A", 64) + ")\n"
	if got := blobref.Extract(body); len(got) != 0 {
		t.Fatalf("Extract = %v, want none", got)
	}
}

func TestLocalImages(t *testing.T) {
	body := "![a](./shots/one.png)\n\n![b](two.png)\n\n" +
		"![abs](/etc/passwd)\n\n![remote](https://x.example/y.png)\n\n" +
		"![blob](/blob/" + strings.Repeat("d", 64) + ")\n"
	got := blobref.LocalImages(body)
	want := []string{"./shots/one.png", "two.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LocalImages = %v, want %v", got, want)
	}
}

func TestReplaceDestination(t *testing.T) {
	body := "![a](./one.png)\n\n![b](./one.png)\n\n![c](./two.png)\n"
	got := blobref.ReplaceDestination(body, map[string]string{
		"./one.png": "/blob/" + strings.Repeat("e", 64),
	})
	if strings.Contains(got, "./one.png") {
		t.Fatalf("destination not replaced:\n%s", got)
	}
	if !strings.Contains(got, "./two.png") {
		t.Fatalf("unmapped destination should be left alone:\n%s", got)
	}
	if strings.Count(got, "/blob/") != 2 {
		t.Fatalf("both occurrences should be replaced:\n%s", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/blobref/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Write the implementation**

Create `internal/blobref/blobref.go`:

```go
// Package blobref finds and rewrites blob references in a markdown task
// body (spec 021). Parsing is an AST walk rather than a regex: a hash in a
// code fence or a plain link is not a reference, and counting one as a
// reference would keep its bytes alive forever.
package blobref

import (
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// blobPath matches a canonical blob destination. Lowercase hex only: the
// upload endpoint emits lowercase, and accepting mixed case would let two
// spellings of one reference disagree.
var blobPath = regexp.MustCompile(`^/blob/([0-9a-f]{64})$`)

var md = goldmark.New()

// walkImages calls fn for every image destination in body, in document
// order.
func walkImages(body string, fn func(dest string)) {
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if img, ok := n.(*ast.Image); ok {
			fn(string(img.Destination))
		}
		return ast.WalkContinue, nil
	})
}

// Extract returns the sorted, deduplicated blob hashes the body embeds.
// This is the authority for the embedded flag on task_blobs.
func Extract(body string) []string {
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if m := blobPath.FindStringSubmatch(dest); m != nil {
			seen[m[1]] = true
		}
	})
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// LocalImages returns the body's image destinations that are local relative
// paths -- no URL scheme, no leading slash -- in document order, deduplicated.
// These are what `lode task add --body-file` uploads and rewrites.
func LocalImages(body string) []string {
	var out []string
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if dest == "" || seen[dest] {
			return
		}
		if strings.HasPrefix(dest, "/") || strings.Contains(dest, "://") {
			return
		}
		if strings.HasPrefix(dest, "data:") || strings.HasPrefix(dest, "mailto:") {
			return
		}
		seen[dest] = true
		out = append(out, dest)
	})
	return out
}

// ReplaceDestination rewrites image destinations according to mapping,
// leaving unmapped destinations and every other token untouched. It edits
// the source text by byte offset rather than re-rendering the AST, so
// nothing else in the body can be reformatted.
func ReplaceDestination(body string, mapping map[string]string) string {
	if len(mapping) == 0 {
		return body
	}
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))

	type edit struct{ from, to string }
	var edits []edit
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if img, ok := n.(*ast.Image); ok {
			dest := string(img.Destination)
			if to, ok := mapping[dest]; ok {
				edits = append(edits, edit{from: dest, to: to})
			}
		}
		return ast.WalkContinue, nil
	})

	out := body
	for _, e := range edits {
		// The destination appears inside "](...)"; anchoring on that
		// avoids rewriting a path that also occurs as prose.
		out = strings.ReplaceAll(out, "]("+e.from+")", "]("+e.to+")")
	}
	return out
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/blobref/ -v`
Expected: PASS — five tests.

- [ ] **Step 6: Commit**

```bash
git add internal/blobref/ go.mod go.sum
git commit -m "feat(blobref): extract and rewrite markdown blob references"
```

---

### Task 2: Store — reference graph reads and writes

**Files:**
- Modify: `internal/store/blobs.go`
- Modify: `internal/store/blobs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/blobs_test.go`:

```go
// TestReconcileEmbedded asserts the derived half of the reference graph:
// embedded tracks the body exactly, and a row that ends up neither embedded
// nor attached is deleted rather than left to violate the CHECK.
func TestReconcileEmbedded(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h1 := "a1" + strings.Repeat("0", 62)
	h2 := "b2" + strings.Repeat("0", 62)
	for _, h := range []string{h1, h2} {
		if _, err := s.InsertBlob(ctx, h, "image/png", 1); err != nil {
			t.Fatalf("insert blob: %v", err)
		}
	}

	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), "WL-1", []string{h1, h2}, "alice")
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := mustListHashes(t, s, "WL-1"); len(got) != 2 {
		t.Fatalf("after first reconcile: %v, want 2", got)
	}

	// Drop h2 from the body: its row must go.
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), "WL-1", []string{h1}, "alice")
	}); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	got := mustListHashes(t, s, "WL-1")
	if len(got) != 1 || got[0] != h1 {
		t.Fatalf("after second reconcile: %v, want [%s]", got, h1)
	}
}

// TestAttachSurvivesBodyEdit is the declared half: an attached blob is not
// in the body, so reconciliation must not touch it.
func TestAttachSurvivesBodyEdit(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := seedTask(t, s, "WL-2"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := "cc" + strings.Repeat("0", 62)
	if _, err := s.InsertBlob(ctx, h, "text/plain", 5); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.AttachBlob(ctx, "WL-2", h, "crash.log", "alice"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), "WL-2", nil, "alice")
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	refs, err := s.ListTaskBlobs(ctx, "WL-2")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 || refs[0].Embedded || !refs[0].Attached {
		t.Fatalf("refs = %+v, want one attached, non-embedded row", refs)
	}
	if refs[0].Filename != "crash.log" || refs[0].MediaType != "text/plain" {
		t.Fatalf("refs[0] = %+v, want filename and media type joined in", refs[0])
	}

	if err := s.DetachBlob(ctx, "WL-2", h); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if refs, _ := s.ListTaskBlobs(ctx, "WL-2"); len(refs) != 0 {
		t.Fatalf("after detach: %+v, want none", refs)
	}
}

func mustListHashes(t *testing.T, s *Store, taskID string) []string {
	t.Helper()
	refs, err := s.ListTaskBlobs(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var out []string
	for _, r := range refs {
		out = append(out, r.Hash)
	}
	return out
}
```

Add `"database/sql"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestReconcileEmbedded|TestAttachSurvives' -v`
Expected: FAIL — `undefined: ReconcileEmbedded`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/blobs.go`:

```go
// TaskBlob is one row of the reference graph, joined to its blob.
type TaskBlob struct {
	Hash      string `json:"hash"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Embedded  bool   `json:"embedded"`
	Attached  bool   `json:"attached"`
}

// ReconcileEmbedded makes the task's embedded references exactly hashes.
// Runs inside the same transaction as the task write, so the flag can never
// disagree with the body that produced it.
//
// Three moves: clear embedded where the body no longer cites it, set it
// where the body does, then delete rows left neither embedded nor attached.
// The delete is what makes GC honest -- a reference the body stopped making
// must stop keeping bytes alive.
func ReconcileEmbedded(tx *sql.Tx, now time.Time, taskID string, hashes []string, actorID string) error {
	if _, err := tx.Exec(
		`UPDATE task_blobs SET embedded = false
		  WHERE task_id = $1 AND embedded AND NOT (hash = ANY($2))`,
		taskID, pqArray(hashes)); err != nil {
		return fmt.Errorf("clear embedded: %w", err)
	}

	for _, h := range hashes {
		if _, err := tx.Exec(
			`INSERT INTO task_blobs (task_id, hash, filename, embedded, created_by, created_at)
			 VALUES ($1, $2, '', true, $3, $4)
			 ON CONFLICT (task_id, hash) DO UPDATE SET embedded = true`,
			taskID, h, nullString(actorID), now.UTC()); err != nil {
			return fmt.Errorf("set embedded %s: %w", h, err)
		}
	}

	if _, err := tx.Exec(
		`DELETE FROM task_blobs WHERE task_id = $1 AND NOT embedded AND NOT attached`,
		taskID); err != nil {
		return fmt.Errorf("prune unreferenced: %w", err)
	}
	return nil
}

// AttachBlob records an explicit, non-body reference.
func (s *Store) AttachBlob(ctx context.Context, taskID, hash, filename, actorID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_blobs (task_id, hash, filename, attached, created_by, created_at)
		 VALUES ($1, $2, $3, true, $4, $5)
		 ON CONFLICT (task_id, hash)
		 DO UPDATE SET attached = true, filename = EXCLUDED.filename`,
		taskID, hash, filename, nullString(actorID), s.nowFn().UTC())
	if err != nil {
		return fmt.Errorf("attach blob: %w", err)
	}
	return nil
}

// DetachBlob clears the explicit reference, deleting the row unless the body
// still embeds the blob.
func (s *Store) DetachBlob(ctx context.Context, taskID, hash string) error {
	res, err := s.db.ExecContext(ctx,
		`WITH cleared AS (
		     UPDATE task_blobs SET attached = false
		      WHERE task_id = $1 AND hash = $2 AND attached
		  RETURNING task_id, hash, embedded)
		 DELETE FROM task_blobs tb
		  USING cleared c
		  WHERE tb.task_id = c.task_id AND tb.hash = c.hash AND NOT c.embedded`,
		taskID, hash)
	if err != nil {
		return fmt.Errorf("detach blob: %w", err)
	}
	// A no-op delete is fine (the blob stayed because it is embedded); a
	// missing reference is not.
	if _, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("detach blob rows: %w", err)
	}
	return nil
}

// ListTaskBlobs returns a task's references joined to their blobs, embedded
// first, then by filename, for a stable display order.
func (s *Store) ListTaskBlobs(ctx context.Context, taskID string) ([]TaskBlob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tb.hash, tb.filename, b.media_type, b.size, tb.embedded, tb.attached
		   FROM task_blobs tb JOIN blobs b ON b.hash = tb.hash
		  WHERE tb.task_id = $1
		  ORDER BY tb.embedded DESC, tb.filename, tb.hash`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task blobs: %w", err)
	}
	defer rows.Close()
	var out []TaskBlob
	for rows.Next() {
		var tb TaskBlob
		if err := rows.Scan(&tb.Hash, &tb.Filename, &tb.MediaType, &tb.Size,
			&tb.Embedded, &tb.Attached); err != nil {
			return nil, fmt.Errorf("scan task blob: %w", err)
		}
		out = append(out, tb)
	}
	return out, rows.Err()
}

// nullString maps "" to a NULL created_by, since the column references
// actors(id) and an empty string is not an actor.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

Add a `pqArray` helper if the codebase has none — check with `grep -rn "pq.Array\|ANY(\$" internal/store/`. If the repo already uses a text-array idiom, follow it; otherwise add:

```go
// pqArray renders a []string as a Postgres text[] literal for = ANY($n).
func pqArray(ss []string) string {
	if len(ss) == 0 {
		return "{}"
	}
	return "{" + strings.Join(ss, ",") + "}"
}
```

(Hashes are `[0-9a-f]{64}` by CHECK constraint, so no quoting or escaping is reachable here.) Add `"strings"` to the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run 'Blob|Reconcile|Attach' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/blobs.go internal/store/blobs_test.go
git commit -m "feat(store): task blob reference graph with derived embedded flag"
```

---

### Task 3: Reconcile on every task write

**Files:**
- Modify: `internal/api/tasks.go` (`createTask` ~line 84, `patchTask`)
- Create: `internal/api/blobs_task_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/blobs_task_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestBodyReferenceReconciled asserts the embedded flag follows the body
// across create and update -- the property GC depends on.
func TestBodyReferenceReconciled(t *testing.T) {
	st, h, token, _ := newTestServerBlobs(t)
	if err := st.CreateProject(t.Context(), "p", "P", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	up := postBlob(t, h, token, "", pngBytes)
	var blob struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(up.Body.Bytes(), &blob)

	rec := doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project":  "p",
		"title":    "map flash",
		"body":     "![shot](/blob/" + blob.Hash + ")",
		"priority": "medium",
		"kind":     "bug",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	refs, err := st.ListTaskBlobs(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 || !refs[0].Embedded {
		t.Fatalf("refs = %+v, want one embedded row", refs)
	}

	// Edit the image out; the reference must go with it.
	patch := doReq(t, h, http.MethodPatch, "/api/v1/tasks/"+created.ID, token,
		map[string]any{"body": "no image any more"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", patch.Code, patch.Body)
	}
	refs, _ = st.ListTaskBlobs(t.Context(), created.ID)
	if len(refs) != 0 {
		t.Fatalf("after edit refs = %+v, want none", refs)
	}
}

// TestBodyReferenceUnknownHash: a body citing a hash with no blob row must
// not create a dangling reference. The FK would reject it; assert the
// handler turns that into a clean 422 rather than a 500.
func TestBodyReferenceUnknownHash(t *testing.T) {
	st, h, token, _ := newTestServerBlobs(t)
	if err := st.CreateProject(t.Context(), "p", "P", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	rec := doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project":  "p",
		"title":    "bad ref",
		"body":     "![x](/blob/" + strings.Repeat("f", 64) + ")",
		"priority": "medium",
		"kind":     "bug",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body)
	}
}
```

`st.CreateProject(ctx, id, name, key)` is the pattern the existing api tests use (`internal/api/appauth_test.go:109`); `strings` needs importing for the second test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestBodyReference -v`
Expected: FAIL — no references recorded.

- [ ] **Step 3: Wire reconciliation into createTask**

In `internal/api/tasks.go`, inside `createTask`'s `RecordEvent` apply function, after `store.CreateTask` returns the task:

```go
		if err := store.ReconcileEmbedded(tx, s.st.Now(), t.ID,
			blobref.Extract(req.Body), actorID); err != nil {
			return err
		}
```

Do the same in `patchTask`'s apply function, after `UpdateTaskFields`, using the task's **resulting** body. When the patch does not change the body, re-read it inside the transaction so reconciliation always runs against what is actually stored:

```go
		body := existingBody
		if req.Body != nil {
			body = *req.Body
		}
		if err := store.ReconcileEmbedded(tx, s.st.Now(), id,
			blobref.Extract(body), actorID); err != nil {
			return err
		}
```

Import `"github.com/sunstoneinstitute/worklode/internal/blobref"`.

- [ ] **Step 4: Map the foreign-key violation to 422**

In `mapStoreErr`, a `task_blobs_hash_fkey` violation means the body cited an unknown hash. Add to the error mapping, alongside the existing `ErrInvalidInput` case:

```go
	// A body citing a hash with no blob row: user error, not a server fault.
	if strings.Contains(err.Error(), "task_blobs_hash_fkey") {
		writeErr(w, http.StatusUnprocessableEntity, "body references an unknown blob")
		return
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run TestBodyReference -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/tasks.go internal/api/blobs_task_test.go internal/api/server.go
git commit -m "feat(api): reconcile embedded blob references on task write"
```

---

### Task 4: Task blob endpoints

**Files:**
- Modify: `internal/api/blobs.go`
- Modify: `internal/api/server.go` (route table)
- Modify: `internal/api/blobs_task_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/blobs_task_test.go`:

```go
func TestTaskBlobAttachDetach(t *testing.T) {
	st, h, token, _ := newTestServerBlobs(t)
	if err := st.CreateProject(t.Context(), "p", "P", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	up := postBlob(t, h, token, "", []byte("crash log line\n"))
	var blob struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(up.Body.Bytes(), &blob)

	rec := doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "p", "title": "crash", "priority": "high", "kind": "bug",
	})
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	att := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+created.ID+"/blobs", token,
		map[string]any{"hash": blob.Hash, "filename": "crash.log"})
	if att.Code != http.StatusOK {
		t.Fatalf("attach: %d %s", att.Code, att.Body)
	}

	list := doReq(t, h, http.MethodGet, "/api/v1/tasks/"+created.ID+"/blobs", token, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	var got struct {
		Blobs []struct {
			Hash     string `json:"hash"`
			Filename string `json:"filename"`
			Attached bool   `json:"attached"`
			URL      string `json:"url"`
		} `json:"blobs"`
	}
	json.Unmarshal(list.Body.Bytes(), &got)
	if len(got.Blobs) != 1 || !got.Blobs[0].Attached || got.Blobs[0].Filename != "crash.log" {
		t.Fatalf("blobs = %+v", got.Blobs)
	}
	if got.Blobs[0].URL != "/blob/"+blob.Hash {
		t.Fatalf("url = %q", got.Blobs[0].URL)
	}

	del := doReq(t, h, http.MethodDelete,
		"/api/v1/tasks/"+created.ID+"/blobs/"+blob.Hash, token, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("detach: %d %s", del.Code, del.Body)
	}
	list = doReq(t, h, http.MethodGet, "/api/v1/tasks/"+created.ID+"/blobs", token, nil)
	json.Unmarshal(list.Body.Bytes(), &got)
	if len(got.Blobs) != 0 {
		t.Fatalf("after detach: %+v", got.Blobs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestTaskBlobAttachDetach -v`
Expected: FAIL — 404.

- [ ] **Step 3: Write the handlers**

Append to `internal/api/blobs.go`:

```go
type taskBlobJSON struct {
	Hash      string `json:"hash"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Embedded  bool   `json:"embedded"`
	Attached  bool   `json:"attached"`
	URL       string `json:"url"`
}

func (s *server) listTaskBlobs(w http.ResponseWriter, r *http.Request) {
	refs, err := s.st.ListTaskBlobs(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]taskBlobJSON, 0, len(refs))
	for _, b := range refs {
		out = append(out, taskBlobJSON{
			Hash: b.Hash, Filename: b.Filename, MediaType: b.MediaType,
			Size: b.Size, Embedded: b.Embedded, Attached: b.Attached,
			URL: "/blob/" + b.Hash,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"blobs": out})
}

type attachBlobRequest struct {
	Hash     string `json:"hash"`
	Filename string `json:"filename"`
}

func (s *server) attachTaskBlob(w http.ResponseWriter, r *http.Request) {
	var req attachBlobRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Hash == "" {
		writeErr(w, http.StatusUnprocessableEntity, "hash is required")
		return
	}
	id := r.PathValue("id")
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if _, err := s.st.GetBlob(r.Context(), req.Hash); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	var actorID string
	if a := actorFrom(r); a != nil {
		actorID = a.ID
	}
	if err := s.st.AttachBlob(r.Context(), id, req.Hash, req.Filename, actorID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "attached"})
}

func (s *server) detachTaskBlob(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DetachBlob(r.Context(), r.PathValue("id"), r.PathValue("hash")); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Register the routes**

```go
	mux.Handle("GET /api/v1/tasks/{id}/blobs", s.auth(s.listTaskBlobs))
	mux.Handle("POST /api/v1/tasks/{id}/blobs", s.auth(s.attachTaskBlob))
	mux.Handle("DELETE /api/v1/tasks/{id}/blobs/{hash}", s.auth(s.detachTaskBlob))
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestTaskBlob -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/blobs.go internal/api/server.go internal/api/blobs_task_test.go
git commit -m "feat(api): task blob attach, detach, and list endpoints"
```

---

### Task 5: CLI client methods

**Files:**
- Modify: `internal/cli/client.go`
- Create: `internal/cli/blobs_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/blobs_test.go`:

```go
package cli_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func TestUploadBlobSendsRawBody(t *testing.T) {
	var gotBody, gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotAuth, gotPath = string(b), r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"hash":"abc","media_type":"image/png","size":3,"url":"/blob/abc"}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_token"})
	got, err := c.UploadBlob(context.Background(), strings.NewReader("xyz"), 3)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got.Hash != "abc" || got.URL != "/blob/abc" {
		t.Fatalf("got %+v", got)
	}
	if gotBody != "xyz" {
		t.Fatalf("body = %q, want raw bytes", gotBody)
	}
	if gotPath != "/api/v1/blobs" || gotAuth != "Bearer wl_token" {
		t.Fatalf("path = %q, auth = %q", gotPath, gotAuth)
	}
}
```

`cli.NewClient` takes a `cli.Config` (`internal/cli/client.go:298`), and the `Client` struct's fields are `baseURL`, `token`, `http` — the implementation below uses those names.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestUploadBlob -v`
Expected: FAIL — `c.UploadBlob undefined`.

- [ ] **Step 3: Write the client methods**

Append to `internal/cli/client.go`:

```go
// Blob is the upload endpoint's response.
type Blob struct {
	Hash      string `json:"hash"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
}

// TaskBlob is one entry of a task's blob list.
type TaskBlob struct {
	Hash      string `json:"hash"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Embedded  bool   `json:"embedded"`
	Attached  bool   `json:"attached"`
	URL       string `json:"url"`
}

// UploadBlob streams r to POST /api/v1/blobs. The body is raw bytes, not
// JSON, so this bypasses do() and its JSON encoding.
func (c *Client) UploadBlob(ctx context.Context, r io.Reader, size int64) (Blob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/blobs", r)
	if err != nil {
		return Blob{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	if size > 0 {
		req.ContentLength = size
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Blob{}, fmt.Errorf("upload blob: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Blob{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		var errBody map[string]string
		if json.Unmarshal(data, &errBody) == nil && errBody["error"] != "" {
			msg = errBody["error"]
		}
		return Blob{}, fmt.Errorf("upload blob: %s: %s", resp.Status, msg)
	}
	var b Blob
	if err := json.Unmarshal(data, &b); err != nil {
		return Blob{}, fmt.Errorf("decode blob: %w", err)
	}
	return b, nil
}

// UploadFile uploads one local file, returning its blob.
func (c *Client) UploadFile(ctx context.Context, path string) (Blob, error) {
	f, err := os.Open(path)
	if err != nil {
		return Blob{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Blob{}, err
	}
	return c.UploadBlob(ctx, f, fi.Size())
}

// ListTaskBlobs returns a task's blob references.
func (c *Client) ListTaskBlobs(ctx context.Context, id string) ([]TaskBlob, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/blobs", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Blobs []TaskBlob `json:"blobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode task blobs: %w", err)
	}
	return out.Blobs, nil
}

// AttachBlob records an explicit reference from a task to an uploaded blob.
func (c *Client) AttachBlob(ctx context.Context, id, hash, filename string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/blobs",
		map[string]string{"hash": hash, "filename": filename})
	return err
}

// DetachBlob removes an explicit reference.
func (c *Client) DetachBlob(ctx context.Context, id, hash string) error {
	_, err := c.do(ctx, http.MethodDelete,
		"/api/v1/tasks/"+url.PathEscape(id)+"/blobs/"+url.PathEscape(hash), nil)
	return err
}
```

Add `"io"` and `"os"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestUploadBlob -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/blobs_test.go
git commit -m "feat(cli): blob upload and task reference client methods"
```

---

### Task 6: lode task attach / detach

**Files:**
- Modify: `internal/cmd/task.go`
- Create: `internal/cmd/task_attach_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/task_attach_test.go`. The harness is `runLode` plus an `httptest.NewServer` bound via `LODE_SERVER`/`LODE_TOKEN`, matching `internal/cmd/project_test.go:110`:

```go
package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// blobSrv records what the CLI sent: uploads, task patches, and attaches.
type blobSrv struct {
	mu       sync.Mutex
	uploads  [][]byte
	patched  []string // bodies sent to PATCH /api/v1/tasks/{id}
	attached []string // filenames sent to POST /api/v1/tasks/{id}/blobs
	created  []string // bodies sent to POST /api/v1/tasks
}

// startBlobSrv wires a fake server. Each upload gets a synthetic hash keyed
// on call order, so assertions do not have to compute SHA-256.
func startBlobSrv(t *testing.T, mediaFor func(body []byte) string) *blobSrv {
	t.Helper()
	s := &blobSrv{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/blobs", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.uploads = append(s.uploads, b)
		n := len(s.uploads)
		s.mu.Unlock()
		hash := strings.Repeat(string(rune('a'+n-1)), 64)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hash": hash, "media_type": mediaFor(b),
			"size": len(b), "url": "/blob/" + hash,
		})
	})
	mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": r.PathValue("id"), "title": "T", "body": "existing body",
			"project": "p", "priority": "medium", "kind": "bug", "state": "ready",
		})
	})
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Body *string `json:"body"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		if req.Body != nil {
			s.patched = append(s.patched, *req.Body)
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"WL-1"}`)
	})
	mux.HandleFunc("POST /api/v1/tasks/{id}/blobs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Filename string `json:"filename"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.attached = append(s.attached, req.Filename)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"attached"}`)
	})
	mux.HandleFunc("POST /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Body string `json:"body"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.created = append(s.created, req.Body)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"WL-1"}`)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return s
}

// writeFile creates a file in a temp dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestAttachEmbedsImagesOnly: an image is appended to the body as markdown;
// a log file is attached only. This is the embedded/attached split.
func TestAttachEmbedsImagesOnly(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "shot.png", "\x89PNG\r\n\x1a\n fake")
	log := writeFile(t, dir, "crash.log", "boom\n")

	srv := startBlobSrv(t, func(b []byte) string {
		if strings.HasPrefix(string(b), "\x89PNG") {
			return "image/png"
		}
		return "text/plain; charset=utf-8"
	})

	out, err := runLode(t, "task", "attach", "WL-1", png, log)
	if err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.uploads) != 2 {
		t.Fatalf("uploads = %d, want 2", len(srv.uploads))
	}
	if len(srv.patched) != 1 {
		t.Fatalf("patches = %d, want 1 (only the image edits the body)", len(srv.patched))
	}
	body := srv.patched[0]
	if !strings.Contains(body, "![shot.png](/blob/"+strings.Repeat("a", 64)+")") {
		t.Fatalf("body missing the image reference:\n%s", body)
	}
	if strings.Contains(body, "crash.log") {
		t.Fatalf("non-embeddable file leaked into the body:\n%s", body)
	}
	if !strings.HasPrefix(body, "existing body") {
		t.Fatalf("existing body not preserved:\n%s", body)
	}
	if len(srv.attached) != 1 || srv.attached[0] != "crash.log" {
		t.Fatalf("attached = %v, want [crash.log]", srv.attached)
	}
}

// TestAttachNoEmbed: --no-embed attaches an image without touching the body.
func TestAttachNoEmbed(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "shot.png", "\x89PNG\r\n\x1a\n fake")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	if out, err := runLode(t, "task", "attach", "--no-embed", "WL-1", png); err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.patched) != 0 {
		t.Fatalf("--no-embed edited the body: %v", srv.patched)
	}
	if len(srv.attached) != 1 || srv.attached[0] != "shot.png" {
		t.Fatalf("attached = %v, want [shot.png]", srv.attached)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestAttach -v`
Expected: FAIL — unknown command `attach`.

- [ ] **Step 3: Write the commands**

Add to `internal/cmd/task.go`, registered on the `task` command:

```go
func newTaskAttachCmd() *cobra.Command {
	var noEmbed bool
	cmd := &cobra.Command{
		Use:   "attach <task-id> <file>...",
		Short: "Upload files and attach them to a task",
		Long: "Images and videos are appended to the task body as markdown so they render\n" +
			"inline; every other type is attached only. Use - to read one blob from stdin,\n" +
			"which pairs with a clipboard tool: pngpaste - | lode task attach WL-42 -",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := clientFrom(cmd)
			if err != nil {
				return err
			}
			id := args[0]

			task, _, err := c.GetTask(ctx, id)
			if err != nil {
				return err
			}
			body := task.Body
			var appended bool

			for _, path := range args[1:] {
				var blob cli.Blob
				name := filepath.Base(path)
				if path == "-" {
					data, err := io.ReadAll(cmd.InOrStdin())
					if err != nil {
						return fmt.Errorf("read stdin: %w", err)
					}
					if len(data) == 0 {
						return fmt.Errorf("stdin is empty")
					}
					name = "pasted"
					blob, err = c.UploadBlob(ctx, bytes.NewReader(data), int64(len(data)))
					if err != nil {
						return err
					}
				} else {
					blob, err = c.UploadFile(ctx, path)
					if err != nil {
						return err
					}
				}

				if !noEmbed && embeddableMedia(blob.MediaType) {
					if body != "" && !strings.HasSuffix(body, "\n") {
						body += "\n"
					}
					body += fmt.Sprintf("\n![%s](%s)\n", name, blob.URL)
					appended = true
					fmt.Fprintf(cmd.OutOrStdout(), "embedded %s (%s)\n", name, blob.Hash[:12])
					continue
				}
				if err := c.AttachBlob(ctx, id, blob.Hash, name); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "attached %s (%s)\n", name, blob.Hash[:12])
			}

			if appended {
				if err := c.UpdateTask(ctx, id, cli.TaskUpdate{Body: &body}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noEmbed, "no-embed", false,
		"attach images without appending them to the body")
	return cmd
}

func newTaskDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach <task-id> <hash>",
		Short: "Remove an attached blob from a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := clientFrom(cmd)
			if err != nil {
				return err
			}
			refs, err := c.ListTaskBlobs(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, r := range refs {
				if r.Hash == args[1] && r.Embedded {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: the body still embeds %s; it stays until the body stops citing it\n",
						args[1][:12])
				}
			}
			return c.DetachBlob(cmd.Context(), args[0], args[1])
		},
	}
}

// embeddableMedia mirrors the server's list (spec 021 section 5).
func embeddableMedia(mediaType string) bool {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml",
		"video/mp4", "video/webm":
		return true
	}
	return false
}
```

Register both with `cmd.AddCommand(newTaskAttachCmd(), newTaskDetachCmd())` alongside the existing task subcommands. Match `clientFrom` and `UpdateTask`/`TaskUpdate` to the file's existing helpers.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/ -run TestAttach -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/task.go internal/cmd/task_attach_test.go
git commit -m "feat(cli): lode task attach and detach"
```

---

### Task 7: Reference rewriting on --body-file

**Files:**
- Modify: `internal/cmd/task.go` (`task add` and `task edit` RunE)
- Create: `internal/cmd/task_bodyfile_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/task_bodyfile_test.go`, reusing `startBlobSrv` and `writeFile` from Task 6:

```go
package cmd

import (
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
	if !strings.Contains(err.Error()+out, "outside") {
		t.Fatalf("error should name the traversal: %v\n%s", err, out)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestBodyFile -v`
Expected: FAIL — destinations unchanged.

- [ ] **Step 3: Write the rewriting helper**

Add to `internal/cmd/task.go`:

```go
// uploadBodyImages uploads every local relative image the body references and
// returns the body with those destinations rewritten to /blob/<hash>.
//
// Uploads complete before the create/update call, so the task is written once
// with final content and the server's embedded reconciliation sees the
// rewritten body. A missing file fails the whole command rather than
// producing a task whose body points at images that were never uploaded.
func uploadBodyImages(ctx context.Context, c *cli.Client, body, baseDir string, out io.Writer) (string, error) {
	locals := blobref.LocalImages(body)
	if len(locals) == 0 {
		return body, nil
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	mapping := make(map[string]string, len(locals))
	for _, rel := range locals {
		abs, err := filepath.Abs(filepath.Join(base, rel))
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(abs, base+string(filepath.Separator)) {
			return "", fmt.Errorf("image %q resolves outside %s", rel, base)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("image %q: %w", rel, err)
		}
		blob, err := c.UploadFile(ctx, abs)
		if err != nil {
			return "", fmt.Errorf("upload %q: %w", rel, err)
		}
		mapping[rel] = blob.URL
		fmt.Fprintf(out, "uploaded %s (%s, %d bytes)\n", rel, blob.MediaType, blob.Size)
	}
	return blobref.ReplaceDestination(body, mapping), nil
}
```

- [ ] **Step 4: Call it from both commands**

In `task add`, after reading `--body-file` into `body` and before building the request:

```go
			if bodyFile != "" && bodyFile != "-" && !noUpload {
				body, err = uploadBodyImages(cmd.Context(), c, body,
					filepath.Dir(bodyFile), cmd.OutOrStdout())
				if err != nil {
					return err
				}
			}
```

Do the same in `task edit`. Register the flag on both:

```go
	cmd.Flags().BoolVar(&noUpload, "no-upload", false,
		"do not upload local images referenced by --body-file")
```

`--body` (inline) never rewrites: there is no base directory to resolve against, and inline bodies come from scripts. `--body-file -` (stdin) does not rewrite either, for the same reason — document both in the flag help:

```go
	cmd.Flags().StringVar(&bodyFile, "body-file", "",
		"read the body from this file (- for stdin); local images referenced from a file are uploaded and rewritten")
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cmd/ -run 'TestBodyFile|TestAttach' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/task.go internal/cmd/task_bodyfile_test.go
git commit -m "feat(cli): upload and rewrite local images referenced from --body-file"
```

---

### Task 8: task show and brief integration

**Files:**
- Modify: `internal/cli/render.go` (`TaskDetailRender` ~line 82, `Markdown`)
- Modify: `internal/store/brief.go`
- Modify: `internal/cli/render_test.go`, `internal/store/brief_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/render_test.go`:

```go
func TestMarkdownAbsolutizesBlobURLs(t *testing.T) {
	var buf bytes.Buffer
	cli.MarkdownWithBase(&buf, "![x](/blob/abc)\n", "https://wl.example")
	if !strings.Contains(buf.String(), "https://wl.example/blob/abc") {
		t.Fatalf("blob URL not absolutized:\n%s", buf.String())
	}
}

func TestTaskDetailRendersAttachments(t *testing.T) {
	var buf bytes.Buffer
	cli.TaskDetailRender(&buf, cli.TaskDetail{
		ID: "WL-1", Title: "crash",
		Blobs: []cli.TaskBlob{{
			Hash: "abc123", Filename: "crash.log",
			MediaType: "text/plain", Size: 4096, Attached: true,
			URL: "/blob/abc123",
		}},
	})
	out := buf.String()
	for _, want := range []string{"attachments:", "crash.log", "text/plain"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
```

Append to `internal/store/brief_test.go` a test asserting `Brief` returns a `Blobs` slice for a task with one embedded and one attached blob.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ ./internal/store/ -run 'TestMarkdownAbsolutizes|TestTaskDetailRendersAttachments|TestBriefBlobs' -v`
Expected: FAIL

- [ ] **Step 3: Absolutize blob URLs in the CLI renderer**

In `internal/cli/render.go`, add:

```go
// blobRef matches a root-relative blob destination in a markdown body.
var blobRef = regexp.MustCompile(`\]\(/blob/([0-9a-f]{64})\)`)

// MarkdownWithBase renders body, first rewriting root-relative /blob/ URLs
// to absolute ones so they are complete and terminal-clickable. The web UI
// resolves them itself; nothing else can.
func MarkdownWithBase(w io.Writer, body, server string) {
	if server != "" {
		body = blobRef.ReplaceAllString(body, "]("+strings.TrimSuffix(server, "/")+"/blob/$1)")
	}
	Markdown(w, body)
}
```

Change `TaskDetailRender` to take the server base and call `MarkdownWithBase`; update its callers.

- [ ] **Step 4: Render the attachments list**

In `TaskDetailRender`, after the body:

```go
	if len(t.Blobs) > 0 {
		fmt.Fprintln(w, "\nattachments:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "  FILE\tTYPE\tSIZE\tWHERE\tURL")
		for _, b := range t.Blobs {
			where := "attached"
			if b.Embedded {
				where = "in body"
			}
			name := b.Filename
			if name == "" {
				name = b.Hash[:12]
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\n",
				name, b.MediaType, b.Size, where, b.URL)
		}
		tw.Flush()
	}
```

Add `Blobs []TaskBlob \`json:"blobs"\`` to `cli.TaskDetail`, and have the server's `getTask` handler populate it from `ListTaskBlobs`. Attached blobs appear nowhere in the markdown, so the CLI has to surface them explicitly.

- [ ] **Step 5: Add blobs to the brief**

In `internal/store/brief.go`, add `Blobs []TaskBlob` to the `Brief` struct with the comment:

```go
	// Blobs are the task's images and attachments, so an agent never has to
	// parse markdown to find them. A vision-capable agent can read the
	// screenshot the reporter actually saw; any agent can pull the log.
	Blobs []TaskBlob
```

Populate it in `Brief` via `ListTaskBlobs`. In the API's brief handler, absolutize each URL against `cfg.PublicURL` before writing the response — agents are not same-origin, and they fetch with their own bearer token.

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cli/render.go internal/cli/render_test.go internal/store/brief.go internal/store/brief_test.go internal/api/tasks.go
git commit -m "feat: surface task blobs in task show and task brief"
```

---

## Done when

- `lode task add --body-file bug.md` with two local PNGs creates one task citing `/blob/…` twice, with two `blobs` rows and two `task_blobs` rows at `embedded = true`.
- Editing that body to drop one image deletes its reference row.
- `lode task attach WL-1 crash.log` creates an `attached` row, appends nothing to the body, and survives a body rewrite.
- `lode task show` prints absolute, clickable blob URLs and an attachments table.
- `lode task brief --json` returns a `blobs` array with absolute URLs.

Continue with `2026-07-31-blobs-3-rendering-gc-import.md`.
