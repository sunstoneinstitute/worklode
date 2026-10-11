// spa.go serves the embedded SPA on the public host (WL-REQ-1304), once
// LODE_LEGACY_URL splits it from the templ cockpit kept on the legacy host
// (see registerCockpit, router.go's "GET /" comment). Only registerRoutes
// calls registerSPA, and only in the two-host case.
package api

import (
	"io/fs"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/webapp"
)

// spaFS returns the SPA's build output: Config.WebAppForTest when a test
// injected one, else webapp.FS().
func (s *server) spaFS() fs.FS {
	if s.cfg.WebAppForTest != nil {
		return s.cfg.WebAppForTest
	}
	return webapp.FS()
}

// registerSPA registers the SPA's two routes on r, the public host's router.
func (s *server) registerSPA(r *router) {
	r.public("GET /assets/", s.spaAssetHandler())
	r.web("GET /", s.spaShell)
}

// spaAssetHandler serves the SPA's content-hashed build output under
// /assets/ (WL-REQ-1304): immutable caching, since the filename changes
// whenever the content does, and a missing file is a plain 404 — never
// index.html, unlike the shell's catch-all below.
func (s *server) spaAssetHandler() http.Handler {
	assets, err := fs.Sub(s.spaFS(), "assets")
	if err != nil {
		// Unreachable in practice: "assets" is a valid fs.ValidPath name, so
		// Sub only fails on a malformed root, and webapp.FS() already panics
		// on that at embed time.
		panic(err)
	}
	fileServer := http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		fileServer.ServeHTTP(sw, r)
		if sw.status == http.StatusNotFound {
			s.observeSPA(spaAssetNotFound)
		} else {
			s.observeSPA(spaAsset)
		}
	})
}

// spaShell handles GET / on the public host: the SPA's single entry point.
// Every path answers index.html in this task; a later task adds the
// redirect for paths the SPA does not own (recording spaRedirect /
// spaNotFound then).
func (s *server) spaShell(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.spaFS(), "index.html")
	if err != nil {
		// dist/index.html is committed (the placeholder, or a real build);
		// an unreadable embed is a broken deploy, not a modeled SPA outcome.
		s.log.Error("spa shell: index.html unreadable", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.setWebHeaders(w)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
	s.observeSPA(spaPage)
}
