package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- blobs ------------------------------------------------------------

// UploadBlob streams r to POST /api/v1/blobs. The body is raw bytes, not
// JSON, so this bypasses do() and its JSON encoding.
func (c *Client) UploadBlob(ctx context.Context, r io.Reader, size int64) (model.BlobResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/blobs", r)
	if err != nil {
		return model.BlobResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	if size > 0 {
		req.ContentLength = size
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return model.BlobResponse{}, fmt.Errorf("upload blob: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return model.BlobResponse{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return model.BlobResponse{}, apiError(resp.StatusCode, data)
	}
	var b model.BlobResponse
	if err := json.Unmarshal(data, &b); err != nil {
		return model.BlobResponse{}, fmt.Errorf("decode blob: %w", err)
	}
	return b, nil
}

// UploadFile uploads one local file, returning its blob.
func (c *Client) UploadFile(ctx context.Context, path string) (model.BlobResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.BlobResponse{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return model.BlobResponse{}, err
	}
	return c.UploadBlob(ctx, f, fi.Size())
}

// ListTaskBlobs returns a task's blob references.
func (c *Client) ListTaskBlobs(ctx context.Context, id string) ([]model.TaskBlob, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/blobs", nil)
	if err != nil {
		return nil, err
	}
	var out model.TaskBlobsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode task blobs: %w", err)
	}
	return out.Blobs, nil
}

// AttachBlob records an explicit reference from a task to an uploaded blob.
func (c *Client) AttachBlob(ctx context.Context, id, hash, filename string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/blobs",
		model.AttachBlobInput{Hash: hash, Filename: filename})
	return err
}

// DetachBlob removes an explicit reference.
func (c *Client) DetachBlob(ctx context.Context, id, hash string) error {
	_, err := c.do(ctx, http.MethodDelete,
		"/api/v1/tasks/"+url.PathEscape(id)+"/blobs/"+url.PathEscape(hash), nil)
	return err
}

// BlobGC runs both garbage-collection sweeps (spec 021 §11). graceHours is
// pointer-typed on the wire so an admin can pass 0 deliberately (tests, or a
// deployment confident nothing is mid-upload) without it reading as "use the
// server default".
func (c *Client) BlobGC(ctx context.Context, dryRun bool, graceHours *int) (model.BlobGCResponse, []byte, error) {
	return doJSON[model.BlobGCResponse](ctx, c, http.MethodPost, "/api/v1/blobs/gc",
		model.BlobGCRequest{DryRun: dryRun, GraceHours: graceHours}, "blob gc result")
}
