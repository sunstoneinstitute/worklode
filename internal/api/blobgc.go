// blobgc.go implements POST /api/v1/blobs/gc: the two garbage-collection
// sweeps spec 021 §11 defines over content-addressed blobs. Admin-only (see
// permBlobAdmin in authz.go) — a sweep deletes data on every actor's behalf,
// which is instance administration, not ordinary blob authoring.
package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// defaultGCGrace keeps both sweeps clear of uploads in flight: the upload
// path writes the object before the row (spec 021 §5), so a blob or object
// seconds old may legitimately have no reference or index row yet.
const defaultGCGrace = 24 * time.Hour

// blobGC runs both sweeps from spec 021 §11.
func (s *server) blobGC(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		writeErr(w, http.StatusNotImplemented, "blob storage is not configured")
		return
	}
	var req model.BlobGCRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	grace := defaultGCGrace
	if req.GraceHours != nil {
		grace = time.Duration(*req.GraceHours) * time.Hour
	}
	ctx := r.Context()
	out := model.BlobGCResponse{Unreferenced: []string{}, OrphanObjects: []string{}}

	// Sweep 1: index rows nothing references.
	unref, err := s.st.UnreferencedBlobs(ctx, grace)
	if err != nil {
		s.observeBlobGCRun(req.DryRun, "error")
		s.mapStoreErr(w, err)
		return
	}
	known := map[string]bool{}
	for _, b := range unref {
		out.Unreferenced = append(out.Unreferenced, b.Hash)
		if req.DryRun {
			continue
		}
		// Row first, then object: a failure between the two leaves an
		// orphan object, which sweep 2 collects.
		deleted, err := s.st.DeleteBlobIfUnreferenced(ctx, b.Hash)
		if err != nil {
			out.Errors = append(out.Errors, "delete row "+b.Hash+": "+err.Error())
			continue
		}
		if !deleted {
			continue // referenced since the listing query; leave it alone
		}
		if err := s.blobs.Delete(ctx, blobstore.Key(b.Hash)); err != nil &&
			!errors.Is(err, blobstore.ErrNotFound) {
			out.Errors = append(out.Errors, "delete object "+b.Hash+": "+err.Error())
			continue
		}
		known[b.Hash] = true
		out.Deleted++
	}
	s.observeBlobGCObjects("unreferenced", len(out.Unreferenced))

	// Sweep 2: objects with no index row. Should find nothing, and
	// occasionally will, because the upload path deliberately creates
	// orphans on partial failure.
	objs, err := s.blobs.List(ctx, "blobs/")
	if err != nil {
		out.Errors = append(out.Errors, "list objects: "+err.Error())
		s.observeBlobGCRun(req.DryRun, "error")
		s.observeBlobGCObjects("deleted", out.Deleted)
		writeJSON(w, http.StatusOK, out)
		return
	}
	cutoff := s.st.Now().Add(-grace)
	for _, o := range objs {
		if o.LastModified.After(cutoff) {
			continue
		}
		hash := o.Key[strings.LastIndexByte(o.Key, '/')+1:]
		if known[hash] {
			continue // already counted by sweep 1
		}
		if _, err := s.st.GetBlob(ctx, hash); err == nil {
			continue // indexed; not an orphan
		}
		out.OrphanObjects = append(out.OrphanObjects, o.Key)
		if req.DryRun {
			continue
		}
		if err := s.blobs.Delete(ctx, o.Key); err != nil && !errors.Is(err, blobstore.ErrNotFound) {
			out.Errors = append(out.Errors, "delete orphan "+o.Key+": "+err.Error())
			continue
		}
		out.Deleted++
	}
	s.observeBlobGCObjects("orphan", len(out.OrphanObjects))
	s.observeBlobGCObjects("deleted", out.Deleted)

	runOutcome := "ok"
	if len(out.Errors) > 0 {
		runOutcome = "error"
	}
	s.observeBlobGCRun(req.DryRun, runOutcome)

	s.log.Info("blob gc", "dry_run", req.DryRun, "unreferenced", len(out.Unreferenced),
		"orphans", len(out.OrphanObjects), "deleted", out.Deleted, "errors", len(out.Errors))
	writeJSON(w, http.StatusOK, out)
}
