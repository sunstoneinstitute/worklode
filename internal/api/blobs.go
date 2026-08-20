// blobs.go implements spec 021's blob endpoints: POST /api/v1/blobs, which
// stores a content-addressed payload, GET /blob/{hash}, which redirects to a
// short-lived presigned URL for one, and the task blob reference endpoints
// (list, attach, detach) that manage a task's row in the reference graph.
// The bytes never transit this process on the way out, which is what makes a
// 100 MiB screen recording affordable to serve.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
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
	if blobref.Embeddable(b.MediaType) {
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

// listTaskBlobs handles GET /api/v1/tasks/{id}/blobs: a task's full
// reference graph row, embedded and attached alike (spec 021 §3). The task is
// checked to exist first, so an unknown id is a 404 rather than an empty list
// that reads as "this task has no blobs".
func (s *server) listTaskBlobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	refs, err := s.st.ListTaskBlobs(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]model.TaskBlob, 0, len(refs))
	for _, b := range refs {
		b.URL = "/blob/" + b.Hash
		out = append(out, b)
	}
	writeJSON(w, http.StatusOK, model.TaskBlobsResponse{Blobs: out})
}

// attachTaskBlob handles POST /api/v1/tasks/{id}/blobs: declares an explicit
// reference to an already-uploaded blob, distinct from the embedded
// references ReconcileEmbedded derives from the body (spec 021 §3). Both the
// task and the blob are checked to exist before the write, so a bad id or
// hash comes back as a clean 404 rather than an FK violation surfacing as a
// 500.
func (s *server) attachTaskBlob(w http.ResponseWriter, r *http.Request) {
	var req model.AttachBlobInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Hash == "" {
		writeErr(w, http.StatusUnprocessableEntity, "hash is required")
		return
	}
	id := r.PathValue("id")
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if _, err := s.st.GetBlob(r.Context(), req.Hash); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if err := s.st.AttachBlob(r.Context(), id, req.Hash, req.Filename, actorIDFrom(r)); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeTaskBlobRef("attached")
	writeJSON(w, http.StatusOK, model.AttachBlobResponse{Status: "attached"})
}

// detachTaskBlob handles DELETE /api/v1/tasks/{id}/blobs/{hash}: clears the
// explicit reference. A row the body still embeds survives with only its
// declared half cleared (DetachBlob); a row with neither half left is
// deleted. Like list and attach, an unknown task id is a 404 -- the delete
// is idempotent in the hash, not in the task.
func (s *server) detachTaskBlob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if err := s.st.DetachBlob(r.Context(), id, r.PathValue("hash")); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeTaskBlobRef("detached")
	w.WriteHeader(http.StatusNoContent)
}
