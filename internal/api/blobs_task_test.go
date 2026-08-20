package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
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
