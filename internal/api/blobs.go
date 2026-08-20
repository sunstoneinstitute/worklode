// blobs.go implements spec 021's two blob endpoints: POST /api/v1/blobs,
// which stores a content-addressed payload, and GET /blob/{hash}, which
// redirects to a short-lived presigned URL for one. The bytes never transit
// this process on the way out, which is what makes a 100 MiB screen recording
// affordable to serve.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxBlobBytes caps a blob upload at 100 MiB (spec 021 §5). Large enough for
// the screen recordings the spec exists to carry; readJSON's 1 MiB
// maxAPIBody does not apply, since this route takes a raw body.
const maxBlobBytes = 100 << 20

// sniffLen is what http.DetectContentType reads.
const sniffLen = 512

// checkSpoolWritable proves the upload spool directory accepts a temp file,
// so a misconfigured deployment fails at boot rather than on the first
// upload. dir empty means os.TempDir(), which on a container with
// readOnlyRootFilesystem is /tmp and is not writable.
func checkSpoolWritable(dir string) error {
	f, err := os.CreateTemp(dir, "lode-spool-check-")
	if err != nil {
		if dir == "" {
			dir = os.TempDir() + " (default; set LODE_BLOB_SPOOL_DIR)"
		}
		return fmt.Errorf("blob spool directory %s is not writable: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	// The create is the whole test. A failed unlink leaves one empty file
	// behind and is not a reason to refuse to serve.
	_ = os.Remove(name)
	return nil
}

// blobResponse projects a stored blob onto the wire shape the endpoints
// answer with. URL is the permanent root-relative reference a task body
// embeds — never the presigned object URL, which expires in minutes and must
// not be persisted anywhere.
func blobResponse(b model.Blob) model.BlobResponse {
	return model.BlobResponse{Hash: b.Hash, MediaType: b.MediaType, Size: b.Size, URL: "/blob/" + b.Hash}
}

// uploadBlob handles POST /api/v1/blobs. It streams the request body to a
// temp file through a SHA-256 hasher -- content addressing means the hash is
// unknown until the last byte, so the handler cannot decide where the bytes
// belong until it has seen all of them, and buffering 100 MiB in memory per
// concurrent upload is not an option.
//
// Write ordering is object-then-row, always: a failure after the PUT leaves
// an orphan object, which the GC sweep collects. The reverse order would
// leave a row pointing at nothing, which renders as a permanently broken
// image. Both are possible; only one is recoverable without a human.
func (s *server) uploadBlob(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		s.observeBlobUpload("unconfigured")
		writeErr(w, http.StatusNotImplemented, "blob storage is not configured")
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxBlobBytes)

	f, err := os.CreateTemp(s.cfg.BlobSpoolDir, "lode-blob-")
	if err != nil {
		s.log.Error("blob spool", "err", err)
		s.observeBlobUpload("error")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	hasher := sha256.New()
	sniff := make([]byte, 0, sniffLen)
	size, err := io.Copy(f, io.TeeReader(body, writerFunc(func(p []byte) {
		hasher.Write(p)
		if len(sniff) < sniffLen {
			sniff = append(sniff, p[:min(len(p), sniffLen-len(sniff))]...)
		}
	})))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.observeBlobUpload("too_large")
			writeErr(w, http.StatusRequestEntityTooLarge, "blob too large")
			return
		}
		s.observeBlobUpload("error")
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if size == 0 {
		s.observeBlobUpload("empty")
		writeErr(w, http.StatusUnprocessableEntity, "blob is empty")
		return
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	// The client's Content-Type is advisory and never persisted: a payload
	// labelled image/png that sniffs as HTML is stored, and served, as HTML.
	mediaType := http.DetectContentType(sniff)

	// Dedup before any object-store traffic: a re-uploaded screenshot costs
	// one query and nothing else.
	if existing, err := s.st.GetBlob(r.Context(), hash); err == nil {
		s.observeBlobUpload("deduplicated")
		writeJSON(w, http.StatusOK, blobResponse(existing))
		return
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.log.Error("blob rewind", "err", err)
		s.observeBlobUpload("error")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.blobs.Put(r.Context(), blobstore.Key(hash), f, size, mediaType); err != nil {
		s.log.Error("blob put", "hash", hash, "err", err)
		s.observeBlobUpload("storage_error")
		writeErr(w, http.StatusBadGateway, "blob storage unavailable")
		return
	}

	b, err := s.st.InsertBlob(r.Context(), hash, mediaType, size)
	if err != nil {
		s.observeBlobUpload("error")
		s.mapStoreErr(w, err)
		return
	}
	s.observeBlobUpload("stored")
	writeJSON(w, http.StatusOK, blobResponse(b))
}

// writerFunc adapts a func to io.Writer for the TeeReader above.
type writerFunc func(p []byte)

func (f writerFunc) Write(p []byte) (int, error) {
	f(p)
	return len(p), nil
}

// presignTTL is how long a blob's signed URL stays valid. Short, because the
// redirect is cheap to re-issue; the redirect's own Cache-Control sits
// comfortably inside it.
const presignTTL = 5 * time.Minute

// embeddableTypes render in place in the web UI and terminal-adjacent
// surfaces. Everything else is a download (spec 021 §5). Nothing is rejected
// on type: a core dump is a legitimate attachment, and an allowlist buys
// nothing once non-embeddable types can only be served as attachments.
var embeddableTypes = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/webp":    true,
	"image/svg+xml": true,
	"video/mp4":     true,
	"video/webm":    true,
}

// embeddable reports whether a media type renders inline. Sniffed types can
// carry parameters (text/plain; charset=utf-8), so compare the bare type.
func embeddable(mediaType string) bool {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	return embeddableTypes[mediaType]
}

// serveBlob handles GET /blob/{hash}: the caller is already authenticated and
// authorized by eitherGuard, so this redirects to a short-lived presigned
// URL. This is the GitHub and GitLab pattern -- the durable identifier lives
// in the body, the credential lives in a URL that expires in minutes, and the
// bytes never transit the application, which is what makes a 100 MiB screen
// recording affordable to serve.
func (s *server) serveBlob(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		s.observeBlobServe("unconfigured")
		writeErr(w, http.StatusNotImplemented, "blob storage is not configured")
		return
	}
	hash := r.PathValue("hash")
	b, err := s.st.GetBlob(r.Context(), hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.observeBlobServe("not_found")
		} else {
			s.observeBlobServe("storage_error")
		}
		s.mapStoreErr(w, err)
		return
	}

	disposition := "attachment"
	if embeddable(b.MediaType) {
		disposition = "inline"
	}

	url, err := s.blobs.PresignGet(r.Context(), blobstore.Key(hash), presignTTL, blobstore.GetOptions{
		ContentType:        b.MediaType,
		ContentDisposition: disposition,
		// Safe at a year because the URL is content-addressed: the bytes
		// behind a hash can never change.
		CacheControl: "private, max-age=31536000, immutable",
	})
	if err != nil {
		s.log.Error("blob presign", "hash", hash, "err", err)
		s.observeBlobServe("storage_error")
		writeErr(w, http.StatusBadGateway, "blob storage unavailable")
		return
	}

	// Inside presignTTL, so a page with twenty images issues twenty
	// redirects once and then serves from cache.
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.observeBlobServe("redirect")
	http.Redirect(w, r, url, http.StatusFound)
}
