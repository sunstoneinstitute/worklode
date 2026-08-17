package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
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

// TestBoardSectionGroupsChildren checks that a parent's children render
// directly beneath it while the rest of the rows keep the order the server
// sent — the server already sorts by priority, so a plain id sort would be
// wrong. The fixture is built so id order and the wanted order disagree:
// WL-5 is critical and arrives first, and parent WL-1's children are WL-9 and
// WL-4.
func TestBoardSectionGroupsChildren(t *testing.T) {
	var buf bytes.Buffer
	BoardRender(&buf, BoardResponse{Projects: []BoardProject{{
		ID: "proj", Name: "Proj",
		Ready: []BoardTask{
			{Task: Task{ID: "WL-5", Title: "Urgent", Priority: "critical"}},
			{Task: Task{ID: "WL-9", Title: "Child B", Priority: "medium"}, Parent: "WL-1"},
			{Task: Task{ID: "WL-1", Title: "Container", Priority: "medium"}},
			{Task: Task{ID: "WL-4", Title: "Child A", Priority: "medium"}, Parent: "WL-1"},
			{Task: Task{ID: "WL-2", Title: "Orphan", Priority: "medium"}, Parent: "WL-7"},
		},
	}}})
	got := buf.String()
	urgent := strings.Index(got, "WL-5")
	parent := strings.Index(got, "WL-1")
	childB := strings.Index(got, "WL-9")
	childA := strings.Index(got, "WL-4")
	orphan := strings.Index(got, "WL-2")
	if !(parent < childB && childB < childA) {
		t.Fatalf("children are not grouped under their parent in arrival order:\n%s", got)
	}
	if urgent > parent {
		t.Fatalf("grouping moved the critical task below the parent:\n%s", got)
	}
	if orphan < childA {
		t.Fatalf("the orphan should keep its own position, last:\n%s", got)
	}
	if !strings.Contains(got, "└ WL-9") || !strings.Contains(got, "└ WL-4") {
		t.Fatalf("child rows are not marked:\n%s", got)
	}
	if strings.Contains(got, "└ WL-2") {
		t.Fatalf("the orphan's parent is not in this bucket, so it must not be marked:\n%s", got)
	}
}

func TestTaskDetailRenderHierarchy(t *testing.T) {
	var buf bytes.Buffer
	TaskDetailRender(&buf, TaskDetail{
		Task: Task{ID: "WL-2", Title: "Piece", Project: "proj", Priority: "medium",
			Kind: "feature", State: "ready"},
		Hierarchy: TaskHierarchy{Parent: &TaskParent{ID: "WL-1", Title: "Container", State: "in_progress"}},
	})
	if got := buf.String(); !strings.Contains(got, "parent:   WL-1") {
		t.Fatalf("output has no parent line:\n%s", got)
	}

	buf.Reset()
	TaskDetailRender(&buf, TaskDetail{
		Task: Task{ID: "WL-1", Title: "Container", Project: "proj", Priority: "medium",
			Kind: "feature", State: "in_progress"},
		Hierarchy: TaskHierarchy{Progress: TaskProgress{Closed: 3, Total: 7}},
	})
	if got := buf.String(); !strings.Contains(got, "progress: 3/7") {
		t.Fatalf("output has no progress line:\n%s", got)
	}
}

func TestTreeRender(t *testing.T) {
	var buf bytes.Buffer
	TreeRender(&buf, []TreeNode{{
		Parent:   Task{ID: "WL-1", Title: "Container", State: "in_progress"},
		Progress: TaskProgress{Closed: 1, Total: 2},
		Children: []Task{
			{ID: "WL-2", Title: "Done piece", State: "merged"},
			{ID: "WL-3", Title: "Open piece", State: "ready"},
		},
	}})
	got := buf.String()
	for _, want := range []string{"WL-1", "Container", "1/2", "WL-2", "WL-3", "merged"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tree output missing %q:\n%s", want, got)
		}
	}
}

func TestMoney(t *testing.T) {
	tests := []struct {
		name   string
		amount string
		want   string
	}{
		{"whole micros truncate", "10.880324", "10.88"},
		{"rounds down below half a cent", "0.414999", "0.41"},
		{"rounds half up", "0.415000", "0.42"},
		// Spend below half a cent is real; reporting it as 0.00 would say
		// the work was free.
		{"tiny amount is not zero", "0.001000", "<0.01"},
		{"exactly zero stays zero", "0.000000", "0.00"},
		{"half a cent rounds to a cent", "0.005000", "0.01"},
		{"carries into the next unit", "1234.999999", "1235.00"},
		{"no fractional part", "7", "7.00"},
		{"unparseable is shown verbatim", "not-a-number", "not-a-number"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Money(tc.amount); got != tc.want {
				t.Fatalf("Money(%q) = %q, want %q", tc.amount, got, tc.want)
			}
		})
	}
}

func TestHumanTokens(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"}, {999, "999"}, {1000, "1.0k"}, {1200, "1.2k"},
		{999_999, "1000.0k"}, {1_000_000, "1.0M"}, {11_779_507, "11.8M"},
	}
	for _, tc := range tests {
		if got := HumanTokens(tc.n); got != tc.want {
			t.Errorf("HumanTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestEventTable(t *testing.T) {
	var buf bytes.Buffer
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	EventTable(&buf, []Event{
		{ID: 1, Source: "github", ExternalID: "d1", Type: "push", ReceivedAt: at},
		{ID: 2, Source: "cli", ExternalID: "c1", Type: "docs.synced", ReceivedAt: at.Add(time.Minute)},
	})
	out := buf.String()
	for _, want := range []string{"ID", "RECEIVED", "SOURCE", "TYPE", "EXTERNAL_ID",
		"1", "github", "push", "d1", "2", "cli", "docs.synced", "c1"} {
		if !strings.Contains(out, want) {
			t.Errorf("EventTable output missing %q:\n%s", want, out)
		}
	}
}

func TestEventSubscriberTable(t *testing.T) {
	var buf bytes.Buffer
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	EventSubscriberTable(&buf, []EventSubscriberStatus{
		{Name: "doc-lifecycle", LastReadOffset: 10, LastAckedOffset: 8, Lag: 2, HolderPID: 4242, UpdatedAt: at},
		{Name: "idle-sub", LastReadOffset: 0, LastAckedOffset: 0, Lag: 0, HolderPID: 0, UpdatedAt: at},
	})
	out := buf.String()
	for _, want := range []string{"NAME", "READ", "ACKED", "LAG", "HOLDER", "UPDATED",
		"doc-lifecycle", "10", "8", "2", "4242", "idle-sub"} {
		if !strings.Contains(out, want) {
			t.Errorf("EventSubscriberTable output missing %q:\n%s", want, out)
		}
	}
	var holderCol string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "idle-sub") {
			fields := strings.Fields(line)
			if len(fields) > 4 {
				holderCol = fields[4]
			}
		}
	}
	if holderCol != "-" {
		t.Errorf("idle-sub HOLDER column = %q, want \"-\"\n%s", holderCol, out)
	}
}

func TestEventStreamRow(t *testing.T) {
	var buf bytes.Buffer
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	EventStreamHeader(&buf)
	EventStreamRow(&buf, Event{ID: 7, Source: "github", ExternalID: "d7", Type: "push", ReceivedAt: at})
	EventStreamRow(&buf, Event{ID: 8, Source: "cli", ExternalID: "c8", Type: "docs.synced", ReceivedAt: at})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want a header and two rows:\n%s", len(lines), buf.String())
	}
	if got := strings.Fields(lines[0]); len(got) != 5 ||
		got[0] != "ID" || got[4] != "EXTERNAL_ID" {
		t.Errorf("header = %v, want EventTable's five columns", got)
	}
	for i, want := range [][]string{{"7", "github", "push", "d7"}, {"8", "cli", "docs.synced", "c8"}} {
		for _, w := range want {
			if !strings.Contains(lines[i+1], w) {
				t.Errorf("row %d missing %q: %s", i, w, lines[i+1])
			}
		}
	}
	// Columns line up across rows even though nothing measured them: the
	// source column starts at the same offset in both.
	if strings.Index(lines[1], "github") != strings.Index(lines[2], "cli") {
		t.Errorf("source column not aligned across rows:\n%s", buf.String())
	}
}
