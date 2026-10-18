package store

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func openIdeaStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "intake", "Intake", "IN"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a", "b"} {
		if err := s.EnsureActor(ctx, a, "human", a); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

func TestIdeaVisibilityAndShare(t *testing.T) {
	t.Parallel()
	s, ctx := openIdeaStore(t)

	if _, err := s.CreateIdea(ctx, "", model.CreateIdeaInput{Title: "x", Type: "general"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("create without author = %v, want ErrInvalidInput", err)
	}
	idea, err := s.CreateIdea(ctx, "a", model.CreateIdeaInput{Title: "Rivers", Body: "secret body", Type: "general"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(idea.ID, "idea_") || len(idea.ID) != len("idea_")+12 {
		t.Fatalf("id = %q", idea.ID)
	}
	if idea.Status != "private" || idea.Author != "a" || idea.Task != "" || idea.Shared != "" || idea.Submitted == "" {
		t.Fatalf("created = %+v", idea)
	}
	if _, has := lastEventPayload(t, s, "idea.created")["title"]; has {
		t.Fatal("idea.created payload carries the title")
	}
	wantPayload(t, s, "idea.created", map[string]any{"idea": idea.ID, "actor": "a"})

	// b cannot see, patch or share a's private idea; an empty caller neither.
	for _, caller := range []string{"b", ""} {
		if list, err := s.ListIdeas(ctx, caller); err != nil || len(list) != 0 {
			t.Fatalf("%q list = %+v, %v", caller, list, err)
		}
		if _, err := s.GetIdea(ctx, caller, idea.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q get = %v, want ErrNotFound", caller, err)
		}
	}
	title := "Stolen"
	if _, err := s.PatchIdea(ctx, "b", idea.ID, model.PatchIdeaInput{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("b patch private = %v, want ErrNotFound", err)
	}
	if _, err := s.ShareIdea(ctx, "b", idea.ID, "intake"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("b share private = %v, want ErrNotFound", err)
	}
	if err := s.DeleteIdea(ctx, "b", idea.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("b delete private = %v, want ErrNotFound", err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return requireShared(ctx, tx, "a", idea.ID) }); !errors.Is(err, ErrNotShared) {
		t.Fatalf("requireShared(a, private) = %v, want ErrNotShared", err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return requireShared(ctx, tx, "b", idea.ID) }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requireShared(b, private) = %v, want ErrNotFound", err)
	}
	if _, err := s.SetIdeaIntake(ctx, "a", idea.ID, model.PatchIdeaIntakeInput{Decision: "park"}); !errors.Is(err, ErrNotShared) {
		t.Fatalf("intake on private = %v, want ErrNotShared", err)
	}

	shared, err := s.ShareIdea(ctx, "a", idea.ID, "intake")
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if shared.Status != "intake" || shared.Task == "" || shared.Shared == "" {
		t.Fatalf("shared = %+v", shared)
	}
	task, err := s.GetTask(ctx, shared.Task)
	if err != nil {
		t.Fatalf("get intake task: %v", err)
	}
	if task.Kind != "design" || task.Project != "intake" || task.State != "ready" ||
		task.Title != "Rivers" || task.Body != "secret body" || task.CreatedBy != "a" {
		t.Fatalf("intake task = %+v", task)
	}
	wantPayload(t, s, "idea.shared", map[string]any{"idea": idea.ID, "task": task.ID, "actor": "a"})

	again, err := s.ShareIdea(ctx, "a", idea.ID, "intake")
	if err != nil || again.Task != shared.Task {
		t.Fatalf("share twice = %+v, %v", again, err)
	}
	var tasks int
	if err := s.db.QueryRow(`SELECT count(*) FROM tasks WHERE project_id = 'intake'`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("intake tasks = %d, %v", tasks, err)
	}

	// Shared: b reads it, cannot patch it, and can record the intake decision.
	if list, err := s.ListIdeas(ctx, "b"); err != nil || len(list) != 1 || list[0].ID != idea.ID {
		t.Fatalf("b list shared = %+v, %v", list, err)
	}
	if _, err := s.GetIdea(ctx, "b", idea.ID); err != nil {
		t.Fatalf("b get shared: %v", err)
	}
	if _, err := s.PatchIdea(ctx, "b", idea.ID, model.PatchIdeaInput{Title: &title}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("b patch shared = %v, want ErrForbidden", err)
	}
	if _, err := s.ShareIdea(ctx, "b", idea.ID, "intake"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("b share shared = %v, want ErrForbidden", err)
	}
	got, err := s.SetIdeaIntake(ctx, "b", idea.ID, model.PatchIdeaIntakeInput{PreResearchAuthorized: true, Decision: "narrow"})
	if err != nil || !got.Intake.PreResearchAuthorized || got.Intake.Decision != "narrow" {
		t.Fatalf("b intake = %+v, %v", got, err)
	}
	if _, err := s.SetIdeaIntake(ctx, "b", idea.ID, model.PatchIdeaIntakeInput{Decision: "maybe"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad decision = %v, want ErrInvalidInput", err)
	}

	// The author's edit of a shared idea edits the intake task too.
	newTitle, newBody := "Rivers and lakes", "new body"
	if _, err := s.PatchIdea(ctx, "a", idea.ID, model.PatchIdeaInput{Title: &newTitle, Body: &newBody}); err != nil {
		t.Fatalf("a patch shared: %v", err)
	}
	if task, _ := s.GetTask(ctx, shared.Task); task.Title != newTitle || task.Body != newBody {
		t.Fatalf("task after patch = %q / %q", task.Title, task.Body)
	}

	// Shared delete by any writer tombstones the idea and its task.
	if err := s.DeleteIdea(ctx, "b", idea.ID, "dup"); err != nil {
		t.Fatalf("b delete shared: %v", err)
	}
	if _, err := s.GetIdea(ctx, "a", idea.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted = %v, want ErrNotFound", err)
	}
	var taskDeleted bool
	if err := s.db.QueryRow(`SELECT deleted_at IS NOT NULL FROM tasks WHERE id = $1`, shared.Task).Scan(&taskDeleted); err != nil || !taskDeleted {
		t.Fatalf("task tombstoned = %v, %v", taskDeleted, err)
	}
}

func TestIdeaBlankingAndPrivateDelete(t *testing.T) {
	t.Parallel()
	s, ctx := openIdeaStore(t)

	idea, err := s.CreateIdea(ctx, "a", model.CreateIdeaInput{
		Title: "Pitch", Type: "general", StoryType: "data-story", LitmusTestID: "lt-1"})
	if err != nil {
		t.Fatal(err)
	}
	if idea.StoryType != "" || idea.LitmusTestID != "" {
		t.Fatalf("general idea kept story fields: %+v", idea)
	}
	pitch, story, lt := "story-pitch", "short-story", "lt-2"
	got, err := s.PatchIdea(ctx, "a", idea.ID, model.PatchIdeaInput{Type: &pitch, StoryType: &story, LitmusTestID: &lt})
	if err != nil || got.StoryType != story || got.LitmusTestID != lt {
		t.Fatalf("pitch patch = %+v, %v", got, err)
	}
	general := "general"
	got, err = s.PatchIdea(ctx, "a", idea.ID, model.PatchIdeaInput{Type: &general})
	if err != nil || got.StoryType != "" || got.LitmusTestID != "" {
		t.Fatalf("back to general = %+v, %v", got, err)
	}
	bad := "poem"
	if _, err := s.PatchIdea(ctx, "a", idea.ID, model.PatchIdeaInput{Type: &bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad type = %v, want ErrInvalidInput", err)
	}
	blank := " "
	if _, err := s.PatchIdea(ctx, "a", idea.ID, model.PatchIdeaInput{Title: &blank}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank title = %v, want ErrInvalidInput", err)
	}

	if err := s.DeleteIdea(ctx, "a", idea.ID, ""); err != nil {
		t.Fatalf("private delete: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM ideas WHERE id = $1`, idea.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after private delete = %d, %v", n, err)
	}
	wantPayload(t, s, "idea.deleted", map[string]any{"idea": idea.ID, "actor": "a"})
}

// TestIdeaQueriesApplyVisibleTo reads ideas.go itself: every SQL string that
// names the ideas table lives in a const that references visibleTo
// (WL-REQ-1799), so no query can skip the predicate. The insert is exempt:
// it writes the caller's own row.
func TestIdeaQueriesApplyVisibleTo(t *testing.T) {
	t.Parallel()
	f, err := parser.ParseFile(token.NewFileSet(), "ideas.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	inConst := map[*ast.BasicLit]bool{}
	consts := 0
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, v := range vs.Values {
				var names ideasRefs
				ast.Inspect(v, names.visit(inConst))
				if names.table && !names.visibleTo && vs.Names[i].Name != "visibleTo" {
					t.Errorf("const %s queries ideas without visibleTo", vs.Names[i].Name)
				}
				if names.table {
					consts++
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && mentionsIdeas(lit.Value) && !inConst[lit] {
			t.Errorf("SQL naming ideas outside a const: %s", lit.Value)
		}
		return true
	})
	if consts < 5 {
		t.Fatalf("found %d query consts; the parse is not seeing ideas.go's queries", consts)
	}
}

type ideasRefs struct{ table, visibleTo bool }

func (r *ideasRefs) visit(seen map[*ast.BasicLit]bool) func(ast.Node) bool {
	return func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			seen[n] = true
			if mentionsIdeas(n.Value) {
				r.table = true
			}
		case *ast.Ident:
			if n.Name == "visibleTo" {
				r.visibleTo = true
			}
		}
		return true
	}
}

func mentionsIdeas(s string) bool {
	for _, k := range []string{"FROM ideas", "UPDATE ideas"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
