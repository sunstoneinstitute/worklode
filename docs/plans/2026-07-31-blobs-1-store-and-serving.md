---
status: accepted
task: WL-17
implements: docs/specs/021-images-in-task-bodies.md
---
# Blobs 1 — Object store and serving Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the content-addressed blob store from `docs/specs/021-images-in-task-bodies.md` §1–§6: schema, a Hetzner Object Storage client, a streaming upload endpoint, and an authenticated `/blob/{hash}` redirect. At the end you can `POST` a PNG and fetch it back through a presigned URL.

**Architecture:** Bytes live in S3-compatible object storage under `blobs/<hash[0:2]>/<hash>`; Postgres holds only the index (`blobs`) and the reference graph (`task_blobs`, unused until plan 2). A new `internal/blobstore` package hides the SDK behind a four-method interface with an in-memory fake for tests. `POST /api/v1/blobs` streams the request body to a temp file through a SHA-256 hasher, dedups on the hash, then PUTs. `GET /blob/{hash}` authenticates via bearer **or** web session and 302s to a short-lived presigned URL.

**Tech Stack:** Go 1.26, `aws-sdk-go-v2` (`config`, `credentials`, `service/s3`) — new dependencies. Postgres via `store.OpenTestStore(t)`. API tests use `newTestServer(t)` / `doReq` from `internal/api/server_test.go`.

**Verification note:** store and api tests skip silently without a reachable Postgres unless `CI=1`. Run `docker compose up -d postgres` first and confirm `ok`, not `SKIP`, in verbose output.

---

### Task 1: Migration 0009 — blobs and task_blobs

**Files:**
- Create: `deploy/base/migrations/0009_blobs.up.sql`
- Create: `deploy/base/migrations/0009_blobs.down.sql`
- Create: `internal/store/blobs_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/blobs_test.go`:

```go
package store

import (
	"context"
	"testing"
)

// TestBlobsSchema asserts migration 0009 created both tables with the
// constraints spec 021 §1 relies on: the CHECK that a task_blobs row must be
// referenced somehow, and RESTRICT on the blobs foreign key.
func TestBlobsSchema(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blobs (hash, media_type, size, created_at)
		 VALUES ('aa', 'image/png', 1, now())`); err != nil {
		t.Fatalf("insert blob: %v", err)
	}

	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Neither embedded nor attached violates task_blobs_referenced.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_blobs (task_id, hash, filename, embedded, attached, created_at)
		 VALUES ('WL-1', 'aa', 'x.png', false, false, now())`)
	if err == nil {
		t.Fatal("expected task_blobs_referenced CHECK to reject an unreferenced row")
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO task_blobs (task_id, hash, filename, embedded, attached, created_at)
		 VALUES ('WL-1', 'aa', 'x.png', true, false, now())`); err != nil {
		t.Fatalf("insert task_blob: %v", err)
	}

	// RESTRICT: a referenced blob cannot be deleted.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blobs WHERE hash = 'aa'`); err == nil {
		t.Fatal("expected ON DELETE RESTRICT to block deleting a referenced blob")
	}

	// Deleting the task cascades its reference, freeing the blob.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = 'WL-1'`); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blobs WHERE hash = 'aa'`); err != nil {
		t.Fatalf("delete blob after cascade: %v", err)
	}
}

// seedTask inserts a minimal project + task pair directly, bypassing the
// event machinery these schema assertions do not need.
func seedTask(t *testing.T, s *Store, id string) error {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, key, name, created_at, updated_at)
		 VALUES ('p', 'P', 'P', now(), now()) ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		 VALUES ($1, 'p', 'T', 'medium', 'feature', 'ready', now(), now())`, id)
	return err
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestBlobsSchema -v`
Expected: FAIL — `relation "blobs" does not exist`.

- [ ] **Step 3: Write the migration**

Create `deploy/base/migrations/0009_blobs.up.sql`:

```sql
-- Content-addressed blobs for task bodies and attachments (spec 021).
--
-- Bytes live in S3-compatible object storage at blobs/<hash[0:2]>/<hash>;
-- this table is the index, not the payload. There is deliberately no key
-- column: the key is a pure function of the hash, and storing it would
-- create a second source of truth that can disagree with the content
-- address.
CREATE TABLE blobs (
    hash       text PRIMARY KEY,
    media_type text NOT NULL,
    size       bigint NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT blobs_hash_format CHECK (hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT blobs_size_positive CHECK (size >= 0)
);

-- The reference graph. A blobs row with no row here is garbage; GC is
-- exactly that query (spec 021 section 11). When spec 014 adds
-- section_blobs, the GC predicate grows a second NOT EXISTS clause -- that
-- is the one place a new reference table has to touch.
--
-- embedded is DERIVED: reconciled from the parsed task body on every write,
-- so removing an image from the body stops keeping its bytes alive.
-- attached is DECLARED: set by `lode task attach`, and survives body edits
-- because it was never in the body.
--
-- ON DELETE RESTRICT on hash is the interlock that makes GC safe: the
-- database refuses to drop a blob anything still references, so a GC bug
-- errors instead of breaking an image.
CREATE TABLE task_blobs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hash       text NOT NULL REFERENCES blobs(hash) ON DELETE RESTRICT,
    filename   text NOT NULL,
    embedded   boolean NOT NULL DEFAULT false,
    attached   boolean NOT NULL DEFAULT false,
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, hash),
    CONSTRAINT task_blobs_referenced CHECK (embedded OR attached)
);

-- Supports the GC predicate, which probes by hash across all tasks.
CREATE INDEX task_blobs_hash_idx ON task_blobs (hash);
```

Create `deploy/base/migrations/0009_blobs.down.sql`:

```sql
DROP TABLE IF EXISTS task_blobs;
DROP TABLE IF EXISTS blobs;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestBlobsSchema -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add deploy/base/migrations/0009_blobs.up.sql deploy/base/migrations/0009_blobs.down.sql internal/store/blobs_test.go
git commit -m "feat(store): blobs and task_blobs schema"
```

---

### Task 2: blobstore package — interface, key derivation, fake

**Files:**
- Create: `internal/blobstore/blobstore.go`
- Create: `internal/blobstore/fake.go`
- Create: `internal/blobstore/blobstore_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/blobstore/blobstore_test.go`:

```go
package blobstore_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

func TestKeySharding(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if got, want := blobstore.Key(hash), "blobs/aa/"+hash; got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
}

func TestFakeRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := blobstore.NewFake()
	key := blobstore.Key(strings.Repeat("b", 64))

	if err := f.Put(ctx, key, strings.NewReader("hello"), 5, "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}

	url, err := f.PresignGet(ctx, key, time.Minute, blobstore.GetOptions{
		ContentType:        "text/plain",
		ContentDisposition: `attachment; filename="a.txt"`,
	})
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if !strings.Contains(url, key) {
		t.Fatalf("presigned URL %q does not contain key %q", url, key)
	}

	objs, err := f.List(ctx, "blobs/bb/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) != 1 || objs[0].Key != key {
		t.Fatalf("List = %+v, want one object keyed %q", objs, key)
	}

	if err := f.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	objs, err = f.List(ctx, "blobs/")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("List after delete = %+v, want empty", objs)
	}
}

// TestFakeContentReadable keeps the fake honest enough for handler tests
// that assert on stored bytes.
func TestFakeContentReadable(t *testing.T) {
	ctx := context.Background()
	f := blobstore.NewFake()
	key := "blobs/cc/x"
	if err := f.Put(ctx, key, strings.NewReader("payload"), 7, "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}
	r, err := f.Open(key)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "payload" {
		t.Fatalf("stored %q, want %q", got, "payload")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/blobstore/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the interface and the fake**

Create `internal/blobstore/blobstore.go`:

```go
// Package blobstore abstracts S3-compatible object storage for
// content-addressed blobs (spec 021). The server holds the only
// credentials; clients receive presigned URLs with a short TTL.
package blobstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Delete and Open for a key that is not present.
var ErrNotFound = errors.New("blobstore: object not found")

// Key returns the object key for a content hash, sharded two hex characters
// deep so the orphan sweep can parallelise over 256 prefixes.
func Key(hash string) string {
	if len(hash) < 2 {
		return "blobs/" + hash
	}
	return "blobs/" + hash[:2] + "/" + hash
}

// GetOptions are the response headers a presigned GET must carry. The
// browser sees the object store's response and not ours, so these ride along
// as S3 response-* overrides rather than as headers we set.
type GetOptions struct {
	ContentType        string
	ContentDisposition string
	CacheControl       string
}

// Object is one listed key.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Store is the object-storage surface the server needs. Implementations are
// S3 (production) and Fake (tests).
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, mediaType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration, opts GetOptions) (string, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Object, error)
}
```

Create `internal/blobstore/fake.go`:

```go
package blobstore

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Store for tests. Presigned URLs are synthetic but
// carry the key, so handler tests can assert the redirect target.
type Fake struct {
	mu      sync.Mutex
	objects map[string][]byte
	times   map[string]time.Time
	// Now, when set, supplies LastModified; tests that exercise the orphan
	// sweep's grace period need to place objects in the past.
	Now func() time.Time
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{
		objects: map[string][]byte{},
		times:   map[string]time.Time{},
	}
}

func (f *Fake) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func (f *Fake) Put(ctx context.Context, key string, r io.Reader, size int64, mediaType string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = b
	f.times[key] = f.now()
	return nil
}

func (f *Fake) PresignGet(ctx context.Context, key string, ttl time.Duration, opts GetOptions) (string, error) {
	f.mu.Lock()
	_, ok := f.objects[key]
	f.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	q := url.Values{}
	q.Set("response-content-type", opts.ContentType)
	q.Set("response-content-disposition", opts.ContentDisposition)
	q.Set("response-cache-control", opts.CacheControl)
	q.Set("X-Amz-Expires", strings.TrimSuffix(ttl.String(), "0s"))
	return "https://fake.objectstorage.test/" + key + "?" + q.Encode(), nil
}

func (f *Fake) Delete(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key]; !ok {
		return ErrNotFound
	}
	delete(f.objects, key)
	delete(f.times, key)
	return nil
}

func (f *Fake) List(ctx context.Context, prefix string) ([]Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Object
	for k, v := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Object{Key: k, Size: int64(len(v)), LastModified: f.times[k]})
		}
	}
	return out, nil
}

// Open reads a stored object. Test-only; not part of Store.
func (f *Fake) Open(key string) (io.Reader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return bytes.NewReader(b), nil
}

// PutAt stores an object with an explicit LastModified, for orphan-sweep
// grace-period tests.
func (f *Fake) PutAt(key string, content []byte, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = content
	f.times[key] = at
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/blobstore/ -v`
Expected: PASS — three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/blobstore/
git commit -m "feat(blobstore): Store interface, key sharding, in-memory fake"
```

---

### Task 3: blobstore S3 implementation

**Files:**
- Create: `internal/blobstore/s3.go`
- Create: `internal/blobstore/s3_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependencies**

```bash
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/blobstore/s3_test.go`:

```go
package blobstore_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

// TestS3PutAndPresign runs the S3 client against an httptest server standing
// in for the gateway. It asserts the two things the spec depends on:
// path-style addressing (Hetzner requires it) and response-* overrides on
// the presigned URL.
func TestS3PutAndPresign(t *testing.T) {
	var gotPath, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, err := blobstore.NewS3(blobstore.S3Config{
		Endpoint:  srv.URL,
		Bucket:    "wl-blobs",
		Region:    "hel1",
		AccessKey: "ak",
		SecretKey: "sk",
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}

	key := blobstore.Key(strings.Repeat("d", 64))
	if err := s.Put(context.Background(), key, strings.NewReader("x"), 1, "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if want := "/wl-blobs/" + key; gotPath != want {
		t.Fatalf("path = %q, want %q (path-style addressing)", gotPath, want)
	}
	if gotContentType != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", gotContentType)
	}

	url, err := s.PresignGet(context.Background(), key, 5*time.Minute, blobstore.GetOptions{
		ContentType:        "image/png",
		ContentDisposition: "inline",
		CacheControl:       "private, max-age=31536000, immutable",
	})
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	for _, want := range []string{
		"response-content-type=image%2Fpng",
		"response-content-disposition=inline",
		"X-Amz-Signature=",
		"/wl-blobs/" + key,
	} {
		if !strings.Contains(url, want) {
			t.Fatalf("presigned URL missing %q:\n%s", want, url)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/blobstore/ -run TestS3 -v`
Expected: FAIL — `undefined: blobstore.NewS3`.

- [ ] **Step 4: Write the implementation**

Create `internal/blobstore/s3.go`:

```go
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config configures an S3-compatible backend. Hetzner Object Storage is
// Ceph RADOS Gateway behind an S3 API and requires path-style addressing,
// which NewS3 always sets -- matching the s3ForcePathStyle the fleet already
// uses for Velero and CNPG against this endpoint.
type S3Config struct {
	Endpoint  string // e.g. https://hel1.your-objectstorage.com
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
}

type s3Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 builds a Store backed by an S3-compatible endpoint.
func NewS3(cfg S3Config) (Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("blobstore: endpoint and bucket are required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1" // SigV4 needs a region string; the gateway ignores it.
	}
	client := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, ""),
	})
	return &s3Store{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64, mediaType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(mediaType),
		// Metadata the gateway returns on every GET, keeping the object
		// self-describing for anyone reading the bucket directly. The
		// presign overrides in PresignGet are what actually reach a browser.
		Metadata: map[string]string{
			"x-content-type-options":   "nosniff",
			"content-security-policy":  "default-src 'none'; sandbox",
		},
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func (s *s3Store) PresignGet(ctx context.Context, key string, ttl time.Duration, opts GetOptions) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentType:        aws.String(opts.ContentType),
		ResponseContentDisposition: aws.String(opts.ContentDisposition),
		ResponseCacheControl:       aws.String(opts.CacheControl),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign %s: %w", key, err)
	}
	return req.URL, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return ErrNotFound
		}
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (s *s3Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, o := range page.Contents {
			obj := Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size)}
			if o.LastModified != nil {
				obj.LastModified = o.LastModified.UTC()
			}
			out = append(out, obj)
		}
	}
	return out, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/blobstore/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/blobstore/s3.go internal/blobstore/s3_test.go go.mod go.sum
git commit -m "feat(blobstore): S3-compatible backend with path-style addressing"
```

---

### Task 4: Store layer — InsertBlob and GetBlob

**Files:**
- Create: `internal/store/blobs.go`
- Modify: `internal/store/blobs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/blobs_test.go`:

```go
// TestInsertBlobIdempotent asserts a second insert of the same hash returns
// the existing row rather than erroring or duplicating -- the dedup the
// upload handler relies on.
func TestInsertBlobIdempotent(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	hash := "ab" + strings.Repeat("c", 62)

	b1, err := s.InsertBlob(ctx, hash, "image/png", 1234)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if b1.Hash != hash || b1.MediaType != "image/png" || b1.Size != 1234 {
		t.Fatalf("unexpected blob: %+v", b1)
	}

	b2, err := s.InsertBlob(ctx, hash, "image/png", 1234)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if !b2.CreatedAt.Equal(b1.CreatedAt) {
		t.Fatal("second insert replaced the row; want the original returned")
	}

	got, err := s.GetBlob(ctx, hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Size != 1234 {
		t.Fatalf("GetBlob size = %d, want 1234", got.Size)
	}

	if _, err := s.GetBlob(ctx, strings.Repeat("f", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBlob(missing) error = %v, want ErrNotFound", err)
	}
}
```

Add `"errors"` and `"strings"` to that file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestInsertBlob -v`
Expected: FAIL — `s.InsertBlob undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/blobs.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Blob is one content-addressed payload. The bytes live in object storage
// at blobstore.Key(Hash); this is the index row (spec 021 section 1).
type Blob struct {
	Hash      string    `json:"hash"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// InsertBlob records a blob, returning the existing row unchanged if the
// hash is already known. Idempotent by construction: identical bytes hash
// identically, so a re-upload must not create a second row or restamp the
// first -- created_at is the orphan sweep's grace-period clock.
func (s *Store) InsertBlob(ctx context.Context, hash, mediaType string, size int64) (Blob, error) {
	var b Blob
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO blobs (hash, media_type, size, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (hash) DO UPDATE SET hash = EXCLUDED.hash
		 RETURNING hash, media_type, size, created_at`,
		hash, mediaType, size, s.nowFn().UTC(),
	).Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt)
	if err != nil {
		return Blob{}, fmt.Errorf("insert blob: %w", err)
	}
	return b, nil
}

// GetBlob returns one blob by hash, or ErrNotFound.
func (s *Store) GetBlob(ctx context.Context, hash string) (Blob, error) {
	var b Blob
	err := s.db.QueryRowContext(ctx,
		`SELECT hash, media_type, size, created_at FROM blobs WHERE hash = $1`, hash,
	).Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Blob{}, ErrNotFound
	}
	if err != nil {
		return Blob{}, fmt.Errorf("get blob: %w", err)
	}
	return b, nil
}
```

The `DO UPDATE SET hash = EXCLUDED.hash` is a no-op write that exists only so `RETURNING` yields a row on conflict; `DO NOTHING` would return none.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestBlobs|TestInsertBlob' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/blobs.go internal/store/blobs_test.go
git commit -m "feat(store): InsertBlob and GetBlob"
```

---

### Task 5: Config wiring

**Files:**
- Modify: `internal/api/server.go` (Config struct ~line 84, server struct, NewServer)
- Modify: `internal/cmd/serve.go` (~line 74)

- [ ] **Step 1: Add the config fields**

In `internal/api/server.go`, append to `Config` after the `SkillScoreFloor` field:

```go
	// Blob storage (spec 021). Off unless BlobEndpoint and BlobBucket are
	// both set: uploads then return 501 and every other surface behaves
	// exactly as before, so a local docker-compose stack needs no bucket.
	BlobEndpoint  string // LODE_BLOB_ENDPOINT, e.g. https://hel1.your-objectstorage.com
	BlobBucket    string // LODE_BLOB_BUCKET
	BlobRegion    string // LODE_BLOB_REGION
	BlobAccessKey string // LODE_BLOB_ACCESS_KEY
	BlobSecretKey string // LODE_BLOB_SECRET_KEY
	BlobSpoolDir  string // LODE_BLOB_SPOOL_DIR; empty means os.TempDir()
```

- [ ] **Step 2: Construct the store in NewServer**

Add a `blobs blobstore.Store` field to the `server` struct. In `NewServer`, before routes are registered:

```go
	if cfg.BlobEndpoint != "" && cfg.BlobBucket != "" {
		bs, err := blobstore.NewS3(blobstore.S3Config{
			Endpoint:  cfg.BlobEndpoint,
			Bucket:    cfg.BlobBucket,
			Region:    cfg.BlobRegion,
			AccessKey: cfg.BlobAccessKey,
			SecretKey: cfg.BlobSecretKey,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("blob store: %w", err)
		}
		s.blobs = bs
	}
```

Import `"github.com/sunstoneinstitute/worklode/internal/blobstore"`.

- [ ] **Step 3: Wire the environment**

In `internal/cmd/serve.go`, add to the `api.Config{...}` literal:

```go
				BlobEndpoint:        os.Getenv("LODE_BLOB_ENDPOINT"),
				BlobBucket:          os.Getenv("LODE_BLOB_BUCKET"),
				BlobRegion:          os.Getenv("LODE_BLOB_REGION"),
				BlobAccessKey:       os.Getenv("LODE_BLOB_ACCESS_KEY"),
				BlobSecretKey:       os.Getenv("LODE_BLOB_SECRET_KEY"),
				BlobSpoolDir:        os.Getenv("LODE_BLOB_SPOOL_DIR"),
```

- [ ] **Step 4: Verify the build and existing tests**

Run: `go build ./... && go test ./internal/api/ -run TestHealth -v`
Expected: builds; existing test passes with blobs unconfigured.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/cmd/serve.go
git commit -m "feat(api): blob storage configuration"
```

---

### Task 6: Upload endpoint — streaming, sniffing, dedup

**Files:**
- Create: `internal/api/blobs.go`
- Create: `internal/api/blobs_test.go`
- Modify: `internal/api/server.go` (route table ~line 358)
- Modify: `internal/api/server_test.go` (add a fake-blobs test server helper)

- [ ] **Step 1: Add the test helper**

Append to `internal/api/server_test.go`:

```go
// newTestServerBlobs is newTestServer with an in-memory blob store attached,
// for the blob endpoints.
func newTestServerBlobs(t *testing.T) (*store.Store, http.Handler, string, *blobstore.Fake) {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	fake := blobstore.NewFake()
	h, _, err := api.NewServer(st, api.Config{BlobStoreForTest: fake})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return st, h, token, fake
}
```

Add a `BlobStoreForTest blobstore.Store` field to `api.Config` with the comment:

```go
	// BlobStoreForTest injects a blobstore.Store directly, bypassing the
	// S3 construction above. Tests only; production sets BlobEndpoint.
	BlobStoreForTest blobstore.Store
```

and honour it first in `NewServer`:

```go
	switch {
	case cfg.BlobStoreForTest != nil:
		s.blobs = cfg.BlobStoreForTest
	case cfg.BlobEndpoint != "" && cfg.BlobBucket != "":
		// ... the NewS3 branch from Task 5
	}
```

- [ ] **Step 2: Write the failing test**

Create `internal/api/blobs_test.go`:

```go
package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
	buf.ReadFrom(stored)
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
	objs, _ := fake.List(t.Context(), "blobs/")
	if len(objs) != 1 {
		t.Fatalf("stored %d objects, want 1", len(objs))
	}
}

func TestUploadBlobTooLarge(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	// 100 MiB + 1. Uses a repeated byte so the test allocates once.
	big := bytes.Repeat([]byte("a"), (100<<20)+1)
	rec := postBlob(t, h, token, "", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestUploadBlob -v`
Expected: FAIL — 404 on the route.

- [ ] **Step 4: Write the handler**

Create `internal/api/blobs.go`:

```go
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
)

// maxBlobBytes caps a blob upload at 100 MiB (spec 021 section 5). Large
// enough for the screen recordings the spec exists to carry; readJSON's
// 1 MiB maxAPIBody does not apply, since this route takes a raw body.
const maxBlobBytes = 100 << 20

// sniffLen is what http.DetectContentType reads.
const sniffLen = 512

// uploadBlob handles POST /api/v1/blobs. It streams the request body to a
// temp file through a SHA-256 hasher -- content addressing means the hash is
// unknown until the last byte, so the handler cannot decide where the bytes
// belong until it has seen all of them, and buffering 100 MiB in memory per
// concurrent upload is not an option.
//
// Write ordering is object-then-row, always: a failure after the PUT leaves
// an orphan object, which the GC sweep collects. The reverse order would
// leave a row pointing at nothing, which renders as a permanently broken
// image. Both are possible; only one is recoverable without a human.
func (s *server) uploadBlob(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		writeErr(w, http.StatusNotImplemented, "blob storage is not configured")
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxBlobBytes)

	f, err := os.CreateTemp(s.cfg.BlobSpoolDir, "lode-blob-")
	if err != nil {
		s.log.Error("blob spool", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	hasher := sha256.New()
	sniff := make([]byte, 0, sniffLen)
	size, err := io.Copy(f, io.TeeReader(body, writerFunc(func(p []byte) {
		hasher.Write(p)
		if len(sniff) < sniffLen {
			sniff = append(sniff, p[:min(len(p), sniffLen-len(sniff))]...)
		}
	})))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "blob too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if size == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "blob is empty")
		return
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	// The client's Content-Type is advisory and never persisted: a payload
	// labelled image/png that sniffs as HTML is stored, and served, as HTML.
	mediaType := http.DetectContentType(sniff)

	// Dedup before any object-store traffic: a re-uploaded screenshot costs
	// one query and nothing else.
	if existing, err := s.st.GetBlob(r.Context(), hash); err == nil {
		writeJSON(w, http.StatusOK, blobResponse(existing))
		return
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.log.Error("blob rewind", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.blobs.Put(r.Context(), blobstore.Key(hash), f, size, mediaType); err != nil {
		s.log.Error("blob put", "hash", hash, "err", err)
		writeErr(w, http.StatusBadGateway, "blob storage unavailable")
		return
	}

	b, err := s.st.InsertBlob(r.Context(), hash, mediaType, size)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blobResponse(b))
}

type blobJSON struct {
	Hash      string `json:"hash"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
}

func blobResponse(b store.Blob) blobJSON {
	return blobJSON{Hash: b.Hash, MediaType: b.MediaType, Size: b.Size, URL: "/blob/" + b.Hash}
}

// writerFunc adapts a func to io.Writer for the TeeReader above.
type writerFunc func(p []byte)

func (f writerFunc) Write(p []byte) (int, error) {
	f(p)
	return len(p), nil
}
```

Import `"errors"` and `"github.com/sunstoneinstitute/worklode/internal/store"`.

- [ ] **Step 5: Register the route**

In `internal/api/server.go`, next to the other `/api/v1` routes:

```go
	mux.Handle("POST /api/v1/blobs", s.auth(s.uploadBlob))
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/api/ -run TestUploadBlob -v`
Expected: PASS — six tests.

- [ ] **Step 7: Commit**

```bash
git add internal/api/blobs.go internal/api/blobs_test.go internal/api/server.go internal/api/server_test.go
git commit -m "feat(api): streaming content-addressed blob upload"
```

---

### Task 7: Serving — eitherAuth and the presigned redirect

**Files:**
- Modify: `internal/api/blobs.go`
- Modify: `internal/api/server.go` (route table, middleware)
- Modify: `internal/api/blobs_test.go`

- [ ] **Step 1: Add the test helpers**

`signSession` is unexported and `blobs_test.go` is in `package api_test`, so add
`internal/api/export_test.go`:

```go
package api

// SignSessionForTest exposes signSession to the api_test package, which
// needs a valid session cookie to exercise the blob route's web-session
// path.
var SignSessionForTest = signSession
```

Append to `internal/api/server_test.go` a variant with a web auth provider configured — the
GitHub login path, since it needs no network at construction (`server.go:233`):

```go
// newTestServerBlobsWebAuth is newTestServerBlobs with GitHub web login
// configured, so webAuth and eitherAuth stop passing requests through.
func newTestServerBlobsWebAuth(t *testing.T) (*store.Store, http.Handler, string, *blobstore.Fake) {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	fake := blobstore.NewFake()
	h, _, err := api.NewServer(st, api.Config{
		BlobStoreForTest:   fake,
		GitHubClientID:     "cid",
		GitHubClientSecret: "secret",
		GitHubOrg:          "sunstoneinstitute",
		SessionSecret:      "sekret",
		PublicURL:          "https://wl.test",
		TokenEncKey:        strings.Repeat("0123456789abcdef", 4),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return st, h, token, fake
}
```

- [ ] **Step 2: Write the failing test**

Append to `internal/api/blobs_test.go`:

```go
func TestServeBlobRedirect(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)

	rec := postBlob(t, h, token, "", pngBytes)
	var up struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(rec.Body.Bytes(), &up)

	req := httptest.NewRequest(http.MethodGet, "/blob/"+up.Hash, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)

	if got.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", got.Code, got.Body)
	}
	loc := got.Header().Get("Location")
	if !strings.Contains(loc, blobstore.Key(up.Hash)) {
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
	json.Unmarshal(rec.Body.Bytes(), &up)
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

// TestServeBlobOpenWithoutProvider pins the bypass: with no web auth
// provider the read-only UI is unauthenticated, and blobs match it. Serving
// a 401 here would render a task page fine and break every image on it.
func TestServeBlobOpenWithoutProvider(t *testing.T) {
	_, h, token, _ := newTestServerBlobs(t)
	rec := postBlob(t, h, token, "", pngBytes)
	var up struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(rec.Body.Bytes(), &up)

	req := httptest.NewRequest(http.MethodGet, "/blob/"+up.Hash, nil)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (webAuth is a pass-through with no provider)", got.Code)
	}
}

// TestServeBlobRequiresSessionWithProvider is the other half: once a
// provider is configured, an anonymous fetch is refused and a valid session
// cookie is honoured.
func TestServeBlobRequiresSessionWithProvider(t *testing.T) {
	st, h, token, _ := newTestServerBlobsWebAuth(t)
	rec := postBlob(t, h, token, "", pngBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var up struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(rec.Body.Bytes(), &up)

	anon := httptest.NewRecorder()
	h.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/blob/"+up.Hash, nil))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", anon.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/blob/"+up.Hash, nil)
	req.AddCookie(&http.Cookie{
		Name:  "wl_session",
		Value: api.SignSessionForTest("sekret", "alice", st.Now().Add(time.Hour)),
	})
	sess := httptest.NewRecorder()
	h.ServeHTTP(sess, req)
	if sess.Code != http.StatusFound {
		t.Fatalf("session status = %d, want 302; body = %s", sess.Code, sess.Body)
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestServeBlob -v`
Expected: FAIL — 404 on `/blob/{hash}`.

- [ ] **Step 4: Write eitherAuth**

Append to `internal/api/server.go`, next to `auth`:

```go
// eitherAuth accepts a bearer token or a web session. Blobs are the only
// route both audiences fetch directly: a browser <img> carries the session
// cookie, the CLI and agents carry a token.
//
// It mirrors webAuth's bypass deliberately. With no web auth provider
// configured the read-only UI is unauthenticated, so a blob route that
// authenticated unconditionally would render a task page fine and 401 every
// image on it. Blobs are not the place to unilaterally tighten the
// installation's auth model -- spec 021 Q021.4 tracks fixing that at the UI
// level.
func (s *server) eitherAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && token != "" {
			actor, err := s.st.Authenticate(r.Context(), token)
			if err == nil {
				next(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
				return
			}
			if !errors.Is(err, store.ErrNotFound) {
				s.mapStoreErr(w, err)
				return
			}
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Same bypass as webAuth: no provider configured means the whole web
		// surface is open, and blobs match it.
		if s.oidc == nil && s.gh == nil {
			next(w, r)
			return
		}
		// Otherwise fall back to a web session. Session cookies are
		// SameSite=Lax, which withholds them from cross-site subresource
		// loads -- so an attacker page embedding <img src="/blob/..."> gets
		// a 401 rather than a probe for what a logged-in victim can see.
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if _, ok := verifySession(s.cfg.SessionSecret, c.Value, s.st.Now()); ok {
				next(w, r)
				return
			}
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized")
	})
}
```

- [ ] **Step 5: Write the serve handler**

Append to `internal/api/blobs.go`:

```go
// presignTTL is how long a blob's signed URL stays valid. Short, because the
// redirect is cheap to re-issue; the redirect's own Cache-Control sits
// comfortably inside it.
const presignTTL = 5 * time.Minute

// embeddableTypes render in place in the web UI and terminal-adjacent
// surfaces. Everything else is a download (spec 021 section 5). Nothing is
// rejected on type: a core dump is a legitimate attachment, and an
// allowlist buys nothing once non-embeddable types can only be served
// as attachments.
var embeddableTypes = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/webp":    true,
	"image/svg+xml": true,
	"video/mp4":     true,
	"video/webm":    true,
}

// embeddable reports whether a media type renders inline. Sniffed types can
// carry parameters (text/plain; charset=utf-8), so compare the bare type.
func embeddable(mediaType string) bool {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	return embeddableTypes[mediaType]
}

// serveBlob handles GET /blob/{hash}: authenticate, then redirect to a
// short-lived presigned URL. This is the GitHub and GitLab pattern -- the
// durable identifier lives in the body, the credential lives in a URL that
// expires in minutes, and the bytes never transit the application, which is
// what makes a 100 MiB screen recording affordable to serve.
func (s *server) serveBlob(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		writeErr(w, http.StatusNotImplemented, "blob storage is not configured")
		return
	}
	hash := r.PathValue("hash")
	b, err := s.st.GetBlob(r.Context(), hash)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	disposition := "attachment"
	if embeddable(b.MediaType) {
		disposition = "inline"
	}

	url, err := s.blobs.PresignGet(r.Context(), blobstore.Key(hash), presignTTL, blobstore.GetOptions{
		ContentType:        b.MediaType,
		ContentDisposition: disposition,
		// Safe at a year because the URL is content-addressed: the bytes
		// behind a hash can never change.
		CacheControl: "private, max-age=31536000, immutable",
	})
	if err != nil {
		s.log.Error("blob presign", "hash", hash, "err", err)
		writeErr(w, http.StatusBadGateway, "blob storage unavailable")
		return
	}

	// Inside presignTTL, so a page with twenty images issues twenty
	// redirects once and then serves from cache.
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, url, http.StatusFound)
}
```

Add `"strings"` and `"time"` to the file's imports.

- [ ] **Step 6: Register the route**

In `internal/api/server.go`, next to the web routes (outside `/api/v1`, since this is an asset route reachable by both audiences):

```go
	mux.Handle("GET /blob/{hash}", s.eitherAuth(s.serveBlob))
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestServeBlob|TestUploadBlob' -v`
Expected: PASS — eleven tests.

- [ ] **Step 8: Run the full suite**

Run: `go test ./...`
Expected: PASS, no regressions.

- [ ] **Step 9: Commit**

```bash
git add internal/api/blobs.go internal/api/blobs_test.go internal/api/server.go internal/api/server_test.go internal/api/export_test.go
git commit -m "feat(api): serve blobs via authenticated presigned redirect"
```

---

### Task 8: Document the configuration

**Files:**
- Modify: `README.md`
- Modify: `docs/specs/021-images-in-task-bodies.md` (§2 verification note)

- [ ] **Step 1: Add a README section**

After the Quickstart's environment-variable block, add:

```markdown
### Blob storage (optional)

Task bodies can embed images and carry attachments once an S3-compatible
bucket is configured. Unset, uploads return `501` and everything else works
as before.

```bash
export LODE_BLOB_ENDPOINT=https://hel1.your-objectstorage.com
export LODE_BLOB_BUCKET=sunstone-worklode-blobs
export LODE_BLOB_REGION=hel1
export LODE_BLOB_ACCESS_KEY=...
export LODE_BLOB_SECRET_KEY=...
```

The bucket must stay private: presigned URLs are the only anonymous read
path, and they expire after five minutes.
```

- [ ] **Step 2: Record the presign verification result**

Spec §2 says to confirm against the live bucket that Hetzner honours presign `response-*` overrides. With a real bucket configured and `LODE_SERVER` / `LODE_TOKEN` exported:

```bash
# Upload a PNG and capture its blob URL.
URL=$(curl -sS -X POST "$LODE_SERVER/api/v1/blobs" \
  -H "Authorization: Bearer $LODE_TOKEN" \
  --data-binary @some.png | jq -r .url)

# Follow the redirect and inspect what the object store actually returned.
curl -sSI -L "$LODE_SERVER$URL" -H "Authorization: Bearer $LODE_TOKEN" \
  | grep -i 'content-type\|content-length\|content-disposition'
```

Expected: `Content-Type: image/png`, a `Content-Length` matching the file, and `Content-Disposition: inline`. If the gateway ignores the overrides — most likely symptom is `Content-Type: binary/octet-stream` — record that in the spec and open a follow-up for the streaming fallback (§2). Do not leave the spec claiming behaviour the gateway does not have.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/specs/021-images-in-task-bodies.md
git commit -m "docs: blob storage configuration and presign verification"
```

---

## Done when

- `POST /api/v1/blobs` returns a hash for a PNG; a second identical upload returns the same body and stores one object.
- A 100 MiB + 1 upload returns 413.
- `GET /blob/{hash}` 302s to a presigned URL for a bearer token and for a web session, 404s for an unknown hash, and sets `inline` for images and `attachment` for everything else.
- With a web auth provider configured, an anonymous `GET /blob/{hash}` is 401; with no provider configured it passes through, matching `webAuth`.
- With no `LODE_BLOB_ENDPOINT`, uploads return 501 and `go test ./...` passes unchanged.

Continue with `2026-07-31-blobs-2-task-references-and-cli.md`.
