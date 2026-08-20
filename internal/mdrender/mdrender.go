// Package mdrender turns an untrusted task body into safe HTML.
//
// The pipeline is GitHub's: render permissively, then sanitise the OUTPUT
// HTML. Escaping at the parser instead would forbid the limited inline HTML
// authors expect; sanitising after gives the same expressiveness with an
// allowlist that is easy to audit in one place.
package mdrender

import (
	"bytes"
	"html/template"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	mdhtml "github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// blobSrc is the only image or video source a body may reference. Remote
// sources are dropped rather than proxied: an imported issue body must not
// turn every page view into a callback to a third party. Spec 021 section 12
// mirrors remote images into blobs at import time, so this costs nothing.
//
// \A and \z anchor the whole value, so a query string, a fragment, a path
// suffix or a trailing newline all fail.
var blobSrc = regexp.MustCompile(`\A/blob/[0-9a-f]{64}\z`)

// Boolean attributes reach the sanitiser with an empty value or with their own
// name as the value, depending on how the author wrote them.
var (
	checkedAttr  = regexp.MustCompile(`(?i)\A(|checked)\z`)
	disabledAttr = regexp.MustCompile(`(?i)\A(|disabled)\z`)
	controlsAttr = regexp.MustCompile(`(?i)\A(|controls)\z`)
	openAttr     = regexp.MustCompile(`(?i)\A(|open)\z`)
	preloadAttr  = regexp.MustCompile(`(?i)\A(none|metadata|auto)\z`)
	checkboxType = regexp.MustCompile(`(?i)\Acheckbox\z`)
	mediaType    = regexp.MustCompile(`(?i)\A(audio|video|image)/[a-z0-9.+-]{1,64}\z`)
	langTag      = regexp.MustCompile(`\A[a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*\z`)
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	// Unsafe here means "let raw HTML through to the sanitiser", not "trust
	// it". The bluemonday policy below is the actual boundary.
	goldmark.WithRendererOptions(mdhtml.WithUnsafe()),
)

var policy = buildPolicy()

// buildPolicy assembles the allowlist from an empty policy rather than from
// bluemonday.UGCPolicy.
//
// UGCPolicy calls AllowImages, which registers img.src with a nil value
// pattern. bluemonday allows an attribute when ANY policy registered for it
// matches, and a nil pattern matches everything, so a stricter rule added on
// top of UGCPolicy is dead code: remote and tracking-pixel image sources would
// still pass. Everything below is UGCPolicy's markdown-relevant allowlist
// rebuilt without that rule. TestPolicyIsNotUGCPolicy fails if this is
// reverted.
func buildPolicy() *bluemonday.Policy {
	// NewPolicy already drops the content of script, style, noscript, object
	// and friends rather than merely unwrapping their tags.
	p := bluemonday.NewPolicy()

	// Global attributes. UGCPolicy also allows a global "id"; omitted because
	// an attacker-chosen id enables DOM clobbering and nothing rendered here
	// needs one. "class" and "style" stay out for the same reason UGCPolicy
	// leaves them out: unsanitised CSS is both a styling and an exfiltration
	// vector.
	p.AllowAttrs("dir").Matching(bluemonday.Direction).Globally()
	p.AllowAttrs("lang").Matching(langTag).Globally()
	p.AllowAttrs("title").Matching(bluemonday.Paragraph).Globally()

	// Parseable URLs, http/https/mailto or relative, rel=nofollow on links.
	// Relative URLs must stay enabled: /blob/<hash> is one.
	p.AllowStandardURLs()
	p.AllowAttrs("href").OnElements("a")

	p.AllowElements(
		"h1", "h2", "h3", "h4", "h5", "h6",
		"p", "div", "span", "br", "hr", "pre", "blockquote",
		"article", "aside", "section", "figure", "figcaption", "summary",
		"abbr", "acronym", "b", "cite", "code", "del", "dfn", "em", "i",
		"ins", "kbd", "mark", "s", "samp", "small", "strike", "strong",
		"sub", "sup", "tt", "u", "var", "wbr",
		"rp", "rt", "ruby",
	)
	p.AllowAttrs("cite").OnElements("blockquote", "q")
	p.AllowAttrs("open").Matching(openAttr).OnElements("details")
	p.AllowLists()
	p.AllowTables()

	// Media, pinned to blobs served by this server.
	p.AllowAttrs("src").Matching(blobSrc).OnElements("img", "video", "source")
	p.AllowAttrs("poster").Matching(blobSrc).OnElements("video")
	p.AllowAttrs("alt").Matching(bluemonday.Paragraph).OnElements("img")
	p.AllowAttrs("controls").Matching(controlsAttr).OnElements("video")
	p.AllowAttrs("preload").Matching(preloadAttr).OnElements("video")
	p.AllowAttrs("type").Matching(mediaType).OnElements("source")

	// GFM task lists render as disabled checkboxes; goldmark emits the two
	// boolean attributes with empty values. No other input type is allowed,
	// so type=image cannot smuggle in a src.
	p.AllowAttrs("type").Matching(checkboxType).OnElements("input")
	p.AllowAttrs("checked").Matching(checkedAttr).OnElements("input")
	p.AllowAttrs("disabled").Matching(disabledAttr).OnElements("input")

	return p
}

// balance re-parses sanitised HTML as a fragment and re-renders it, so the
// result nests correctly on its own.
//
// bluemonday passes an unmatched end tag straight through and never closes an
// unclosed one, and the caller drops this HTML inside a container element. A
// body starting with "</div>" would close that container and let the rest of
// the body sit in the page chrome; a body opening an <a> and never closing it
// would turn the remainder of the page into one link to the author's URL.
// Re-rendering is safe because the input has already been through the policy:
// parsing cannot invent elements or attributes, and no raw-text element
// (script, style, textarea, title) survives sanitising.
func balance(fragment []byte) ([]byte, error) {
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(bytes.NewReader(fragment), ctx)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&out, n); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

// Body renders an untrusted markdown body to sanitised HTML.
func Body(body string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(body), &buf); err != nil {
		// Rendering is a nicety; never lose the body over it.
		return template.HTML(template.HTMLEscapeString(body))
	}
	out, err := balance(policy.SanitizeBytes(buf.Bytes()))
	if err != nil {
		return template.HTML(template.HTMLEscapeString(body))
	}
	return template.HTML(out)
}
