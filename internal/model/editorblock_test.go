package model_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestEditorBlockRejectsUnknownType(t *testing.T) {
	b := model.EditorBlock{Type: "video"}
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "video") {
		t.Fatalf("Validate() = %v, want an error naming video", err)
	}
}

func TestEditorBlockValidatesChildren(t *testing.T) {
	ok := model.EditorBlock{Type: "paragraph", Children: []model.EditorBlock{{Type: "callout"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	bad := model.EditorBlock{Type: "quote", Children: []model.EditorBlock{{Type: "audio"}}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("Validate() = %v, want an error naming audio", err)
	}
}

func roundTrip(t *testing.T, in model.EditorBlock) (map[string]any, model.EditorBlock) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	var out model.EditorBlock
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return generic, out
}

func TestEditorBlockTableContentIsContentOnTheWire(t *testing.T) {
	w := 100
	in := model.EditorBlock{ID: "t", Type: "table", TableContent: &model.TableContent{
		Type: "tableContent", ColumnWidths: []*int{&w, nil},
		Rows: []model.TableRow{{Cells: []model.TableCell{
			{Content: []model.InlineContent{{Type: "text", Text: "a"}}},
			{Type: "tableCell", Props: json.RawMessage(`{"colspan":1}`), Content: []model.InlineContent{{Type: "text", Text: "b"}}},
		}}},
	}}
	generic, out := roundTrip(t, in)
	c, ok := generic["content"].(map[string]any)
	if !ok || c["type"] != "tableContent" {
		t.Fatalf("content = %v, want a tableContent object", generic["content"])
	}
	if _, ok := generic["tableContent"]; ok {
		t.Fatal("tableContent leaked onto the wire")
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip: got %+v", out)
	}
}

func TestEditorBlockParagraphRoundTrip(t *testing.T) {
	in := model.EditorBlock{ID: "p", Type: "paragraph", Content: []model.InlineContent{{Type: "text", Text: "hi", Styles: map[string]bool{"bold": true}}}}
	generic, out := roundTrip(t, in)
	if _, ok := generic["content"].([]any); !ok {
		t.Fatalf("content = %v, want an array", generic["content"])
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip: got %+v", out)
	}
}
