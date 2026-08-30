package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestBrief(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	// Claim before adding blockers: Claim refuses a blocked task, so the
	// lease has to be acquired first, then the blocking edge added.
	lease, err := s.Claim(ctx, task.ID, "stig", "host:/wt-1", 0)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	openBlocker := createTask(t, s, leaseTestNow, defaultTaskInput()) // stays ready = open
	if err := addEdge(t, s, openBlocker.ID, task.ID, "blocks"); err != nil {
		t.Fatalf("add open blocks edge: %v", err)
	}
	// Blockers in a closed state (see taskClosed) no longer block, so they
	// must not appear in the brief -- merged and everything past it.
	for _, state := range []string{"merged", "deployed_dev", "deployed_prod", "released"} {
		closedBlocker := createTask(t, s, leaseTestNow, defaultTaskInput())
		walkTo(t, s, closedBlocker.ID, state)
		if err := addEdge(t, s, closedBlocker.ID, task.ID, "blocks"); err != nil {
			t.Fatalf("add %s blocks edge: %v", state, err)
		}
	}

	b, err := s.Brief(ctx, task.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if b.Task.ID != task.ID || b.Body != task.Body {
		t.Fatalf("brief task = %+v, want id=%s body=%q", b.Task, task.ID, task.Body)
	}
	if want := task.ID + "-a-task"; b.Branch != want {
		t.Fatalf("branch = %q, want %q", b.Branch, want)
	}
	if len(b.OpenBlockers) != 1 || b.OpenBlockers[0].ID != openBlocker.ID {
		t.Fatalf("open blockers = %+v, want just %s", b.OpenBlockers, openBlocker.ID)
	}
	if b.OpenBlockers[0].State != "ready" || b.OpenBlockers[0].Title == "" {
		t.Fatalf("open blocker projection = %+v, want state/title populated", b.OpenBlockers[0])
	}
	if b.Lease == nil || b.Lease.ID != lease.ID || b.Lease.Worktree != "host:/wt-1" {
		t.Fatalf("lease = %+v, want active lease on host:/wt-1", b.Lease)
	}
	if b.GoverningDesign != nil || b.AffectedComponents != nil || b.DefinitionOfDone != nil {
		t.Fatalf("reserved fields must stay nil in v1: %+v", b)
	}
}

func TestBriefNoBlockersNoLease(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	b, err := s.Brief(ctx, task.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.OpenBlockers) != 0 {
		t.Fatalf("open blockers = %+v, want none", b.OpenBlockers)
	}
	if b.Lease != nil {
		t.Fatalf("lease = %+v, want nil", b.Lease)
	}
}

// TestBriefOpenBlockersMultiCharKey regression-tests openBlockers' ordering
// query against a project whose key is not 2 chars. The bug: openBlockers
// ordered by CAST(substr(id, 4) AS INTEGER), which assumes a 3-char "WL-"
// prefix. For a 4-char key like "DEMO", substr("DEMO-2", 4) = "O-2", which
// is not valid integer input, so the query used to error (surfacing as a 500
// on the task-brief read path) instead of ordering blockers numerically.
func TestBriefOpenBlockersMultiCharKey(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "demo", "Demo", "DEMO"); err != nil {
		t.Fatalf("CreateProject demo: %v", err)
	}

	demoInput := defaultTaskInput()
	demoInput.ProjectID = "demo"

	target := createTask(t, s, leaseTestNow, demoInput)   // DEMO-1
	blocker1 := createTask(t, s, leaseTestNow, demoInput) // DEMO-2
	blocker2 := createTask(t, s, leaseTestNow, demoInput) // DEMO-3

	if err := addEdge(t, s, blocker1.ID, target.ID, "blocks"); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker1.ID, target.ID, err)
	}
	if err := addEdge(t, s, blocker2.ID, target.ID, "blocks"); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker2.ID, target.ID, err)
	}

	b, err := s.Brief(ctx, target.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.OpenBlockers) != 2 {
		t.Fatalf("open blockers = %+v, want 2", b.OpenBlockers)
	}
	if b.OpenBlockers[0].ID != "DEMO-2" || b.OpenBlockers[1].ID != "DEMO-3" {
		t.Fatalf("open blockers = [%s %s], want [DEMO-2 DEMO-3]",
			b.OpenBlockers[0].ID, b.OpenBlockers[1].ID)
	}
}

func TestBriefNotFound(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	if _, err := s.Brief(t.Context(), "HDB-999", BriefOptions{Skills: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Brief unknown task: err = %v, want ErrNotFound", err)
	}
}

// TestBriefResolvesPins pins a real skill and an unknown one: the real skill
// resolves with content, the unknown one produces a warning instead of
// failing the brief.
func TestBriefResolvesPins(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	in := defaultTaskInput()
	in.Skills = []string{"tdd", "tdd", "ghost"}
	task := createTask(t, s, leaseTestNow, in)

	b, err := s.Brief(ctx, task.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	// One entry, not two: CreateTask normalizes the pin list, and ResolvePins
	// dedupes again — a repeated pin must not inline the same body twice.
	if len(b.PinnedSkills) != 1 || b.PinnedSkills[0].Name != "tdd" || b.PinnedSkills[0].SkillMD == "" {
		t.Fatalf("pinned skills = %+v, want one tdd entry with SkillMD populated", b.PinnedSkills)
	}
	if len(b.SkillWarnings) != 1 || b.SkillWarnings[0] != "pinned skill not found: ghost" {
		t.Fatalf("skill warnings = %+v, want [pinned skill not found: ghost]", b.SkillWarnings)
	}
}

// TestResolvePinsDedupes covers ResolvePins' own dedupe, which the brief path
// can no longer reach now that both task write paths normalize: the API's
// recommend endpoint passes caller-supplied pins straight through.
func TestResolvePinsDedupes(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"tdd", "tdd", "ghost", "ghost"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 1 || pinned[0].Name != "tdd" {
		t.Fatalf("pinned = %+v, want one tdd entry", pinned)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one", warnings)
	}
}

// TestResolvePinsAfterColonFallback covers 025 §9.1's skill-identifier rule:
// a plugin-qualified pin against an unqualified registry name falls back to
// the segment after the colon.
func TestResolvePinsAfterColonFallback(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("test-driven-development", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"superpowers:test-driven-development"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 1 || pinned[0].Name != "test-driven-development" || pinned[0].SkillMD == "" {
		t.Fatalf("pinned = %+v, want test-driven-development resolved via fallback with content", pinned)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
}

// TestResolvePinsDedupesOnResolvedName: two spellings of one pin — bare and
// plugin-qualified — resolve to the same registry row through the after-colon
// fallback and must yield that skill once, not twice with its whole content
// duplicated in the brief.
func TestResolvePinsDedupesOnResolvedName(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"tdd", "superpowers:tdd"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 1 || pinned[0].Name != "tdd" {
		t.Fatalf("pinned = %+v, want one tdd entry", pinned)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none: a duplicate resolution is not an error", warnings)
	}
}

// TestResolvePinsDedupesTwoQualifiedPins: the same after-colon fallback maps
// two differently-qualified pins onto one registry row.
func TestResolvePinsDedupesTwoQualifiedPins(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("x", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"a:x", "b:x"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 1 || pinned[0].Name != "x" {
		t.Fatalf("pinned = %+v, want one x entry", pinned)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
}

// TestResolvePinsExactBeatsFallback: a pin that matches a qualified registry
// name exactly resolves to it directly and never consults the fallback.
func TestResolvePinsExactBeatsFallback(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("superpowers:writing-plans", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"superpowers:writing-plans"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 1 || pinned[0].Name != "superpowers:writing-plans" || pinned[0].SkillMD == "" {
		t.Fatalf("pinned = %+v, want superpowers:writing-plans resolved exactly", pinned)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
}

// TestResolvePinsUnknownBothWarns: a pin absent under both its own name and
// its after-colon suffix warns exactly as an unqualified miss does today.
func TestResolvePinsUnknownBothWarns(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()

	pinned, warnings, err := s.ResolvePins(ctx, []string{"other:absent"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned = %+v, want none", pinned)
	}
	if len(warnings) != 1 || warnings[0] != "pinned skill not found: other:absent" {
		t.Fatalf("warnings = %+v, want [pinned skill not found: other:absent]", warnings)
	}
}

// TestResolvePinsNoColonNoFallback: a pin with no colon has no suffix to
// fall back to, so a miss just warns.
func TestResolvePinsNoColonNoFallback(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()

	pinned, warnings, err := s.ResolvePins(ctx, []string{"absent"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned = %+v, want none", pinned)
	}
	if len(warnings) != 1 || warnings[0] != "pinned skill not found: absent" {
		t.Fatalf("warnings = %+v, want [pinned skill not found: absent]", warnings)
	}
}

// TestResolvePinsFallbackOnlyOnUnqualifiedNames: the fallback tries the
// after-colon suffix as a registry name literally — it must not match a
// differently-qualified registry row that happens to share the suffix.
func TestResolvePinsFallbackOnlyOnUnqualifiedNames(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("b:x", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"a:x"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned = %+v, want none (must not match b:x)", pinned)
	}
	if len(warnings) != 1 || warnings[0] != "pinned skill not found: a:x" {
		t.Fatalf("warnings = %+v, want [pinned skill not found: a:x]", warnings)
	}
}

// TestResolvePinsFallbackDeletedSkill: a fallback hit on a soft-deleted skill
// behaves exactly like an exact-match hit on one — content plus warning.
func TestResolvePinsFallbackDeletedSkill(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("test-driven-development", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if _, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	pinned, warnings, err := s.ResolvePins(ctx, []string{"superpowers:test-driven-development"})
	if err != nil {
		t.Fatalf("ResolvePins: %v", err)
	}
	if len(pinned) != 1 || pinned[0].Name != "test-driven-development" || pinned[0].SkillMD == "" {
		t.Fatalf("pinned = %+v, want test-driven-development resolved via fallback despite deletion", pinned)
	}
	// Warnings name the pin as written, not the resolved registry name.
	if len(warnings) != 1 || warnings[0] != "pinned skill removed from its source repo: superpowers:test-driven-development" {
		t.Fatalf("warnings = %+v, want a removed-from-source-repo warning naming the pin as written", warnings)
	}
}

// TestBriefWithoutSkillsSkipsTheQuery: the flag must skip the work, not just
// omit the output. Renaming the skills table away makes any pin lookup fail
// outright, so a brief that still succeeds provably never ran one.
func TestBriefWithoutSkillsSkipsTheQuery(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	in := defaultTaskInput()
	in.Skills = []string{"tdd"}
	task := createTask(t, s, leaseTestNow, in)

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE skills RENAME TO skills_moved`); err != nil {
		t.Fatalf("rename skills table: %v", err)
	}
	b, err := s.Brief(ctx, task.ID, BriefOptions{Skills: false})
	if err != nil {
		t.Fatalf("Brief without skills still queried them: %v", err)
	}
	if len(b.PinnedSkills) != 0 || len(b.SkillWarnings) != 0 {
		t.Fatalf("brief carried skills it was told to skip: %+v", b)
	}
	// The pin list itself is still on the task; only the resolution is skipped.
	if len(b.Task.Skills) != 1 {
		t.Fatalf("task pins = %+v, want them still reported", b.Task.Skills)
	}
	if _, err := s.Brief(ctx, task.ID, BriefOptions{Skills: true}); err == nil {
		t.Fatal("setup is not proving anything: the pin lookup should have failed")
	}
}

// TestBriefNoPins guards the bounded-queries contract: no pins, no extra
// query, and the fields stay empty rather than nil-panicking a caller.
func TestBriefNoPins(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	b, err := s.Brief(ctx, task.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.PinnedSkills) != 0 || len(b.SkillWarnings) != 0 {
		t.Fatalf("brief with no pins = %+v, want no pinned skills or warnings", b)
	}
}

// TestBriefResolvesDeletedPin guards that a pin surviving upstream deletion
// still resolves with its content: a brief must never break because a skill
// was withdrawn from its source repo.
func TestBriefResolvesDeletedPin(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if _, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	in := defaultTaskInput()
	in.Skills = []string{"tdd"}
	task := createTask(t, s, leaseTestNow, in)

	b, err := s.Brief(ctx, task.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.PinnedSkills) != 1 || b.PinnedSkills[0].Name != "tdd" || b.PinnedSkills[0].SkillMD == "" {
		t.Fatalf("pinned skills = %+v, want tdd resolved with content despite deletion", b.PinnedSkills)
	}
	if len(b.SkillWarnings) != 1 || b.SkillWarnings[0] != "pinned skill removed from its source repo: tdd" {
		t.Fatalf("skill warnings = %+v, want a removed-from-source-repo warning", b.SkillWarnings)
	}
}

func TestBriefParent(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	b, err := s.Brief(t.Context(), child.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if b.Parent == nil || b.Parent.ID != container.ID || b.Parent.Title != container.Title ||
		b.Parent.State != container.State {
		t.Fatalf("parent = %+v, want %s", b.Parent, container.ID)
	}
	if b.Parent.Body != "" {
		t.Fatalf("parent body = %q, want empty (one hop carries id, title, state only)", b.Parent.Body)
	}

	root, err := s.Brief(t.Context(), container.ID, BriefOptions{Skills: true})
	if err != nil {
		t.Fatalf("Brief root: %v", err)
	}
	if root.Parent != nil {
		t.Fatalf("parent of a root task = %+v, want nil", root.Parent)
	}
}

// TestBriefPlanBlockers: the brief names the open tasks of a plan ordered
// before this task's plan (025 §9.3), so an agent handed a refused task can
// see what is holding it.
func TestBriefPlanBlockers(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	blocked := mintReadyPlan(t, s, "plan-b", planTaskBody("", "Plan B"))
	blockers := mintReadyPlan(t, s, "plan-a", planTaskBody("blocks: plan-b\n", "Plan A"))

	b, err := s.Brief(t.Context(), blocked[0], BriefOptions{})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.OpenBlockers) != 1 || b.OpenBlockers[0].ID != blockers[0] {
		t.Errorf("OpenBlockers = %#v, want plan A's open task %s", b.OpenBlockers, blockers[0])
	}
	if len(b.BlockingPlans) != 1 || b.BlockingPlans[0].Slug != "plan-a" {
		t.Errorf("BlockingPlans = %#v, want plan-a", b.BlockingPlans)
	}

	walkTo(t, s, blockers[0], "merged")

	b, err = s.Brief(t.Context(), blocked[0], BriefOptions{})
	if err != nil {
		t.Fatalf("Brief after release: %v", err)
	}
	if len(b.OpenBlockers) != 0 || len(b.BlockingPlans) != 0 {
		t.Errorf("after plan A closed: blockers = %#v, plans = %#v, want none",
			b.OpenBlockers, b.BlockingPlans)
	}
}

// TestBriefBlockedByDraftPlan: a draft blocking plan has minted no task, so
// the brief has no blocker to name and reports the plan itself instead.
func TestBriefBlockedByDraftPlan(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	blocked := mintReadyPlan(t, s, "plan-d", planTaskBody("", "Plan D"))
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-c",
		Body: planTaskBody("blocks: plan-d\n", "Plan C"), CreatedBy: "stig",
	})

	b, err := s.Brief(t.Context(), blocked[0], BriefOptions{})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.OpenBlockers) != 0 {
		t.Errorf("OpenBlockers = %#v, want none: a draft plan has minted no task", b.OpenBlockers)
	}
	if len(b.BlockingPlans) != 1 || b.BlockingPlans[0].Slug != "plan-c" ||
		b.BlockingPlans[0].Status != "draft" {
		t.Errorf("BlockingPlans = %#v, want draft plan-c", b.BlockingPlans)
	}
}

// TestBriefBlobs checks Brief surfaces a task's blobs -- one embedded (via
// ReconcileEmbedded, as a body edit would produce) and one attached (via
// AttachBlob) -- with their reference-graph flags and metadata intact.
func TestBriefBlobs(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("seed actor: %v", err)
	}

	embeddedHash := "a1" + strings.Repeat("0", 62)
	attachedHash := "b2" + strings.Repeat("0", 62)
	for _, h := range []string{embeddedHash, attachedHash} {
		if _, err := s.InsertBlob(ctx, h, "image/png", 1024); err != nil {
			t.Fatalf("insert blob %s: %v", h, err)
		}
	}

	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), task.ID, []string{embeddedHash}, "alice")
	}); err != nil {
		t.Fatalf("reconcile embedded: %v", err)
	}
	if err := s.AttachBlob(ctx, task.ID, attachedHash, "crash.log", "alice"); err != nil {
		t.Fatalf("attach blob: %v", err)
	}

	b, err := s.Brief(ctx, task.ID, BriefOptions{})
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.Blobs) != 2 {
		t.Fatalf("blobs = %+v, want 2", b.Blobs)
	}
	byHash := make(map[string]model.TaskBlob, len(b.Blobs))
	for _, blob := range b.Blobs {
		byHash[blob.Hash] = blob
	}
	embedded, ok := byHash[embeddedHash]
	if !ok || !embedded.Embedded || embedded.Attached {
		t.Fatalf("embedded blob = %+v, want embedded=true attached=false", embedded)
	}
	if embedded.MediaType != "image/png" {
		t.Fatalf("embedded blob media type = %q, want image/png", embedded.MediaType)
	}
	attached, ok := byHash[attachedHash]
	if !ok || attached.Embedded || !attached.Attached {
		t.Fatalf("attached blob = %+v, want embedded=false attached=true", attached)
	}
	if attached.Filename != "crash.log" {
		t.Fatalf("attached blob filename = %q, want crash.log", attached.Filename)
	}
}
