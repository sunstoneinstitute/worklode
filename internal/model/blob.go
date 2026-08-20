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
