package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/blobstore"
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

// TestTaskBlobRefMetrics proves attach and detach each bump
// worklode_task_blob_refs_total under their own action label, and that the
// unused label is still pre-initialised to zero rather than absent.
func TestTaskBlobRefMetrics(t *testing.T) {
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	fake := blobstore.NewFake()
	main, admin, err := api.NewServer(st, api.Config{WebOpen: true, BlobStoreForTest: fake})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := st.CreateProject(t.Context(), "p", "P", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	up := postBlob(t, main, token, "", []byte("crash log line\n"))
	var blob struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(up.Body.Bytes(), &blob)

	rec := doReq(t, main, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "p", "title": "crash", "priority": "high", "kind": "bug",
	})
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	att := doReq(t, main, http.MethodPost, "/api/v1/tasks/"+created.ID+"/blobs", token,
		map[string]any{"hash": blob.Hash, "filename": "crash.log"})
	if att.Code != http.StatusOK {
		t.Fatalf("attach: %d %s", att.Code, att.Body)
	}
	del := doReq(t, main, http.MethodDelete,
		"/api/v1/tasks/"+created.ID+"/blobs/"+blob.Hash, token, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("detach: %d %s", del.Code, del.Body)
	}

	body := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_task_blob_refs_total{action="attached"} 1`,
		`worklode_task_blob_refs_total{action="detached"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q: %s", want, body)
		}
	}
}
