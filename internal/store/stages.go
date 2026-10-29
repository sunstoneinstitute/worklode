package store

import (
	"cmp"
	"slices"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// StageWork is what the derivation reads about the project's work.
type StageWork struct {
	OpenTasks            []OpenTask // id and created_at of every open task
	Deliverables         int
	TerminalDeliverables int
}

// OpenTask is an open task's id and creation time.
type OpenTask struct {
	ID        string
	CreatedAt time.Time
}

// DeriveStage derives a project's Sunstone stage (WL-REQ-119) from its
// decisions (oldest first) and open work. Carryover is open tasks created
// before the latest transition, ordered by numeric id. Nothing is stored.
func DeriveStage(horizon string, decisions []model.StageDecision, work StageWork) model.StageView {
	v := model.StageView{History: decisions, Carryover: []string{}}
	if n := len(decisions); n > 0 {
		last := decisions[n-1]
		v.Stage = last.Stage
		for _, t := range work.OpenTasks {
			if t.CreatedAt.Before(last.DecidedAt) {
				v.Carryover = append(v.Carryover, t.ID)
			}
		}
		slices.SortFunc(v.Carryover, func(a, b string) int {
			return cmp.Or(cmp.Compare(numericTaskID(a), numericTaskID(b)), cmp.Compare(a, b))
		})
	}
	v.RecommendClosure = horizon == "bounded" && work.Deliverables > 0 &&
		work.TerminalDeliverables == work.Deliverables && len(work.OpenTasks) == 0
	return v
}
