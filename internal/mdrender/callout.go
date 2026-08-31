// GitHub alert syntax (plan doc 175, WL-415): a blockquote whose first
// paragraph starts, on its own line, with "[!NOTE]" or one of the other four
// kinds becomes a styled callout instead of a literal blockquote. The
// sanitiser allowlist change that lets the emitted class/title survive
// Body() is a separate task (plan doc 175 task 2); this file only turns the
// AST node and emits the pinned markup.
//
// The transformer runs as a parser.ASTTransformer, after both block and
// inline parsing, rather than matching raw text earlier in the pipeline.
// Goldmark's inline parser splits "[!NOTE]" into three Text nodes ("[",
// "!NOTE", "]") because '[' triggers the link parser — the same splitting
// autolink.go's textRuns works around — so detection concatenates a
// paragraph's leading Text nodes up to the first line break rather than
// reading one node's value.
package mdrender

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// calloutTitles gives the canonical spelling to render for each kind,
// keyed by the lowercase name calloutMarker matches into. Matching is
// case-insensitive on input ("[!note]" works); the emitted class and title
// always use these spellings.
var calloutTitles = map[string]string{
	"note":      "Note",
	"tip":       "Tip",
	"important": "Important",
	"warning":   "Warning",
	"caution":   "Caution",
}

// calloutMarker matches a blockquote's first paragraph's first line, and
// only that whole line: GitHub's rule is the marker alone on its own line,
// so any text before or after it — checked by requiring \A and \z against
// the reconstructed line — disqualifies the blockquote.
var calloutMarker = regexp.MustCompile(`(?i)\A\[!(note|tip|important|warning|caution)\]\z`)

// calloutNode replaces a blockquote that matched a callout marker. It keeps
// only the kind; its children are the blockquote's own children, with the
// marker line's inline nodes removed.
type calloutNode struct {
	gast.BaseBlock
	Name string // lowercase key into calloutTitles: "note", "tip", ...
}

var kindCallout = gast.NewNodeKind("Callout")

func (n *calloutNode) Kind() gast.NodeKind { return kindCallout }

func (n *calloutNode) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, map[string]string{"Name": n.Name}, nil)
}

// calloutTransformer walks the parsed tree once, collecting every
// blockquote before mutating any of them: replacing a node mid-walk would
// strand the walker on removed nodes' sibling pointers (the same reason
// autolink.go's docRefLinker collects its runs first).
type calloutTransformer struct{}

func (calloutTransformer) Transform(doc *gast.Document, reader text.Reader, _ parser.Context) {
	var blockquotes []*gast.Blockquote
	_ = gast.Walk(doc, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if entering {
			if bq, ok := n.(*gast.Blockquote); ok {
				blockquotes = append(blockquotes, bq)
			}
		}
		return gast.WalkContinue, nil
	})
	source := reader.Source()
	for _, bq := range blockquotes {
		name, ok := stripCalloutMarker(bq, source)
		if !ok {
			continue
		}
		cn := &calloutNode{Name: name}
		for c := bq.FirstChild(); c != nil; {
			next := c.NextSibling()
			bq.RemoveChild(bq, c)
			cn.AppendChild(cn, c)
			c = next
		}
		if parent := bq.Parent(); parent != nil {
			parent.ReplaceChild(parent, bq, cn)
		}
	}
}

// stripCalloutMarker reports whether bq's first child is a paragraph whose
// first line is a callout marker, alone. If so, it removes that line's
// inline nodes from the paragraph — deleting the paragraph too if the
// marker was its only content — and returns the matched kind.
func stripCalloutMarker(bq *gast.Blockquote, source []byte) (string, bool) {
	p, ok := bq.FirstChild().(*gast.Paragraph)
	if !ok {
		return "", false
	}
	var line strings.Builder
	var lastOfLine gast.Node
	for n := p.FirstChild(); n != nil; n = n.NextSibling() {
		t, ok := n.(*gast.Text)
		if !ok {
			return "", false // anything but plain text on the marker line disqualifies it
		}
		line.Write(t.Segment.Value(source))
		lastOfLine = n
		if t.SoftLineBreak() || t.HardLineBreak() {
			break
		}
	}
	m := calloutMarker.FindStringSubmatch(line.String())
	if m == nil {
		return "", false
	}
	for n := p.FirstChild(); ; {
		next := n.NextSibling()
		done := n == lastOfLine
		p.RemoveChild(p, n)
		if done {
			break
		}
		n = next
	}
	if p.ChildCount() == 0 {
		bq.RemoveChild(bq, p)
	}
	return strings.ToLower(m[1]), true
}

// calloutHTMLRenderer emits the pinned markup:
//
//	<aside class="callout callout-KIND">
//	  <p class="callout-title">Title</p>
//	  ...children, rendered as normal...
//	</aside>
type calloutHTMLRenderer struct{}

func (calloutHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindCallout, renderCallout)
}

func renderCallout(w util.BufWriter, _ []byte, n gast.Node, entering bool) (gast.WalkStatus, error) {
	cn := n.(*calloutNode)
	if entering {
		_, _ = w.WriteString(`<aside class="callout callout-` + cn.Name + "\">\n")
		_, _ = w.WriteString(`<p class="callout-title">` + calloutTitles[cn.Name] + "</p>\n")
	} else {
		_, _ = w.WriteString("</aside>\n")
	}
	return gast.WalkContinue, nil
}

// calloutExtension registers the transformer and renderer above. Priority
// 500 runs it before withDocRefLinks' docRefLinker (900, see autolink.go):
// structural transforms first, then reference linking over whatever text
// remains.
type calloutExtension struct{}

func (calloutExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(calloutTransformer{}, 500)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(calloutHTMLRenderer{}, 500)))
}

// calloutExt is the extension value registered on md and mdDoc, beside
// emojiExt and mermaidExt.
var calloutExt = calloutExtension{}
