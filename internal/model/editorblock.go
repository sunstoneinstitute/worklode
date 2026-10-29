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
	// TableContent holds a table block's content. On the wire it is `content`
	// (see MarshalJSON); Content is empty for a table.
	TableContent *TableContent `json:"-"`
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

// TableContent is BlockNote 0.56's tableContent. HeaderRows and HeaderCols are
// optional there. A column width is null when unset.
type TableContent struct {
	Type         string     `json:"type"`
	ColumnWidths []*int     `json:"columnWidths"`
	HeaderRows   *int       `json:"headerRows,omitempty"`
	HeaderCols   *int       `json:"headerCols,omitempty"`
	Rows         []TableRow `json:"rows"`
}

// TableRow is one row of a TableContent.
type TableRow struct {
	Cells []TableCell `json:"cells"`
}

// TableCell is a cell as BlockNote 0.56 accepts it: a bare array of inline
// content, or a "tableCell" object with props. Type is empty for the bare form.
type TableCell struct {
	Type    string          `json:"type,omitempty"`
	Props   json.RawMessage `json:"props,omitempty"`
	Content []InlineContent `json:"content"`
}

// MarshalJSON writes the bare array form when Type is empty.
func (c TableCell) MarshalJSON() ([]byte, error) {
	if c.Type == "" {
		return json.Marshal(c.Content)
	}
	type plain TableCell
	return json.Marshal(plain(c))
}

// UnmarshalJSON reads either cell form.
func (c *TableCell) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		*c = TableCell{}
		return json.Unmarshal(b, &c.Content)
	}
	type plain TableCell
	return json.Unmarshal(b, (*plain)(c))
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

// MarshalJSON puts a table block's TableContent under `content`, as BlockNote does.
func (b EditorBlock) MarshalJSON() ([]byte, error) {
	type plain EditorBlock
	if b.Type != "table" || b.TableContent == nil {
		return json.Marshal(plain(b))
	}
	return json.Marshal(struct {
		plain
		Content *TableContent `json:"content"`
	}{plain(b), b.TableContent})
}

// UnmarshalJSON reads `content` as a TableContent for a table block, and as
// inline content for every other type.
func (b *EditorBlock) UnmarshalJSON(data []byte) error {
	type plain EditorBlock
	var raw struct {
		plain
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = EditorBlock(raw.plain)
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	if b.Type == "table" {
		b.TableContent = new(TableContent)
		return json.Unmarshal(raw.Content, b.TableContent)
	}
	return json.Unmarshal(raw.Content, &b.Content)
}
