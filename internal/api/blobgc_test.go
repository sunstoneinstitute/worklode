package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
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

func TestBlobGCUnconfigured(t *testing.T) {
	_, h, token := newTestServer(t) // no blob store attached
	rec := doReq(t, h, http.MethodPost, "/api/v1/blobs/gc", token, map[string]any{"dry_run": true})
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// TestBlobGCMetrics asserts both GC counters reach the admin /metrics with
// the run's actual mode and what each sweep found — a dry run and an apply
// are both 200, so http_requests_total cannot tell them apart.
func TestBlobGCMetrics(t *testing.T) {
	fake := blobstore.NewFake()
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	main, admin, err := api.NewServer(st, api.Config{WebOpen: true, BlobStoreForTest: fake})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	postBlob(t, main, token, "", pngBytes)
	if rec := doReq(t, main, http.MethodPost, "/api/v1/blobs/gc", token,
		map[string]any{"dry_run": true, "grace_hours": 0}); rec.Code != http.StatusOK {
		t.Fatalf("dry run gc: %d %s", rec.Code, rec.Body)
	}
	if rec := doReq(t, main, http.MethodPost, "/api/v1/blobs/gc", token,
		map[string]any{"dry_run": false, "grace_hours": 0}); rec.Code != http.StatusOK {
		t.Fatalf("apply gc: %d %s", rec.Code, rec.Body)
	}

	scrape := doReq(t, admin, "GET", "/metrics", "", nil)
	if scrape.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", scrape.Code)
	}
	body := scrape.Body.String()
	for _, want := range []string{
		`worklode_blob_gc_runs_total{mode="dry_run",outcome="ok"} 1`,
		`worklode_blob_gc_runs_total{mode="apply",outcome="ok"} 1`,
		// Pre-initialised, so a mode/outcome pair nothing hit still reports zero.
		`worklode_blob_gc_runs_total{mode="apply",outcome="error"} 0`,
		`worklode_blob_gc_objects_total{action="unreferenced"} 2`,
		`worklode_blob_gc_objects_total{action="deleted"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}
