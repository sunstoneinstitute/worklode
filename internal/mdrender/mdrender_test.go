package mdrender_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
)

const validHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// hostileBodies is the corpus TestHostileBodies runs through Body and
// TestDocBodyIsSanitisedLikeATaskBody runs through DocBody: both flavours have
// to neutralise every shape here, which is what "the document body runs the
// same pipeline" means in practice.
var hostileBodies = []struct {
	name, body string
	absent     []string
}{
	// From the plan.
	{"script tag", "<script>alert(1)</script>", []string{"<script", "alert(1)"}},
	{"javascript href", "[x](javascript:alert(1))", []string{"javascript:"}},
	{"remote image", "![](https://evil.example/p.png)", []string{"evil.example"}},
	{"protocol-relative image", "![](//evil.example/p.png)", []string{"evil.example"}},
	{"traversal src", `<img src="/blob/../../etc/passwd">`, []string{"etc/passwd"}},
	{"onerror", `<img src="/blob/` + validHash + `" onerror="alert(1)">`, []string{"onerror"}},
	{"data uri", "![](data:text/html;base64,PHNjcmlwdD4=)", []string{"data:"}},
	{"iframe", `<iframe src="https://evil.example"></iframe>`, []string{"<iframe"}},
	{"svg script", `<svg><script>alert(1)</script></svg>`, []string{"<script"}},
	{"uppercase hash", "![](/blob/" + strings.ToUpper(validHash) + ")", []string{"/blob/"}},

	// javascript: obfuscation. bluemonday parses the URL and matches the
	// scheme, but only after its own whitespace handling, so each shape
	// is pinned separately.
	{"mixed case scheme", `<a href="JaVaScRiPt:alert(1)">x</a>`, []string{"alert(1)"}},
	{"tab in scheme", "<a href=\"java\tscript:alert(1)\">x</a>", []string{"alert(1)"}},
	{"entity tab in scheme", `<a href="java&#09;script:alert(1)">x</a>`, []string{"alert(1)"}},
	{"leading space scheme", `<a href=" javascript:alert(1)">x</a>`, []string{"alert(1)"}},
	{"newline in scheme", "<a href=\"java\nscript:alert(1)\">x</a>", []string{"alert(1)"}},
	{"vbscript href", `<a href="vbscript:msgbox(1)">x</a>`, []string{"vbscript"}},
	{"data html href", `<a href="data:text/html,PHN2Zz4=">x</a>`, []string{"data:"}},
	{"reference javascript image", "![x][ref]\n\n[ref]: javascript:alert(1)\n", []string{"javascript", "alert(1)"}},
	{"reference remote image", "![x][ref]\n\n[ref]: https://evil.example/p.png\n", []string{"evil.example"}},

	// CSS is an exfiltration channel (background: url(...)) and a
	// mutation-XSS vector, so neither the element nor the attribute is
	// allowed anywhere.
	{"style block", "<style>body{background:url(https://evil.example/x)}</style>", []string{"evil.example", "<style"}},
	{"style attribute", `<p style="background:url(https://evil.example/x)">hi</p>`, []string{"evil.example", "style="}},
	{"style on allowed img", `<img src="/blob/` + validHash + `" style="background:url(https://evil.example/x)">`, []string{"evil.example", "style="}},

	// Remote fetches by any other name.
	{"srcset", `<img src="/blob/` + validHash + `" srcset="https://evil.example/2x.png 2x">`, []string{"evil.example", "srcset"}},
	{"picture source srcset", `<picture><source srcset="https://evil.example/a.png"><img src="/blob/` + validHash + `"></picture>`, []string{"evil.example", "srcset"}},
	{"video poster", `<video src="/blob/` + validHash + `" poster="https://evil.example/p.png"></video>`, []string{"evil.example", "poster"}},
	{"source src remote", `<video><source src="https://evil.example/v.mp4" type="video/mp4"></video>`, []string{"evil.example"}},
	{"link stylesheet", `<link rel="stylesheet" href="https://evil.example/x.css">`, []string{"evil.example", "<link"}},

	// Document-scope hijacking.
	{"base href", `<base href="https://evil.example/">`, []string{"evil.example", "<base"}},
	{"meta refresh", `<meta http-equiv="refresh" content="0;url=https://evil.example">`, []string{"evil.example", "<meta"}},
	{"form", `<form action="https://evil.example"><input name="x"></form>`, []string{"evil.example", "<form", "name="}},
	{"object", `<object data="https://evil.example/x"></object>`, []string{"evil.example", "<object"}},
	{"embed", `<embed src="https://evil.example/x">`, []string{"evil.example", "<embed"}},
	{"math foreignobject", `<math><foreignObject><script>alert(1)</script></foreignObject></math>`, []string{"alert(1)", "<script", "<math"}},

	// Mutation shapes: markup whose meaning changes when a browser
	// re-parses the sanitiser's output.
	{"noscript mutation", `<noscript><p title="</noscript><img src=x onerror=alert(1)>">`, []string{"onerror", "alert(1)"}},
	{"svg style mutation", `<svg><style><img src=x onerror=alert(1)>`, []string{"onerror", "alert(1)"}},
	{"broken comment", "<!--><script>alert(1)</script>", []string{"alert(1)", "<script"}},
	// A CDATA section is a bogus comment that swallows the <script> start
	// tag with it; what is left of the payload comes back as escaped text,
	// so the assertion is about markup, not about the word "alert".
	{"cdata", "<![CDATA[<script>alert(1)</script>]]>", []string{"<script", "<!["}},

	// Attribute-level tricks.
	{"odd case handler", `<img src=x OnErRoR=alert(1)>`, []string{"alert", "rror"}},
	{"unquoted handler", `<img src="/blob/` + validHash + `" onload=alert(1)>`, []string{"onload", "alert"}},
	{"onclick on link", `<a href="https://example.com" onclick="alert(1)">x</a>`, []string{"onclick", "alert"}},
	{"id clobbering", `<div id="config">x</div>`, []string{"id="}},

	// src regexp bypasses. The pattern must anchor both ends against the
	// whole value, not a line or a prefix.
	{"src query", `<img src="/blob/` + validHash + `?x=1">`, []string{"/blob/"}},
	{"src fragment", `<img src="/blob/` + validHash + `#f">`, []string{"/blob/"}},
	{"src traversal suffix", `<img src="/blob/` + validHash + `/../../etc/passwd">`, []string{"/blob/", "passwd"}},
	{"src 63 hex", `<img src="/blob/` + validHash[:63] + `">`, []string{"/blob/"}},
	{"src 65 hex", `<img src="/blob/` + validHash + `f">`, []string{"/blob/"}},
	{"src trailing newline", "<img src=\"/blob/" + validHash + "\n\">", []string{"/blob/"}},
	{"src protocol relative", `<img src="//evil.example/blob/` + validHash + `">`, []string{"evil.example", "/blob/"}},
	{"src absolute remote blob", `<img src="https://evil.example/blob/` + validHash + `">`, []string{"evil.example"}},
	{"src backslash", `<img src="\\evil.example\blob\` + validHash + `">`, []string{"evil.example"}},
	{"src nul byte", "<img src=\"/blob/\x00" + validHash + "\">", []string{"/blob/"}},

	// WL-416 (plan doc 175 task 2): the callout class allowlist is exactly
	// two anchored rules, aside[class] and p[class]. These pin what an
	// attacker could try to smuggle through it.
	{"aside onclick stripped, class kept", `<aside class="callout callout-note" onclick="alert(1)">x</aside>`, []string{"onclick", "alert(1)"}},
	{"aside invalid class stripped", `<aside class="evil">x</aside>`, []string{`class="evil"`, "evil"}},
	{"div class stripped, rule is per-element", `<div class="callout">x</div>`, []string{`class="callout"`, "callout"}},
	{"aside script stripped", `<aside><script>alert(1)</script></aside>`, []string{"<script", "alert(1)"}},
}

// TestHostileBodies is the load-bearing test. Task bodies are untrusted:
// spec 020's inbox import writes GitHub issue text straight into tasks.body,
// so anyone who can open an issue on a mapped repo controls this input.
func TestHostileBodies(t *testing.T) {
	for _, tc := range hostileBodies {
		t.Run(tc.name, func(t *testing.T) {
			got := string(mdrender.Body(mdrender.ProjectKeys{}, tc.body))
			for _, bad := range tc.absent {
				if strings.Contains(got, bad) {
					t.Fatalf("output contains %q:\n%s", bad, got)
				}
			}
		})
	}
}

// TestPolicyIsNotUGCPolicy pins the reason the package builds its own policy.
// bluemonday's UGCPolicy calls AllowImages, which registers img.src with a nil
// value pattern, and a nil pattern in an attribute's policy list matches every
// value regardless of any stricter pattern added afterwards. Reverting to
// UGCPolicy — with or without an additional Matching(blobSrc) rule — makes
// these cases fail.
func TestPolicyIsNotUGCPolicy(t *testing.T) {
	for _, body := range []string{
		`<img src="https://example.com/tracker.png">`,
		`<img src="http://example.com/tracker.png">`,
		`<img src="/not-a-blob/x.png">`,
		"![](https://example.com/tracker.png)",
	} {
		got := string(mdrender.Body(mdrender.ProjectKeys{}, body))
		// The element itself may remain when markdown gave it an alt
		// attribute; what must never remain is a source to fetch.
		if strings.Contains(got, "src=") || strings.Contains(got, "example.com") {
			t.Fatalf("non-blob image source survived %q:\n%s", body, got)
		}
	}
}

// TestSafeMarkupSurvives: sanitising must not gut ordinary formatting.
func TestSafeMarkupSurvives(t *testing.T) {
	body := "# Heading\n\n**bold** and `code`\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"- [ ] todo\n- [x] done\n\n" +
		"<b>raw bold</b>\n\n" +
		"[link](https://example.com)\n\n" +
		"![shot](/blob/" + validHash + ")\n\n" +
		"> [!NOTE]\n> A note body.\n"
	got := string(mdrender.Body(mdrender.ProjectKeys{}, body))
	for _, want := range []string{
		"<h1", "<strong>", "<code>", "<table", "<b>raw bold</b>",
		`href="https://example.com"`, `rel="nofollow"`,
		`src="/blob/` + validHash + `"`,
		// WL-416 (plan doc 175 task 2): the [!NOTE] callout end to end —
		// the aside and title classes must survive Body(), not just the
		// pre-sanitiser renderer output callout_test.go pins.
		`<aside class="callout callout-note">`,
		`<p class="callout-title">Note</p>`,
		"A note body.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

// TestCalloutTitleClassSurvivesRaw pins a deliberate stance, not an
// accident: p[class="callout-title"] is allowed wherever a body writes it,
// not only where callout.go emits it. It is inert styling with no script or
// URL sink behind it — the same stance buildPolicy already takes on other
// cosmetic survivals like raw <b> — so a body cannot abuse the rule to gain
// anything a legitimate callout title could not already do.
func TestCalloutTitleClassSurvivesRaw(t *testing.T) {
	got := string(mdrender.Body(mdrender.ProjectKeys{}, `<p class="callout-title">Not a real callout</p>`))
	if !strings.Contains(got, `<p class="callout-title">Not a real callout</p>`) {
		t.Fatalf("raw p.callout-title did not survive sanitising:\n%s", got)
	}
}

// TestTaskListCheckboxes covers spec 021 section 15 criterion 7. goldmark
// renders the GFM checkbox with empty attribute values, which a value pattern
// written for the attribute names would reject.
func TestTaskListCheckboxes(t *testing.T) {
	got := string(mdrender.Body(mdrender.ProjectKeys{}, "- [ ] todo\n- [x] done\n"))
	for _, want := range []string{`<input`, `type="checkbox"`, `disabled=""`, `checked=""`} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "<input") != 2 {
		t.Fatalf("expected two checkboxes:\n%s", got)
	}
	// Only the checkbox shape is allowed: no other input type may ride in.
	for _, body := range []string{
		`<input type="image" src="/blob/` + validHash + `">`,
		`<input type="hidden" name="x" value="y">`,
		`<input type="text">`,
	} {
		if out := string(mdrender.Body(mdrender.ProjectKeys{}, body)); strings.Contains(out, "<input") {
			t.Fatalf("non-checkbox input survived %q:\n%s", body, out)
		}
	}
}

func TestVideoAllowed(t *testing.T) {
	body := `<video src="/blob/` + validHash + `" controls poster="/blob/` + validHash + `"></video>` +
		"\n\n" + `<video controls><source src="/blob/` + validHash + `" type="video/mp4"></video>`
	got := string(mdrender.Body(mdrender.ProjectKeys{}, body))
	for _, want := range []string{"<video", "controls", `poster="/blob/`, "<source", `type="video/mp4"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

// TestOutputIsBalanced: the caller embeds the result inside a container
// element, so a stray end tag must not close that container from the inside
// and an unclosed element must not swallow the page chrome that follows.
func TestOutputIsBalanced(t *testing.T) {
	for _, body := range []string{
		"</div></div>text",
		"</div><script>alert(1)</script>",
		"<div>unclosed",
		"</td></tr></table></p>",
		"<blockquote>",
	} {
		got := string(mdrender.Body(mdrender.ProjectKeys{}, body))
		if strings.Count(got, "<div") != strings.Count(got, "</div") {
			t.Fatalf("unbalanced div in output for %q:\n%s", body, got)
		}
		if strings.Count(got, "<blockquote") != strings.Count(got, "</blockquote") {
			t.Fatalf("unbalanced blockquote in output for %q:\n%s", body, got)
		}
		if strings.Contains(got, "</p>") && !strings.Contains(got, "<p>") {
			t.Fatalf("stray </p> in output for %q:\n%s", body, got)
		}
	}
	// An unclosed anchor would otherwise make the rest of the page one link.
	got := string(mdrender.Body(mdrender.ProjectKeys{}, `<a href="https://evil.example">`))
	if !strings.Contains(got, "</a>") {
		t.Fatalf("unclosed anchor in output:\n%s", got)
	}
}

// TestRoundTripDoesNotMutate: the balancing pass re-parses already sanitised
// HTML, so markup smuggled through an attribute value must stay a value.
func TestRoundTripDoesNotMutate(t *testing.T) {
	for _, body := range []string{
		`<b title="&lt;img src=x onerror=alert(1)&gt;">x</b>`,
		`<p title='"><img src=x onerror=alert(1)>'>x</p>`,
		"<p title=\"\xc0\xbcscript\xc0\xbe\">x</p>",
	} {
		got := string(mdrender.Body(mdrender.ProjectKeys{}, body))
		if strings.Contains(got, "onerror") || strings.Contains(got, "<img") {
			t.Fatalf("attribute value became markup for %q:\n%s", body, got)
		}
	}
}

// TestLinkHrefSchemes covers spec 021 section 8.1's a[href] scheme list.
// Protocol-relative "//host" has no scheme for bluemonday to check, so it
// counts as relative and would otherwise survive; root-relative links must
// keep working, since /blob/<hash> is one.
func TestLinkHrefSchemes(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"https", `<a href="https://example.com/x">t</a>`, `href="https://example.com/x"`},
		{"http", `<a href="http://example.com/x">t</a>`, `href="http://example.com/x"`},
		{"mailto", `<a href="mailto:x@example.com">t</a>`, `href="mailto:x@example.com"`},
		{"fragment", `<a href="#sec-1">t</a>`, `href="#sec-1"`},
		{"root relative", `<a href="/blob/` + validHash + `">t</a>`, `href="/blob/` + validHash + `"`},
		{"uppercase scheme", `<a href="HTTPS://example.com/x">t</a>`, `href="https://example.com/x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(mdrender.Body(mdrender.ProjectKeys{}, tc.body)); !strings.Contains(got, tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, got)
			}
		})
	}
	for _, tc := range []struct{ name, body string }{
		{"protocol relative", `<a href="//evil.example/x">t</a>`},
		{"protocol relative markdown", `[t](//evil.example/x)`},
		{"backslash protocol relative", `<a href="/\evil.example/x">t</a>`},
		{"ftp", `<a href="ftp://evil.example/x">t</a>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(mdrender.Body(mdrender.ProjectKeys{}, tc.body))
			if strings.Contains(got, "href=") || strings.Contains(got, "evil.example") {
				t.Fatalf("off-origin href survived %q:\n%s", tc.body, got)
			}
		})
	}
}

// TestFallbackEscapes drives Body into each of its three fallback paths and
// pins that the body comes back as escaped source rather than as markup. All
// three are reachable from a body an issue author controls, so this is the
// coverage that matters, not that html/template escapes.
func TestFallbackEscapes(t *testing.T) {
	const payload = `<script>alert('x' & "y")</script>`
	const escaped = `&lt;script&gt;alert(&#39;x&#39; &amp; &#34;y&#34;)&lt;/script&gt;`

	cases := []struct {
		name string
		body string
	}{
		// Over maxBody: goldmark is quadratic on some inline shapes, so
		// oversize input is never handed to the parser.
		{"over size", payload + strings.Repeat("x", 64<<10)},
		// Over the parser's 512 open elements. 1.2 KB of plain markdown.
		{"over nesting", payload + "\n\n" + strings.Repeat("> ", 600) + "x"},
		// Over maxRendered: distinct titles defeat the Noah's Ark clause, so
		// every following block re-opens all 400 formatting elements.
		{"over rendered", payload + "\n\n" + amplifier(400, 4700)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(mdrender.Body(mdrender.ProjectKeys{}, tc.body))
			if !strings.Contains(got, escaped) {
				t.Fatalf("fallback did not escape the source:\n%.500s", got)
			}
			if strings.Contains(got, "<script") || strings.Contains(got, "<p>") {
				t.Fatalf("fallback emitted markup:\n%.500s", got)
			}
			if len(got) > 4<<20 {
				t.Fatalf("fallback output is %d bytes", len(got))
			}
		})
	}
}

// amplifier builds the balance() expansion bomb: opens formatting elements
// with distinct attribute values, then blocks that each re-open all of them.
func amplifier(tags, blocks int) string {
	var b strings.Builder
	for i := 0; i < tags; i++ {
		fmt.Fprintf(&b, `<b title="t%d">`, i)
	}
	for i := 0; i < blocks; i++ {
		b.WriteString("<div>y</div>")
	}
	return b.String()
}

// TestRenderedOutputIsBounded: the amplifier above is under maxBody and is
// accepted by the parser, so only the maxRendered cap stops it.
func TestRenderedOutputIsBounded(t *testing.T) {
	body := amplifier(400, 4800)
	if len(body) > 64<<10 {
		t.Fatalf("fixture is %d bytes, over maxBody — it would take the wrong fallback", len(body))
	}
	if got := len(mdrender.Body(mdrender.ProjectKeys{}, body)); got > 4<<20 {
		t.Fatalf("rendered output is %d bytes", got)
	}
}

// WL-356: the doc flavour has its own ceiling — the task-sized 64 KiB cap
// was showing raw escaped source for 12 of the corpus's largest documents.
func TestDocBodyRendersPastTheTaskCap(t *testing.T) {
	// A well-formed doc body in the (64 KiB, 512 KiB] band.
	body := "## 1. Big {#sec-1}\n\n" + strings.Repeat("A paragraph of ordinary prose.\n\n", 3000)
	if len(body) <= 64<<10 || len(body) > 512<<10 {
		t.Fatalf("fixture is %d bytes; want between the task and doc caps", len(body))
	}

	got := string(mdrender.DocBody(mdrender.ProjectKeys{}, body))
	if !strings.Contains(got, `<h2 id="sec-1">`) {
		t.Fatalf("doc body over the task cap was not rendered:\n%.300s", got)
	}

	// The same body through the task flavour still takes the oversize
	// fallback — tasks keep the 64 KiB inbox-import bound.
	if task := string(mdrender.Body(mdrender.ProjectKeys{}, body)); strings.Contains(task, "<h2") {
		t.Fatalf("task flavour rendered past its cap:\n%.300s", task)
	}

	// And the doc flavour still has a ceiling of its own.
	over := "x" + strings.Repeat("y", 512<<10)
	if doc := string(mdrender.DocBody(mdrender.ProjectKeys{}, over)); strings.Contains(doc, "<p>") {
		t.Fatalf("doc flavour rendered past maxDocBody")
	}
}
