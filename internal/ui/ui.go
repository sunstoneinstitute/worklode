// Package ui is Worklode's web UI package: embedded design-system assets
// (self-hosted stylesheet, fonts, htmx) plus the templ page components that
// render the cockpit. It owns everything under /assets/ (see Assets, served
// by internal/api's assetHandler) and depends on nothing beyond stdlib,
// internal/model, and the templ runtime — it must never import internal/api,
// so the dependency only ever points one way.
package ui

//go:generate ../../scripts/gen-web.sh

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
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

// assetURL ties each browser-cached asset URL to the embedded bytes served by
// this binary, so new markup cannot load an older stylesheet or script.
func assetURL(name string) string {
	b, err := fs.ReadFile(uiFS, "assets/"+name)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("/assets/%s?v=%x", name, sum[:6])
}

// fmtTime renders every timestamp the same way across the web UI: UTC,
// "2006-01-02 15:04". Every page-facing timestamp goes through it, so the web
// UI never disagrees with itself about how a time is written.
func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

// FmtAge renders how long ago t was, relative to now, in the coarsest unit
// that stays legible: minutes under an hour, hours under a day, days beyond
// that. The Reviews queue (spec 029 §7.1) is the first page to show it; any
// later page needing a relative age should call this rather than inventing
// a second phrasing.
func FmtAge(t, now time.Time) string {
	d := now.Sub(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		return fmt.Sprintf("%dm ago", m)
	}
	if d < 24*time.Hour {
		h := int(d / time.Hour)
		return fmt.Sprintf("%dh ago", h)
	}
	days := int(d / (24 * time.Hour))
	return fmt.Sprintf("%dd ago", days)
}

// fmtBytes renders a byte count for display: exact below 1 kB, one decimal
// place above it. Attachment sizes span a 33-byte PNG and a 100 MiB screen
// recording (spec 021 §5), and neither reads as a plain byte count.
func fmtBytes(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10) + " B"
	}
	size, unit := float64(n)/1000, "kB"
	for _, u := range []string{"kB", "MB", "GB"} {
		unit = u
		if size < 1000 {
			break
		}
		size /= 1000
	}
	return strconv.FormatFloat(size, 'f', 1, 64) + " " + unit
}
