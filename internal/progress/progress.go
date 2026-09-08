// Package progress derives the WL-SPEC-66 §1 progress model — plan state,
// section state, spec grouping, and the next act — from typed facts about a
// project's specs, plans and tasks. Every function is a pure function: no
// I/O, no clock, no imports beyond stdlib and internal/model. Derive never
// stores anything; a reader recomputes it per request (§1).
package progress

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Input is what one project's corpus and task set look like to the
// derivation. A reader fills it (Task 4); tests fill it by hand.
type Input struct {
	Project string
	Specs   []Spec
	Plans   []Plan
	Rally   *model.RallyBand
}

// Spec is one spec document and its sections, in document order.
type Spec struct {
	Doc      int64
	Ref      string
	Title    string
	Updated  time.Time
	Sections []Section
}

// Section is one heading of a spec: no body text, the derivation never reads it.
type Section struct {
	Anchor  string
	Heading string
	Depth   int
}

// Plan is one plan document: its covers edges, its requires list, and every
// task minted from it. Tasks includes abandoned ones; Derive drops them from
// every count and strip (§1.1).
type Plan struct {
	Doc      int64
	Ref      string
	Title    string
	Status   string // draft | accepted | superseded
	Covers   []Cover
	Requires []string
	Tasks    []Task
}

// Cover is one covers edge from a plan to a spec section.
type Cover struct {
	Spec   int64
	Anchor string
	Level  string // full | partial | none
}

// Task is one task minted from a plan.
type Task struct {
	ID, Title, State, Position string
}

// CoverState pairs a Cover with the state of the plan that made it, so
// SectionState is testable without building any plan at all.
type CoverState struct {
	Cover
	PlanRef string
	State   string
}

// sectionRank is §1.2's "furthest along" order, most complete first.
var sectionRank = []string{"built", "in_progress", "not_started", "no_record", "draft"}

// BarOrder is §2.1's section-bar order: sectionRank plus "unplanned" at the
// end. "bound" never appears — it is not owed (§1.4). It is exported because
// every renderer of the derived model orders section states by it.
var BarOrder = append(append([]string{}, sectionRank...), "unplanned")

// groupOrder is §1.3's fixed group order.
var groupOrder = []string{"active", "planning", "no_record", "built"}

// TaskClass is the one place §1.1's task classification lives; it reads the
// state machine rather than listing states, so a state added to
// model.TaskStates lands in a class without touching this function.
// TestTaskClassPartitionsTaskStates fails the build until someone names the
// new state's class. PlanState, Derive's counts and the task strip all call
// this; nothing else compares a task state to a literal.
func TaskClass(state string) string {
	if slices.Contains(model.SettableTaskStates, state) {
		return "landed"
	}
	if state == "abandoned" {
		return "abandoned"
	}
	if slices.Index(model.TaskStates, state) > slices.Index(model.TaskStates, "ready") {
		return "active"
	}
	return "unstarted"
}

// PlanState is §1.1's table: a plan's state from its document status and its
// minted tasks. Abandoned tasks are ignored.
func PlanState(status string, tasks []Task) string {
	switch status {
	case "draft":
		return "draft"
	case "superseded":
		return "built" // spent, 026 §2.1
	}
	var landed, active, unstarted int
	for _, t := range tasks {
		switch TaskClass(t.State) {
		case "landed":
			landed++
		case "active":
			active++
		case "unstarted":
			unstarted++
		}
	}
	total := landed + active + unstarted
	switch {
	case total == 0:
		return "no_record"
	case landed == total:
		return "built"
	case landed > 0 || active > 0:
		return "in_progress"
	default:
		return "not_started"
	}
}

// SectionState is §1.2: a section's state is the furthest-along state among
// its non-"none" covers. A section with no covers at all is unplanned; one
// whose covers are all "none" is bound (§1.4) — not owed, not drawn. Partial
// is set only when every remaining cover is at coverage "partial".
func SectionState(covers []CoverState) (state string, partial bool) {
	if len(covers) == 0 {
		return "unplanned", false
	}
	var active []CoverState
	for _, c := range covers {
		if c.Level != "none" {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return "bound", false
	}
	bestRank := len(sectionRank)
	partial = true
	for _, c := range active {
		if c.Level != "partial" {
			partial = false
		}
		if r := slices.Index(sectionRank, c.State); r >= 0 && r < bestRank {
			bestRank = r
			state = c.State
		}
	}
	return state, partial
}

// planInfo is a plan's state and derived strip, computed once and reused
// across every spec the plan covers.
type planInfo struct {
	Plan
	State        string
	TaskCells    []model.ProgressTask // abandoned tasks omitted, §2.3
	Landed, Open int                  // Open is active plus unstarted
}

// Derive turns one project's facts into model.ProjectProgress, holding every
// rule of §1.1 to §1.3.
func Derive(in Input) model.ProjectProgress {
	plans := make(map[int64]planInfo, len(in.Plans))
	for _, p := range in.Plans {
		pi := planInfo{Plan: p, State: PlanState(p.Status, p.Tasks)}
		for _, t := range p.Tasks {
			class := TaskClass(t.State)
			if class == "abandoned" {
				continue
			}
			if class == "landed" {
				pi.Landed++
			} else {
				pi.Open++
			}
			pi.TaskCells = append(pi.TaskCells, model.ProgressTask{
				ID: t.ID, Title: t.Title, State: t.State,
				Class: class, Position: t.Position,
			})
		}
		plans[p.Doc] = pi
	}

	byGroup := make(map[string][]model.ProgressSpec, len(groupOrder))
	barCounts := make(map[string]int, len(BarOrder))

	for _, spec := range in.Specs {
		coversByAnchor := make(map[string][]CoverState)
		coveringDocs := make(map[int64]bool)
		for _, p := range in.Plans {
			pi := plans[p.Doc]
			for _, c := range p.Covers {
				if c.Spec != spec.Doc {
					continue
				}
				coversByAnchor[c.Anchor] = append(coversByAnchor[c.Anchor], CoverState{
					Cover: c, PlanRef: p.Ref, State: pi.State,
				})
				if c.Level != "none" {
					coveringDocs[p.Doc] = true
				}
			}
		}

		var sections []model.ProgressSection
		var sawActive, sawPlanning, sawNoRecord bool
		unplannedCount := 0
		for _, sec := range spec.Sections {
			covers := coversByAnchor[sec.Anchor]
			state, partial := SectionState(covers)
			var coverPlans []string
			for _, c := range covers {
				if c.Level == "none" {
					continue
				}
				coverPlans = append(coverPlans, c.PlanRef)
			}
			sections = append(sections, model.ProgressSection{
				Anchor: sec.Anchor, Heading: sec.Heading, Depth: sec.Depth,
				State: state, Partial: partial, Plans: coverPlans,
			})
			if state == "bound" {
				continue // not owed: not counted, not a group signal
			}
			barCounts[state]++
			switch state {
			case "in_progress", "not_started":
				sawActive = true
			case "unplanned", "draft":
				sawPlanning = true
			case "no_record":
				sawNoRecord = true
			}
			if state == "unplanned" {
				unplannedCount++
			}
		}

		groupKey := "built"
		switch {
		case sawActive:
			groupKey = "active"
		case sawPlanning:
			groupKey = "planning"
		case sawNoRecord:
			groupKey = "no_record"
		}

		var docs []int64
		for d := range coveringDocs {
			docs = append(docs, d)
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i] < docs[j] })

		var specPlans []model.ProgressPlan
		var openPlans, draftPlans, noRecordPlans []string
		openTasks := 0
		for _, d := range docs {
			pi := plans[d]
			specPlans = append(specPlans, model.ProgressPlan{
				Doc: pi.Doc, Ref: pi.Ref, Title: pi.Title, Status: pi.Status,
				State: pi.State, Requires: pi.Requires, Tasks: pi.TaskCells,
				Landed: pi.Landed, Open: pi.Open,
			})
			switch pi.State {
			case "in_progress", "not_started":
				openPlans = append(openPlans, pi.Ref)
				openTasks += pi.Open
			case "draft":
				draftPlans = append(draftPlans, pi.Ref)
			case "no_record":
				noRecordPlans = append(noRecordPlans, pi.Ref)
			}
		}

		byGroup[groupKey] = append(byGroup[groupKey], model.ProgressSpec{
			Doc: spec.Doc, Ref: spec.Ref, Title: spec.Title, Updated: spec.Updated,
			Group:    groupKey,
			Next:     nextAct(openTasks, openPlans, draftPlans, unplannedCount, noRecordPlans),
			Sections: sections,
			Plans:    specPlans,
		})
	}

	out := model.ProjectProgress{Project: in.Project, Rally: in.Rally}
	for _, key := range groupOrder {
		specs := byGroup[key]
		sort.SliceStable(specs, func(i, j int) bool { return specs[i].Updated.After(specs[j].Updated) })
		out.Groups = append(out.Groups, model.ProgressGroup{Key: key, Specs: specs})
		switch key {
		case "active":
			out.Counts.Active = len(specs)
		case "planning":
			out.Counts.Planning = len(specs)
		case "no_record":
			out.Counts.NoRecord = len(specs)
		case "built":
			out.Counts.Built = len(specs)
		}
	}
	for _, state := range BarOrder {
		if n := barCounts[state]; n > 0 {
			out.Bar = append(out.Bar, model.ProgressSlice{State: state, Count: n})
		}
	}
	return out
}

// nextAct is §1.3's next-act ladder: the first rule that applies wins.
func nextAct(openTasks int, openPlans, draftPlans []string, unplannedCount int, noRecordPlans []string) model.ProgressAct {
	switch {
	case len(openPlans) > 0:
		return model.ProgressAct{
			Kind:  "in_progress",
			Text:  fmt.Sprintf("%d open task(s) in %s", openTasks, strings.Join(openPlans, ", ")),
			Plans: openPlans,
		}
	case len(draftPlans) > 0:
		return model.ProgressAct{
			Kind:  "draft",
			Text:  fmt.Sprintf("accept %s", strings.Join(draftPlans, ", ")),
			Plans: draftPlans,
		}
	case unplannedCount > 0:
		return model.ProgressAct{
			Kind: "unplanned",
			Text: fmt.Sprintf("%d section(s) unplanned", unplannedCount),
		}
	case len(noRecordPlans) > 0:
		return model.ProgressAct{
			Kind:  "no_record",
			Text:  fmt.Sprintf("no execution record for %s", strings.Join(noRecordPlans, ", ")),
			Plans: noRecordPlans,
		}
	default:
		return model.ProgressAct{Kind: "none", Text: "nothing outstanding"}
	}
}
