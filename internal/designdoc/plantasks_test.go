package designdoc

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// mustParsePlan parses a plan body with a minimal H1 title prepended, the
// shape every real plan document has.
func mustParsePlan(t *testing.T, body string) *Document {
	t.Helper()
	src := "# Plan title\n\n" + body
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestPlanTasksThreeDefinitions(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

### Task 1 — First task

`+"```yaml"+`
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
`+"```"+`

Do the first thing.

- [ ] step one

### Task 2 — Second task

`+"```yaml"+`
kind: bug
priority: low
blockedBy: [1]
`+"```"+`

Do the second thing.

### Task 3 — Third task

`+"```yaml"+`
kind: chore
`+"```"+`

Do the third thing.
`)

	defs, err := PlanTasks(d)
	if err != nil {
		t.Fatalf("PlanTasks: %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("got %d defs, want 3", len(defs))
	}

	if defs[0].Title != "First task" || defs[1].Title != "Second task" || defs[2].Title != "Third task" {
		t.Errorf("titles = %q, %q, %q", defs[0].Title, defs[1].Title, defs[2].Title)
	}
	if defs[0].Kind != "feature" || defs[0].Priority != "high" {
		t.Errorf("def 1 kind/priority = %q/%q, want feature/high", defs[0].Kind, defs[0].Priority)
	}
	if !reflect.DeepEqual(defs[0].Skills, []string{"superpowers:test-driven-development"}) {
		t.Errorf("def 1 skills = %v", defs[0].Skills)
	}
	if len(defs[0].BlockedBy) != 0 {
		t.Errorf("def 1 blockedBy = %v, want empty", defs[0].BlockedBy)
	}
	if !strings.Contains(defs[0].Body, "Do the first thing.") || !strings.Contains(defs[0].Body, "- [ ] step one") {
		t.Errorf("def 1 body = %q, missing prose/steps", defs[0].Body)
	}
	if strings.Contains(defs[0].Body, "kind: feature") {
		t.Errorf("def 1 body still contains the yaml fence: %q", defs[0].Body)
	}

	if defs[1].Kind != "bug" || defs[1].Priority != "low" {
		t.Errorf("def 2 kind/priority = %q/%q, want bug/low", defs[1].Kind, defs[1].Priority)
	}
	if !reflect.DeepEqual(defs[1].BlockedBy, []int{1}) {
		t.Errorf("def 2 blockedBy = %v, want [1]", defs[1].BlockedBy)
	}

	// Fence carrying only kind: defaults apply.
	if defs[2].Kind != "chore" {
		t.Errorf("def 3 kind = %q, want chore", defs[2].Kind)
	}
	if defs[2].Priority != "medium" {
		t.Errorf("def 3 priority = %q, want default medium", defs[2].Priority)
	}
	if len(defs[2].Skills) != 0 {
		t.Errorf("def 3 skills = %v, want none", defs[2].Skills)
	}
	if len(defs[2].BlockedBy) != 0 {
		t.Errorf("def 3 blockedBy = %v, want none", defs[2].BlockedBy)
	}
}

func TestPlanTasksMissingOrKindlessFence(t *testing.T) {
	tests := map[string]string{
		"no fence at all": `## Tasks

### Task 1 — Only task

Just prose, no yaml fence.
`,
		"fence without kind": `## Tasks

### Task 1 — Only task

` + "```yaml" + `
priority: high
` + "```" + `

Prose.
`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			d := mustParsePlan(t, body)
			_, err := PlanTasks(d)
			if err == nil {
				t.Fatal("PlanTasks: want error, got nil")
			}
			if !strings.Contains(err.Error(), "kind is required") {
				t.Errorf("error = %q, want it to mention kind is required", err)
			}
			if !strings.Contains(err.Error(), "1") {
				t.Errorf("error = %q, want it to name task 1", err)
			}
		})
	}
}

// TestPlanTasksAliasesDeprecatedKind proves a pre-025 plan body using the
// retired "spec" spelling still mints, as "design" (WL-138).
func TestPlanTasksAliasesDeprecatedKind(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

### Task 1 — Only task

`+"```yaml"+`
kind: spec
`+"```"+`

Prose.
`)
	defs, err := PlanTasks(d)
	if err != nil {
		t.Fatalf("PlanTasks: %v", err)
	}
	if defs[0].Kind != "design" {
		t.Errorf("kind = %q, want design", defs[0].Kind)
	}
}

func TestPlanTasksBlockedByBasic(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

### Task 1 — First

`+"```yaml"+`
kind: feature
`+"```"+`

First.

### Task 2 — Second

`+"```yaml"+`
kind: feature
blockedBy: [1]
`+"```"+`

Second.
`)
	defs, err := PlanTasks(d)
	if err != nil {
		t.Fatalf("PlanTasks: %v", err)
	}
	if !reflect.DeepEqual(defs[1].BlockedBy, []int{1}) {
		t.Errorf("def 2 blockedBy = %v, want [1]", defs[1].BlockedBy)
	}
}

func TestPlanTasksBlockedByDanglingOrSelf(t *testing.T) {
	tests := map[string]string{
		"names a number not in the file": `## Tasks

### Task 1 — First

` + "```yaml" + `
kind: feature
blockedBy: [5]
` + "```" + `

First.
`,
		"names its own number": `## Tasks

### Task 1 — First

` + "```yaml" + `
kind: feature
blockedBy: [1]
` + "```" + `

First.
`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			d := mustParsePlan(t, body)
			_, err := PlanTasks(d)
			if err == nil {
				t.Fatal("PlanTasks: want error, got nil")
			}
			if !strings.Contains(err.Error(), "1") {
				t.Errorf("error = %q, want it to name task 1", err)
			}
			if !strings.Contains(err.Error(), "blockedBy") {
				t.Errorf("error = %q, want it to name the blockedBy number", err)
			}
		})
	}
}

func TestPlanTasksBlockedByCycle(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

### Task 1 — First

`+"```yaml"+`
kind: feature
blockedBy: [2]
`+"```"+`

First.

### Task 2 — Second

`+"```yaml"+`
kind: feature
blockedBy: [1]
`+"```"+`

Second.
`)
	_, err := PlanTasks(d)
	if err == nil {
		t.Fatal("PlanTasks: want error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want it to mention a cycle", err)
	}
	if !strings.Contains(err.Error(), "1") || !strings.Contains(err.Error(), "2") {
		t.Errorf("error = %q, want it to name tasks 1 and 2", err)
	}
}

func TestPlanTasksUnmintableOrUnknownKind(t *testing.T) {
	tests := map[string]string{
		"review":   "review",
		"spike":    "spike",
		"nonsense": "epic",
	}
	for name, kind := range tests {
		t.Run(name, func(t *testing.T) {
			d := mustParsePlan(t, `## Tasks

### Task 1 — Only task

`+"```yaml"+`
kind: `+kind+`
`+"```"+`

Prose.
`)
			_, err := PlanTasks(d)
			if err == nil {
				t.Fatal("PlanTasks: want error, got nil")
			}
			if !strings.Contains(err.Error(), "1") {
				t.Errorf("error = %q, want it to name task 1", err)
			}
			if !strings.Contains(err.Error(), kind) {
				t.Errorf("error = %q, want it to name the value %q", err, kind)
			}
		})
	}
}

func TestPlanTasksUnmintablePriority(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

### Task 1 — Only task

`+"```yaml"+`
kind: feature
priority: urgent
`+"```"+`

Prose.
`)
	_, err := PlanTasks(d)
	if err == nil {
		t.Fatal("PlanTasks: want error, got nil")
	}
	if !strings.Contains(err.Error(), "priority") || !strings.Contains(err.Error(), "urgent") {
		t.Errorf("error = %q, want it to name the bad priority", err)
	}
}

func TestPlanTasksNumberingErrors(t *testing.T) {
	fence := "```yaml\nkind: feature\n```\n\nProse.\n"
	tests := map[string]string{
		"gap": `## Tasks

### Task 1 — First

` + fence + `
### Task 3 — Third

` + fence,
		"out of order": `## Tasks

### Task 2 — Second

` + fence + `
### Task 1 — First

` + fence,
		"duplicate": `## Tasks

### Task 1 — First

` + fence + `
### Task 1 — First again

` + fence,
		"starts at 2": `## Tasks

### Task 2 — Only task

` + fence,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			d := mustParsePlan(t, body)
			_, err := PlanTasks(d)
			if err == nil {
				t.Fatal("PlanTasks: want error, got nil")
			}
			if !strings.Contains(err.Error(), "without gaps") {
				t.Errorf("error = %q, want it to mention the numbering rule", err)
			}
		})
	}
}

func TestPlanTasksHeadingNearMisses(t *testing.T) {
	fence := "```yaml\nkind: feature\n```\n\nProse.\n"
	tests := map[string]string{
		"hyphen instead of em dash":  "### Task 1 - Title\n\n" + fence,
		"en dash instead of em dash": "### Task 1 – Title\n\n" + fence,
		"empty title":                "### Task 1 — \n\n" + fence,
	}
	for name, heading := range tests {
		t.Run(name, func(t *testing.T) {
			d := mustParsePlan(t, "## Tasks\n\n"+heading)
			_, err := PlanTasks(d)
			if err == nil {
				t.Fatal("PlanTasks: want error, got nil")
			}
			if !strings.Contains(err.Error(), "Task <N>") {
				t.Errorf("error = %q, want it to quote the expected heading shape", err)
			}
		})
	}
}

func TestPlanTasksStrayContentBeforeFirstHeading(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

This paragraph should not be here.

### Task 1 — Only task

`+"```yaml"+`
kind: feature
`+"```"+`

Prose.
`)
	_, err := PlanTasks(d)
	if err == nil {
		t.Fatal("PlanTasks: want error, got nil")
	}
	if !strings.Contains(err.Error(), "non-blank content") {
		t.Errorf("error = %q, want it to mention stray content", err)
	}
}

func TestPlanTasksTwoTasksSections(t *testing.T) {
	d := mustParsePlan(t, `## Tasks

### Task 1 — Only task

`+"```yaml"+`
kind: feature
`+"```"+`

Prose.

## Tasks

### Task 1 — Duplicate section's task

`+"```yaml"+`
kind: feature
`+"```"+`

Prose.
`)
	_, err := PlanTasks(d)
	if err == nil {
		t.Fatal("PlanTasks: want error, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error = %q, want it to say exactly one \"## Tasks\" section", err)
	}
}

func TestPlanTasksHeadingsOutsideTasksSectionAreIgnored(t *testing.T) {
	d := mustParsePlan(t, `## Background

### Task 1 — Not a real task definition

This lives outside ## Tasks and must be ignored.

## Tasks

### Task 1 — Real task

`+"```yaml"+`
kind: feature
`+"```"+`

Prose.
`)
	defs, err := PlanTasks(d)
	if err != nil {
		t.Fatalf("PlanTasks: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	if defs[0].Title != "Real task" {
		t.Errorf("title = %q, want %q", defs[0].Title, "Real task")
	}
}

func TestPlanTasksNoTasksSectionOrNoHeadings(t *testing.T) {
	tests := map[string]string{
		"no ## Tasks section at all": `## Background

Just some prose.
`,
		"## Tasks section with no task headings": `## Tasks

## Background

Nothing here.
`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			d := mustParsePlan(t, body)
			_, err := PlanTasks(d)
			if err == nil {
				t.Fatal("PlanTasks: want error, got nil")
			}
			if !strings.Contains(err.Error(), "plan defines no tasks") {
				t.Errorf("error = %q, want \"plan defines no tasks\"", err)
			}
		})
	}
}

// TestPlanMintableKindsMatchLiveKindSet guards the invariant 025 §9.1 wants:
// the plan-mintable kind subset cannot silently drift from the live kind set.
// ns.TaskKinds is that set — generated from ns/concept.ttl's wlc:TaskKind
// scheme, which the tasks.kind CHECK constraint and internal/api's validKinds
// also mirror (TestTaskKindsAgreeAcrossSources holds those together). A plan
// may mint every kind except the two that are not plannable units of the
// plan's own work.
func TestPlanMintableKindsMatchLiveKindSet(t *testing.T) {
	want := make([]string, 0, len(ns.TaskKinds))
	for _, k := range ns.TaskKinds {
		if k == "review" || k == "spike" {
			continue
		}
		want = append(want, k)
	}
	sort.Strings(want)

	got := make([]string, len(planMintableKinds))
	copy(got, planMintableKinds)
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("planMintableKinds = %v, want %v", got, want)
	}
}
