package store

import (
	"slices"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestDeriveStage(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(d int) time.Time { return t0.AddDate(0, 0, d) }
	research := model.StageDecision{Stage: "research", Reason: "go", DecidedAt: at(1)}
	report := model.StageDecision{Stage: "report", Reason: "write", DecidedAt: at(10)}
	back := model.StageDecision{Stage: "research", Reason: "redo", DecidedAt: at(20)}

	cases := []struct {
		name      string
		horizon   string
		decisions []model.StageDecision
		work      StageWork
		stage     string
		history   int
		carryover []string
		closure   bool
	}{
		{name: "no decisions", horizon: "bounded",
			work: StageWork{OpenTasks: []OpenTask{{"WL-1", at(0)}}}},
		{name: "carryover only before latest decision", horizon: "standing",
			decisions: []model.StageDecision{research, report},
			work: StageWork{OpenTasks: []OpenTask{
				{"WL-10", at(5)}, {"WL-9", at(5)}, {"WL-11", at(12)}}},
			stage: "report", history: 2, carryover: []string{"WL-9", "WL-10"}},
		{name: "return to earlier stage", horizon: "standing",
			decisions: []model.StageDecision{research, report, back},
			stage:     "research", history: 3},
		{name: "closure recommended", horizon: "bounded",
			decisions: []model.StageDecision{research},
			work:      StageWork{Deliverables: 2, TerminalDeliverables: 2},
			stage:     "research", history: 1, closure: true},
		{name: "standing horizon", horizon: "standing",
			decisions: []model.StageDecision{research},
			work:      StageWork{Deliverables: 2, TerminalDeliverables: 2},
			stage:     "research", history: 1},
		{name: "zero deliverables", horizon: "bounded",
			decisions: []model.StageDecision{research},
			stage:     "research", history: 1},
		{name: "open task blocks closure", horizon: "bounded",
			decisions: []model.StageDecision{research},
			work: StageWork{Deliverables: 1, TerminalDeliverables: 1,
				OpenTasks: []OpenTask{{"WL-1", at(5)}}},
			stage: "research", history: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := DeriveStage(c.horizon, c.decisions, c.work)
			if v.Stage != c.stage || len(v.History) != c.history || v.RecommendClosure != c.closure {
				t.Fatalf("got stage=%q history=%d closure=%v", v.Stage, len(v.History), v.RecommendClosure)
			}
			if !slices.Equal(v.Carryover, c.carryover) {
				t.Fatalf("carryover = %v, want %v", v.Carryover, c.carryover)
			}
		})
	}
}
