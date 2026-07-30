package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestProjectTableShowsKey(t *testing.T) {
	var b strings.Builder
	ProjectTable(&b, []Project{{ID: "worklode", Name: "Worklode", Key: "WL",
		Repos: []RepoMapping{{Repo: "a/b", DoneState: "released"}}}})
	out := b.String()
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "WL") {
		t.Fatalf("ProjectTable output missing KEY/WL:\n%s", out)
	}
	if !strings.Contains(out, "a/b (released)") {
		t.Fatalf("ProjectTable output missing repo done_state:\n%s", out)
	}
}

// TestBoardSectionGroupsChildren checks that an epic's children render
// directly beneath it, in id order, whatever order the server sent them.
func TestBoardSectionGroupsChildren(t *testing.T) {
	var buf bytes.Buffer
	BoardRender(&buf, BoardResponse{Projects: []BoardProject{{
		ID: "proj", Name: "Proj",
		Ready: []BoardTask{
			{Task: Task{ID: "WL-9", Title: "Loose", Priority: "medium"}},
			{Task: Task{ID: "WL-3", Title: "Child B", Priority: "medium"}, Parent: "WL-1"},
			{Task: Task{ID: "WL-1", Title: "Container", Priority: "medium"}},
			{Task: Task{ID: "WL-2", Title: "Child A", Priority: "medium"}, Parent: "WL-1"},
		},
	}}})
	got := buf.String()
	epic := strings.Index(got, "WL-1")
	childA := strings.Index(got, "WL-2")
	childB := strings.Index(got, "WL-3")
	loose := strings.Index(got, "WL-9")
	if !(epic < childA && childA < childB) {
		t.Fatalf("children are not grouped under their epic:\n%s", got)
	}
	if loose < epic {
		t.Fatalf("the loose task should sort by its own id, after WL-1:\n%s", got)
	}
	if !strings.Contains(got, "└ WL-2") {
		t.Fatalf("child rows are not marked:\n%s", got)
	}
}
