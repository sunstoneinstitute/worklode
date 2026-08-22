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
// in for the gateway. It asserts the things the spec depends on: path-style
// addressing (Hetzner requires it), response-* overrides on the presigned
// URL, and the two request-shape choices that keep PutObject acceptable to
// Ceph RGW -- no x-amz-meta-* user metadata and no flexible checksum.
func TestS3PutAndPresign(t *testing.T) {
	var gotPath, gotContentType string
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotHeader = r.Header.Clone()
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
	// x-amz-meta-* is the wire form of PutObjectInput.Metadata. Browsers never
	// read it, so it could not carry the CSP/nosniff headers it was once meant
	// to; asserting on the wire keeps a re-added Metadata block from passing.
	for name := range gotHeader {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
			t.Errorf("PutObject sent user metadata header %s: %q (021 §6 relies on "+
				"Content-Disposition, not object metadata)", name, gotHeader.Get(name))
		}
	}
	// The SDK defaults to WhenSupported, which adds x-amz-checksum-crc32; RGW
	// support for it is version-dependent, so NewS3 pins WhenRequired.
	for name := range gotHeader {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-checksum-") {
			t.Errorf("PutObject sent flexible checksum header %s: %q (want "+
				"RequestChecksumCalculationWhenRequired)", name, gotHeader.Get(name))
		}
	}
	if sdk := gotHeader.Get("X-Amz-Sdk-Checksum-Algorithm"); sdk != "" {
		t.Errorf("PutObject requested checksum algorithm %q, want none", sdk)
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

// TestS3RequiresCredentials pins the boot-time gate. A StaticCredentialsProvider
// built from empty strings fails per request with ErrStaticCredentialsEmpty, so
// without this an operator who sets endpoint and bucket but fumbles the secret
// gets a server that boots clean and then 502s every blob operation.
func TestS3RequiresCredentials(t *testing.T) {
	full := blobstore.S3Config{
		Endpoint:  "https://hel1.example.invalid",
		Bucket:    "wl-blobs",
		Region:    "hel1",
		AccessKey: "ak",
		SecretKey: "sk",
	}
	if _, err := blobstore.NewS3(full); err != nil {
		t.Fatalf("NewS3 with a complete config: %v", err)
	}
	for _, tc := range []struct {
		name string
		blot func(*blobstore.S3Config)
	}{
		{"no endpoint", func(c *blobstore.S3Config) { c.Endpoint = "" }},
		{"no bucket", func(c *blobstore.S3Config) { c.Bucket = "" }},
		{"no access key", func(c *blobstore.S3Config) { c.AccessKey = "" }},
		{"no secret key", func(c *blobstore.S3Config) { c.SecretKey = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := full
			tc.blot(&cfg)
			s, err := blobstore.NewS3(cfg)
			if err == nil {
				t.Fatalf("NewS3(%s) = %v, want an error", tc.name, s)
			}
			if !strings.Contains(err.Error(), "required") {
				t.Errorf("error %q does not say what is required", err)
			}
		})
	}
}
