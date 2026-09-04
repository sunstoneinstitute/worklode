// deliverables.go is the client and the rendering for spec 029 §3's
// deliverable: a declared, checkable output of a project.
package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// SetDeliverableMilestone calls PATCH /api/v1/deliverables/{id}, attaching
// (milestone non-empty) or detaching (milestone "") the deliverable.
func (c *Client) SetDeliverableMilestone(ctx context.Context, deliverable, milestone string) (model.Deliverable, []byte, error) {
	return doJSON[model.Deliverable](ctx, c, http.MethodPatch,
		"/api/v1/deliverables/"+url.PathEscape(deliverable), model.EditDeliverableInput{Milestone: &milestone}, "deliverable")
}

// DeliverableTable prints deliverables with their milestone attachment, if
// any.
func DeliverableTable(w io.Writer, ds []model.Deliverable) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "MILESTONE"},
		titleColumn("NAME"),
	)
	for _, d := range ds {
		milestone := d.Milestone
		if milestone == "" {
			milestone = "-"
		}
		tbl.add(d.ID, milestone, d.Name)
	}
	tbl.flush(w)
}
