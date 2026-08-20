// Package blobref finds and rewrites blob references in a markdown task
// body (spec 021). Parsing is an AST walk rather than a regex: a hash in a
// code fence or a plain link is not a reference, and counting one as a
// reference would keep its bytes alive forever.
package blobref

import (
	"fmt"
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

// blobHTMLRef matches a /blob/<hash> substring anywhere in a raw-HTML node's
// source text, e.g. inside `<img src="/blob/…">`. Unlike blobPath it is not
// anchored to a whole attribute value -- raw HTML is scanned, not parsed, so
// the sanitiser's own quoting rules (plan 3) are not re-implemented here.
// The trailing \b rejects a run of more than 64 hex digits, matching
// blobPath's exact-length grammar. A hash that happens to sit inside an
// HTML comment still matches; that only pins a blob against GC, which is
// the safe direction to be wrong in.
var blobHTMLRef = regexp.MustCompile(`/blob/([0-9a-f]{64})\b`)

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

// walkRawHTML calls fn with every raw-HTML source segment in body -- inline
// (ast.RawHTML, e.g. `<img …>` sitting in a paragraph) and block
// (ast.HTMLBlock, e.g. an `<img …>` on its own line) -- in document order.
// Neither node type is reachable by walking for ast.Image: goldmark leaves
// raw HTML unparsed, which is exactly what plan 3's sanitiser then passes
// through, so a /blob/ reference inside one is live.
func walkRawHTML(body string, fn func(raw string)) {
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.RawHTML:
			for i := 0; i < v.Segments.Len(); i++ {
				seg := v.Segments.At(i)
				fn(string(seg.Value(src)))
			}
		case *ast.HTMLBlock:
			lines := v.Lines()
			for i := 0; i < lines.Len(); i++ {
				line := lines.At(i)
				fn(string(line.Value(src)))
			}
			if v.HasClosure() {
				closure := v.ClosureLine
				fn(string(closure.Value(src)))
			}
		}
		return ast.WalkContinue, nil
	})
}

// Extract returns the sorted, deduplicated blob hashes the body embeds.
// This is the authority for the embedded flag on task_blobs.
//
// A hash cited only through raw HTML -- an <img>/<video>/<source> the
// sanitiser passes through (plan 3) -- counts as embedded too, found by
// regexp-scanning each raw-HTML node's source text rather than parsing the
// HTML properly: cheap, and biased toward over-counting, which only pins a
// blob against GC rather than risking collecting one a body still displays.
// Extract still walks the AST to find those nodes rather than regexp-
// scanning the whole body, so a hash sitting in a fenced code block or an
// inline code span -- never turned into ast.RawHTML/ast.HTMLBlock -- is not
// scanned and does not count.
func Extract(body string) []string {
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if m := blobPath.FindStringSubmatch(dest); m != nil {
			seen[m[1]] = true
		}
	})
	walkRawHTML(body, func(raw string) {
		for _, m := range blobHTMLRef.FindAllStringSubmatch(raw, -1) {
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

// RemoteImages returns the body's http(s) image destinations, deduplicated
// in document order. These are what import mirrors into blobs (spec 021 §12).
//
// Markdown images only -- deliberately not the raw-HTML `<img src="https://…">`
// that Extract also scans. ReplaceDestination moves *ast.Image destinations
// and nothing else, so a raw-HTML URL returned here could be fetched and
// stored but never referenced: bytes in the bucket that no body points at,
// which is exactly what GC then has to clean up. Raw-HTML remote images stay
// remote, and §8's renderer drops them rather than rendering a beacon.
func RemoteImages(body string) []string {
	var out []string
	seen := map[string]bool{}
	walkImages(body, func(dest string) {
		if seen[dest] {
			return
		}
		if !strings.HasPrefix(dest, "http://") && !strings.HasPrefix(dest, "https://") {
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

// ReplaceDestination rewrites the destination of every image whose
// destination mapping names, splicing the source by byte offset: only the
// destination token itself is replaced, so an image title, a link label, the
// same path spelled in prose or inside a code fence, and a plain link to the
// same file all survive verbatim. Spec 021 §7 keeps a linked local file
// linked -- `lode task attach` is the tool for those -- so only *ast.Image
// destinations move.
//
// An angle-bracket destination (`<./my shot.png>`) is replaced whole,
// brackets included: the `/blob/<hash>` form contains no spaces, so the
// brackets have nothing left to protect.
//
// It errors rather than half-rewriting when a mapped destination cannot be
// located in the source -- a reference-style image, whose destination is
// written at the definition and not at the image. The caller has already
// uploaded that file, so failing beats writing a body that points at a local
// path the reader cannot resolve.
func ReplaceDestination(body string, mapping map[string]string) (string, error) {
	if len(mapping) == 0 {
		return body, nil
	}
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))

	type edit struct {
		start, stop int
		to          string
	}
	var edits []edit
	// cursor is the offset past everything already accounted for, so a
	// destination is only ever matched at or after the image that owns it.
	cursor := 0
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if c := consumedThrough(n); c > cursor {
				cursor = c
			}
			return ast.WalkContinue, nil
		}
		// On exit, so the node's own label text has already pushed the
		// cursor past the "](" that introduces the destination.
		dest, isImage := inlineDestination(n)
		if dest == "" {
			return ast.WalkContinue, nil
		}
		to, mapped := mapping[dest]
		start, stop, ok := findDestination(src, cursor, dest)
		if !ok {
			if mapped && isImage {
				return ast.WalkStop, fmt.Errorf(
					"cannot locate image destination %q in the body: only inline images (![alt](path)) can be rewritten", dest)
			}
			return ast.WalkContinue, nil
		}
		cursor = stop
		if mapped && isImage {
			edits = append(edits, edit{start: start, stop: stop, to: to})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", err
	}

	// Edits come out in document order; apply them back to front so an
	// earlier edit's offsets stay valid.
	out := body
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = out[:e.start] + e.to + out[e.stop:]
	}
	return out, nil
}

// inlineDestination returns an inline node's link destination and whether the
// node is an image, or "" for anything else.
func inlineDestination(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.Image:
		return string(v.Destination), true
	case *ast.Link:
		return string(v.Destination), false
	}
	return "", false
}

// consumedThrough returns the offset a node proves the scan has reached.
// Blocks contribute the start of their first line, which is what steps the
// cursor over a preceding fenced code block; inline text contributes the end
// of its own segment, which is what steps it over a code span. Everything
// else contributes nothing and leaves the cursor where it was.
func consumedThrough(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Stop
	case *ast.RawHTML:
		if v.Segments.Len() > 0 {
			return v.Segments.At(v.Segments.Len() - 1).Stop
		}
		return 0
	}
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
		return n.Lines().At(0).Start
	}
	return 0
}

// findDestination locates dest as a link destination at or after from,
// returning its byte span. goldmark stores Destination as the raw source
// bytes (angle brackets stripped, escapes unresolved), so the comparison is
// exact; anchoring on "](" is what keeps a bare path in prose from matching.
func findDestination(src []byte, from int, dest string) (start, stop int, ok bool) {
	if from < 0 {
		from = 0
	}
	for i := from; i+1 < len(src); i++ {
		if src[i] != ']' || src[i+1] != '(' {
			continue
		}
		j := i + 2
		for j < len(src) && isDestSpace(src[j]) {
			j++
		}
		if j < len(src) && src[j] == '<' {
			end := j + 1 + len(dest)
			if end < len(src) && string(src[j+1:end]) == dest && src[end] == '>' {
				return j, end + 1, true
			}
			continue
		}
		end := j + len(dest)
		if end <= len(src) && string(src[j:end]) == dest &&
			(end == len(src) || src[end] == ')' || isDestSpace(src[end])) {
			return j, end, true
		}
	}
	return 0, 0, false
}

func isDestSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
