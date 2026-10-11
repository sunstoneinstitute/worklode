// spa.go serves the embedded SPA on the public host (WL-REQ-1304).
package api

import (
	"io/fs"
	"net/http"
	"strings"

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

// spaShell answers a path spaHandles with index.html. A path it does not
// handle redirects to LODE_LEGACY_URL (WL-REQ-1305), kept as its own host so
// the public host never serves the legacy one's content. Without a legacy
// host it falls back to index.html, which the SPA renders as its own
// not-found page; unreachable today, since registerSPA only registers while
// a legacy host splits the two.
func (s *server) spaShell(w http.ResponseWriter, r *http.Request) {
	if spaHandles(r.URL.Path) {
		s.serveSPAShell(w, spaPage)
		return
	}
	if s.cfg.LegacyURL == "" {
		s.serveSPAShell(w, spaNotFound)
		return
	}
	target := strings.TrimRight(s.cfg.LegacyURL, "/") + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusFound)
	s.observeSPA(spaRedirect)
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
