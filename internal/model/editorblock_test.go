package model_test

import (
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
