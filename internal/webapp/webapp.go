// Package webapp embeds the SPA's build output (WL-REQ-1310). Without a
// build, dist/ holds only the committed placeholder index.html, so go build
// and go test never need Node.js.
package webapp

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS is the build output rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist/ is committed; a missing directory is a broken checkout
	}
	return sub
}

// Built reports whether a real build is embedded: the placeholder ships no
// assets/ directory.
func Built() bool {
	_, err := fs.Stat(FS(), "assets")
	return err == nil
}
