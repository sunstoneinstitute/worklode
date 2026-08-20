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
type BlobResponse struct {
	Hash      string `json:"hash"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
}
