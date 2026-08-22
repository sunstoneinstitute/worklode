// blobs.go implements spec 021's blob endpoints: POST /api/v1/blobs, which
// stores a content-addressed payload, GET /blob/{hash}, which redirects to a
// short-lived presigned URL for one, and the task blob reference endpoints
// (list, attach, detach) that manage a task's row in the reference graph.
// The bytes never transit this process on the way out, which is what makes a
// 100 MiB screen recording affordable to serve.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/ffmpeg"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxBlobBytes caps a blob upload at 100 MiB (spec 021 §5). Large enough for
// the screen recordings the spec exists to carry; readJSON's 1 MiB
// maxAPIBody does not apply, since this route takes a raw body.
const maxBlobBytes = 100 << 20

// uploadCap is the cap this server enforces: maxBlobBytes unless a test
// lowered it. Tests need a small cap because the streaming path spools every
// byte the client sends before MaxBytesReader can refuse it, so asserting the
// 413 at the real cap writes 100 MiB to the spool directory on every run —
// which on a runner with a small or tmpfs /tmp fails with ENOSPC (a 500)
// instead of the 413 it means to assert.
func (s *server) uploadCap() int64 {
	if s.cfg.MaxBlobBytesForTest > 0 {
		return s.cfg.MaxBlobBytesForTest
	}
	return maxBlobBytes
}

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
// not be persisted anywhere. poster is the hash of the video's extracted
// first frame, or "" for everything else.
func blobResponse(b model.Blob, poster string) model.BlobResponse {
	r := model.BlobResponse{Hash: b.Hash, MediaType: b.MediaType, Size: b.Size, URL: "/blob/" + b.Hash}
	if poster != "" {
		r.PosterURL = "/blob/" + poster
	}
	return r
}

// maxFilenameBytes caps the download name a reference may carry. Longer than
// any filename a filesystem accepts, short enough that the presigned URL's
// response-content-disposition override stays well inside every gateway's
// query-string limit.
const maxFilenameBytes = 200

// blobURL renders the root-relative reference a client follows to fetch one
// blob reference. The name rides along as a query parameter because
// `task_blobs.filename` is per-reference while `/blob/{hash}` is per-blob
// (spec 021 §2): one blob two tasks attached under different names has no
// single name the route could serve it under, so the reference carries its
// own. A reference with no name (every embedded image) keeps the bare URL.
//
// The parameter is deliberately outside anything signed — see
// contentDisposition. Body text is never rewritten this way: an embedded
// `](/blob/<hash>)` must keep matching blobref's anchored grammar, or the
// reference stops pinning its blob against GC.
func blobURL(hash, filename string) string {
	u := "/blob/" + hash
	if name := sanitizeFilename(filename); name != "" {
		u += "?filename=" + url.QueryEscape(name)
	}
	return u
}

// sanitizeFilename reduces a caller-supplied name to something safe to put in
// a response header, or returns "" when nothing usable is left. The value
// reaches us from a query parameter anyone may craft, so it is treated as
// hostile even though it was originally a `lode task attach` basename:
//
//   - control characters go first, CR and LF above all — they are the header
//     injection vector, and nothing legitimate carries them;
//   - only the last path segment survives, so neither `../` nor a Windows
//     `C:\` prefix can suggest a directory to a browser that honours one;
//   - invalid UTF-8 is refused outright rather than repaired, because the
//     RFC 8187 half of the header must be well-formed UTF-8.
//
// What it does not do is police extensions: naming a text file `.exe` is a
// social problem, not a serving one, and §6's inline/attachment token — not
// the filename — is what decides whether the bytes can execute.
func sanitizeFilename(s string) string {
	if s == "" || len(s) > maxFilenameBytes || !utf8.ValidString(s) {
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSpace(s)
	if s == "." || s == ".." {
		return ""
	}
	return s
}

// contentDisposition renders the header spec 021 §2 promises: the
// inline/attachment token, plus the reference's own name when it has one.
// Formatting goes through mime.FormatMediaType rather than string
// concatenation — it owns the quoting rules and the RFC 2231/8187 encoding of
// a non-ASCII value, and a name it cannot represent comes back as "" rather
// than as a malformed header. A non-ASCII name gets both halves RFC 6266 asks
// for: a folded ASCII `filename=` for anything that reads only that, and the
// exact percent-encoded `filename*` after it, which every current browser
// prefers.
//
// The name is not part of anything signed, and that is the intended design.
// The presigned S3 URL this value ends up in is SigV4-signed over its query
// string, response-content-disposition included, so the name cannot be
// swapped on the object-store leg. On our own leg — `/blob/{hash}?filename=`
// — the parameter is unsigned, so a caller who may read a blob may choose
// what their own browser saves it as. That grants no access: the bytes,
// the media type and the inline/attachment token are all decided server-side
// from `blobs`, and only the token carries §6's security weight. The residual
// risk is a hand-crafted link that saves a known blob under a misleading
// name, which is strictly weaker than the same actor hosting the file
// themselves, and signing the parameter would not remove it — whoever can
// mint a link can mint one with the name they wanted in the first place.
func contentDisposition(kind, filename string) string {
	name := sanitizeFilename(filename)
	if name == "" {
		return kind
	}
	full := mime.FormatMediaType(kind, map[string]string{"filename": name})
	if full == "" {
		return kind
	}
	ascii := foldASCII(name)
	if ascii == name {
		return full // already `kind; filename="…"`
	}
	// full is `kind; filename*=utf-8''…`; keep that param and put the folded
	// ASCII fallback in front of it.
	_, star, ok := strings.Cut(full, "; ")
	fallback := mime.FormatMediaType(kind, map[string]string{"filename": ascii})
	if !ok || fallback == "" {
		return full
	}
	return fallback + "; " + star
}

// foldASCII maps every non-ASCII rune to '_' for the RFC 6266 fallback half.
// Quoting the result is mime.FormatMediaType's job, not ours.
func foldASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0x7f {
			return '_'
		}
		return r
	}, s)
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

	body := http.MaxBytesReader(w, r.Body, s.uploadCap())

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
		// Whatever went wrong reading the body is a fact about this server's
		// plumbing (a spool write failure, a reset connection), so it goes to
		// the log and the client gets the category only.
		s.log.Error("blob body", "err", err)
		s.observeBlobUpload("error")
		writeErr(w, http.StatusBadRequest, "could not read request body")
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

	// A video's poster is extracted before the dedup check, and from the
	// spooled file rather than from the bucket, so re-uploading the same
	// recording answers with the same poster it did the first time instead of
	// silently dropping to a black rectangle. The extra ffmpeg run on a
	// deduplicated video is the price, and it is one frame.
	var poster string
	if blobref.Video(mediaType) {
		poster = s.storePoster(r.Context(), f.Name())
	}

	// Dedup before any object-store traffic: a re-uploaded screenshot costs
	// one query and nothing else.
	if existing, err := s.st.GetBlob(r.Context(), hash); err == nil {
		s.observeBlobUpload("deduplicated")
		writeJSON(w, http.StatusOK, blobResponse(existing, poster))
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
	writeJSON(w, http.StatusOK, blobResponse(b, poster))
}

// storePoster extracts the first frame of the just-spooled video at path and
// stores it as a blob of its own, returning its hash — or "" when there is no
// poster to be had.
//
// Every failure is a "": a poster is decoration, and refusing a 100 MiB
// screen recording because a frame would not decode would trade the whole
// feature for the garnish. The outcome is counted either way, so an image
// that shipped without ffmpeg reads as a flat line of "unavailable" rather
// than as silence.
//
// The poster is an ordinary blob, indexed and content-addressed like any
// other, which is what keeps it out of the schema: whatever body embeds the
// <video poster="/blob/…"> pins it through the same reference graph as the
// video itself, and a poster nobody ever embedded is collected by the same GC
// sweep (spec 021 §11).
func (s *server) storePoster(ctx context.Context, path string) string {
	img, err := ffmpeg.Poster(ctx, path)
	if err != nil {
		if errors.Is(err, ffmpeg.ErrUnavailable) {
			s.observePosterExtraction("unavailable")
			return ""
		}
		s.log.Warn("video poster", "err", err)
		s.observePosterExtraction("failed")
		return ""
	}

	sum := sha256.Sum256(img)
	hash := hex.EncodeToString(sum[:])
	if _, err := s.st.GetBlob(ctx, hash); err == nil {
		s.observePosterExtraction("deduplicated")
		return hash
	}
	// Object-then-row, for the reason uploadBlob gives.
	if err := s.blobs.Put(ctx, blobstore.Key(hash), bytes.NewReader(img), int64(len(img)), ffmpeg.PosterMediaType); err != nil {
		s.log.Error("poster put", "hash", hash, "err", err)
		s.observePosterExtraction("storage_error")
		return ""
	}
	if _, err := s.st.InsertBlob(ctx, hash, ffmpeg.PosterMediaType, int64(len(img))); err != nil {
		s.log.Error("poster index", "hash", hash, "err", err)
		s.observePosterExtraction("error")
		return ""
	}
	s.observePosterExtraction("stored")
	return hash
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

	kind := "attachment"
	if blobref.Embeddable(b.MediaType) {
		kind = "inline"
	}
	// The name is the reference's, echoed back from the URL blobURL minted;
	// a request without one still gets the bare token, which is what an
	// embedded image and a hand-typed hash URL both look like.
	disposition := contentDisposition(kind, r.URL.Query().Get("filename"))

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
		b.URL = blobURL(b.Hash, b.Filename)
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
