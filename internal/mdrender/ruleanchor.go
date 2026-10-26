package mdrender

import (
	"regexp"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// A heading that carries an arranged rule gets an empty <span id="rule-<n>">
// before it, beside the heading's own sec- id, so a #rule-<n> fragment lands
// on the rule wherever the document arranges it (WL-REQ-1809). An element has
// one id, hence the span. Only the document flavour has heading ids, so a
// task body never gets one.

// ruleNumber is the n of a rule ref ("WL-REQ-1809" -> "1809").
var ruleNumber = regexp.MustCompile(`-(\d+)\z`)

type ruleAnchorNode struct {
	gast.BaseBlock
	Number string
}

var kindRuleAnchor = gast.NewNodeKind("RuleAnchor")

func (n *ruleAnchorNode) Kind() gast.NodeKind { return kindRuleAnchor }

func (n *ruleAnchorNode) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, map[string]string{"Number": n.Number}, nil)
}

type ruleAnchorTransformer struct{}

func (ruleAnchorTransformer) Transform(doc *gast.Document, _ text.Reader, pc parser.Context) {
	var keys ProjectKeys
	if pc != nil {
		keys, _ = pc.Get(projectKeysKey).(ProjectKeys)
	}
	if len(keys.inPage) == 0 {
		return
	}
	numberOf := make(map[string]string, len(keys.inPage)) // anchor -> rule number
	for ref, anchor := range keys.inPage {
		if m := ruleNumber.FindStringSubmatch(ref); m != nil {
			numberOf[anchor] = m[1]
		}
	}
	var headings []*gast.Heading
	_ = gast.Walk(doc, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if h, ok := n.(*gast.Heading); ok && entering {
			headings = append(headings, h)
		}
		return gast.WalkContinue, nil
	})
	for _, h := range headings {
		id, ok := h.AttributeString("id")
		if !ok {
			continue
		}
		var anchor string
		switch v := id.(type) {
		case []byte:
			anchor = string(v)
		case string:
			anchor = v
		}
		if n, ok := numberOf[anchor]; ok {
			h.Parent().InsertBefore(h.Parent(), h, &ruleAnchorNode{Number: n})
		}
	}
}

type ruleAnchorHTMLRenderer struct{}

func (ruleAnchorHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindRuleAnchor, func(w util.BufWriter, _ []byte, n gast.Node, entering bool) (gast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString(`<span id="rule-` + n.(*ruleAnchorNode).Number + "\"></span>\n")
		}
		return gast.WalkContinue, nil
	})
}

type ruleAnchorExtension struct{}

func (ruleAnchorExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(ruleAnchorTransformer{}, 500)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(ruleAnchorHTMLRenderer{}, 500)))
}

// ruleAnchorExt is registered on mdDoc only.
var ruleAnchorExt = ruleAnchorExtension{}
