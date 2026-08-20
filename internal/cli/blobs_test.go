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
