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
