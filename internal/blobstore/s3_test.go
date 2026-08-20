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
