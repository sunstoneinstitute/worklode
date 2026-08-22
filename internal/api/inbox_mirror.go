// Mirroring remote images into blobs (spec 021 §12). An issue body arrives
// referencing https://user-images.githubusercontent.com/…; those URLs need
// GitHub auth for private repos, do not last forever, and §8's renderer
// blocks remote img src outright, so an imported bug report's screenshots
// would render as nothing. Every remote reference therefore becomes a
// /blob/<hash> through the same upload path as §5.
//
// The caller is promoteInbox (admin.go), not importInbox: see the call site
// there for why.

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
	"github.com/sunstoneinstitute/worklode/internal/blobstore"
	"github.com/sunstoneinstitute/worklode/internal/safefetch"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// mirrorHosts are the only hosts mirroring will fetch from. The import path
// knows exactly which hosts it expects, so the allowlist can be this narrow.
var mirrorHosts = []string{"githubusercontent.com", "github.com"}

// mirrorTokenScopes are the destinations the installation's GitHub App token
// may be sent to (021 §12). Narrower than mirrorHosts on both sides,
// deliberately.
//
// Narrower than github.com: the host is fetchable because a body can
// reference an asset there, but it also serves every ordinary page an issue
// might link to, and a token attached to those is a credential handed to a
// URL whoever filed the issue chose. Only the uploaded-attachment subtree —
// github.com/user-attachments/, where GitHub has moved issue attachments
// (WL-292) — carries the token, judged by safefetch on the decoded,
// dot-segment-resolved path so an encoded `..` cannot walk it back out.
//
// Narrower than githubusercontent.com itself: objects.githubusercontent.com is
// the S3-backed host GitHub redirects to with the signature in the query
// string, and S3 rejects a request that carries both a query signature and an
// Authorization header ("Only one auth mechanism allowed"). Scoping to the
// whole parent host would turn a fetch that works today into a 400. The hosts
// below are the ones that actually serve an issue body's images, and
// raw.githubusercontent.com on a private repo is the case that needs the token
// at all. safefetch decides per redirect hop, so a hop onto objects. drops it.
var mirrorTokenScopes = []string{
	"user-images.githubusercontent.com",
	"private-user-images.githubusercontent.com",
	"raw.githubusercontent.com",
	"github.com/user-attachments/",
}

// mirrorTimeout bounds the whole pass, not one fetch. safefetch already caps
// a single fetch at 30 seconds, but the number of image references in a body
// is chosen by whoever filed the issue, so without a whole-pass budget a
// promote could be held open for as long as an attacker cares to write
// `![](…)`. Images the budget cuts off keep their original URLs like any
// other per-image failure.
const mirrorTimeout = 60 * time.Second

// maxMirroredImages bounds how many remote references one pass will fetch.
// safefetch buffers up to maxBlobBytes per image in memory, and the number of
// `![](…)` references in a body is chosen by whoever filed the issue; a bug
// report with more than a handful of screenshots is already unusual, and
// references beyond the cap keep their original URL -- the same failure mode
// already documented above for any other per-image failure.
const maxMirroredImages = 20

// mirrorRemoteImages rewrites a body's remote image references to /blob/
// URLs, uploading each through the normal blob path. Everything becomes a
// blob, so nothing in a rendered body points off-site -- which is also what
// makes the renderer's hard restriction on remote img src cost nothing.
//
// Failure is per-image and never fatal: the original URL stays, the failure
// is logged, and the promote proceeds. A partially-mirrored body beats a
// failed promote, and the renderer drops the leftover rather than turning it
// into a tracking beacon.
//
// It performs network I/O, so it must be called before the caller's
// transaction opens -- a slow origin must never hold a database lock.
//
// repo is the issue's "owner/name", used only to mint the installation token
// the images may need; an empty repo, or an instance with no GitHub App, just
// fetches unauthenticated.
func (s *server) mirrorRemoteImages(ctx context.Context, repo, body string) string {
	if s.blobs == nil {
		return body
	}
	remotes := blobref.RemoteImages(body)
	if len(remotes) == 0 {
		return body
	}
	ctx, cancel := context.WithTimeout(ctx, mirrorTimeout)
	defer cancel()

	f := s.mirrorFetcherForTest
	if f == nil {
		f = safefetch.New(mirrorHosts, maxBlobBytes)
	}
	// Under the same budget as the fetches: minting is two GitHub round trips,
	// and an unreachable GitHub must not extend the pass past mirrorTimeout.
	f = f.WithBearer(mirrorTokenScopes, s.mirrorToken(ctx, repo))

	mapping := map[string]string{}
	stored, deduped := 0, 0
	if len(remotes) > maxMirroredImages {
		s.observeImageMirror(mirrorCapped, len(remotes)-maxMirroredImages)
		remotes = remotes[:maxMirroredImages]
	}
	for _, src := range remotes {
		data, _, err := f.Get(ctx, src)
		if err != nil {
			s.log.Warn("mirror image skipped", "url", src, "err", err)
			s.observeImageMirror(mirrorFetchFailed, 1)
			continue
		}
		// The origin's Content-Type is unverified, so the sniff is the
		// authority -- as it is for uploadBlob.
		mediaType := http.DetectContentType(data)
		// A URL chosen by whoever filed the issue, whose bytes end up behind
		// an <img src>, so anything that cannot render in place is not
		// mirrored: storing arbitrary attacker-supplied bytes under an image
		// reference buys a hosting primitive and a broken image, and the
		// original URL left in place renders as nothing (§8) instead.
		if !blobref.Embeddable(mediaType) {
			s.log.Warn("mirror image skipped", "url", src, "media_type", mediaType)
			s.observeImageMirror(mirrorNotEmbeddable, 1)
			continue
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		// Dedup on the index row, not on a bare error: a database failure
		// must not read as "not present" and re-PUT bytes that are there.
		_, err = s.st.GetBlob(ctx, hash)
		switch {
		case err == nil:
			deduped++
		case errors.Is(err, store.ErrNotFound):
			// Object before row, as uploadBlob does: an orphan object is
			// collectable, a row pointing at nothing is a broken image.
			if err := s.blobs.Put(ctx, blobstore.Key(hash),
				bytes.NewReader(data), int64(len(data)), mediaType); err != nil {
				s.log.Warn("mirror image put failed", "url", src, "err", err)
				s.observeImageMirror(mirrorStoreFailed, 1)
				continue
			}
			if _, err := s.st.InsertBlob(ctx, hash, mediaType, int64(len(data))); err != nil {
				s.log.Warn("mirror image index failed", "url", src, "err", err)
				s.observeImageMirror(mirrorStoreFailed, 1)
				continue
			}
			stored++
		default:
			s.log.Warn("mirror image index lookup failed", "url", src, "err", err)
			s.observeImageMirror(mirrorStoreFailed, 1)
			continue
		}
		mapping[src] = "/blob/" + hash
	}
	out, err := blobref.ReplaceDestination(body, mapping)
	if err != nil {
		// Reference-style images are the only way to get here: the bytes are
		// already stored, but the body cannot be rewritten piecemeal, so it
		// is kept exactly as written rather than half-rewritten.
		s.log.Warn("mirror rewrite failed", "err", err)
		s.observeImageMirror(mirrorRewriteFailed, stored+deduped)
		return body
	}
	s.observeImageMirror(mirrorStored, stored)
	s.observeImageMirror(mirrorDeduplicated, deduped)
	return out
}

// mirrorToken mints the installation token §12 asks for, returning "" when
// there is none to be had.
//
// Failure is never fatal and never blocks the pass: a public repo's images
// fetch fine unauthenticated, and a private repo's images then fail per-image
// exactly as any other fetch failure does -- the original URL stays and §8
// renders it as nothing. That is strictly better than refusing the promote
// over a credential most bodies do not need. It is minted once per pass rather
// than per image because one promote's images are all one repo's.
//
// Note what the token widens: promote's body is a request field, not the
// stored issue's text, so any caller with inbox triage can name a repo and a
// githubusercontent URL and have the server fetch it credentialed and store
// the bytes as a readable blob. The bound is the installation's own scope --
// the repo must map to a project, and the token is only ever the token that
// repo's installation would issue -- so this reaches nothing the caller's
// project does not already cover. Widening the host scope is what would break
// that; see mirrorTokenScopes.
func (s *server) mirrorToken(ctx context.Context, repo string) string {
	if s.appAuth == nil || strings.TrimSpace(repo) == "" {
		return ""
	}
	token, err := s.appAuth.InstallationToken(ctx, repo)
	if err != nil {
		// githubauth never puts token material in an error, so this is safe
		// to log.
		s.log.Warn("mirror token unavailable", "repo", repo, "err", err)
		s.observeMirrorToken(mirrorTokenFailed)
		return ""
	}
	s.observeMirrorToken(mirrorTokenMinted)
	return token
}
