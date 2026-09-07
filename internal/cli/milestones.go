// milestones.go is the client and the rendering for spec 029 §2's milestone:
// one ordered container in a project, holding tasks and deliverables.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// CreateMilestone calls POST /api/v1/projects/{id}/milestones.
func (c *Client) CreateMilestone(ctx context.Context, project string, in model.CreateMilestoneInput) (model.Milestone, []byte, error) {
	return doJSON[model.Milestone](ctx, c,
		http.MethodPost, "/api/v1/projects/"+project+"/milestones", in, "milestone")
}

// ListMilestones calls GET /api/v1/projects/{id}/milestones.
func (c *Client) ListMilestones(ctx context.Context, project string) (model.MilestoneListResponse, []byte, error) {
	return doJSON[model.MilestoneListResponse](ctx, c,
		http.MethodGet, "/api/v1/projects/"+url.PathEscape(project)+"/milestones", nil, "milestone list")
}

// GetMilestone calls GET /api/v1/milestones/{id}.
func (c *Client) GetMilestone(ctx context.Context, id string) (model.MilestoneDetail, []byte, error) {
	return doJSON[model.MilestoneDetail](ctx, c,
		http.MethodGet, "/api/v1/milestones/"+url.PathEscape(id), nil, "milestone")
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

// MilestoneRender prints one milestone with its progress and its children —
// the future `lode show <milestone>` view. Reuses TaskTable and
// DeliverableTable so a milestone's children render exactly like their own
// listings do.
func MilestoneRender(w io.Writer, d model.MilestoneDetail) {
	fmt.Fprintf(w, "%s  %s\n", d.ID, d.Title)
	fmt.Fprintf(w, "  project:  %s\n", d.Project)
	fmt.Fprintf(w, "  position: %d\n", d.Position)
	fmt.Fprintf(w, "  created:  %s by %s\n", LocalTime(d.CreatedAt), dash(d.CreatedBy))
	fmt.Fprintf(w, "  updated:  %s\n", LocalTime(d.UpdatedAt))
	fmt.Fprintf(w, "  progress: tasks %s, deliverables %s\n",
		progressCell(d.Progress.TasksClosed, d.Progress.TasksTotal),
		progressCell(d.Progress.DeliverablesLive, d.Progress.DeliverablesTotal))
	if len(d.Tasks) > 0 {
		fmt.Fprintln(w, "\ntasks:")
		TaskTable(w, d.Tasks)
	}
	if len(d.Deliverables) > 0 {
		fmt.Fprintln(w, "\ndeliverables:")
		DeliverableTable(w, d.Deliverables)
	}
}
