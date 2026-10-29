package model

import (
	"encoding/json"
	"fmt"
	"slices"
)

// EditorBlock is one block of the body editor's document in BlockNote 0.56's
// model (WL-RULE-1870). Wire names match BlockNote. Blocks exist only between
// lode-server and the SPA while a Markdown field is edited; Range is the
// block's byte span in the stored Markdown.
type EditorBlock struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Props is the type's own props object, passed through opaque: its shape
	// varies by Type, and the untyped-map guard refuses map[string]any.
	Props   json.RawMessage `json:"props,omitempty"`
	Content []InlineContent `json:"content,omitempty"`
	// TableContent is BlockNote's tableContent, which it sends as `content`
	// on a table block. Go cannot hold both shapes in one field, so the
	// conversion code that WL-1370 adds maps between the two.
	TableContent *TableContent `json:"tableContent,omitempty"`
	Children     []EditorBlock `json:"children,omitempty"`
	Range        ByteRange     `json:"range"`
}

// InlineContent is a run of text or a link. Type is "text" or "link"; Content
// holds a link's text runs. Offset is the run's byte offset in the stored
// Markdown.
type InlineContent struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Styles  map[string]bool `json:"styles,omitempty"`
	Href    string          `json:"href,omitempty"`
	Content []InlineContent `json:"content,omitempty"`
	Offset  int             `json:"offset"`
}

// TableContent is BlockNote 0.56's tableContent: rows of cells of inline content.
type TableContent struct {
	Type string     `json:"type"`
	Rows []TableRow `json:"rows"`
}

// TableRow is one row of a TableContent.
type TableRow struct {
	Cells [][]InlineContent `json:"cells"`
}

// ByteRange is a half-open byte span [Start, End) in the stored Markdown.
type ByteRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// BlocksBody is a Markdown field served as blocks.
type BlocksBody struct {
	Blocks []EditorBlock `json:"blocks"`
}

// editorBlockTypes is the block list of WL-REQ-1871 (Worklode Markdown).
var editorBlockTypes = []string{
	"paragraph", "heading", "bulletListItem", "numberedListItem", "checkListItem",
	"table", "codeBlock", "quote", "callout", "image", "source",
}

// Validate refuses a block, or a descendant, whose type is not in
// WL-REQ-1871's list, naming the type.
func (b EditorBlock) Validate() error {
	if !slices.Contains(editorBlockTypes, b.Type) {
		return fmt.Errorf("editor block type %q is not in Worklode Markdown (WL-REQ-1871)", b.Type)
	}
	for _, c := range b.Children {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	return nil
}
