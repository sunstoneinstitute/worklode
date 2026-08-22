package model

import "time"

// Blob is one content-addressed payload. The bytes live in object storage at
// blobstore.Key(Hash); this is the index row (spec 021 §1). There is
// deliberately no key field — the key is a pure function of the hash, and
// storing it would create a second source of truth that can disagree with
// the content address.
type Blob struct {
	Hash      string    `json:"hash"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// BlobResponse is the response body of POST /api/v1/blobs and the shape the
// blob endpoints answer with. URL is the root-relative permanent reference a
// task body embeds (spec 021 §2) — /blob/<hash> — not the presigned object
// URL, which is short-lived and never persisted.
//
// PosterURL is the same kind of reference to a second blob: the video's first
// frame, extracted at upload so an embedded <video> is a picture of the bug
// rather than a black rectangle (spec 021 §5). Empty for every non-video
// upload, and for a video on a deployment whose image has no ffmpeg — a
// poster is decoration, and its absence is never an upload failure.
type BlobResponse struct {
	Hash      string `json:"hash"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
	PosterURL string `json:"poster_url,omitempty"`
}

// TaskBlob is one row of a task's blob reference graph (spec 021 §1), joined
// to the blob it names. Embedded is derived from the body on every task
// write; Attached is declared by `lode task attach` and survives body edits.
// URL is the root-relative /blob/<hash> reference, filled in at the HTTP
// boundary — the store leaves it empty, because a reference's address is a
// serving concern, not a storage one.
type TaskBlob struct {
	Hash      string `json:"hash"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Embedded  bool   `json:"embedded"`
	Attached  bool   `json:"attached"`
	URL       string `json:"url"`
}

// TaskBlobsResponse is the response body of GET /api/v1/tasks/{id}/blobs.
// Blobs is always an array, never null.
type TaskBlobsResponse struct {
	Blobs []TaskBlob `json:"blobs"`
}

// AttachBlobInput is the request body of POST /api/v1/tasks/{id}/blobs:
// an already-uploaded hash, plus the filename to display and serve it under.
type AttachBlobInput struct {
	Hash     string `json:"hash"`
	Filename string `json:"filename"`
}

// AttachBlobResponse is the response body of POST /api/v1/tasks/{id}/blobs.
type AttachBlobResponse struct {
	Status string `json:"status"`
}

// BlobGCRequest is the request body of POST /api/v1/blobs/gc (spec 021 §11).
// GraceHours is a pointer so an omitted field falls back to the server's
// default grace period rather than being read as an explicit zero.
type BlobGCRequest struct {
	DryRun     bool `json:"dry_run"`
	GraceHours *int `json:"grace_hours"`
}

// BlobGCResponse is the response body of POST /api/v1/blobs/gc: what both GC
// sweeps found, and — outside dry-run — deleted. Unreferenced and
// OrphanObjects are always arrays, never null.
type BlobGCResponse struct {
	Unreferenced  []string `json:"unreferenced"`
	OrphanObjects []string `json:"orphan_objects"`
	Deleted       int      `json:"deleted"`
	Errors        []string `json:"errors,omitempty"`
}
