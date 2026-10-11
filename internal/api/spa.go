// spa.go serves the embedded SPA on the public host (WL-REQ-1304).
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

// spaAssetHandler serves the content-hashed build output, immutable. A
// missing file is a 404, never index.html.
func (s *server) spaAssetHandler() http.Handler {
	assets, err := fs.Sub(s.spaFS(), "assets")
	if err != nil {
		panic(err) // "assets" is a valid path; Sub cannot fail
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

// spaShell answers every path with index.html.
func (s *server) spaShell(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.spaFS(), "index.html")
	if err != nil {
		// dist/index.html is committed, so this is a broken build.
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
