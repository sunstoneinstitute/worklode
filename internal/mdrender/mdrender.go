// Package mdrender turns a markdown body into safe HTML.
//
// The pipeline is GitHub's: render permissively, then sanitise the OUTPUT
// HTML. Escaping at the parser instead would forbid the limited inline HTML
// authors expect; sanitising after gives the same expressiveness with an
// allowlist that is easy to audit in one place.
//
// There are two flavours of body, differing only in the parser option and the
// one attribute that option produces — see flavour. A task body is untrusted
// (spec 020's inbox import writes GitHub issue text straight into it); a
// design-document body arrives through the doc.write-gated docs API, which is
// a different threat model but not a reason to run a different pipeline. Both
// run this one.
package mdrender

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	mdhtml "github.com/yuin/goldmark/renderer/html"
	mermaid "go.abhg.dev/goldmark/mermaid"
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
	mermaidClass = regexp.MustCompile(`\Amermaid\z`)
)

// linkHref is the only shape an a[href] may take: spec 021 section 8.1's three
// schemes, an in-page fragment or query, or a root-relative path.
//
// bluemonday's own AllowRelativeURLs treats "//evil.example/x" as relative — it
// has no scheme to check — so a protocol-relative URL points off-origin under
// the page's own scheme. Rejecting a leading "//" (and "/\", which some parsers
// treat the same way) is the point of this pattern.
//
// A scheme-less path that does not start with "/" is rejected too. It would
// resolve against whatever cockpit path the reader happens to be on, which is
// not a base an imported issue body knows anything about, and the spec's list
// does not cover it. Leading whitespace is tolerated because bluemonday trims
// it before parsing the URL, so matching on the untrimmed value would drop
// links it would otherwise keep.
var linkHref = regexp.MustCompile(`\A[\t\n\f\r ]*(?i:https?:|mailto:|[#?]|/(?:[^/\\]|\z))`)

// sectionAnchor is the only id value a rendered body may carry, and only on a
// heading (see buildDocPolicy). It is the 025 section 3 anchor grammar: the
// "sec-" prefix, then a section number (3.1a) or a lowercase slug (purpose).
//
// It must agree with internal/designdoc's anchorRE, which is what the corpus
// itself is linted against; TestSectionAnchorMatchesDesigndoc fails if the two
// drift apart. \A and \z rather than ^ and $ for the reason blobSrc gives.
var sectionAnchor = regexp.MustCompile(`\Asec-[a-z0-9][a-z0-9.-]*\z`)

// emojiExt renders ":emoji:" shortcodes as the literal Unicode character
// rather than the extension's default <img> tag. An <img> would point at a
// CDN, which buildPolicy's img[src] rule (blobSrc only) would strip anyway —
// Unicode substitution is what makes emoji actually render instead of
// silently vanishing, and it needs no allowlist change at all.
var emojiExt = emoji.New(emoji.WithRenderingMethod(emoji.Unicode))

// mermaidExt turns a ```mermaid fence into <pre class="mermaid">, for
// mermaid.js (loaded separately by the pages that need it, self-hosted per
// the page CSP's script-src 'self') to draw client-side. RenderModeClient is
// pinned rather than left at the default RenderModeAuto: auto-detection
// shells out to the "mmdc" CLI when present on $PATH to render server-side,
// which is both a subprocess a rendering path should never invoke over
// untrusted input and a silent behaviour change depending on what happens to
// be installed on the host. NoScript suppresses the extension's own <script>
// tags — bluemonday drops them anyway since script is not in buildPolicy's
// allowlist, so emitting them would just be dead output.
var mermaidExt = &mermaid.Extender{RenderMode: mermaid.RenderModeClient, NoScript: true}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, emojiExt, mermaidExt),
	// Unsafe here means "let raw HTML through to the sanitiser", not "trust
	// it". The bluemonday policy below is the actual boundary.
	goldmark.WithRendererOptions(mdhtml.WithUnsafe()),
	goldmark.WithParserOptions(withDocRefLinks),
)

// mdDoc is md plus goldmark's attribute syntax, which is what turns the
// corpus's "## 3.1 Title {#sec-3.1}" into <h3 id="sec-3.1"> instead of into
// literal text. The option is not global because a task body has no anchor
// convention: enabling it there would start eating "{#...}" out of prose that
// never meant it as markup.
//
// WithAttribute lets an author write any attribute, not just id — the
// allowlist, not the parser, is what keeps that harmless.
var mdDoc = goldmark.New(
	goldmark.WithExtensions(extension.GFM, emojiExt, mermaidExt),
	goldmark.WithParserOptions(parser.WithAttribute(), withDocRefLinks),
	goldmark.WithRendererOptions(mdhtml.WithUnsafe()),
)

// mdMeta parses only front matter, into the parser.Context DocMeta hands it —
// it never renders a body and is never given to render(), so it shares
// nothing with the sanitised pipeline above and cannot affect what a page
// shows. mdDoc's own frontmatter is stripped as raw text by stripFrontmatter
// before Convert ever sees it (WL-301), so wiring this extension into mdDoc
// itself would parse nothing; DocMeta exists to reach the fields that removal
// throws away.
var mdMeta = goldmark.New(goldmark.WithExtensions(meta.Meta))

// DocMeta parses a design-document body's YAML front matter and returns it as
// a plain map, for callers that want the fields programmatically rather than
// rendered. It does no sanitisation and returns no HTML, so it is safe to
// call on an untrusted body without going through render.
func DocMeta(body string) (map[string]any, error) {
	ctx := parser.NewContext()
	if err := mdMeta.Convert([]byte(body), io.Discard, parser.WithContext(ctx)); err != nil {
		return nil, err
	}
	return meta.Get(ctx), nil
}

var (
	policy    = buildPolicy()
	docPolicy = buildDocPolicy()
)

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
	// linkHref must be the ONLY policy registered for a[href]: bluemonday
	// allows an attribute when any registered policy matches, so registering a
	// bare AllowAttrs("href") alongside this one would make it dead code, the
	// same trap buildPolicy exists to avoid.
	p.AllowAttrs("href").Matching(linkHref).OnElements("a")

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
	// AllowTables brings two of bluemonday's own match-anything patterns with
	// it: td/th "nowrap" uses `(?i)|nowrap`, whose empty alternation matches
	// every value, and "scope" uses an unanchored `(?i)(?:row|col)(?:group)?`,
	// so scope="colXXX" passes. Neither is a URL or a script sink and both are
	// escaped by html.Render, so this is cosmetic. It cannot be tightened by
	// adding a stricter Matching() on top — that would OR with the loose one,
	// not replace it — so leave it alone unless the whole table allowlist is
	// rewritten here.
	p.AllowTables()

	// mermaidExt's client renderer marks its container "pre.mermaid" for
	// mermaid.js to find; this is the one class value that element may carry.
	// It is not the "class" global UGCPolicy leaves out reversed — it is
	// scoped to one element and one exact value, not a general reopening of
	// class/style.
	p.AllowAttrs("class").Matching(mermaidClass).OnElements("pre")

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

// buildDocPolicy is buildPolicy plus the single attribute a design document
// needs: an id on a heading, so the Sections table on the document page can
// link at the section it names.
//
// buildPolicy's reason for dropping the global id — an attacker-chosen id
// enables DOM clobbering — is not weakened here. Two things keep it: the id is
// allowed on headings only, and its value must be a section anchor, so a body
// cannot name "main-content", "global-nav" or any other id the page's own
// chrome uses. It also cannot collide with a form control name, which is the
// other half of the clobbering trick.
//
// This must stay the ONLY policy registered for id, on any element: bluemonday
// allows an attribute when any registered policy matches, so a second, looser
// id rule anywhere would make sectionAnchor dead code. That is the trap
// buildPolicy exists to avoid, and TestDocPolicyAllowsOnlyAnchorIDs is what
// fails if it is sprung.
func buildDocPolicy() *bluemonday.Policy {
	p := buildPolicy()
	p.AllowAttrs("id").Matching(sectionAnchor).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	return p
}

// flavour is one render pipeline: the parser that produced the HTML, the
// allowlist that reduces it, and the name both are reported under. The two
// differ by exactly one parser option and one attribute; everything that makes
// the pipeline safe — the caps, balance, the rest of the allowlist — is shared
// on purpose, because a document body being more trusted than a task body is
// not a reason to sanitise it less.
type flavour struct {
	kind    string
	md      goldmark.Markdown
	policy  *bluemonday.Policy
	maxBody int // per-flavour body ceiling (WL-356): tasks keep maxBody, docs get maxDocBody
}

// Body kinds, reported as the "kind" label of the cache's metrics and mixed
// into the cache key so one body cannot be served under the other flavour's
// render.
const (
	kindTask = "task"
	kindDoc  = "doc"
)

var (
	taskFlavour = flavour{kind: kindTask, md: md, policy: policy, maxBody: maxBody}
	docFlavour  = flavour{kind: kindDoc, md: mdDoc, policy: docPolicy, maxBody: maxDocBody}
)

// maxRendered bounds what balance will emit.
//
// HTML5's "reconstruct the active formatting elements" step re-opens every
// still-open formatting element inside each following block, so a body of a few
// hundred <b> tags with distinct attributes (distinct values evade the Noah's
// Ark three-copy clause) followed by a few thousand blocks expands by two to
// three orders of magnitude: 64 KiB of input measured at 48 MB of output and
// half a gigabyte of allocation. A browser parsing the unbalanced fragment
// would build the same DOM, so the expansion is not new work for the client —
// the harm is that balance turns it into a server-side string. 1 MiB is 16x
// maxBody, far past anything ordinary markdown expands to, and Cache means it
// is paid once per body rather than once per view.
const maxRendered = 1 << 20

var errTooLarge = errors.New("mdrender: rendered output over maxRendered")

// cappedWriter fails the render instead of letting the buffer grow past limit,
// so the amplification above costs a bounded allocation rather than the full
// expansion.
type cappedWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errTooLarge
	}
	return w.buf.Write(p)
}

// balance re-parses sanitised HTML as a fragment and re-renders it, so the
// result nests correctly on its own.
//
// bluemonday passes an unmatched end tag straight through and never closes an
// unclosed one, and the caller drops this HTML inside a container element. A
// body starting with "</div>" would close that container and let the rest of
// the body sit in the page chrome; a body opening an <a> and never closing it
// would turn the remainder of the page into one link to the author's URL.
//
// LANDMINE: re-parsing sanitised output is the classic mutation-XSS setup, and
// it is safe here only because of what the policy above happens to exclude.
// bluemonday's output can never contain <svg>, <math> or <template>, so
// ParseFragment never enters foreign content or a template content document,
// where tokenisation rules differ and where essentially every classic mXSS
// lives; and it can never contain a raw-text element (script, style, textarea,
// title), so html.Render's literal-text path — which writes children through
// unescaped — is unreachable. Both are emergent properties of the current
// allowlist, not guarantees of this function. Adding <style>, <svg>, <math> or
// <template> to buildPolicy turns balance into a live mXSS engine.
//
// Two error paths, both handled by falling back in Body: html.ParseFragment
// gives up past 512 open elements (x/net/html parser.oe), which plain markdown
// reaches with a few hundred nested blockquotes, and cappedWriter rejects
// output over maxRendered.
func balance(fragment []byte) ([]byte, error) {
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(bytes.NewReader(fragment), ctx)
	if err != nil {
		return nil, err
	}
	out := cappedWriter{limit: maxRendered}
	for _, n := range nodes {
		if err := html.Render(&out, n); err != nil {
			return nil, err
		}
	}
	return out.buf.Bytes(), nil
}

// maxBody is the largest body Body will parse.
//
// goldmark's inline parser is quadratic on some shapes — "[x](" repeated costs
// seconds of CPU well before the input gets large — and nothing caches the
// result, so this cap is the only thing bounding one page view. 64 KiB is
// GitHub's own issue-body limit and therefore the largest body spec 020's
// inbox import can produce; the API's 1 MiB request cap sizes a request, not a
// body a human wrote. The cap does not make a hostile 64 KiB body cheap, it
// only stops the cost from growing; Cache is what stops it from being paid
// again on every view.
const maxBody = 64 << 10

// maxDocBody is the doc flavour's ceiling (WL-356). A design-document body
// arrives through the doc.write-gated docs API — authored, not spec 020's
// untrusted issue import — and the corpus's largest specs and plans are
// routinely past the task cap (025 is 137 KB). The DoS argument is weaker
// (permission-gated writes, and Cache pays the render once per body) but a
// ceiling stays: 512 KiB keeps a hostile-shaped body bounded and sits under
// maxRendered's 1 MiB output cap.
const maxDocBody = 512 << 10

// Render outcomes, reported by the cache as the "outcome" label of
// worklode_mdrender_renders_total. outcomeOversize means the body was refused
// before the parser saw it; outcomeFallback means the pipeline ran and one of
// its bounds rejected the result.
const (
	outcomeOK       = "ok"
	outcomeOversize = "oversize"
	outcomeFallback = "fallback"
)

// Body renders an untrusted markdown task body to sanitised HTML.
//
// keys is the live project-key set, which decides which bare <KEY>-<n> tokens
// in the body are task ids worth linking; the zero ProjectKeys links none.
// See autolink.go for why the caller supplies it.
//
// Callers pass the result to templ.Raw, which takes a string, so the
// template.HTML type is erased at that boundary: the safety contract is this
// function, not the return type.
//
// Body renders on every call. A cockpit serving the same body to many readers
// wants (*Cache).Body instead — see cache.go for why paying this per view is
// not affordable.
func Body(keys ProjectKeys, body string) template.HTML {
	html, _ := render(taskFlavour, keys, body)
	return html
}

// DocBody renders a design-document body to sanitised HTML. It is Body with
// section anchors kept: same caps, same balance pass, same allowlist but for
// an id on a heading. (*Cache).DocBody is the cached form.
func DocBody(keys ProjectKeys, body string) template.HTML {
	html, _ := render(docFlavour, keys, body)
	return html
}

// stripFrontmatter drops a leading YAML frontmatter block from a document
// body (WL-301): the doc page renders those fields structurally, and a
// frontmatter fence rendered as markdown is a thematic break plus prose
// noise. Applied to the doc flavour only, inside render, so the cached and
// uncached paths agree and the cache key stays the stored body.
func stripFrontmatter(body string) string {
	rest, ok := strings.CutPrefix(body, "---\n")
	if !ok {
		return body
	}
	if i := strings.Index(rest, "\n---\n"); i >= 0 {
		return rest[i+5:]
	}
	if trimmed, ok := strings.CutSuffix(rest, "\n---"); ok {
		_ = trimmed
		return ""
	}
	return body
}

// render is Body plus the outcome label the cache reports.
func render(f flavour, keys ProjectKeys, body string) (template.HTML, string) {
	if f.kind == kindDoc {
		body = stripFrontmatter(body)
	}
	if len(body) > f.maxBody {
		return template.HTML(template.HTMLEscapeString(body)), outcomeOversize
	}
	var buf bytes.Buffer
	// A fresh parse context per Convert: the two goldmark values are
	// package-level and shared by concurrent renders, so the key set cannot
	// ride the parser.
	if err := f.md.Convert([]byte(body), &buf, withProjectKeys(keys)); err != nil {
		// Defensive: goldmark cannot fail writing into a bytes.Buffer. Kept so
		// that a future sink which can fail does not lose the body.
		return template.HTML(template.HTMLEscapeString(body)), outcomeFallback
	}
	out, err := balance(f.policy.SanitizeBytes(buf.Bytes()))
	if err != nil {
		// Escaped source reads badly, but it is the only safe fallback: the
		// unbalanced fragment is exactly what balance exists to suppress.
		return template.HTML(template.HTMLEscapeString(body)), outcomeFallback
	}
	return template.HTML(out), outcomeOK
}
