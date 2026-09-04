package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestProjectTableShowsKey(t *testing.T) {
	var b strings.Builder
	ProjectTable(&b, []model.Project{{ID: "worklode", Name: "Worklode", Key: "WL",
		Repos: []model.RepoMapping{{Repo: "a/b", DoneState: "released"}}}})
	out := b.String()
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "WL") {
		t.Fatalf("ProjectTable output missing KEY/WL:\n%s", out)
	}
	if !strings.Contains(out, "a/b (released)") {
		t.Fatalf("ProjectTable output missing repo done_state:\n%s", out)
	}
}

// TestCrewTableFallsBackToActorID: display_name is a nullable column, so a
// member added before their actor had one shows a blank NAME cell unless the
// table falls back to the actor id — matching what internal/ui/crew.templ
// already does for the web page.
func TestCrewTableFallsBackToActorID(t *testing.T) {
	var b strings.Builder
	CrewTable(&b, []model.CrewMember{
		{Actor: "ada", DisplayName: "Ada Lovelace", Roles: []string{"editor"}},
		{Actor: "bob", DisplayName: "", Roles: []string{"reporter"}},
	})
	out := b.String()
	if !strings.Contains(out, "Ada Lovelace") {
		t.Fatalf("CrewTable output missing the display name for ada:\n%s", out)
	}
	var bobLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "bob") {
			bobLine = line
		}
	}
	fields := strings.Fields(bobLine)
	if len(fields) < 2 || fields[0] != "bob" || fields[1] != "bob" {
		t.Fatalf("CrewTable should fall back to the actor id in the NAME column when display_name is empty, got line %q in:\n%s", bobLine, out)
	}
}

// TestDocRefFormsTheShorthand: a spec or ADR renders as the 025 §14.3
// shorthand a person cites; a plan, which carries no number and so has no
// shorthand, renders as its kind alone; and a document whose project key did
// not reach the client degrades to the unqualified form rather than printing a
// leading dash.
func TestDocRefFormsTheShorthand(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  model.Doc
		want string
	}{
		{"spec", model.Doc{ProjectKey: "WL", Kind: "spec", Number: 29}, "WL-SPEC-29"},
		{"adr", model.Doc{ProjectKey: "WL", Kind: "adr", Number: 7}, "WL-ADR-7"},
		{"plan", model.Doc{ProjectKey: "WL", Kind: "plan", Number: 7}, "WL-PLAN-7"},
		{"no key", model.Doc{Kind: "spec", Number: 29}, "SPEC-29"},
		{"unnumbered", model.Doc{ProjectKey: "WL", Kind: "plan"}, "plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DocRef(tc.doc); got != tc.want {
				t.Fatalf("DocRef = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDocTableIsRefStatusTitle: the table is exactly three columns. The ref
// carries the kind and the number, and everything else a document has —
// the integer id, the slug, the version — is worth less than the width the
// title gets instead, so it stays in --json (WL-336).
func TestDocTableIsRefStatusTitle(t *testing.T) {
	var b strings.Builder
	DocTable(&b, []model.Doc{{
		ID: 4242, ProjectKey: "WL", Kind: "spec", Number: 29,
		Slug: "029-research-work-in-the-backbone", Title: "Research work",
		Status: "draft", Version: 2,
	}})
	out := b.String()
	header, row, _ := strings.Cut(out, "\n")
	if got := strings.Fields(header); len(got) != 3 ||
		got[0] != "REF" || got[1] != "STATUS" || got[2] != "TITLE" {
		t.Fatalf("DocTable header = %v, want [REF STATUS TITLE]", got)
	}
	if fields := strings.Fields(row); len(fields) == 0 || fields[0] != "WL-SPEC-29" {
		t.Fatalf("DocTable first cell should be the formatted id, got row %q", row)
	}
	for _, unwanted := range []struct{ name, text string }{
		{"the integer id", "4242"},
		{"the slug", "029-research-work-in-the-backbone"},
	} {
		if strings.Contains(out, unwanted.text) {
			t.Errorf("DocTable should not print %s; --json carries it:\n%s", unwanted.name, out)
		}
	}
}

// TestDocTableRendersEveryKindTheSameWay: 029 §4 puts every kind on its
// project's sequence, so a plan reads as WL-PLAN-7 beside WL-SPEC-29 rather
// than as the bare kind word it showed while 025 §14.3 gave it no number.
func TestDocTableRendersEveryKindTheSameWay(t *testing.T) {
	var b strings.Builder
	DocTable(&b, []model.Doc{
		{ProjectKey: "WL", Kind: "spec", Number: 29, Title: "Research work", Status: "draft"},
		{ProjectKey: "WL", Kind: "plan", Number: 7, Title: "Milestones", Status: "draft"},
		{ProjectKey: "WL", Kind: "adr", Number: 43, Title: "Secrets catalog", Status: "draft"},
	})
	for _, want := range []string{"WL-SPEC-29", "WL-PLAN-7", "WL-ADR-43"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("DocTable missing %s:\n%s", want, b.String())
		}
	}
}

// TestDocPlanningTableLeadsWithRef: the gap tables share the document view
// family, so they open with the same formatted id rather than the integer one.
func TestDocPlanningTableLeadsWithRef(t *testing.T) {
	var b strings.Builder
	DocPlanningTable(&b, []model.Doc{{
		ID: 91, ProjectKey: "WL", Kind: "spec", Number: 7, Slug: "007-drift-and-overview",
	}}, []model.DocPlanningGap{{
		Doc: 91, Sections: 9, Gaps: []model.DocSectionGap{{Anchor: "sec-4", Coverage: "unplanned"}},
	}})
	out := b.String()
	header, row, _ := strings.Cut(out, "\n")
	if fields := strings.Fields(header); len(fields) == 0 || fields[0] != "REF" {
		t.Fatalf("DocPlanningTable should open with a REF column, got header %q", header)
	}
	if fields := strings.Fields(row); len(fields) == 0 || fields[0] != "WL-SPEC-7" {
		t.Fatalf("DocPlanningTable first cell should be the formatted id, got row %q", row)
	}
	if strings.Contains(out, "007-drift-and-overview") {
		t.Errorf("DocPlanningTable should not print the slug:\n%s", out)
	}
}

// TestDocPlanningTableAnnotatesAnchorsWithCoverage: the ANCHORS column
// renders each gap as anchor(coverage), matching 026 §2.1's sample output
// line.
func TestDocPlanningTableAnnotatesAnchorsWithCoverage(t *testing.T) {
	var b strings.Builder
	DocPlanningTable(&b, []model.Doc{{ID: 1, Number: 7, Slug: "007-drift-and-overview"}},
		[]model.DocPlanningGap{{
			Doc: 1, Sections: 9,
			Gaps: []model.DocSectionGap{
				{Anchor: "sec-2.4", Coverage: "partial"},
				{Anchor: "sec-4", Coverage: "unplanned"},
			},
		}})
	out := b.String()
	if !strings.Contains(out, "2/9") {
		t.Fatalf("DocPlanningTable output missing the 2/9 ratio:\n%s", out)
	}
	if !strings.Contains(out, "sec-2.4(partial) sec-4(unplanned)") {
		t.Fatalf("DocPlanningTable output missing annotated anchors:\n%s", out)
	}
}

// TestDocPlanningTableAnnotatesDeferredAnchorWithOwner: a "deferred" gap
// carrying an owner renders as sec-N(deferred:OWNER) rather than the plain
// sec-N(coverage) form (026 §2.1, §5.3).
func TestDocPlanningTableAnnotatesDeferredAnchorWithOwner(t *testing.T) {
	var b strings.Builder
	DocPlanningTable(&b, []model.Doc{{ID: 1, Number: 7, Slug: "007-drift-and-overview"}},
		[]model.DocPlanningGap{{
			Doc: 1, Sections: 2,
			Gaps: []model.DocSectionGap{
				{Anchor: "sec-1", Coverage: "deferred", Owner: "006-knowledge-graph"},
				{Anchor: "sec-2", Coverage: "unplanned"},
			},
		}})
	out := b.String()
	if !strings.Contains(out, "sec-1(deferred:006-knowledge-graph) sec-2(unplanned)") {
		t.Fatalf("DocPlanningTable output missing the deferred owner annotation:\n%s", out)
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
	BoardRender(&buf, model.BoardResponse{Projects: []model.BoardProject{{
		ID: "proj", Name: "Proj",
		Ready: []model.BoardTask{
			{Task: model.Task{ID: "WL-5", Title: "Urgent", Priority: "critical"}},
			{Task: model.Task{ID: "WL-9", Title: "Child B", Priority: "medium"}, Parent: "WL-1"},
			{Task: model.Task{ID: "WL-1", Title: "Container", Priority: "medium"}},
			{Task: model.Task{ID: "WL-4", Title: "Child A", Priority: "medium"}, Parent: "WL-1"},
			{Task: model.Task{ID: "WL-2", Title: "Orphan", Priority: "medium"}, Parent: "WL-7"},
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

// TestSkillTableWraps checks the two things the skill list needs that a plain
// tabwriter cannot give it: the description column wraps at the terminal width
// instead of running off the edge, and its continuation lines line up under
// the first one rather than under the name.
func TestSkillTableWraps(t *testing.T) {
	var buf bytes.Buffer
	skillTable(&buf, []model.Skill{
		{Name: "tdd", Description: "Red-green-refactor discipline for every feature and bugfix, applied before implementation code exists"},
		{Name: "systematic-debugging", Description: "Short one"},
	}, 60)
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	for _, l := range lines {
		if len(l) > 60 {
			t.Fatalf("line exceeds width 60 (%d): %q\n%s", len(l), l, got)
		}
	}
	if !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "DESCRIPTION") {
		t.Fatalf("missing header row:\n%s", got)
	}
	// "systematic-debugging" is the widest name, so the description column
	// starts two spaces past it, and every wrapped line starts there too.
	indent := len("systematic-debugging") + 2
	descCol := strings.Index(lines[1], "Red-green")
	if descCol != indent {
		t.Fatalf("description column starts at %d, want %d:\n%s", descCol, indent, got)
	}
	if len(lines) < 3 || strings.TrimSpace(lines[2]) == "" {
		t.Fatalf("long description did not wrap onto a second line:\n%s", got)
	}
	if strings.Index(lines[2], strings.TrimSpace(lines[2])) != indent {
		t.Fatalf("continuation line is not indented to the description column:\n%s", got)
	}
}

// TestSkillTableWrapsLongWord checks a word wider than the description column
// is emitted whole rather than dropped or split.
func TestSkillTableWrapsLongWord(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("x", 50)
	skillTable(&buf, []model.Skill{{Name: "a", Description: "see " + long + " end"}}, 20)
	got := buf.String()
	if !strings.Contains(got, long) {
		t.Fatalf("unbreakable word was lost:\n%s", got)
	}
	if !strings.Contains(got, "end") {
		t.Fatalf("text after the unbreakable word was lost:\n%s", got)
	}
}

// TestSkillTableLongNameKeepsColumn checks a name past the column cap takes a
// row of its own instead of shoving its description out of alignment.
func TestSkillTableLongNameKeepsColumn(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("n", maxSkillNameWidth+8)
	skillTable(&buf, []model.Skill{
		{Name: "short", Description: "first"},
		{Name: long, Description: "second"},
	}, 80)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if lines[2] != long {
		t.Fatalf("overlong name did not take its own row: %q\n%s", lines[2], buf.String())
	}
	if strings.Index(lines[1], "first") != strings.Index(lines[3], "second") {
		t.Fatalf("descriptions are not in the same column:\n%s", buf.String())
	}
}

func TestSkillSyncRender(t *testing.T) {
	var buf bytes.Buffer
	SkillSyncRender(&buf, model.SkillSyncReport{Synced: 12, Changed: 3, Deleted: 1})
	want := "synced 12 skill(s): 3 changed, 1 deleted\n"
	if got := buf.String(); got != want {
		t.Fatalf("SkillSyncRender = %q, want %q", got, want)
	}
}

// A partial failure still reports the real work alongside the per-source
// errors (SkillSyncReport's doc comment) — the errors must not replace the
// counts.
func TestSkillSyncRenderPartialFailure(t *testing.T) {
	var buf bytes.Buffer
	SkillSyncRender(&buf, model.SkillSyncReport{
		Synced: 5, Changed: 2, Errors: []string{"acme/other: not installed"},
	})
	out := buf.String()
	for _, want := range []string{
		"synced 5 skill(s): 2 changed, 0 deleted",
		"  error: acme/other: not installed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestTaskDetailRenderHierarchy(t *testing.T) {
	var buf bytes.Buffer
	TaskDetailRender(&buf, model.TaskDetail{
		Task: model.Task{ID: "WL-2", Title: "Piece", Project: "proj", Priority: "medium",
			Kind: "feature", State: "ready"},
		Hierarchy: model.TaskHierarchy{Parent: &model.TaskParent{ID: "WL-1", Title: "Container", State: "in_progress"}},
	}, "")
	if got := buf.String(); !strings.Contains(got, "parent:   WL-1") {
		t.Fatalf("output has no parent line:\n%s", got)
	}

	buf.Reset()
	TaskDetailRender(&buf, model.TaskDetail{
		Task: model.Task{ID: "WL-1", Title: "Container", Project: "proj", Priority: "medium",
			Kind: "feature", State: "in_progress"},
		Hierarchy: model.TaskHierarchy{Progress: model.TaskProgress{Closed: 3, Total: 7}},
	}, "")
	if got := buf.String(); !strings.Contains(got, "progress: 3/7") {
		t.Fatalf("output has no progress line:\n%s", got)
	}
}

// TestTaskDetailRenderLeaseShowsWorktree checks a leased task's worktree
// identity is rendered, not just the holder.
func TestTaskDetailRenderLeaseShowsWorktree(t *testing.T) {
	var buf bytes.Buffer
	TaskDetailRender(&buf, model.TaskDetail{
		Task: model.Task{ID: "WL-2", Title: "Piece", Project: "proj", Priority: "medium",
			Kind: "feature", State: "in_progress"},
		Lease: &model.Lease{ActorID: "stig", Worktree: "host:/.worktrees/wl-2"},
	}, "")
	if got := buf.String(); !strings.Contains(got, "worktree: host:/.worktrees/wl-2") {
		t.Fatalf("output has no worktree line:\n%s", got)
	}
}

// TestTaskDetailRenderSessions checks each agent session on the lease renders
// with its agent, session id, and status — ended sessions show when they
// ended, running ones show as active.
func TestTaskDetailRenderSessions(t *testing.T) {
	var buf bytes.Buffer
	ended := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	in, out := int64(12300), int64(4100)
	cost := "0.417"
	TaskDetailRender(&buf, model.TaskDetail{
		Task: model.Task{ID: "WL-2", Title: "Piece", Project: "proj", Priority: "medium",
			Kind: "feature", State: "in_progress"},
		Lease: &model.Lease{ActorID: "stig", Worktree: "host:/.worktrees/wl-2"},
		AgentSessions: []model.AgentSession{
			{Agent: "claude-code", SessionID: "sess-1", EndedAt: &ended,
				InputTokens: &in, OutputTokens: &out, CostAmount: &cost, CostCurrency: "USD"},
			{Agent: "codex", SessionID: "sess-2"},
		},
	}, "")
	got := buf.String()
	for _, want := range []string{"sessions:", "claude-code", "sess-1", "12.3k", "4.1k", "0.42", "codex", "sess-2", "active"} {
		if !strings.Contains(got, want) {
			t.Fatalf("session output missing %q:\n%s", want, got)
		}
	}
}

func TestTreeRender(t *testing.T) {
	var buf bytes.Buffer
	TreeRender(&buf, []model.TaskTreeNode{{
		Parent:   model.Task{ID: "WL-1", Title: "Container", State: "in_progress"},
		Progress: model.TaskProgress{Closed: 1, Total: 2},
		Children: []model.Task{
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
	EventTable(&buf, []model.Event{
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
	EventSubscriberTable(&buf, []model.EventSubscriberStatus{
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
	EventStreamRow(&buf, model.Event{ID: 7, Source: "github", ExternalID: "d7", Type: "push", ReceivedAt: at})
	EventStreamRow(&buf, model.Event{ID: 8, Source: "cli", ExternalID: "c8", Type: "docs.synced", ReceivedAt: at})

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

// TestBoardHolderShowsUsernameAndTimeLeft pins the HOLDER cell to
// "stig (1h14m left)". Actor ids are Keycloak preferred_username values, which
// in a realm that logs users in by email carry a domain the column has no room
// for, and an absolute expiry timestamp makes the reader do the subtraction.
func TestBoardHolderShowsUsernameAndTimeLeft(t *testing.T) {
	var buf bytes.Buffer
	BoardRender(&buf, model.BoardResponse{Projects: []model.BoardProject{{
		ID: "proj", Name: "Proj",
		InProgress: []model.BoardTask{{
			Task: model.Task{ID: "WL-1", Title: "Work", Priority: "medium"},
			Holder: &model.Holder{
				ActorID:   "stig@sunstoneinstitute.ai",
				ExpiresAt: time.Now().Add(74*time.Minute + 30*time.Second),
			},
		}},
	}}})
	got := buf.String()
	if !strings.Contains(got, "stig (1h14m left)") {
		t.Fatalf("holder cell is not %q:\n%s", "stig (1h14m left)", got)
	}
	if strings.Contains(got, "sunstoneinstitute.ai") {
		t.Fatalf("the email domain is still in the holder cell:\n%s", got)
	}
}

// TestBoardBlockedAndReadyShowKind checks that blocked and ready rows, which
// can never have a holder, show the task kind in that column instead — and
// that the column header reads KIND rather than HOLDER for those buckets.
func TestBoardBlockedAndReadyShowKind(t *testing.T) {
	var buf bytes.Buffer
	BoardRender(&buf, model.BoardResponse{Projects: []model.BoardProject{{
		ID: "proj", Name: "Proj",
		Blocked: []model.BoardTask{{
			Task: model.Task{ID: "WL-1", Title: "Stuck", Priority: "medium", Kind: "bug"},
		}},
		Ready: []model.BoardTask{{
			Task: model.Task{ID: "WL-2", Title: "Next", Priority: "medium", Kind: "design"},
		}},
	}}})
	got := buf.String()
	if strings.Contains(got, "HOLDER") {
		t.Fatalf("blocked/ready sections should not show a HOLDER column:\n%s", got)
	}
	if !strings.Contains(got, "KIND") {
		t.Fatalf("blocked/ready sections should show a KIND column:\n%s", got)
	}
	if !strings.Contains(got, "bug") || !strings.Contains(got, "design") {
		t.Fatalf("kind values not rendered:\n%s", got)
	}
}

// TestBoardInProgressShowsHolderAndKind checks that the IN PROGRESS bucket
// carries both columns: HOLDER (who's on it) and KIND (what it is), with KIND
// trailing HOLDER.
func TestBoardInProgressShowsHolderAndKind(t *testing.T) {
	var buf bytes.Buffer
	BoardRender(&buf, model.BoardResponse{Projects: []model.BoardProject{{
		ID: "proj", Name: "Proj",
		InProgress: []model.BoardTask{{
			Task: model.Task{ID: "WL-1", Title: "Work", Priority: "medium", Kind: "bug"},
			Holder: &model.Holder{
				ActorID:   "stig@sunstoneinstitute.ai",
				ExpiresAt: time.Now().Add(74*time.Minute + 30*time.Second),
			},
		}},
	}}})
	got := buf.String()
	if !strings.Contains(got, "HOLDER") || !strings.Contains(got, "KIND") {
		t.Fatalf("in progress section should show both HOLDER and KIND columns:\n%s", got)
	}
	if strings.Index(got, "HOLDER") > strings.Index(got, "KIND") {
		t.Fatalf("KIND header should trail HOLDER:\n%s", got)
	}
	if !strings.Contains(got, "stig (1h14m left)") || !strings.Contains(got, "bug") {
		t.Fatalf("holder and kind values not rendered:\n%s", got)
	}
}

// TestLeaseLeft covers the boundaries of the remaining-lease rendering: a
// sub-minute lease must not read as "0m left", and an expired one must say so
// rather than counting backwards.
func TestLeaseLeft(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{2 * time.Hour, "2h0m left"},
		{74*time.Minute + 30*time.Second, "1h14m left"},
		{59 * time.Minute, "59m left"},
		{30 * time.Second, "<1m left"},
		{0, "expired"},
		{-time.Hour, "expired"},
	} {
		if got := leaseLeft(now.Add(tc.in), now); got != tc.want {
			t.Errorf("leaseLeft(+%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestActorName keeps the shortening conservative: only an email-shaped id
// loses anything, and an id that is already a bare username is untouched.
func TestActorName(t *testing.T) {
	for in, want := range map[string]string{
		"stig@sunstoneinstitute.ai": "stig",
		"agent-1":                   "agent-1",
		"@example.com":              "@example.com",
		"":                          "",
	} {
		if got := actorName(in); got != want {
			t.Errorf("actorName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTimelineSummaryEdgeChange pins timelineSummary's "edge" case:
// store.AddEdge/RemoveEdge log {"field":"edge","op","type","from","to"}, not
// the {"field","old","new"} shape every other state_log row uses, so a naive
// key read used to render a blank "edge: " line. Both directions must render
// non-blank and name both endpoints.
func TestTimelineSummaryEdgeChange(t *testing.T) {
	added := model.TimelineEntry{
		Type:   "state",
		Change: []byte(`{"field":"edge","op":"add","type":"blocks","from":"WL-1","to":"WL-2"}`),
	}
	if got, want := timelineSummary(added), "edge added: WL-1 blocks WL-2"; got != want {
		t.Fatalf("timelineSummary(add) = %q, want %q", got, want)
	}

	removed := model.TimelineEntry{
		Type:   "state",
		Change: []byte(`{"field":"edge","op":"remove","type":"blocks","from":"WL-1","to":"WL-2"}`),
	}
	if got, want := timelineSummary(removed), "edge removed: WL-1 blocks WL-2"; got != want {
		t.Fatalf("timelineSummary(remove) = %q, want %q", got, want)
	}
}

// TestMarkdownAbsolutizesBlobURLs checks MarkdownWithBase rewrites a
// root-relative /blob/ reference to an absolute one against the given
// server, so a rendered body is terminal-clickable and unambiguous off the
// web UI.
func TestMarkdownAbsolutizesBlobURLs(t *testing.T) {
	var buf bytes.Buffer
	MarkdownWithBase(&buf, "![x](/blob/"+strings.Repeat("a", 64)+")\n", "https://wl.example")
	if !strings.Contains(buf.String(), "https://wl.example/blob/"+strings.Repeat("a", 64)) {
		t.Fatalf("blob URL not absolutized:\n%s", buf.String())
	}
}

// TestTaskDetailRendersAttachments checks the attachments table names an
// attached blob's file, media type, and size -- fields the body markdown
// never carries for a blob that is not embedded.
func TestTaskDetailRendersAttachments(t *testing.T) {
	var buf bytes.Buffer
	TaskDetailRender(&buf, model.TaskDetail{
		Task: model.Task{ID: "WL-1", Title: "crash"},
		Blobs: []model.TaskBlob{{
			Hash: "abc123", Filename: "crash.log",
			MediaType: "text/plain", Size: 4096, Attached: true,
			URL: "/blob/abc123",
		}},
	}, "")
	out := buf.String()
	for _, want := range []string{"attachments:", "crash.log", "text/plain"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// --- BriefRender ------------------------------------------------------

func TestBriefRenderShowsSecrets(t *testing.T) {
	var buf bytes.Buffer
	BriefRender(&buf, model.Brief{
		Task:   model.Task{ID: "SE-1", Title: "t", State: "ready", Priority: "medium", Secrets: []string{"A_TOKEN", "B_KEY"}},
		Branch: "lode/SE-1-t",
	})
	if !strings.Contains(buf.String(), "secrets: A_TOKEN, B_KEY") {
		t.Fatalf("brief output missing secrets line:\n%s", buf.String())
	}
}

func TestBriefRenderRendersSkillsSection(t *testing.T) {
	var buf bytes.Buffer
	BriefRender(&buf, model.Brief{
		Task:   model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "WL-1-t",
		Skills: model.SkillRecommendation{
			Pinned:   []model.PinnedSkill{{Name: "tdd", Description: "Red-green-refactor"}},
			Matches:  []model.SkillMatch{{Name: "debugging", Description: "Systematic debugging", Score: 0.87}},
			Warnings: []string{"pinned skill not found: ghost"},
			Provider: "openai-compatible",
		},
	})
	out := buf.String()
	for _, want := range []string{
		"Skills:",
		"pinned  tdd — Red-green-refactor (content in brief)",
		"0.8700  debugging — Systematic debugging",
		"warning: pinned skill not found: ghost",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
}

// A brief whose only skills content is warnings still prints them: a user who
// misspelled every pin would otherwise see nothing, which is the one case the
// warnings exist for.
func TestBriefRenderRendersWarningsOnlySkillsSection(t *testing.T) {
	var buf bytes.Buffer
	BriefRender(&buf, model.Brief{
		Task:   model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "WL-1-t",
		Skills: model.SkillRecommendation{
			Warnings: []string{"pinned skill not found: ghost"},
			Provider: "openai-compatible",
		},
	})
	if out := buf.String(); !strings.Contains(out, "warning: pinned skill not found: ghost") {
		t.Fatalf("output = %q, want the warning rendered", out)
	}
}

func TestBriefRenderOmitsSkillsSectionWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	BriefRender(&buf, model.Brief{
		Task:   model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "WL-1-t",
		Skills: model.SkillRecommendation{Provider: "none"},
	})
	if strings.Contains(buf.String(), "Skills:") {
		t.Fatalf("output = %q, want no Skills section when there is nothing to show", buf.String())
	}
}

// TestBriefRenderRendersBlockingPlans: the plans holding a task (025 §9.3) are
// rendered even when they have minted no task to list under "blocked by".
func TestBriefRenderRendersBlockingPlans(t *testing.T) {
	var buf bytes.Buffer
	BriefRender(&buf, model.Brief{
		Task:          model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch:        "WL-1-t",
		BlockingPlans: []model.DocRef{{ID: 7, Slug: "plan-a", Title: "Plan A", Status: "draft"}},
		Skills:        model.SkillRecommendation{Provider: "none"},
	})
	if out := buf.String(); !strings.Contains(out, "blocked by plans:\n  - plan-a: Plan A (draft)") {
		t.Fatalf("output = %q, want the blocking plan rendered", out)
	}
}

// --- ProjectDetailRender / CostRender ---------------------------------

// TestProjectDetailRenderShowsFocusReposAndCost checks the whole `lode project
// show` view renders from cli: identity with the project key, the focus line,
// the repo block, and one cost block per currency.
func TestProjectDetailRenderShowsFocusReposAndCost(t *testing.T) {
	var buf bytes.Buffer
	ProjectDetailRender(&buf, model.ProjectDetail{
		Project: model.Project{
			ID: "worklode", Key: "WL", Name: "Worklode",
			Focus: []string{"cockpit", "docs"},
			Repos: []model.RepoMapping{{Repo: "a/b", DoneState: "merged"}},
		},
		Cost: model.CostReport{
			Days: []model.CostDay{{
				Day: "2026-08-20", Currency: "USD", CostAmount: "1.500000",
				TokenCounts: model.TokenCounts{InputTokens: 2000, OutputTokens: 1_500_000},
			}},
			Totals: []model.CostTotals{{Currency: "USD", CostAmount: "1.500000"}},
		},
	}, "last 7 days")
	out := buf.String()
	for _, want := range []string{
		"worklode (WL) — Worklode",
		"focus: cockpit, docs",
		"a/b", "done: merged",
		"cost, last 7 days: 1.50 USD",
		"in 2.0k", "out 1.5M",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFocusLineWithNoFocus(t *testing.T) {
	var buf bytes.Buffer
	FocusLine(&buf, nil)
	if got := buf.String(); got != "focus: (none)\n" {
		t.Fatalf("FocusLine(nil) = %q, want %q", got, "focus: (none)\n")
	}
}

// An unpriced-token shortfall is called out under the block, so a total that
// understates the bill says so.
func TestCostRenderNotesUnpricedTokens(t *testing.T) {
	var buf bytes.Buffer
	CostRender(&buf, model.CostReport{
		Totals: []model.CostTotals{{Currency: "USD", CostAmount: "0.000000", UnpricedTokens: 12_000}},
	}, "all time")
	if out := buf.String(); !strings.Contains(out, "12.0k tokens from models with no price on file") {
		t.Fatalf("output missing the unpriced note:\n%s", out)
	}
}

func TestCostRenderWithNoTotals(t *testing.T) {
	var buf bytes.Buffer
	CostRender(&buf, model.CostReport{}, "all time")
	if out := buf.String(); !strings.Contains(out, "cost, all time: none recorded") {
		t.Fatalf("output = %q, want the none-recorded line", out)
	}
}

// TestTaskCostRenderNamesScope: `lode task cost --children` has to say so, or
// a folded-in total reads as the task's own.
func TestTaskCostRenderNamesScope(t *testing.T) {
	var buf bytes.Buffer
	TaskCostRender(&buf, model.TaskCost{
		Task: "WL-1", IncludesChildren: true, Sessions: 3,
	}, "all time")
	out := buf.String()
	for _, want := range []string{"WL-1 (including child tasks)", "sessions with recorded usage: 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// --- PinnedSkillList --------------------------------------------------

// A task with no pins says so: a bare blank line reads as a rendering bug.
func TestPinnedSkillListEmpty(t *testing.T) {
	var buf bytes.Buffer
	PinnedSkillList(&buf, nil)
	if got := buf.String(); got != "(no pinned skills)\n" {
		t.Fatalf("PinnedSkillList(nil) = %q", got)
	}
}

func TestPinnedSkillListOnePerLine(t *testing.T) {
	var buf bytes.Buffer
	PinnedSkillList(&buf, []string{"tdd", "debugging"})
	if got := buf.String(); got != "tdd\ndebugging\n" {
		t.Fatalf("PinnedSkillList = %q, want one skill per line", got)
	}
}

// --- ReposDoctorRender ------------------------------------------------

// A nil app_installed renders as unchecked either way, but the reason
// separates "no App configured" from "the check did not finish"; only a false
// one may read as NOT INSTALLED.
func TestReposDoctorRenderDistinguishesAppStates(t *testing.T) {
	no := false
	var buf bytes.Buffer
	ReposDoctorRender(&buf, model.ReposDoctorResponse{
		Repos: []model.RepoDoctor{
			{Repo: "acme/app", Project: "demo", Stale: true, UnappliedEvents: 3},
			{Repo: "acme/slow", Project: "demo", AppError: "context deadline exceeded"},
			{Repo: "acme/gone", Project: "demo", AppInstalled: &no,
				AppError: "github app is not installed on this repo"},
		},
		UnmappedSenders: []model.UnmappedSender{
			{Repo: "acme/unmapped", Events: 2, LastEventAt: time.Now()},
		},
	})
	out := buf.String()
	for _, want := range []string{
		"acme/app (project demo)",
		"unchecked (no GitHub App configured)",
		"unchecked (context deadline exceeded)",
		"NOT INSTALLED (github app is not installed on this repo)",
		"last event: never",
		"STALE: no delivery since mapping — run `lode task reconcile --repo acme/app`",
		"unmapped sender: acme/unmapped (2 events",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// --- ReconcileRender --------------------------------------------------

// A dry run has to say it repaired nothing; the counts are otherwise
// indistinguishable from a real run's.
func TestReconcileRenderNamesDryRun(t *testing.T) {
	var buf bytes.Buffer
	ReconcileRender(&buf, model.ReconcileResponse{
		RunID: "r1", DryRun: true,
		Replay: &model.ReplayResult{Candidates: 4, Replayed: 3, StillUnmapped: 1,
			Errors: []string{"boom"}},
		PollSkipped: "no github app configured",
	})
	out := buf.String()
	for _, want := range []string{
		"run r1",
		"replay: would repair 3 of 4 candidate event(s), 1 still unmapped",
		"  error: boom",
		"poll: skipped (no github app configured)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// The poll section renders its own fields, per candidate, and carries its
// own dry-run marker — the counts alone do not say whether anything was
// written.
func TestReconcileRenderPoll(t *testing.T) {
	var buf bytes.Buffer
	ReconcileRender(&buf, model.ReconcileResponse{
		RunID: "r3", DryRun: true,
		Replay: &model.ReplayResult{},
		Poll: &model.PollResult{
			RunID: "r3", DryRun: true, Candidates: 2,
			Repaired: []model.TaskRepair{{
				TaskID: "WL-7", Repo: "acme/app", State: "in_review",
				PRsUpdated: []int64{12}, CommitsLanded: []string{"abc", "def"},
			}},
			Errors: []string{"acme/other: not installed"},
		},
	})
	out := buf.String()
	for _, want := range []string{
		"poll: examined (dry run) 2 candidate task(s)",
		"  WL-7 (acme/app, was in_review): 1 PR(s), 2 landed commit(s)",
		"  error: acme/other: not installed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// A failed poll must not swallow the replay report: engine 1 has already
// written by then, so the operator needs both lines.
func TestReconcileRenderPollFailureKeepsReplay(t *testing.T) {
	var buf bytes.Buffer
	ReconcileRender(&buf, model.ReconcileResponse{
		RunID:     "r4",
		Replay:    &model.ReplayResult{Candidates: 2, Replayed: 2},
		PollError: "acme/app: 502 from github",
	})
	out := buf.String()
	for _, want := range []string{
		"replay: repaired 2 of 2 candidate event(s), 0 still unmapped",
		"poll: failed (acme/app: 502 from github)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// The two caps a replay run works under are re-run signals, so the human
// view has to name them — a truncated batch and a trimmed error list are
// invisible in the counts alone.
func TestReconcileRenderNamesCaps(t *testing.T) {
	var buf bytes.Buffer
	ReconcileRender(&buf, model.ReconcileResponse{
		RunID: "r2",
		Replay: &model.ReplayResult{Candidates: 500, Replayed: 400, Truncated: true,
			Errors: []string{"boom"}, ErrorsOmitted: 99},
	})
	out := buf.String()
	for _, want := range []string{
		"  error: boom",
		"  ... and 99 more error(s), not reported",
		"  batch full: more candidates remain, run again",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestProjectionFailureTable covers the quarantine view: the empty case says
// so in words rather than printing a bare header, and a multi-line error is
// collapsed so it cannot break the row it belongs to.
func TestProjectionFailureTable(t *testing.T) {
	var empty bytes.Buffer
	ProjectionFailureTable(&empty, nil)
	if got := empty.String(); !strings.Contains(got, "no projects are quarantined") {
		t.Errorf("empty quarantine rendered %q", got)
	}

	at := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	ProjectionFailureTable(&buf, []model.ProjectionFailure{{
		Project: "alpha", Attempts: 3,
		FirstFailedAt: at, LastFailedAt: at, NextAttemptAt: at,
		LastError: "put graph:\n  503 Service Unavailable",
	}})
	out := buf.String()
	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 1 {
		t.Errorf("one failure rendered %d extra lines; a multi-line error broke the row:\n%s", lines, out)
	}
	for _, want := range []string{"alpha", "put graph: 503 Service Unavailable"} {
		if !strings.Contains(out, want) {
			t.Errorf("quarantine row missing %q:\n%s", want, out)
		}
	}
}

// TestApprovalTableNamesWhoItAwaits: the queue's whole job is saying who is
// blocking what, and required_actor_name is a nullable column — a lane whose
// actor has no display name must still name the actor, and a role-scoped lane
// must name the role.
func TestApprovalTableNamesWhoItAwaits(t *testing.T) {
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	name, actor, role := "Bob Reviewer", "carol", "reviewers"
	var buf bytes.Buffer
	ApprovalTable(&buf, []model.AwaitingApproval{
		{Approval: model.Approval{ID: 1, EntityKind: "doc", EntityID: "doc:9",
			SubjectRevision: "3", RequiredActor: &actor, State: "awaiting", CreatedAt: at},
			Title: "Approvals for documents", Project: "worklode",
			RequiredActorName: &name},
		{Approval: model.Approval{ID: 2, EntityKind: "doc", EntityID: "doc:9",
			SubjectRevision: "3", RequiredActor: &actor, State: "awaiting", CreatedAt: at},
			Title: "Approvals for documents", Project: "worklode"},
		{Approval: model.Approval{ID: 3, EntityKind: "pr", EntityID: "acme/site#7",
			SubjectRevision: "abc", RequiredRole: &role, State: "awaiting", CreatedAt: at},
			Title: "Fix the thing", Project: "worklode"},
	})
	out := buf.String()
	for _, want := range []string{"ID", "KIND", "AWAITING", "PROJECT", "REQUESTED", "TITLE",
		"doc", "pr", "Bob Reviewer", "carol", "reviewers", "worklode",
		"Approvals for documents", "Fix the thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("ApprovalTable output missing %q:\n%s", want, out)
		}
	}
}

// TestBoardShowsBlockerColumns checks the two sides of a blocker edge: a
// blocked row names what holds it, a ready row names what it holds up, and a
// ready bucket where nothing blocks anything grows no column of dashes.
func TestBoardShowsBlockerColumns(t *testing.T) {
	render := func(b model.BoardProject) string {
		var buf bytes.Buffer
		BoardRender(&buf, model.BoardResponse{Projects: []model.BoardProject{b}})
		return buf.String()
	}
	got := render(model.BoardProject{
		ID: "proj", Name: "Proj",
		Ready: []model.BoardTask{{
			Task:     model.Task{ID: "WL-1", Title: "Blocker", Priority: "medium", Kind: "task"},
			Blocking: []string{"WL-2"},
		}},
		Blocked: []model.BoardTask{{
			Task:      model.Task{ID: "WL-2", Title: "Stuck", Priority: "medium", Kind: "task"},
			BlockedBy: []string{"WL-1", "0042-some-plan"},
		}},
	})
	if !strings.Contains(got, "BLOCKED BY") || !strings.Contains(got, "WL-1, 0042-some-plan") {
		t.Fatalf("blocked row does not name its blockers:\n%s", got)
	}
	if !strings.Contains(got, "BLOCKING") || !strings.Contains(got, "WL-2") {
		t.Fatalf("ready row does not name what it blocks:\n%s", got)
	}

	quiet := render(model.BoardProject{
		ID: "proj", Name: "Proj",
		Ready: []model.BoardTask{{Task: model.Task{ID: "WL-3", Title: "Free", Priority: "medium", Kind: "task"}}},
	})
	if strings.Contains(quiet, "BLOCKING") {
		t.Fatalf("ready bucket with no blocker edges still has the column:\n%s", quiet)
	}
}
