package model_test

import (
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestParseChecklistFindsMarkedLines(t *testing.T) {
	body := "Some intro text.\n\n- [ ] first item\n- [x] second item\n* [X] third item\nnot a checklist line\n"

	got := model.ParseChecklist(body)

	want := []model.ChecklistItem{
		{Ordinal: 0, Title: "first item", Checked: false},
		{Ordinal: 1, Title: "second item", Checked: true},
		{Ordinal: 2, Title: "third item", Checked: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseChecklist() = %#v, want %#v", got, want)
	}
}

func TestParseChecklistTreatsNonSpaceMarksAsChecked(t *testing.T) {
	body := "- [v] verified item\n- [-] cancelled item\n"

	got := model.ParseChecklist(body)

	want := []model.ChecklistItem{
		{Ordinal: 0, Title: "verified item", Checked: true},
		{Ordinal: 1, Title: "cancelled item", Checked: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseChecklist() = %#v, want %#v", got, want)
	}
}

func TestParseChecklistNoItemsReturnsEmptySlice(t *testing.T) {
	got := model.ParseChecklist("no checklist here")

	if len(got) != 0 {
		t.Fatalf("ParseChecklist() = %#v, want empty", got)
	}
}

func TestSetChecklistMarkChecksAnUncheckedItem(t *testing.T) {
	body := "intro\n- [ ] first item\n- [ ] second item\n"

	newBody, item, ok := model.SetChecklistMark(body, 1, true)

	if !ok {
		t.Fatalf("SetChecklistMark() ok = false, want true")
	}
	wantItem := model.ChecklistItem{Ordinal: 1, Title: "second item", Checked: true}
	if item != wantItem {
		t.Errorf("item = %#v, want %#v", item, wantItem)
	}
	wantBody := "intro\n- [ ] first item\n- [x] second item\n"
	if newBody != wantBody {
		t.Errorf("newBody = %q, want %q", newBody, wantBody)
	}
}

func TestSetChecklistMarkUnchecksACheckedItem(t *testing.T) {
	body := "- [x] only item\n"

	newBody, item, ok := model.SetChecklistMark(body, 0, false)

	if !ok {
		t.Fatalf("SetChecklistMark() ok = false, want true")
	}
	if item.Checked {
		t.Errorf("item.Checked = true, want false")
	}
	wantBody := "- [ ] only item\n"
	if newBody != wantBody {
		t.Errorf("newBody = %q, want %q", newBody, wantBody)
	}
}

func TestSetChecklistMarkOutOfRangeOrdinalReturnsNotOK(t *testing.T) {
	body := "- [ ] only item\n"

	newBody, _, ok := model.SetChecklistMark(body, 5, true)

	if ok {
		t.Fatalf("SetChecklistMark() ok = true, want false")
	}
	if newBody != body {
		t.Errorf("newBody = %q, want unchanged %q", newBody, body)
	}
}
