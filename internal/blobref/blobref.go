// Package blobref finds and rewrites blob references in a markdown task
// body (spec 021). Parsing is an AST walk rather than a regex: a hash in a
// code fence or a plain link is not a reference, and counting one as a
// reference would keep its bytes alive forever.
package blobref

import (
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// blobPath matches a canonical blob destination. Lowercase hex only: the
// upload endpoint emits lowercase, and accepting mixed case would let two
// spellings of one reference disagree.
var blobPath = regexp.MustCompile(`^/blob/([0-9a-f]{64})$`)

var md = goldmark.New()

// walkImages calls fn for every image destination in body, in document
// order.
func walkImages(body string, fn func(dest string)) {
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if img, ok := n.(*ast.Image); ok {
			fn(string(img.Destination))
		}
		return ast.WalkContinue, nil
	})
}

// Extract returns the sorted, deduplicated blob hashes the body embeds.
// This is the authority for the embedded flag on task_blobs.
func Extract(body string) []string {
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if m := blobPath.FindStringSubmatch(dest); m != nil {
			seen[m[1]] = true
		}
	})
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// LocalImages returns the body's image destinations that are local relative
// paths -- no URL scheme, no leading slash -- in document order, deduplicated.
// These are what `lode task add --body-file` uploads and rewrites.
func LocalImages(body string) []string {
	var out []string
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if dest == "" || seen[dest] {
			return
		}
		if strings.HasPrefix(dest, "/") || strings.Contains(dest, "://") {
			return
		}
		if strings.HasPrefix(dest, "data:") || strings.HasPrefix(dest, "mailto:") {
			return
		}
		seen[dest] = true
		out = append(out, dest)
	})
	return out
}

// embeddableTypes render in place in the web UI and terminal-adjacent
// surfaces. Everything else is a download (spec 021 §5). Nothing is rejected
// on type: a core dump is a legitimate attachment, and an allowlist buys
// nothing once non-embeddable types can only be served as attachments.
var embeddableTypes = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/webp":    true,
	"image/svg+xml": true,
	"video/mp4":     true,
	"video/webm":    true,
}

// Embeddable reports whether a media type renders inline. Sniffed types can
// carry parameters (text/plain; charset=utf-8), so compare the bare type.
// Shared by the server (serveBlob's Content-Disposition) and `lode task
// attach` (whether to embed a freshly uploaded blob in the body), so both
// read one copy of the list.
func Embeddable(mediaType string) bool {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	return embeddableTypes[mediaType]
}

// ReplaceDestination rewrites image destinations according to mapping,
// leaving unmapped destinations and every other token untouched. It edits
// the source text by byte offset rather than re-rendering the AST, so
// nothing else in the body can be reformatted.
func ReplaceDestination(body string, mapping map[string]string) string {
	if len(mapping) == 0 {
		return body
	}
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))

	type edit struct{ from, to string }
	var edits []edit
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if img, ok := n.(*ast.Image); ok {
			dest := string(img.Destination)
			if to, ok := mapping[dest]; ok {
				edits = append(edits, edit{from: dest, to: to})
			}
		}
		return ast.WalkContinue, nil
	})

	out := body
	for _, e := range edits {
		// The destination appears inside "](...)"; anchoring on that
		// avoids rewriting a path that also occurs as prose.
		out = strings.ReplaceAll(out, "]("+e.from+")", "]("+e.to+")")
	}
	return out
}
