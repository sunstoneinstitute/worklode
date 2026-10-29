package mdblocks

import (
	"bytes"
	"maps"

	emast "github.com/yuin/goldmark-emoji/ast"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// inline converts n's inline children to runs (WL-REQ-1871). A run's Offset
// is the byte offset of its first character in src, and each later character
// of a text run sits at Offset plus the byte length of the characters before
// it. An emoji shortcode is its own one-character run, at the shortcode's
// colon, so the runs around it keep that mapping. A link's Offset is its
// first text run's.
func inline(src []byte, n ast.Node) []model.InlineContent {
	c := inlineConv{src: src}
	c.walk(n, nil)
	return c.out
}

type inlineConv struct {
	src []byte
	out []model.InlineContent
	end int // source offset just past the last run, for finding a shortcode
}

func (c *inlineConv) walk(n ast.Node, styles map[string]bool) {
	for k := n.FirstChild(); k != nil; k = k.NextSibling() {
		switch k := k.(type) {
		case *ast.Text:
			c.text(string(k.Segment.Value(c.src)), k.Segment.Start, styles, false)
			if k.SoftLineBreak() || k.HardLineBreak() {
				c.text("\n", lineEnd(c.src, k.Segment.Stop), styles, false)
			}
		case *ast.Emphasis:
			if k.Level == 2 {
				c.walk(k, with(styles, "bold"))
			} else {
				c.walk(k, with(styles, "italic"))
			}
		case *east.Strikethrough:
			c.walk(k, with(styles, "strike"))
		case *ast.CodeSpan:
			c.walk(k, with(styles, "code"))
		case *ast.Link:
			c.link(string(k.Destination), k.Pos(), func(sub *inlineConv) { sub.walk(k, styles) })
		case *ast.AutoLink:
			label := k.Label(c.src)
			at := k.Pos()
			if k.AutoLinkType == ast.AutoLinkURL || k.AutoLinkType == ast.AutoLinkEmail {
				at = c.end + bytes.Index(c.src[c.end:], label)
			}
			c.link(string(k.URL(c.src)), at, func(sub *inlineConv) { sub.text(string(label), at, styles, false) })
		case *emast.Emoji:
			// The node has no position: the shortcode is the first one after the previous run.
			at := c.end + bytes.Index(c.src[c.end:], []byte(":"+string(k.ShortName)+":"))
			c.text(string(k.Value.Unicode), at, styles, true)
			c.end = at + len(k.ShortName) + 2
		default:
			c.walk(k, styles)
		}
	}
}

// text appends a run, merging it into the previous one when both are plain
// text with the same styles and contiguous in src.
func (c *inlineConv) text(t string, at int, styles map[string]bool, emoji bool) {
	if t == "" {
		return
	}
	c.end = at + len(t)
	if l := len(c.out) - 1; !emoji && l >= 0 {
		p := &c.out[l]
		if p.Type == "text" && !c.isEmoji(*p) && p.Offset+len(p.Text) == at && maps.Equal(p.Styles, styles) {
			p.Text += t
			return
		}
	}
	c.out = append(c.out, model.InlineContent{Type: "text", Text: t, Styles: styles, Offset: at})
}

// isEmoji reports whether r is an emoji run: its text is not the source's bytes.
func (c *inlineConv) isEmoji(r model.InlineContent) bool {
	return r.Offset+len(r.Text) > len(c.src) || string(c.src[r.Offset:r.Offset+len(r.Text)]) != r.Text
}

func (c *inlineConv) link(href string, pos int, fill func(*inlineConv)) {
	sub := inlineConv{src: c.src, end: c.end}
	fill(&sub)
	l := model.InlineContent{Type: "link", Href: href, Content: sub.out, Offset: pos}
	if len(sub.out) > 0 {
		l.Offset = sub.out[0].Offset
	}
	c.out = append(c.out, l)
	c.end = sub.end
}

func with(styles map[string]bool, s string) map[string]bool {
	m := maps.Clone(styles)
	if m == nil {
		m = map[string]bool{}
	}
	m[s] = true
	return m
}
