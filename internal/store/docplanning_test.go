package store

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestDocAcceptPlanRejected: a plan whose body defines no ## Tasks section
// refuses to accept — PlanTasks's error surfaces as ErrInvalidInput, and an
// accepted plan with no tasks must never exist (025 §9.2).
func TestDocAcceptPlanRejected(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody, CreatedBy: "stig",
	})

	_, _, err := acceptDoc(t, s, doc.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "task") {
		t.Errorf("err = %v, want it to name task minting", err)
	}
}

// TestDocAcceptPlanMintsTasks: accepting a plan mints one draft task per
// ## Tasks definition, in the plan's project, carrying plan_doc, title, body,
// kind, priority and skills from its definition and created_by the accepting
// actor — and nothing above them: no child_of edge is written for any of
// them.
func TestDocAcceptPlanMintsTasks(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})

	accepted, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}
	if len(minted) != 3 {
		t.Fatalf("minted %d tasks, want 3", len(minted))
	}

	wantTitles := []string{"First task", "Second task", "Third task"}
	wantKinds := []string{"feature", "bug", "chore"}
	wantPriorities := []string{"high", "medium", "low"}
	for i, task := range minted {
		if task.State != "draft" {
			t.Errorf("minted task %d state = %q, want draft", i, task.State)
		}
		if task.Project != "p1" {
			t.Errorf("minted task %d project = %q, want p1", i, task.Project)
		}
		if task.Title != wantTitles[i] {
			t.Errorf("minted task %d title = %q, want %q", i, task.Title, wantTitles[i])
		}
		if task.Kind != wantKinds[i] {
			t.Errorf("minted task %d kind = %q, want %q", i, task.Kind, wantKinds[i])
		}
		if task.Priority != wantPriorities[i] {
			t.Errorf("minted task %d priority = %q, want %q", i, task.Priority, wantPriorities[i])
		}
		if task.CreatedBy != "stig" {
			t.Errorf("minted task %d created_by = %q, want stig", i, task.CreatedBy)
		}
	}
	if !strings.Contains(minted[0].Body, "Do the first thing.") {
		t.Errorf("minted task 0 body = %q, missing prose", minted[0].Body)
	}
	if len(minted[0].Skills) != 1 || minted[0].Skills[0] != "superpowers:test-driven-development" {
		t.Errorf("minted task 0 skills = %v, want [superpowers:test-driven-development]", minted[0].Skills)
	}

	for _, task := range minted {
		var planDoc sql.NullInt64
		if err := s.db.QueryRow(`SELECT plan_doc FROM tasks WHERE id = $1`, task.ID).Scan(&planDoc); err != nil {
			t.Fatalf("read plan_doc of %s: %v", task.ID, err)
		}
		if !planDoc.Valid || planDoc.Int64 != doc.ID {
			t.Errorf("task %s plan_doc = %v, want %d", task.ID, planDoc, doc.ID)
		}
	}

	// Nothing above the minted tasks: no child_of edge involves any of them.
	for _, task := range minted {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM task_edges WHERE type = 'child_of' AND (from_task = $1 OR to_task = $1)`,
			task.ID).Scan(&n); err != nil {
			t.Fatalf("count child_of edges of %s: %v", task.ID, err)
		}
		if n != 0 {
			t.Errorf("task %s has %d child_of edges, want none", task.ID, n)
		}
	}
}

// TestDocAcceptPlanInvariant: before accept, no task carries the plan's id;
// after, the count equals the definition count; a second accept of the same
// body mints nothing, so the set can never double-mint (025 §9.2 AC2).
func TestDocAcceptPlanInvariant(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "invariant-plan", Body: planMintBody, CreatedBy: "stig",
	})

	if before := countTasksWithPlanDoc(t, s, doc.ID); before != 0 {
		t.Fatalf("tasks with plan_doc before accept = %d, want 0", before)
	}

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	after := countTasksWithPlanDoc(t, s, doc.ID)
	if after != len(minted) {
		t.Fatalf("tasks with plan_doc after accept = %d, want %d", after, len(minted))
	}

	// Re-accepting an unedited plan is a no-op, not a refusal: every
	// declaration already has a row, so there is nothing to mint (025 §9.2).
	_, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("second accept: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second accept minted %d tasks, want none", len(again))
	}
	if got := countTasksWithPlanDoc(t, s, doc.ID); got != after {
		t.Fatalf("tasks with plan_doc after the second accept = %d, want unchanged %d", got, after)
	}
}

// TestDocAcceptPlanBlockedByMintsBlocksEdge: blockedBy: [1] on Task 2's
// definition yields a blocks edge from minted task 1 to minted task 2; the
// blocked task is absent from the ready set until task 1 closes, using the
// existing blockedCondition — no new machinery here, and no plan-to-plan gate
// (that is a later task).
func TestDocAcceptPlanBlockedByMintsBlocksEdge(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "blocked-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	task1, task2 := minted[0].ID, minted[1].ID

	var edgeType string
	err = s.db.QueryRow(
		`SELECT type FROM task_edges WHERE from_task = $1 AND to_task = $2`, task1, task2,
	).Scan(&edgeType)
	if err != nil {
		t.Fatalf("read edge %s -> %s: %v", task1, task2, err)
	}
	if edgeType != "blocks" {
		t.Fatalf("edge %s -> %s type = %q, want blocks", task1, task2, edgeType)
	}

	// Promote both minted tasks out of draft so the ready-set check exercises
	// blockedCondition rather than the draft-state filter.
	if err := transition(t, s, taskTestNow, task1, "draft", "ready"); err != nil {
		t.Fatalf("transition task1 to ready: %v", err)
	}
	if err := transition(t, s, taskTestNow, task2, "draft", "ready"); err != nil {
		t.Fatalf("transition task2 to ready: %v", err)
	}

	if !isBlocked(t, s, task2) {
		t.Fatalf("IsBlocked(%s): want true while task1 open", task2)
	}
	ready, err := s.readyCandidates(t.Context(), "p1", "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	for _, r := range ready {
		if r.ID == task2 {
			t.Fatalf("readyCandidates offered %s, which task1 still blocks", task2)
		}
	}

	walkTo(t, s, task1, "merged")

	if isBlocked(t, s, task2) {
		t.Fatalf("IsBlocked(%s): want false after task1 merged", task2)
	}
	ready, err = s.readyCandidates(t.Context(), "p1", "")
	if err != nil {
		t.Fatalf("readyCandidates after release: %v", err)
	}
	found := false
	for _, r := range ready {
		if r.ID == task2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("readyCandidates after task1 merged omitted %s", task2)
	}
}

// TestDocAcceptPlanReAcceptMintsOnlyNewDeclarations: an accepted plan stays
// freely mutable, so re-accepting one mints the declarations that have no row
// yet and leaves every existing row alone (025 §9.2) — including a row whose
// declaration's prose changed, and one that has since left draft. The new
// task's declared blockedBy is wired even though its blocker was minted by the
// first accept.
func TestDocAcceptPlanReAcceptMintsOnlyNewDeclarations(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "remint-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if len(minted) != 3 {
		t.Fatalf("first accept minted %d tasks, want 3", len(minted))
	}
	// Execution has started on task 1: a re-accept must not walk it back.
	if err := transition(t, s, taskTestNow, minted[0].ID, "draft", "ready"); err != nil {
		t.Fatalf("transition %s to ready: %v", minted[0].ID, err)
	}
	before := map[string]taskSnapshot{}
	for _, task := range minted {
		before[task.ID] = snapshotTask(t, s, task.ID)
	}

	if _, err := updateDocBody(t, s, doc.ID, planMintBodyFourth); err != nil {
		t.Fatalf("UpdateDocBody on the accepted plan: %v", err)
	}

	accepted, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("status = %q, want accepted", accepted.Status)
	}
	if len(again) != 1 {
		t.Fatalf("re-accept minted %d tasks, want 1", len(again))
	}
	if again[0].Title != "Fourth task" || again[0].Kind != "chore" || again[0].Priority != "high" {
		t.Errorf("minted %+v, want the fourth declaration", again[0])
	}
	if got := countTasksWithPlanDoc(t, s, doc.ID); got != 4 {
		t.Errorf("tasks with plan_doc = %d, want 4", got)
	}

	for id, want := range before {
		if got := snapshotTask(t, s, id); got != want {
			t.Errorf("task %s = %+v after the re-accept, want it untouched (%+v)", id, got, want)
		}
	}

	// The new task's blockedBy names a task the first accept minted, so the
	// edge crosses the two accepts.
	var edgeType string
	err = s.db.QueryRow(`SELECT type FROM task_edges WHERE from_task = $1 AND to_task = $2`,
		minted[0].ID, again[0].ID).Scan(&edgeType)
	if err != nil {
		t.Fatalf("read edge %s -> %s: %v", minted[0].ID, again[0].ID, err)
	}
	if edgeType != "blocks" {
		t.Errorf("edge type = %q, want blocks", edgeType)
	}
}

// TestDocAcceptPlanReAcceptWithoutNewDeclarationsIsNoOp: an edit that adds no
// declaration — prose rewritten under an existing one — leaves the re-accept
// with nothing to mint, and that is a success rather than an error (025 §9.2).
func TestDocAcceptPlanReAcceptWithoutNewDeclarationsIsNoOp(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "noop-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	before := map[string]taskSnapshot{}
	for _, task := range minted {
		before[task.ID] = snapshotTask(t, s, task.ID)
	}

	edited := strings.Replace(planMintBody, "Do the first thing.", "Do the first thing, carefully.", 1)
	if _, err := updateDocBody(t, s, doc.ID, edited); err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}

	_, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("re-accept minted %d tasks, want none", len(again))
	}
	if got := countTasksWithPlanDoc(t, s, doc.ID); got != len(minted) {
		t.Errorf("tasks with plan_doc = %d, want unchanged %d", got, len(minted))
	}
	for id, want := range before {
		if got := snapshotTask(t, s, id); got != want {
			t.Errorf("task %s = %+v after the no-op re-accept, want it untouched (%+v)", id, got, want)
		}
	}
}

// TestDocAcceptPlanReAcceptNeverRemintsDeletedTask: a soft-deleted task keeps
// its declaration's key, so the withdrawal survives the next re-accept
// (025 §9.2 — withdrawing work is a task transition, and re-acceptance leaves
// existing rows alone).
func TestDocAcceptPlanReAcceptNeverRemintsDeletedTask(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "withdrawn-plan", Body: planMintBody, CreatedBy: "stig",
	})

	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	if err := deleteTask(t, s, minted[2].ID, "stig", "not needed after all"); err != nil {
		t.Fatalf("delete %s: %v", minted[2].ID, err)
	}

	_, again, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("re-accept minted %d tasks, want none: the declaration still has its row", len(again))
	}
}

// TestDocUpdateBodyMintedPlanRequiresReadableTasks: once a plan has minted
// tasks, a body edit that leaves its ## Tasks section unreadable is refused at
// the write rather than surfacing as drift at the next accept (025 §9.2). The
// body does not move.
func TestDocUpdateBodyMintedPlanRequiresReadableTasks(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "drift-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	for name, body := range map[string]string{
		"section removed":  "---\nstatus: accepted\n---\n\n# A plan\n\nNo tasks here.\n",
		"kindless fence":   strings.Replace(planMintBody, "kind: feature\n", "", 1),
		"duplicate titles": strings.Replace(planMintBody, "Task 3 — Third task", "Task 3 — First task", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := updateDocBody(t, s, doc.ID, body)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			got, err := s.GetDoc(t.Context(), doc.ID)
			if err != nil {
				t.Fatalf("GetDoc: %v", err)
			}
			if got.Body != planMintBody {
				t.Errorf("body moved to %q, want the refused edit not to land", got.Body)
			}
		})
	}
}

// TestDocUpdateBodyUnmintedPlanTasksUnchecked: the readable-## Tasks rule
// binds only once something is minted. A draft plan is written a paragraph at
// a time, and an accepted plan that minted nothing is §9.2's historical
// import; neither has a task set to drift from.
func TestDocUpdateBodyUnmintedPlanTasksUnchecked(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	draft := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "draft-plan", Body: planMintBody, CreatedBy: "stig",
	})
	half := "---\nstatus: draft\n---\n\n# A plan\n\nStill thinking.\n"
	if _, err := updateDocBody(t, s, draft.ID, half); err != nil {
		t.Fatalf("UpdateDocBody on an unaccepted plan: %v", err)
	}

	imported := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "imported-plan", Body: planBody,
		CreatedBy: "stig", Status: "accepted",
	})
	if _, err := updateDocBody(t, s, imported.ID, half); err != nil {
		t.Fatalf("UpdateDocBody on an accepted plan that minted nothing: %v", err)
	}
}

// TestDocUpdateBodyPlanBumpsVersion: a plan is edited in place rather than
// revised (025 §9), so its body edit is the next version of the document —
// which is what lets the acceptance event of §15.3, keyed on IRI and version,
// tell a re-accept after an edit from a retry of the same accept.
func TestDocUpdateBodyPlanBumpsVersion(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "versioned-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if doc.Version != 1 {
		t.Fatalf("version = %d, want 1", doc.Version)
	}
	updated, err := updateDocBody(t, s, doc.ID,
		strings.Replace(planMintBody, "Do the first thing.", "Do it now.", 1))
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}

	// A draft spec's body edit is not a publication, so its version holds.
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 91, Slug: "091-x", Body: specBody, CreatedBy: "stig",
	})
	editedSpec, err := updateDocBody(t, s, spec.ID, specBody+"\nmore\n")
	if err != nil {
		t.Fatalf("UpdateDocBody on a draft spec: %v", err)
	}
	if editedSpec.Version != 1 {
		t.Errorf("spec version = %d, want 1", editedSpec.Version)
	}
}

// TestPlanTaskKeyBackfillDisambiguatesDuplicateTitles: 0043 backfills
// plan_task_key from tasks.title, and nothing before it stopped two
// declarations in one plan from sharing a title. The earliest row keeps the
// title — the key a re-accept then matches that declaration to — and the rest
// are disambiguated, so the partial unique index can be created and no
// existing row is lost.
func TestPlanTaskKeyBackfillDisambiguatesDuplicateTitles(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 42)); err != nil {
		t.Fatalf("migrate through 0042: %v", err)
	}
	db := s.DBForTests()
	if _, err := db.Exec(`INSERT INTO projects (id, name, key) VALUES ('p1','P1','P1')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var planID int64
	if err := db.QueryRow(
		`INSERT INTO docs (project_id, kind, slug, title, body, created_at, updated_at)
		 VALUES ('p1', 'plan', 'dup-plan', 'Dup plan', 'body', now(), now()) RETURNING id`,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan doc: %v", err)
	}
	// Inserted directly: the store is held at 0042, where tasks has no
	// plan_task_key column for CreateTask to write.
	for i, id := range []string{"P1-1", "P1-2", "P1-3"} {
		created := taskTestNow.Add(time.Duration(i) * time.Second)
		title := "Same title"
		if id == "P1-3" {
			title = "Its own title"
		}
		if _, err := db.Exec(
			`INSERT INTO tasks (id, project_id, title, body, priority, kind, state,
			                    created_at, updated_at, plan_doc)
			 VALUES ($1, 'p1', $2, 'b', 'medium', 'feature', 'draft', $3, $3, $4)`,
			id, title, created, planID); err != nil {
			t.Fatalf("seed task %s: %v", id, err)
		}
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	keys := map[string]string{}
	rows, err := db.Query(`SELECT id, plan_task_key FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("read keys: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			t.Fatalf("scan key: %v", err)
		}
		keys[id] = key
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read keys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("kept %d tasks, want all 3", len(keys))
	}
	if keys["P1-1"] != "Same title" {
		t.Errorf("earliest duplicate key = %q, want the title itself", keys["P1-1"])
	}
	if keys["P1-2"] == "Same title" || keys["P1-2"] == "" {
		t.Errorf("later duplicate key = %q, want it disambiguated", keys["P1-2"])
	}
	if keys["P1-3"] != "Its own title" {
		t.Errorf("unique title key = %q, want the title itself", keys["P1-3"])
	}
}

// TestDocAcceptPlanParseFailureRefusesAndStaysDraft: a plan whose body fails
// designdoc.PlanTasks refuses to accept, wrapped as ErrInvalidInput, and its
// status stays draft — the status flip and the mint never run.
func TestDocAcceptPlanParseFailureRefusesAndStaysDraft(t *testing.T) {
	t.Parallel()
	frontmatter := "---\nstatus: draft\n---\n\n# A plan\n\n"
	cases := map[string]string{
		"no tasks section": frontmatter + "No tasks here.\n",
		"dangling blockedBy": frontmatter + `## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: feature
blockedBy: [2]
` + "```" + `

Do it.
`,
		"cyclic blockedBy": frontmatter + `## Tasks

### Task 1 — First

` + "```yaml" + `
kind: feature
blockedBy: [2]
` + "```" + `

a

### Task 2 — Second

` + "```yaml" + `
kind: feature
blockedBy: [1]
` + "```" + `

b
`,
		"missing kind": frontmatter + `## Tasks

### Task 1 — Only task

` + "```yaml" + `
priority: medium
` + "```" + `

Do it.
`,
		"unmintable kind": frontmatter + `## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: review
` + "```" + `

Do it.
`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := openDocStore(t)
			doc := mustCreateDoc(t, s, DocInput{
				Project: "p1", Kind: "plan", Slug: "bad-plan", Body: body, CreatedBy: "stig",
			})

			_, minted, err := acceptDoc(t, s, doc.ID, "stig")
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if minted != nil {
				t.Errorf("minted = %v, want nil", minted)
			}
			got, err := s.GetDoc(t.Context(), doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "draft" {
				t.Errorf("status = %q, want draft", got.Status)
			}
			if n := countTasksWithPlanDoc(t, s, doc.ID); n != 0 {
				t.Errorf("tasks with plan_doc after refused accept = %d, want 0", n)
			}
		})
	}
}

// TestDocAcceptPlanWrongActorForbidden: acceptance is the owner's act,
// exactly as for a spec (025 §7); a forbidden accept mints nothing.
func TestDocAcceptPlanWrongActorForbidden(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "gated-plan", Body: planMintBody, CreatedBy: "stig",
	})

	if _, _, err := acceptDoc(t, s, doc.ID, "ada"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if got, err := s.GetDoc(t.Context(), doc.ID); err != nil || got.Status != "draft" {
		t.Fatalf("doc = %+v, %v; want it still draft", got, err)
	}
	if n := countTasksWithPlanDoc(t, s, doc.ID); n != 0 {
		t.Errorf("tasks with plan_doc after forbidden accept = %d, want 0", n)
	}
}

// TestDocPlanTasksMintedMetric: RecordPlanTasksMinted adds n to
// worklode_doc_plan_tasks_minted_total. AcceptDoc itself does not call it —
// it is a package-level function with no *Store — so the API handler calls
// it after the accepting transaction commits; this exercises that method
// directly, the pattern TestDocOperationsMetric above uses for docOp.
func TestDocPlanTasksMintedMetric(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	s.RecordPlanTasksMinted(3)
	if got := testutil.ToFloat64(s.metrics.docTasksMinted); got != 3 {
		t.Fatalf("worklode_doc_plan_tasks_minted_total = %v, want 3", got)
	}
	s.RecordPlanTasksMinted(2)
	if got := testutil.ToFloat64(s.metrics.docTasksMinted); got != 5 {
		t.Fatalf("worklode_doc_plan_tasks_minted_total = %v, want 5", got)
	}
}

// TestDocPlanTasksMintedMetricNilSafe: a store opened without WithMetrics
// records nothing.
func TestDocPlanTasksMintedMetricNilSafe(t *testing.T) {
	t.Parallel()
	var m *storeMetrics
	m.planTasksMinted(3)
}

// TestDocNeedsPlanningReportsUncoveredSections: an accepted spec is listed
// with exactly the anchors no accepted plan's covers edge names, in document
// order, alongside its total section count.
func TestDocNeedsPlanningReportsUncoveredSections(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "025-x#sec-1")

	slugs, gaps := needsPlanningSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"025-x"}) {
		t.Fatalf("needs planning = %v, want [025-x]", slugs)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	got := gaps[0]
	if got.Doc != spec.ID {
		t.Errorf("gap doc = %d, want %d", got.Doc, spec.ID)
	}
	if got.Sections != 3 {
		t.Errorf("gap sections = %d, want 3", got.Sections)
	}
	if !slices.Equal(gapAnchors(got), []string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Errorf("gaps = %v, want [sec-2(unplanned) sec-2.1(unplanned)]", gapAnchors(got))
	}
}

// TestDocNeedsPlanningFullyCoveredSpecOmitted: every section named by some
// accepted plan takes the spec out of the set, and two plans naming the same
// section is legal and unremarked (026 §2.1).
func TestDocNeedsPlanningFullyCoveredSpecOmitted(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "025-x#sec-1", "025-x#sec-2")
	coveringPlan(t, s, "plan-b", true, "025-x#sec-2", "025-x#sec-2.1")

	slugs, gaps := needsPlanningSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("needs planning = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocNeedsPlanningDraftSpecNotOwedPlanning: 026 §2.1 — a draft spec is not
// yet a planning gap.
func TestDocNeedsPlanningDraftSpecNotOwedPlanning(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})

	slugs, _ := needsPlanningSlugs(t, s, "p1")
	if len(slugs) != 0 {
		t.Fatalf("needs planning = %v, want empty for a draft spec", slugs)
	}
}

// TestDocNeedsPlanningDraftPlanDoesNotCover: 026 §2.1 — a draft plan has not
// yet undertaken work, so its covers edges discharge nothing.
func TestDocNeedsPlanningDraftPlanDoesNotCover(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", false, "025-x#sec-1", "025-x#sec-2", "025-x#sec-2.1")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want every anchor unplanned", gaps)
	}
}

// TestDocNeedsPlanningWholeDocumentEdgeCoversNothing: a covers edge with no
// fragment names no section, so it discharges none (026 §2.1 — it cannot say
// which present section it undertakes and would silently claim future ones).
func TestDocNeedsPlanningWholeDocumentEdgeCoversNothing(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "025-x")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want every anchor unplanned", gaps)
	}
}

// TestDocNeedsPlanningNoSpecSentinelCoversNothing: `covers: NO-SPEC` resolves
// to no document (026 §4.3), so it lands in to_external and contributes
// nothing — no special case needed. The plan itself is never a planning gap.
func TestDocNeedsPlanningNoSpecSentinelCoversNothing(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	coveringPlan(t, s, "plan-a", true, "NO-SPEC")

	slugs, gaps := needsPlanningSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"025-x"}) {
		t.Fatalf("needs planning = %v, want the spec alone", slugs)
	}
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want every anchor unplanned", gaps)
	}
}

// TestDocNeedsPlanningScopesToProject: an empty project answers over every
// project; a named one narrows to it.
func TestDocNeedsPlanningScopesToProject(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustAcceptedSpec(t, s, "025-x")
	other := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 25, Slug: "025-y", Body: specBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, other.ID, "stig"); err != nil {
		t.Fatalf("accept p2 spec: %v", err)
	}

	all, _ := needsPlanningSlugs(t, s, "")
	if !slices.Equal(all, []string{"025-x", "025-y"}) {
		t.Fatalf("unscoped needs planning = %v, want both specs", all)
	}
	scoped, _ := needsPlanningSlugs(t, s, "p2")
	if !slices.Equal(scoped, []string{"025-y"}) {
		t.Fatalf("p2 needs planning = %v, want [025-y]", scoped)
	}
}

// --- NeedsPlanning three-valued coverage (026 §2.1's outcome table) --------

// TestDocNeedsPlanningFullCoverageDischarges: an accepted plan claiming a
// section `full` discharges it; the sections no plan names stay unplanned.
func TestDocNeedsPlanningFullCoverageDischarges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "full"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged, sec-2/sec-2.1 unplanned", gaps)
	}
}

// TestDocNeedsPlanningPartialWithNoClosureIsPartialGap: a `partial` claim
// with no fullCoverageWith set closes nothing, so the section stays a
// "partial" gap (026 §2.1).
func TestDocNeedsPlanningPartialWithNoClosureIsPartialGap(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "partial"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial", gaps)
	}
}

// TestDocNeedsPlanningPartialClosedByFullSiblingDischarges: fullCoverageWith
// naming an accepted plan that itself covers the same section `full` closes
// the claim, discharging the section (026 §2.1).
func TestDocNeedsPlanningPartialClosedByFullSiblingDischarges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-1", level: "full"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged via fullCoverageWith", gaps)
	}
}

// TestDocNeedsPlanningPartialClosedByPartialSiblingDischarges: a
// fullCoverageWith sibling that itself only contributes `partial` still
// closes the claim (026 §2.1 asks only that it "contribute full or partial",
// not that its own claim be closed).
func TestDocNeedsPlanningPartialClosedByPartialSiblingDischarges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-1", level: "partial"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged even though the sibling is only partial", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresDraftSibling: fullCoverageWith is
// checked, never taken on trust — a draft sibling closes nothing (026 §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresDraftSibling(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", false, coverageRef{ref: "025-x#sec-1", level: "full"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: a draft sibling closes nothing", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureRequiresEveryNamedSibling: 026 §2.1's
// closure test is universal over the named plans, not existential — one
// qualifying sibling does not close the claim when a second sibling, named
// alongside it, is still a draft.
func TestDocNeedsPlanningPartialClosureRequiresEveryNamedSibling(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	accepted := levelledPlan(t, s, "plan-sibling-accepted", true,
		coverageRef{ref: "025-x#sec-1", level: "partial"})
	draft := levelledPlan(t, s, "plan-sibling-draft", false,
		coverageRef{ref: "025-x#sec-1", level: "partial"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial",
		fullCoverageWith: []string{accepted.Slug, draft.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: a draft among two named siblings blocks closure", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresNoneSibling: a fullCoverageWith
// sibling that itself claims `none` contributes nothing to the closure (026
// §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresNoneSibling(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-1", level: "none"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: a none sibling closes nothing", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresSiblingCoveringDifferentSection:
// fullCoverageWith is scoped to the same section — a sibling that covers a
// different one of the spec's sections closes nothing for this one, even
// though it discharges its own (026 §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresSiblingCoveringDifferentSection(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	sibling := levelledPlan(t, s, "plan-sibling", true, coverageRef{ref: "025-x#sec-2", level: "full"})
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{sibling.Slug},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial and sec-2 discharged on its own merits", gaps)
	}
}

// TestDocNeedsPlanningPartialClosureIgnoresUnresolvableReference: a
// fullCoverageWith entry this project cannot resolve is, by definition,
// unresolvable and closes nothing (026 §2.1).
func TestDocNeedsPlanningPartialClosureIgnoresUnresolvableReference(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-main", true, coverageRef{
		ref: "025-x#sec-1", level: "partial", fullCoverageWith: []string{"nowhere-plan"},
	})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: an unresolvable reference closes nothing", gaps)
	}
}

// TestDocNeedsPlanningNoneOnlyIsBoundOnlyGap: a section every accepted plan
// naming it claims `none` for is "bound-only" — acknowledged but not planned
// (026 §2.1).
func TestDocNeedsPlanningNoneOnlyIsBoundOnlyGap(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "none"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(bound-only)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 bound-only", gaps)
	}
}

// TestDocNeedsPlanningPartialByOneFullByAnotherDischarges: one plan's
// `partial` claim and another's `full` claim on the same section together
// discharge it (026 §2.1's outcome table).
func TestDocNeedsPlanningPartialByOneFullByAnotherDischarges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "partial"})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "full"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged by the full claim", gaps)
	}
}

// TestDocNeedsPlanningNoneByOneAndPartialByAnotherIsPartialGap: `partial`
// dominates `none` — one plan claiming `none` does not demote a section
// another plan claims `partial` down to "bound-only" (026 §2.1).
func TestDocNeedsPlanningNoneByOneAndPartialByAnotherIsPartialGap(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "none"})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "partial"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: partial dominates bound-only", gaps)
	}
}

// TestDocNeedsPlanningSupersededPlanDischarges: 026 §2.1's amended discharging
// set is "accepted or superseded" — a superseded plan is one that was
// accepted and then carried out (025 §9), so its full claim still discharges
// the section it covered.
func TestDocNeedsPlanningSupersededPlanDischarges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	plan := levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "full"})
	setDocStatus(t, s, plan.ID, "superseded")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged: a superseded plan discharges what it covered", gaps)
	}
}

// --- NeedsPlanning defers classification (026 §2.1, §5.3) -----------------

// TestDocNeedsPlanningDeferredSectionReportsOwner: an accepted plan's defers
// entry reports the section "deferred" with its owner's slug; a section no
// plan names at all stays "unplanned" (026 §2.1, §5.3).
func TestDocNeedsPlanningDeferredSectionReportsOwner(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-spec)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred to owner-spec", gaps)
	}
}

// TestDocNeedsPlanningDeferredOutranksBoundOnly: with one plan bound by a
// section (`none`) and another deferring it, the section reports deferred —
// a deferral says who is owed the rest, not merely that the section was read
// (026 §2.1's precedence: partial > deferred > bound-only > unplanned).
func TestDocNeedsPlanningDeferredOutranksBoundOnly(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	levelledPlan(t, s, "plan-a", true, coverageRef{ref: "025-x#sec-1", level: "none"})
	deferringPlan(t, s, "plan-b", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-spec)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred: deferred outranks bound-only", gaps)
	}
}

// TestDocNeedsPlanningTwoDeferralOwnersAggregated: §5.3's one-owner rule is
// per plan, so two plans may defer one section to two owners. The report
// aggregates them deterministically, comma-joined without a space — the CLI
// joins anchors with spaces, so a spaced separator would split the token.
func TestDocNeedsPlanningTwoDeferralOwnersAggregated(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-a", Body: specBody, CreatedBy: "stig",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 7, Slug: "owner-b", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-a"}})
	deferringPlan(t, s, "plan-b", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-b"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-a,owner-b)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred to owner-a,owner-b", gaps)
	}
}

// TestDocNeedsPlanningDeferralDeliveredByCoveringPlan: a deferral is
// delivered by any plan discharging the section, not only by the named owner
// — once a second accepted plan covers the section `full`, it disappears
// from the gaps the same as any other discharged section (026 §2.1).
func TestDocNeedsPlanningDeferralDeliveredByCoveringPlan(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "full"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 discharged: a deferral is delivered by any covering plan", gaps)
	}
}

// TestDocNeedsPlanningPartialWithDeferralReportsPartial: a section claimed
// `partial` by one plan and deferred by another reports "partial" — partial
// outranks deferred in 026 §2.1's precedence.
func TestDocNeedsPlanningPartialWithDeferralReportsPartial(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})
	levelledPlan(t, s, "plan-b", true, coverageRef{ref: "025-x#sec-1", level: "partial"})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(partial)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 partial: partial outranks deferred", gaps)
	}
}

// TestDocNeedsPlanningDraftPlanDeferralIgnored: a draft plan has not yet
// undertaken work, so its defers entries classify nothing (026 §2.1).
func TestDocNeedsPlanningDraftPlanDeferralIgnored(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	deferringPlan(t, s, "plan-a", false, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(unplanned)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 unplanned: a draft plan's deferral does not count", gaps)
	}
}

// TestDocNeedsPlanningSupersededPlanDeferralCounts: the deferring set is
// accepted or superseded, never draft (026 §5.3) — a superseded plan's
// deferral stands, being spent does not deliver a handoff the plan never
// made.
func TestDocNeedsPlanningSupersededPlanDeferralCounts(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "owner-spec", Body: specBody, CreatedBy: "stig",
	})
	plan := deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "owner-spec"}})
	setDocStatus(t, s, plan.ID, "superseded")

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:owner-spec)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 still deferred: a superseded plan's deferral stands", gaps)
	}
}

// TestDocNeedsPlanningDeferralOwnerExternalVerbatim: an owner reference this
// project cannot resolve is reported verbatim, the same fallback
// fullCoverageWith uses (026 §2.1, §5.3).
func TestDocNeedsPlanningDeferralOwnerExternalVerbatim(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	deferringPlan(t, s, "plan-a", true, []deferralRef{{spec: "025-x#sec-1", to: "999-nowhere-owner.md"}})

	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || !slices.Equal(gapAnchors(gaps[0]),
		[]string{"sec-1(deferred:999-nowhere-owner.md)", "sec-2(unplanned)", "sec-2.1(unplanned)"}) {
		t.Fatalf("gaps = %v, want sec-1 deferred to the unresolved reference verbatim", gaps)
	}
}

// TestDocNeedsExecutionOpenTask: an accepted plan with any non-closed task in
// its set is pending work.
func TestDocNeedsExecutionOpenTask(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	if got := needsExecutionSlugs(t, s, "p1"); !slices.Equal(got, []string{"mint-plan"}) {
		t.Fatalf("needs execution = %v, want [mint-plan]", got)
	}
}

// TestDocNeedsExecutionAllTasksClosed: once every task in the set is closed —
// delivered or abandoned, taskClosed's notion — the plan drops out.
func TestDocNeedsExecutionAllTasksClosed(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, doc.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	for i, task := range minted {
		if err := transition(t, s, taskTestNow, task.ID, "draft", "ready"); err != nil {
			t.Fatalf("ready %s: %v", task.ID, err)
		}
		if i == 0 {
			walkTo(t, s, task.ID, "abandoned")
			continue
		}
		walkTo(t, s, task.ID, "merged")
	}

	if got := needsExecutionSlugs(t, s, "p1"); len(got) != 0 {
		t.Fatalf("needs execution = %v, want empty once every task is closed", got)
	}
}

// TestDocNeedsExecutionDraftPlanOmitted: a draft plan has undertaken nothing.
func TestDocNeedsExecutionDraftPlanOmitted(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})

	if got := needsExecutionSlugs(t, s, "p1"); len(got) != 0 {
		t.Fatalf("needs execution = %v, want empty for a draft plan", got)
	}
}

// TestDocNeedsExecutionUnmintedAcceptedPlanOmitted: the only accepted plans
// with no task set are the importer's spent plans, which are not pending work
// — the deliberate departure from 025 §18's "unminted or unfinished".
func TestDocNeedsExecutionUnmintedAcceptedPlanOmitted(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "spent-plan", Status: "accepted", CreatedBy: "stig",
		Body: "---\nstatus: accepted\n---\n\n# A spent plan\n\nAll done long ago.\n",
	})

	if got := needsExecutionSlugs(t, s, "p1"); len(got) != 0 {
		t.Fatalf("needs execution = %v, want empty for an unminted accepted plan", got)
	}
}

// TestDocNeedsExecutionScopesToProjectAndKind: accepted specs never appear,
// and a project narrows the set.
func TestDocNeedsExecutionScopesToProjectAndKind(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "mint-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	if got := needsExecutionSlugs(t, s, ""); !slices.Equal(got, []string{"mint-plan"}) {
		t.Fatalf("needs execution = %v, want the plan alone", got)
	}
	if got := needsExecutionSlugs(t, s, "p2"); len(got) != 0 {
		t.Fatalf("needs execution in p2 = %v, want empty", got)
	}
}

// --- BareSupersededSections (025 §6 rule 2) ---------------------------------

// TestDocBareSupersededNoReplacesEdge: a superseded spec with no replaces
// edge naming it at all is reported in full, anchors in document order.
func TestDocBareSupersededNoReplacesEdge(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"006-old"}) {
		t.Fatalf("bare superseded = %v, want [006-old]", slugs)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	got := gaps[0]
	if got.Doc != old.ID {
		t.Errorf("gap doc = %d, want %d", got.Doc, old.ID)
	}
	if got.Sections != 3 {
		t.Errorf("gap sections = %d, want 3", got.Sections)
	}
	if !slices.Equal(got.Unexplained, []string{"sec-1", "sec-2", "sec-2.1"}) {
		t.Errorf("unexplained = %v, want every anchor", got.Unexplained)
	}
}

// TestDocBareSupersededDocumentLevelEdgeExplainsWholeDoc: a document-scoped
// replaces edge discharges every section of the document it names, even
// though the successor never names one of them by anchor.
func TestDocBareSupersededDocumentLevelEdgeExplainsWholeDoc(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocBareSupersededSectionEdgesExplainEverySection: a section-scoped
// replaces edge naming each of the superseded document's anchors leaves
// nothing unexplained.
func TestDocBareSupersededSectionEdgesExplainEverySection(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md#sec-1\n" +
			"    - 006-old.md#sec-2\n    - 006-old.md#sec-2.1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocBareSupersededSomeSectionsExplained: only the anchors no
// section-scoped edge names are reported; Sections stays the document's full
// count.
func TestDocBareSupersededSomeSectionsExplained(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md#sec-1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	_, gaps := bareSupersededSlugs(t, s, "p1")
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	if gaps[0].Sections != 3 {
		t.Errorf("gap sections = %d, want 3", gaps[0].Sections)
	}
	if !slices.Equal(gaps[0].Unexplained, []string{"sec-2", "sec-2.1"}) {
		t.Errorf("unexplained = %v, want [sec-2 sec-2.1]", gaps[0].Unexplained)
	}
}

// TestDocBareSupersededOnlySupersededDocsReported: an accepted and a draft
// document are never reported, whatever edges name them.
func TestDocBareSupersededOnlySupersededDocsReported(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-accepted", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 7, Slug: "007-draft", Body: specBody,
		CreatedBy: "stig",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty", slugs, gaps)
	}
}

// TestDocBareSupersededExternalEdgeDoesNotExplain: a replaces reference that
// resolves to no row in this project lands in to_external and explains
// nothing — the superseded document it would have named is still reported in
// full.
func TestDocBareSupersededExternalEdgeDoesNotExplain(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 999-nowhere.md#sec-1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"006-old"}) {
		t.Fatalf("bare superseded = %v, want [006-old]", slugs)
	}
	if len(gaps) != 1 || !slices.Equal(gaps[0].Unexplained, []string{"sec-1", "sec-2", "sec-2.1"}) {
		t.Fatalf("gaps = %v, want every anchor unexplained", gaps)
	}
}

// TestDocBareSupersededScopesToProject: an empty project answers over every
// project; a named one narrows to it.
func TestDocBareSupersededScopesToProject(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 6, Slug: "006-old-2", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})

	all, _ := bareSupersededSlugs(t, s, "")
	if !slices.Equal(all, []string{"006-old", "006-old-2"}) {
		t.Fatalf("unscoped bare superseded = %v, want both docs", all)
	}
	scoped, _ := bareSupersededSlugs(t, s, "p2")
	if !slices.Equal(scoped, []string{"006-old-2"}) {
		t.Fatalf("p2 bare superseded = %v, want [006-old-2]", scoped)
	}
}

// TestDocBareSupersededPlanNeverReported: a plan carries no sections (025
// §9), so it can never appear here even when superseded.
func TestDocBareSupersededPlanNeverReported(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "old-plan", CreatedBy: "stig", Status: "superseded",
		Body: "---\nstatus: superseded\n---\n\n# An old plan\n\nSpent.\n",
	})

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if len(slugs) != 0 || len(gaps) != 0 {
		t.Fatalf("bare superseded = %v / %v, want empty for a superseded plan", slugs, gaps)
	}
}

// TestDocBareSupersededKindNarrows: kind narrows the same way project does —
// "" answers both a superseded spec and a superseded ADR, "spec" or "adr"
// answers only its own.
func TestDocBareSupersededKindNarrows(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old-spec", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 7, Slug: "007-old-adr", Body: specBody,
		CreatedBy: "stig", Status: "superseded",
	})

	if got, _ := bareSupersededKindSlugs(t, s, "p1", ""); !slices.Equal(got, []string{"006-old-spec", "007-old-adr"}) {
		t.Fatalf("unscoped bare superseded = %v, want both docs", got)
	}
	if got, _ := bareSupersededKindSlugs(t, s, "p1", "spec"); !slices.Equal(got, []string{"006-old-spec"}) {
		t.Fatalf("spec-scoped bare superseded = %v, want [006-old-spec]", got)
	}
	if got, _ := bareSupersededKindSlugs(t, s, "p1", "adr"); !slices.Equal(got, []string{"007-old-adr"}) {
		t.Fatalf("adr-scoped bare superseded = %v, want [007-old-adr]", got)
	}
}

// TestDocBareSupersededViaAcceptPath: supersedeReplacedDocs flips a target on
// from_anchor IS NULL (a document-level source), not on to_anchor — so a
// document-level source naming a section-scoped target flips the whole
// target document to superseded while its `replaces` edge only names one of
// its sections. That leaves the other two sections bare, reachable through
// the real accept path rather than a fixture that sets Status directly.
func TestDocBareSupersededViaAcceptPath(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	successor := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md#sec-1\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})

	if _, _, err := acceptDoc(t, s, successor.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc(025-new): %v", err)
	}

	got, err := s.GetDoc(t.Context(), old.ID)
	if err != nil {
		t.Fatalf("GetDoc(006-old): %v", err)
	}
	if got.Status != "superseded" {
		t.Fatalf("006-old status = %q, want superseded", got.Status)
	}

	slugs, gaps := bareSupersededSlugs(t, s, "p1")
	if !slices.Equal(slugs, []string{"006-old"}) {
		t.Fatalf("bare superseded = %v, want [006-old]", slugs)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want one entry", gaps)
	}
	if gaps[0].Sections != 3 {
		t.Errorf("gap sections = %d, want 3", gaps[0].Sections)
	}
	if !slices.Equal(gaps[0].Unexplained, []string{"sec-2", "sec-2.1"}) {
		t.Errorf("unexplained = %v, want [sec-2 sec-2.1]", gaps[0].Unexplained)
	}
}
