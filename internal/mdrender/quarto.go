// Quarto spellings of Worklode Markdown (WL-REQ-1871): the stored spelling
// is Quarto's, so the renderer reads it beside GitHub's. internal/mdblocks
// parses with the same extension, which is why it lives here: mdblocks
// imports mdrender, never the reverse.
package mdrender

import (
	"bytes"
	"regexp"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	mermaid "go.abhg.dev/goldmark/mermaid"
)

// CalloutNode is a callout: a Quarto `::: {.callout-KIND}` fence or a
// GitHub `> [!KIND]` alert. Its children are the callout's blocks.
type CalloutNode = calloutNode

// CalloutFence is the extension that parses `::: {.callout-KIND}` … `:::`
// into a CalloutNode. It adds no renderer: calloutExt renders the node.
var CalloutFence goldmark.Extender = calloutFenceExtension{}

// calloutOpen is the opening fence: three or more colons and one callout
// class, alone on the line.
var calloutOpen = regexp.MustCompile(`\A(:{3,})[ \t]*\{\.callout-(note|tip|important|warning|caution)\}[ \t]*\n?\z`)

// calloutFenceParser parses a callout fence. A closing fence has at least
// as many colons as the opening one and nothing else.
type calloutFenceParser struct{}

func (calloutFenceParser) Trigger() []byte { return []byte{':'} }

func (calloutFenceParser) Open(_ gast.Node, reader text.Reader, pc parser.Context) (gast.Node, parser.State) {
	line, seg := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 {
		return nil, parser.NoChildren
	}
	m := calloutOpen.FindSubmatch(line[pos:])
	if m == nil {
		return nil, parser.NoChildren
	}
	n := &calloutNode{Name: string(m[2]), fence: len(m[1])}
	n.SetPos(seg.Start + pos)
	reader.AdvanceToEOL()
	return n, parser.HasChildren
}

func (calloutFenceParser) Continue(n gast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, seg := reader.PeekLine()
	cn := n.(*calloutNode)
	if pos := pc.BlockOffset(); pos >= 0 {
		rest := bytes.TrimLeft(line[pos:], ":")
		if colons := len(line) - pos - len(rest); colons >= cn.fence && util.IsBlank(rest) {
			cn.Close = seg.Start + pos
			reader.AdvanceToEOL()
			return parser.Close
		}
	}
	return parser.Continue | parser.HasChildren
}

func (calloutFenceParser) Close(gast.Node, text.Reader, parser.Context) {}

func (calloutFenceParser) CanInterruptParagraph() bool { return true }
func (calloutFenceParser) CanAcceptIndentedLine() bool { return false }

// braceMermaid turns a ```{mermaid} fence into the node mermaidExt renders.
// mermaidExt's own transformer matches the bare `mermaid` info string only.
type braceMermaid struct{}

func (braceMermaid) Transform(doc *gast.Document, reader text.Reader, _ parser.Context) {
	var found []*gast.FencedCodeBlock
	_ = gast.Walk(doc, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if cb, ok := n.(*gast.FencedCodeBlock); ok && entering && string(cb.Language(reader.Source())) == "{mermaid}" {
			found = append(found, cb)
		}
		return gast.WalkContinue, nil
	})
	for _, cb := range found {
		b := new(mermaid.Block)
		b.SetLines(cb.Lines())
		cb.Parent().ReplaceChild(cb.Parent(), cb, b)
	}
}

type calloutFenceExtension struct{}

// Extend registers the fence parser ahead of goldmark's setext, list and
// definition-list parsers.
func (calloutFenceExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(util.Prioritized(calloutFenceParser{}, 50)))
}

// braceMermaidExtension registers braceMermaid. Only the renderer uses it: the block
// parser keeps the fence as a codeBlock with language mermaid.
type braceMermaidExtension struct{}

func (braceMermaidExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(braceMermaid{}, 100)))
}

var braceMermaidExt = braceMermaidExtension{}
