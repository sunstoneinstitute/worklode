// Package ui is Worklode's web UI package: embedded design-system assets
// (self-hosted stylesheet, fonts, htmx) plus the templ page components that
// render the cockpit. It owns everything under /assets/ (see Assets, served
// by internal/api's assetHandler) and depends on nothing beyond stdlib,
// internal/model, and the templ runtime — it must never import internal/api,
// so the dependency only ever points one way.
package ui

//go:generate ../../scripts/gen-web.sh

import (
	"embed"
	"io/fs"
	"time"
)

//go:embed assets
var uiFS embed.FS

// Assets returns the embedded assets subtree (stylesheet, self-hosted fonts,
// htmx) served at /assets/ by internal/api's assetHandler.
func Assets() fs.FS {
	assets, err := fs.Sub(uiFS, "assets")
	if err != nil {
		panic(err)
	}
	return assets
}

// FmtTime renders every timestamp the same way across the web UI: UTC,
// "2006-01-02 15:04". Every page-facing timestamp goes through it, so the web
// UI never disagrees with itself about how a time is written.
func FmtTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }
