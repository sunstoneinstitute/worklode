package store

import (
	"database/sql"
	"testing"
)

// abandon transitions a task straight to abandoned from whatever state it
// currently holds, through RecordEvent — the closing move every test in this
// file uses to make a plan's last minted task close.
func abandon(t *testing.T, s *Store, taskID string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, eventID int64) error {
			from, err := TaskState(tx, taskID)
			if err != nil {
				return err
			}
			return Transition(tx, s.Now(), taskID, from, "abandoned", eventID)
		})
	if err != nil {
		t.Fatalf("abandon %s: %v", taskID, err)
	}
}

// setRuleStatus runs SetRuleStatus through RecordEvent, the way a caller
// (increment 4's split/merge lineage) will.
func setRuleStatus(t *testing.T, s *Store, ruleID int64, status string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "rule.status", nil,
		func(tx *sql.Tx, eventID int64) error {
			return SetRuleStatus(tx, s.Now(), ruleID, status, eventID)
		})
	return err
}

// TestPlanSpentWhenLastTaskCloses: closing the last open task minted by a
// plan flips the plan to spent and governs any ungoverned task from the
// plan's arrangement (S5, increment 3 R4).
func TestPlanSpentWhenLastTaskCloses(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	// Drop one task's governance by hand so the close has something to repair.
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM task_governed_by WHERE task_id = $1`, tasks[1].ID); err != nil {
		t.Fatal(err)
	}
	abandon(t, s, tasks[0].ID)
	if d, _ := s.GetDoc(t.Context(), plan.ID); d.Status != "accepted" {
		t.Fatalf("one task still open: plan should stay accepted, got %s", d.Status)
	}
	abandon(t, s, tasks[1].ID)
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "spent" {
		t.Fatalf("last task closed: plan should be spent, got %s %v", d.Status, err)
	}
	gov, err := s.GovernedBy(t.Context(), tasks[1].ID)
	if err != nil || len(gov) == 0 {
		t.Fatalf("closing the plan should govern its ungoverned task: %d %v", len(gov), err)
	}
}

// TestWithdrawPlanGovernsItsTasks: lode doc withdraw on a plan governs every
// ungoverned task from the arrangement before closing (S5).
func TestWithdrawPlanGovernsItsTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) == 0 {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM task_governed_by WHERE task_id = $1`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := withdrawDoc(t, s, plan.ID, "test"); err != nil {
		t.Fatal(err)
	}
	gov, err := s.GovernedBy(t.Context(), tasks[0].ID)
	if err != nil || len(gov) == 0 {
		t.Fatalf("withdrawn plan's task should be governed: %d %v", len(gov), err)
	}
}

// TestWithdrawnRuleMakesPlansStale: SetRuleStatus withdrawn marks every
// accepted plan arranging the rule stale (S23).
func TestWithdrawnRuleMakesPlansStale(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	id := ruleID(t, s, "P1", 1)
	if err := setRuleStatus(t, s, id, "withdrawn"); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "stale" {
		t.Fatalf("plan arranging a withdrawn rule should be stale, got %s %v", d.Status, err)
	}
	// The doc.stale event this path records carries a distinct cause from
	// the 025 §8.6 amend path (TestPatchMarksUnexecutedPlansStale pins
	// "amended" there) and names the withdrawn rule, not a spec slug.
	evs := staleEvents(t, s, StaleExternalID(d.Slug, d.Version))
	if len(evs) != 1 {
		t.Fatalf("doc.stale events = %d, want 1", len(evs))
	}
	if evs[0]["cause"] != "rule_withdrawn" || evs[0]["spec"] != "P1-RULE-1" {
		t.Errorf("payload = %v, want cause rule_withdrawn on P1-RULE-1", evs[0])
	}
	if err := setRuleStatus(t, s, id, "nonsense"); err == nil {
		t.Fatal("unknown status should be refused")
	}
}

// TestListDocsHidesTerminalPlans: withdrawn and spent plans, and withdrawn or
// superseded documents of other kinds, are hidden only when HideTerminal asks
// for it and no Status is named (S5, R6). The zero
// filter sees them, because callers such as the corpus importer's slug lookup
// need the whole corpus (final review C1).
func TestListDocsHidesTerminalPlans(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := withdrawDoc(t, s, plan.ID, "test"); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan"})
	hidden, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan", HideTerminal: true})
	byStatus, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan", Status: "withdrawn", HideTerminal: true})
	if len(all) != 1 || len(hidden) != 0 || len(byStatus) != 1 {
		t.Fatalf("zero filter %d (want 1), hidden %d (want 0), by status %d (want 1)", len(all), len(hidden), len(byStatus))
	}
	adr := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "adr", Slug: "old", Body: ruleDocV1, CreatedBy: "stig"})
	if _, err := s.db.ExecContext(t.Context(), `UPDATE docs SET status = 'superseded' WHERE id = $1`, adr.ID); err != nil {
		t.Fatal(err)
	}
	if adrs, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "adr", HideTerminal: true}); len(adrs) != 0 {
		t.Fatalf("superseded ADR listed with HideTerminal: %d docs, want 0", len(adrs))
	}

	// The terminal-plan hide is a default-listing rule, not a Deleted one: a
	// tombstoned plan that was withdrawn before deletion must still show up
	// under --deleted (044 §5's listing has no terminal-status concept of
	// its own to apply).
	if err := deleteDoc(t, s, plan.ID, "stig", "cleanup"); err != nil {
		t.Fatal(err)
	}
	deletedList, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan", Deleted: true, HideTerminal: true})
	if len(deletedList) != 1 {
		t.Fatalf("deleted-plan listing = %d, want 1 (a withdrawn plan hidden after deletion)", len(deletedList))
	}
}

// TestDeleteLastInProgressTaskLeavesPlanAccepted: DeleteTask tombstones a
// task before rolling it off in_progress, so settlePlan sees it as already
// gone. Deleting the plan's only remaining live task must not spend the
// plan — nothing was delivered (C1 fix review, increment 3).
func TestDeleteLastInProgressTaskLeavesPlanAccepted(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	// Remove one minted task outright, while it is still ready (no
	// transitionKnown call, so no settlePlan run) — leaving the other as
	// the plan's only live task.
	if err := deleteTask(t, s, tasks[1].ID, "stig", "not needed"); err != nil {
		t.Fatal(err)
	}
	if err := transition(t, s, s.Now(), tasks[0].ID, "ready", "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := deleteTask(t, s, tasks[0].ID, "stig", "superseded"); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "accepted" {
		t.Fatalf("plan with zero live minted tasks should stay accepted, got %s %v", d.Status, err)
	}
}

// TestPlanNeverSpentWithNoLiveTasks: a plan whose every minted task was
// soft-deleted never becomes spent, even when both deletes run through the
// in_progress rollup that calls settlePlan (C1 fix review, increment 3).
func TestPlanNeverSpentWithNoLiveTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	for _, task := range tasks {
		if err := transition(t, s, s.Now(), task.ID, "ready", "in_progress"); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range tasks {
		if err := deleteTask(t, s, task.ID, "stig", "cancelled"); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "accepted" {
		t.Fatalf("plan with every task deleted should stay accepted, not spent, got %s %v", d.Status, err)
	}
}

// TestSpentPlanStillCounts: a spent plan is a finished accepted plan. Its
// spec's covered section stays planned, the spec stays executed, and the
// §8.7 sweeper does not pick the plan up (final review C2, increment 3).
func TestSpentPlanStillCounts(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	for _, task := range tasks {
		abandon(t, s, task.ID)
	}
	if d, _ := s.GetDoc(t.Context(), plan.ID); d.Status != "spent" {
		t.Fatalf("plan status = %s, want spent", d.Status)
	}

	_, gaps, err := s.NeedsPlanning(t.Context(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gaps {
		for _, sec := range g.Gaps {
			if g.Doc == spec.ID && sec.Anchor == "sec-1" {
				t.Errorf("sec-1 covered by a spent plan reported as needing planning: %+v", g)
			}
		}
	}

	cands, err := s.StaleCandidateDocs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := candidateByID(cands, plan.ID); ok {
		t.Errorf("spent plan %d is a stale candidate", plan.ID)
	}
	specC, ok := candidateByID(cands, spec.ID)
	if !ok || !specC.HasExecution {
		t.Errorf("spec covered by a spent plan: candidate %v, HasExecution %v, want true", ok, specC.HasExecution)
	}
}

// TestDeleteLastOpenReadyTaskSpendsPlan: deleting a plan's last open task
// while it is ready still settles the plan, because DeleteTask runs
// settlePlan itself rather than only through the in_progress rollback
// (final review M1, increment 3).
func TestDeleteLastOpenReadyTaskSpendsPlan(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	abandon(t, s, tasks[0].ID)
	if err := deleteTask(t, s, tasks[1].ID, "stig", "not needed"); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "spent" {
		t.Fatalf("last open task deleted while ready: plan should be spent, got %s %v", d.Status, err)
	}
}
