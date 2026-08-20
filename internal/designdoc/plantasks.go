package designdoc

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// PlanTask is one task definition in a plan document's ## Tasks section —
// the plan task format 025 §9.2 mints from (docs/authoring-design-docs.md
// carries the canonical definition).
type PlanTask struct {
	Number    int
	Title     string   // heading text after "Task N — "
	Body      string   // the section's own content, yaml fence excluded
	Kind      string   // required; one of the §4.1 mintable four
	Priority  string   // default "medium"
	Skills    []string // plugin:skill ids the executing agent loads
	BlockedBy []int    // task numbers within this plan
}

// planTaskHeadingRE matches a task subsection heading's Title (the heading
// text with hashes, number and anchor already stripped by Parse): "Task 1 —
// Short imperative title". The em dash is part of the format (025 §9.1);
// hyphens and en dashes are near misses, not accepted alternatives.
var planTaskHeadingRE = regexp.MustCompile(`^Task\s+(\d+)\s+—\s+(.+)$`)

// planMintableKinds is the subset of task kinds a plan may mint (025 §9.1):
// review tasks are created by the review lifecycle and spikes are inputs to
// planning, so neither is plan-declarable. Membership is tested with
// slices.Contains, so the list is also the lookup — there is nothing to drift.
var planMintableKinds = []string{"feature", "bug", "chore", "design"}

// planPriorities is the priority values a task definition may declare
// (docs/authoring-design-docs.md's key table); "medium" is the default when
// the key is absent.
var planPriorities = []string{"critical", "high", "medium", "low"}

// planTaskFence is the yaml metadata block a task subsection opens with.
// KnownFields makes a typoed key an error rather than a silent drop,
// matching parseFrontmatter's stance.
type planTaskFence struct {
	Kind      string   `yaml:"kind"`
	Priority  string   `yaml:"priority"`
	Skills    []string `yaml:"skills"`
	BlockedBy []int    `yaml:"blockedBy"`
}

// PlanTasks extracts the task definitions the accept transaction mints:
// the `### Task N — Title` sections under the single `## Tasks` heading,
// each opening with a yaml metadata fence (kind required; priority, skills,
// blockedBy optional). Validation errors name the task; the numbers run
// 1, 2, 3… in document order without gaps, and blockedBy must be acyclic
// (025 §9.1).
func PlanTasks(d *Document) ([]PlanTask, error) {
	var sections []*Section
	for _, sec := range d.Sections {
		if sec.Level == 2 && strings.TrimSpace(sec.Title) == "Tasks" {
			sections = append(sections, sec)
		}
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("plan defines no tasks: no \"## Tasks\" section")
	}
	if len(sections) > 1 {
		return nil, fmt.Errorf("plan has %d \"## Tasks\" sections, want exactly one", len(sections))
	}
	tasksSec := sections[0]

	if strings.TrimSpace(tasksSec.Body) != "" {
		return nil, fmt.Errorf(
			"\"## Tasks\": non-blank content before the first task heading (§4.1 allows only task subsections): %q",
			strings.TrimSpace(tasksSec.Body))
	}
	if len(tasksSec.Children) == 0 {
		return nil, fmt.Errorf("plan defines no tasks: \"## Tasks\" section has no task headings")
	}

	defs := make([]PlanTask, 0, len(tasksSec.Children))
	for _, child := range tasksSec.Children {
		def, err := parsePlanTask(child)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}

	if err := checkPlanTaskNumbering(defs); err != nil {
		return nil, err
	}
	if err := checkPlanTaskBlockedBy(defs); err != nil {
		return nil, err
	}

	return defs, nil
}

// parsePlanTask parses one `### Task N — Title` subsection: the heading
// shape, its yaml fence, and the fence's fields.
func parsePlanTask(sec *Section) (PlanTask, error) {
	heading := strings.TrimRight(sec.headingSource(), "\r\n")
	// A heading that does not match, and one whose title is only whitespace,
	// are the same defect and report the same way.
	badShape := func() error {
		return fmt.Errorf("task heading %q: want \"Task <N> — <title>\" (em dash)", heading)
	}
	m := planTaskHeadingRE.FindStringSubmatch(strings.TrimSpace(sec.Title))
	if m == nil {
		return PlanTask{}, badShape()
	}
	number, err := strconv.Atoi(m[1])
	if err != nil {
		return PlanTask{}, fmt.Errorf("task heading %q: invalid task number: %w", heading, err)
	}
	title := strings.TrimSpace(m[2])
	if title == "" {
		return PlanTask{}, badShape()
	}

	fenceSrc, body, found := splitPlanTaskFence(sec.Body)
	var meta planTaskFence
	if found && strings.TrimSpace(fenceSrc) != "" {
		dec := yaml.NewDecoder(strings.NewReader(fenceSrc))
		dec.KnownFields(true)
		if err := dec.Decode(&meta); err != nil {
			return PlanTask{}, fmt.Errorf("task %d: yaml fence: %w", number, err)
		}
	}

	if meta.Kind == "" {
		return PlanTask{}, fmt.Errorf("task %d: kind is required", number)
	}
	// Normalise without counting an alias use: a plan body is stored input,
	// discoverable by querying the documents themselves, unlike a request
	// (see kindAliasUses in internal/api/server.go).
	meta.Kind, _ = ns.NormalizeTaskKind(meta.Kind)
	if !slices.Contains(planMintableKinds, meta.Kind) {
		return PlanTask{}, fmt.Errorf(
			"task %d: kind %q is not plan-mintable; want one of %s",
			number, meta.Kind, strings.Join(planMintableKinds, ", "))
	}

	priority := meta.Priority
	if priority == "" {
		priority = "medium"
	}
	if !slices.Contains(planPriorities, priority) {
		return PlanTask{}, fmt.Errorf(
			"task %d: priority %q invalid; want one of %s",
			number, priority, strings.Join(planPriorities, ", "))
	}

	return PlanTask{
		Number:    number,
		Title:     title,
		Body:      body,
		Kind:      meta.Kind,
		Priority:  priority,
		Skills:    meta.Skills,
		BlockedBy: meta.BlockedBy,
	}, nil
}

// splitPlanTaskFence finds the yaml metadata fence a task section must open
// with (blank lines aside) and returns its content plus the section body
// with the fence removed. found is false when the section does not open
// with a ```yaml/~~~yaml fence — a fence appearing later in the body is
// ordinary body content, not metadata.
func splitPlanTaskFence(body string) (fenceContent, rest string, found bool) {
	lines := splitLines(body)
	// TrimSpace already drops the \r a CRLF line carries.
	lineText := func(i int) string { return strings.TrimSpace(lines[i].text(body)) }

	i := 0
	for i < len(lines) && lineText(i) == "" {
		i++
	}
	if i >= len(lines) {
		return "", body, false
	}

	openLine := lineText(i)
	var mark string
	switch {
	case strings.HasPrefix(openLine, "```"):
		mark = "```"
	case strings.HasPrefix(openLine, "~~~"):
		mark = "~~~"
	default:
		return "", body, false
	}
	if strings.TrimSpace(strings.TrimPrefix(openLine, mark)) != "yaml" {
		return "", body, false
	}

	j := i + 1
	for j < len(lines) {
		if strings.HasPrefix(lineText(j), mark) {
			break
		}
		j++
	}
	if j >= len(lines) {
		// Unterminated fence: not a recognisable metadata block.
		return "", body, false
	}

	fenceContent = body[lines[i+1].start:lines[j].start]
	rest = body[:lines[i].start] + body[lines[j].end:]
	return fenceContent, rest, true
}

// checkPlanTaskNumbering enforces that N runs 1, 2, 3… in document order
// without gaps (§4.1).
func checkPlanTaskNumbering(defs []PlanTask) error {
	for i, def := range defs {
		if def.Number != i+1 {
			return fmt.Errorf(
				"task numbers must run 1, 2, 3… in document order without gaps: "+
					"task %q is number %d, want %d", def.Title, def.Number, i+1)
		}
	}
	return nil
}

// checkPlanTaskBlockedBy validates that every blockedBy entry names another
// task this plan defines, not itself, and that the resulting graph is
// acyclic.
func checkPlanTaskBlockedBy(defs []PlanTask) error {
	n := len(defs)
	for _, def := range defs {
		for _, b := range def.BlockedBy {
			if b == def.Number {
				return fmt.Errorf("task %d: blockedBy names itself", def.Number)
			}
			if b < 1 || b > n {
				return fmt.Errorf(
					"task %d: blockedBy names task %d, which this plan does not define",
					def.Number, b)
			}
		}
	}
	if cycle := findBlockedByCycle(defs); cycle != nil {
		return fmt.Errorf("blockedBy cycle among tasks %v", cycle)
	}
	return nil
}

// findBlockedByCycle walks BlockedBy depth-first and returns the task
// numbers on the first cycle found, or nil if the graph is acyclic. Numbers
// are visited in order so the result is deterministic.
func findBlockedByCycle(defs []PlanTask) []int {
	byNumber := make(map[int]PlanTask, len(defs))
	numbers := make([]int, 0, len(defs))
	for _, def := range defs {
		byNumber[def.Number] = def
		numbers = append(numbers, def.Number)
	}
	slices.Sort(numbers)

	const (
		white = iota
		gray
		black
	)
	color := make(map[int]int, len(defs))
	var stack []int
	var cycle []int

	var visit func(n int) bool
	visit = func(n int) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, b := range byNumber[n].BlockedBy {
			switch color[b] {
			case white:
				if visit(b) {
					return true
				}
			case gray:
				if i := slices.Index(stack, b); i >= 0 {
					cycle = slices.Clone(stack[i:])
				}
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}

	for _, n := range numbers {
		if color[n] == white {
			if visit(n) {
				return cycle
			}
		}
	}
	return nil
}
