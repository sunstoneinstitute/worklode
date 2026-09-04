package hookrun

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// TestSessionStartSkillsHappyPath covers a pinned skill (content inlined,
// dir materialized) and a matched skill (dir materialized, pointed at by its
// install line) both landing successfully.
func TestSessionStartSkillsHappyPath(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-1", "happy")

	tddContent := "# TDD\nRed-green-refactor.\n"
	tddArchive, tddHash := buildSkillArchive(t, tddContent)
	diagContent := "# Diagnose\nSystematic debugging.\n"
	diagArchive, diagHash := buildSkillArchive(t, diagContent)

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-1", Title: "Happy path", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{
				{Name: "tdd", Description: "Red-green-refactor discipline", Hash: tddHash, Content: tddContent},
			},
			Matches: []model.SkillMatch{
				{Name: "diagnose", Description: "Systematic debugging", Hash: diagHash, Score: 0.87},
			},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("tdd", tddHash, tddArchive)
	back.setArchive("diagnose", diagHash, diagArchive)

	stdout, _ := runSessionStart(t, wtDir, "s-skills-happy")
	ctx := additionalContext(t, stdout)

	// Pull the paths the hook actually emitted rather than assuming
	// skillstore's internal layout — Ensure owns that shape, not this test.
	pinnedPath := extractSupportingFilesPath(t, ctx)
	matchLoc := extractMatchLocation(t, ctx, "diagnose")

	wantSection := "\n## Skills\n" +
		"\n### Pinned: tdd\n" + tddContent + "\n" +
		"(supporting files: " + pinnedPath + ")\n" +
		"\n### Possibly relevant org skills\nRead the SKILL.md if relevant to this task:\n" +
		"- diagnose (0.87): Systematic debugging — " + matchLoc + "\n"
	if !strings.HasSuffix(ctx, wantSection) {
		t.Fatalf("additionalContext = %q\nwant suffix %q", ctx, wantSection)
	}

	// Verify the emitted paths actually resolve to the fetched content, not
	// just that some string got printed.
	got, err := os.ReadFile(filepath.Join(pinnedPath, "SKILL.md"))
	if err != nil || string(got) != tddContent {
		t.Fatalf("SKILL.md at pinned path %s = %q, %v; want %q", pinnedPath, got, err, tddContent)
	}
	if !strings.HasSuffix(matchLoc, "/SKILL.md") {
		t.Fatalf("match location %q should point at a SKILL.md file", matchLoc)
	}
	got, err = os.ReadFile(matchLoc)
	if err != nil || string(got) != diagContent {
		t.Fatalf("content at match location %s = %q, %v; want %q", matchLoc, got, err, diagContent)
	}

	// Project-scope delivery (spec 008 §17.3): the worktree now carries
	// .agents/skills/<name> resolving to the store version dir, so a
	// harness opened in wtDir reads this task's skills without a `lode
	// install`.
	storeDir := filepath.Join(filepath.Dir(os.Getenv("LODE_SKILLS_DIR")), "store")
	// Resolve after the fact: LODE_SKILLS_DIR is a t.TempDir() path, which on
	// macOS lives under /var/folders — a symlink to /private/var/folders —
	// while the worktree link below is resolved by EvalSymlinks and comes
	// back with /private already in it. Normalize both sides the same way.
	storeDir, err = filepath.EvalSymlinks(storeDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(storeDir): %v", err)
	}
	link := filepath.Join(wtDir, ".agents", "skills", "tdd")
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || !strings.HasPrefix(resolved, storeDir) {
		t.Fatalf("worktree link = %s (%v), want it under the store dir %s", resolved, err, storeDir)
	}

	// …and .agents/ is excluded via info/exclude, not .gitignore — the
	// links are machine-local and must never become a commit.
	exclPath, err := worktree.ExcludeFile(root)
	if err != nil {
		t.Fatalf("ExcludeFile: %v", err)
	}
	excl, err := os.ReadFile(exclPath)
	if err != nil || !strings.Contains(string(excl), ".agents/") {
		t.Fatalf("info/exclude missing .agents/: %s (%v)", excl, err)
	}
	if _, err := os.Stat(filepath.Join(wtDir, ".gitignore")); err == nil {
		t.Fatal("a .gitignore appeared")
	}

	// The links must not show up as untracked — the real user-visible
	// property info/exclude (not .gitignore) is meant to guarantee.
	clean, err := worktree.IsClean(wtDir)
	if err != nil || !clean {
		out, _ := exec.Command("git", "-C", wtDir, "status", "--porcelain").CombinedOutput()
		t.Fatalf("git status not clean after linking: %v\n%s", err, out)
	}

	// Idempotent: a second session-start leaves exactly one .agents/ line
	// in info/exclude, not a duplicate.
	runSessionStart(t, wtDir, "s-skills-happy-2")
	excl2, err := os.ReadFile(exclPath)
	if err != nil {
		t.Fatalf("read info/exclude after second session-start: %v", err)
	}
	if n := strings.Count(string(excl2), ".agents/\n"); n != 1 {
		t.Fatalf("info/exclude has %d .agents/ lines after two session-starts, want exactly 1: %s", n, excl2)
	}
}

// TestSessionStartSkillsWorktreeLinkPreservesForeignFile covers the
// ownership discipline linkWorktreeSkill must not skip (spec 008 §18 row
// 4): a plain file sitting at .agents/skills/<name> before session-start —
// something Worklode did not create — must survive untouched, with a
// warning explaining why that skill was not linked, rather than being
// silently clobbered by the symlink swap.
func TestSessionStartSkillsWorktreeLinkPreservesForeignFile(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-9", "foreign")

	tddContent := "# TDD\n"
	tddArchive, tddHash := buildSkillArchive(t, tddContent)
	brief := model.Brief{
		Task:   model.Task{ID: "PROJ-9", Title: "Foreign file", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{Pinned: []model.PinnedSkill{{Name: "tdd", Description: "d", Hash: tddHash, Content: tddContent}}},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("tdd", tddHash, tddArchive)

	link := filepath.Join(wtDir, ".agents", "skills", "tdd")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const foreignContent = "not a skill link\n"
	if err := os.WriteFile(link, []byte(foreignContent), 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	_, stderr := runSessionStart(t, wtDir, "s-foreign")

	if !strings.Contains(stderr, "tdd") || !strings.Contains(stderr, "not a symlink") {
		t.Fatalf("stderr missing the foreign-file warning: %q", stderr)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("foreign file gone: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("foreign file was replaced with a symlink")
	}
	got, err := os.ReadFile(link)
	if err != nil || string(got) != foreignContent {
		t.Fatalf("foreign file content = %q, %v; want %q untouched", got, err, foreignContent)
	}
}

// TestEnsureExcludedAddsMissingTrailingNewline covers a hand-edited
// info/exclude with no trailing newline: appending ".agents/" via
// os.O_APPEND without checking for one would otherwise merge it onto the
// end of the file's last pattern.
func TestEnsureExcludedAddsMissingTrailingNewline(t *testing.T) {
	root := initGitRepo(t)
	exclPath, err := worktree.ExcludeFile(root)
	if err != nil {
		t.Fatalf("ExcludeFile: %v", err)
	}
	const existing = "*.bak" // deliberately no trailing newline
	if err := os.WriteFile(exclPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write info/exclude: %v", err)
	}

	ensureExcluded(Options{Stderr: &bytes.Buffer{}}, root)

	got, err := os.ReadFile(exclPath)
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	want := existing + "\n.agents/\n"
	if string(got) != want {
		t.Fatalf("info/exclude = %q, want %q", got, want)
	}
}

// TestSessionStartSkillsArchiveFetchFailure covers the warn-only discipline:
// one skill's archive 500s, the hook still exits 0 and emits the full brief,
// a warning lands on stderr, and the OTHER (pinned) skill is still installed.
// The failed match must fall back to an install hint rather than a bogus path.
func TestSessionStartSkillsArchiveFetchFailure(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-2", "archfail")

	tddContent := "# TDD\n"
	tddArchive, tddHash := buildSkillArchive(t, tddContent)
	_, diagHash := buildSkillArchive(t, "# Diagnose\n") // archive itself is never served: forced 500

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-2", Title: "Archive failure", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned:  []model.PinnedSkill{{Name: "tdd", Description: "d", Hash: tddHash, Content: tddContent}},
			Matches: []model.SkillMatch{{Name: "diagnose", Description: "Systematic debugging", Hash: diagHash, Score: 0.5}},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("tdd", tddHash, tddArchive)
	back.failArchive("diagnose")

	stdout, stderr := runSessionStart(t, wtDir, "s-archfail")
	ctx := additionalContext(t, stdout)

	if !strings.Contains(ctx, tddContent) {
		t.Fatalf("additionalContext missing pinned content: %q", ctx)
	}
	if !strings.Contains(stderr, "diagnose") {
		t.Fatalf("stderr missing warning about the failed skill: %q", stderr)
	}

	// The failed match falls back to the install hint, never a local path —
	// whatever shape a successful path would have taken.
	loc := extractMatchLocation(t, ctx, "diagnose")
	if loc != "lode skill install diagnose" {
		t.Fatalf("match location for a failed install = %q, want the install hint", loc)
	}

	// The other (pinned) skill still installed: its emitted path resolves to
	// the real content despite diagnose's failure.
	pinnedPath := extractSupportingFilesPath(t, ctx)
	got, err := os.ReadFile(filepath.Join(pinnedPath, "SKILL.md"))
	if err != nil || string(got) != tddContent {
		t.Fatalf("SKILL.md at pinned path %s = %q, %v; want %q", pinnedPath, got, err, tddContent)
	}
}

// TestSessionStartSkillsEmptySection covers a brief with no skills at all:
// no "## Skills" heading, and no crash.
func TestSessionStartSkillsEmptySection(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-3", "empty")

	brief := model.Brief{Task: model.Task{ID: "PROJ-3", Title: "No skills", State: "in_progress", Priority: "low"}}
	newSkillsBackbone(t, brief)

	stdout, _ := runSessionStart(t, wtDir, "s-empty")
	ctx := additionalContext(t, stdout)
	if strings.Contains(ctx, "## Skills") {
		t.Fatalf("additionalContext should have no Skills heading for an empty skills section: %q", ctx)
	}
}

// TestSessionStartSkillsPinnedEmptyHashSkipped covers a pinned skill with no
// hash (e.g. a skill pinned before its content synced): ensureSkills skips
// the fetch without warning, and the inline content still reaches the model.
func TestSessionStartSkillsPinnedEmptyHashSkipped(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-4", "nohash")

	draftContent := "# Draft\n"
	brief := model.Brief{
		Task: model.Task{ID: "PROJ-4", Title: "No hash", State: "in_progress", Priority: "low"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{{Name: "draft-skill", Description: "d", Hash: "", Content: draftContent}},
		},
	}
	newSkillsBackbone(t, brief)

	stdout, stderr := runSessionStart(t, wtDir, "s-nohash")
	ctx := additionalContext(t, stdout)
	if !strings.Contains(ctx, draftContent) {
		t.Fatalf("additionalContext missing pinned content: %q", ctx)
	}
	if strings.Contains(ctx, "supporting files") {
		t.Fatalf("a hashless pinned skill must not report a local dir: %q", ctx)
	}
	if strings.Contains(stderr, "draft-skill") {
		t.Fatalf("a hashless pinned skill must not produce a warning: %q", stderr)
	}
}

// TestSessionStartSkillsFetchBudgetBounded covers the fix for a measured
// regression: 1 pin + 5 matches (5 is the brief's default match limit,
// defaultSkillLimit; see internal/api/skills.go) against a hanging archive
// endpoint used to cost
// 12s+ of dead air at session start, strictly linear in skill count. The
// fetch loop must instead be bounded overall by skillsBudget and run with
// bounded concurrency (skillFetchConcurrency), never serially per skill.
func TestSessionStartSkillsFetchBudgetBounded(t *testing.T) {
	shrinkSkillFetchBudget(t, 500*time.Millisecond, 500*time.Millisecond, 2)

	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-5", "budget")

	_, tddHash := buildSkillArchive(t, "# TDD\n")
	matches := make([]model.SkillMatch, 0, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("match-%d", i)
		_, hash := buildSkillArchive(t, "# "+name+"\n")
		matches = append(matches, model.SkillMatch{Name: name, Description: "d", Hash: hash, Score: 0.5})
	}

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-5", Title: "Budget", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned:  []model.PinnedSkill{{Name: "tdd", Description: "d", Hash: tddHash, Content: "# TDD\n"}},
			Matches: matches,
		},
	}
	back := newSkillsBackbone(t, brief)
	back.hangArchive("tdd")
	for _, m := range matches {
		back.hangArchive(m.Name)
	}

	start := time.Now()
	stdout, stderr := runSessionStart(t, wtDir, "s-budget")
	elapsed := time.Since(start)

	// 6 skills serialized at the old 2s-per-fetch rate would be 12s; bounded
	// by a 500ms budget the whole loop — regardless of skill count — must
	// land near that budget, not near (skill count × archiveTimeout).
	if elapsed > skillsBudget+time.Second {
		t.Fatalf("session-start with 6 hanging archives took %s, want well under skillsBudget=%s", elapsed, skillsBudget)
	}

	// The concurrency cap actually held: with 6 hanging fetches and a limit
	// of 2, the server must never see more than 2 requests in flight at once.
	if peak := back.peakConcurrency(); peak != 2 {
		t.Fatalf("peak concurrent archive fetches = %d, want exactly the skillFetchConcurrency limit (2)", peak)
	}

	// The never-fail invariant holds throughout: the brief still comes
	// through, and every hung skill was warned about.
	ctx := additionalContext(t, stdout)
	if !strings.Contains(ctx, "PROJ-5") {
		t.Fatalf("additionalContext missing the brief despite hanging archives: %q", ctx)
	}
	if !strings.Contains(stderr, "tdd") {
		t.Fatalf("stderr missing warning for the hung pinned skill: %q", stderr)
	}
	for _, m := range matches {
		if !strings.Contains(stderr, m.Name) {
			t.Fatalf("stderr missing warning for hung match %s: %q", m.Name, stderr)
		}
	}
}

// TestSessionStartSkillsPinnedByteCapEmitsPointer covers the fix for the
// second measured regression: pinned content is inlined with no size bound,
// so one large SKILL.md (or several) could inject hundreds of KB into every
// session start. Once the running total of inlined pinned bytes would
// exceed maxInlinedSkillBytes, later pins get a pointer instead of their
// content — never a mid-document truncation.
func TestSessionStartSkillsPinnedByteCapEmitsPointer(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-6", "bytecap")

	small := "# Small\n"
	big := "# Big\n" + strings.Repeat("x", maxInlinedSkillBytes) // alone, guarantees overflow
	smallArchive, smallHash := buildSkillArchive(t, small)
	bigArchive, bigHash := buildSkillArchive(t, big)

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-6", Title: "Byte cap", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{
				{Name: "small", Description: "d", Hash: smallHash, Content: small},
				{Name: "big", Description: "d", Hash: bigHash, Content: big},
			},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("small", smallHash, smallArchive)
	back.setArchive("big", bigHash, bigArchive)

	stdout, _ := runSessionStart(t, wtDir, "s-bytecap")
	ctx := additionalContext(t, stdout)

	// The small pin fits under budget and is inlined in full.
	if got, want := pinnedBodyLine(t, ctx, "small"), "# Small"; got != want {
		t.Fatalf("small pin body line = %q, want %q (should be inlined in full)", got, want)
	}
	if strings.Contains(ctx, big) {
		t.Fatalf("big pin's content must not appear in full once the byte budget is exceeded")
	}

	// The big pin gets a pointer, sized and located, never a truncated body.
	line := pinnedBodyLine(t, ctx, "big")
	wantPrefix := "(content omitted — " + humanKB(len(big)) + "; read it at "
	if !strings.HasPrefix(line, wantPrefix) || !strings.HasSuffix(line, "/SKILL.md)") {
		t.Fatalf("big pin body line = %q, want prefix %q and suffix %q", line, wantPrefix, "/SKILL.md)")
	}
}

// TestSessionStartSkillsPinnedByteCapFallsBackToInstallHint covers the
// interaction the coordinator flagged between fixes 1-3: an over-budget
// pinned skill whose archive ALSO failed to fetch must point at the install
// hint, not a local path that was never actually populated.
func TestSessionStartSkillsPinnedByteCapFallsBackToInstallHint(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-7", "bytecapfail")

	big := "# Big\n" + strings.Repeat("y", maxInlinedSkillBytes)
	_, bigHash := buildSkillArchive(t, big)

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-7", Title: "Byte cap fetch failure", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{{Name: "big", Description: "d", Hash: bigHash, Content: big}},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.failArchive("big")

	stdout, _ := runSessionStart(t, wtDir, "s-bytecapfail")
	ctx := additionalContext(t, stdout)

	got := pinnedBodyLine(t, ctx, "big")
	want := "(content omitted — " + humanKB(len(big)) + "; read it at lode skill install big)"
	if got != want {
		t.Fatalf("pinned body line = %q, want %q", got, want)
	}
}
