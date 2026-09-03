package store

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestComputeMilestoneProgress(t *testing.T) {
	for _, tt := range []struct {
		name        string
		tasks, dels []string
		want        model.MilestoneProgress
	}{
		{"empty milestone", nil, nil, model.MilestoneProgress{}},
		{"open tasks only",
			[]string{"draft", "ready", "in_progress", "in_review"}, nil,
			model.MilestoneProgress{TasksTotal: 4}},
		{"every delivered state counts closed, abandoned included",
			[]string{"merged", "deployed_dev", "deployed_prod", "released", "abandoned", "ready"}, nil,
			model.MilestoneProgress{TasksTotal: 6, TasksClosed: 5}},
		{"published and updated are live; the rest are not",
			nil, []string{"published", "updated", "deprecated", "removed", "failed", ""},
			model.MilestoneProgress{DeliverablesTotal: 6, DeliverablesLive: 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeMilestoneProgress(tt.tasks, tt.dels); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
