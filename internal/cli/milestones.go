// milestones.go is the client and the rendering for spec 029 §2's milestone:
// one ordered container in a project, holding tasks and deliverables.
package cli

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// CreateMilestone calls POST /api/v1/projects/{id}/milestones.
func (c *Client) CreateMilestone(ctx context.Context, project string, in model.CreateMilestoneInput) (model.Milestone, []byte, error) {
	return doJSON[model.Milestone](ctx, c,
		http.MethodPost, "/api/v1/projects/"+project+"/milestones", in, "milestone")
}

// MilestoneTable prints milestones in position order. Progress is derived on
// read, so a milestone with no children shows a dash rather than "0/0".
func MilestoneTable(w io.Writer, ms []model.Milestone) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "POS"},
		column{header: "TASKS"},
		column{header: "OUTPUTS"},
		titleColumn("TITLE"),
	)
	for _, m := range ms {
		tbl.add(m.ID, strconv.Itoa(m.Position),
			progressCell(m.Progress.TasksClosed, m.Progress.TasksTotal),
			progressCell(m.Progress.DeliverablesLive, m.Progress.DeliverablesTotal),
			m.Title)
	}
	tbl.flush(w)
}

// progressCell renders one of a milestone's derived buckets as "closed/total",
// or a dash when the milestone holds nothing of that kind.
func progressCell(done, total int) string {
	if total == 0 {
		return "-"
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(total)
}
