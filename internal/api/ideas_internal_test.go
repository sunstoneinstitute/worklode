package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestMapStoreErrNotShared: ErrNotShared is a 409 through mapStoreErr, for
// the intake-side routes that do not go through writeIdeaErr.
func TestMapStoreErrNotShared(t *testing.T) {
	rec := httptest.NewRecorder()
	(&server{}).mapStoreErr(rec, fmt.Errorf("idea x: %w", store.ErrNotShared))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// TestMapStoreErrReviewOpen: a second open review on one subject is a 409.
func TestMapStoreErrReviewOpen(t *testing.T) {
	rec := httptest.NewRecorder()
	(&server{}).mapStoreErr(rec, fmt.Errorf("doc 1: %w", store.ErrReviewOpen))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}
