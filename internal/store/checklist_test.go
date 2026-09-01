package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// setChecklistItem drives SetChecklistItem through RecordEvent, the way
// production code will use it (mirrors setTaskSkills).
func setChecklistItem(t *testing.T, s *Store, taskID string, in model.SetChecklistItemInput) (model.ChecklistItem, error) {
	t.Helper()
	var item model.ChecklistItem
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.checklist_set", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			item, err = SetChecklistItem(tx, taskTestNow, taskID, in)
			return err
		})
	return item, err
}

func checklistTaskInput() TaskInput {
	in := defaultTaskInput()
	in.Body = "intro\n- [ ] first item\n- [x] second item\n- [ ] third item\n"
	return in
}

func TestSetChecklistItemByOrdinalChecksItem(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, checklistTaskInput())

	ord := 0
	item, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Ordinal: &ord, Checked: true})
	if err != nil {
		t.Fatalf("SetChecklistItem: %v", err)
	}
	want := model.ChecklistItem{Ordinal: 0, Title: "first item", Checked: true}
	if item != want {
		t.Fatalf("item = %#v, want %#v", item, want)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	items := model.ParseChecklist(got.Body)
	if !items[0].Checked {
		t.Fatalf("item 0 not persisted as checked: %#v", items)
	}
	if !items[1].Checked || items[1].Title != "second item" {
		t.Fatalf("item 1 changed unexpectedly: %#v", items)
	}
}

func TestSetChecklistItemByTitleUnchecksItem(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, checklistTaskInput())

	title := "second item"
	item, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Title: &title, Checked: false})
	if err != nil {
		t.Fatalf("SetChecklistItem: %v", err)
	}
	want := model.ChecklistItem{Ordinal: 1, Title: "second item", Checked: false}
	if item != want {
		t.Fatalf("item = %#v, want %#v", item, want)
	}
}

func TestSetChecklistItemAmbiguousTitleIsInvalidInput(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	in := defaultTaskInput()
	in.Body = "- [ ] dup\n- [ ] dup\n"
	task := createTask(t, s, taskTestNow, in)

	title := "dup"
	_, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Title: &title, Checked: true})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ambiguous title: want ErrInvalidInput, got %v", err)
	}
}

func TestSetChecklistItemUnknownTitleIsInvalidInput(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, checklistTaskInput())

	title := "no such item"
	_, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Title: &title, Checked: true})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown title: want ErrInvalidInput, got %v", err)
	}
}

func TestSetChecklistItemOrdinalOutOfRangeIsInvalidInput(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, checklistTaskInput())

	ord := 99
	_, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Ordinal: &ord, Checked: true})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("out of range ordinal: want ErrInvalidInput, got %v", err)
	}
}

func TestSetChecklistItemRequiresExactlyOneIdentifier(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, checklistTaskInput())

	if _, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Checked: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("neither ordinal nor title: want ErrInvalidInput, got %v", err)
	}

	ord := 0
	title := "first item"
	if _, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Ordinal: &ord, Title: &title, Checked: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("both ordinal and title: want ErrInvalidInput, got %v", err)
	}
}

func TestSetChecklistItemUnknownTaskIsNotFound(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	ord := 0
	_, err := setChecklistItem(t, s, "WL-999", model.SetChecklistItemInput{Ordinal: &ord, Checked: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown task: want ErrNotFound, got %v", err)
	}
}

// TestSetChecklistItemRaceNoLostUpdates checks the FOR UPDATE lock: N
// goroutines each check a distinct ordinal on the same task concurrently, and
// every one of them must land — a lost update would silently drop one.
func TestSetChecklistItemRaceNoLostUpdates(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	in := defaultTaskInput()
	const n = 8
	var body string
	for i := 0; i < n; i++ {
		body += fmt.Sprintf("- [ ] item %d\n", i)
	}
	in.Body = body
	task := createTask(t, s, taskTestNow, in)

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(ord int) {
			defer wg.Done()
			if _, err := setChecklistItem(t, s, task.ID, model.SetChecklistItemInput{Ordinal: &ord, Checked: true}); err != nil {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d SetChecklistItem calls failed", failures.Load())
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	items := model.ParseChecklist(got.Body)
	if len(items) != n {
		t.Fatalf("items = %d, want %d", len(items), n)
	}
	for _, it := range items {
		if !it.Checked {
			t.Fatalf("item %#v was lost (still unchecked)", it)
		}
	}
}
