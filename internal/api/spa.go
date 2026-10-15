// spa.go serves the embedded SPA (WL-REQ-1304).
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

// registerSPA registers /static/ and every spaRoutes pattern to the SPA
// shell (WL-REQ-1303). The templ cockpit registers no pattern listed here.
func (s *server) registerSPA(r *router) {
	r.public("GET /static/", s.spaAssetHandler())
	for _, route := range spaRoutes {
		r.web("GET "+muxPattern(route), s.spaShell)
	}
}

// spaAssetHandler serves the content-hashed build output, immutable. A
// missing file is a 404, never index.html.
func (s *server) spaAssetHandler() http.Handler {
	assets, err := fs.Sub(s.spaFS(), "static")
	if err != nil {
		panic(err) // "static" is a valid path; Sub cannot fail
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(assets)))
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

// spaShell answers a spaRoutes path with index.html.
func (s *server) spaShell(w http.ResponseWriter, _ *http.Request) {
	s.serveSPAShell(w, spaPage)
}

// serveSPAShell writes index.html and records outcome.
func (s *server) serveSPAShell(w http.ResponseWriter, outcome string) {
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
	s.observeSPA(outcome)
}
