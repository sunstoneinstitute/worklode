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

// ListDeliverables calls GET /api/v1/projects/{id}/deliverables.
func (c *Client) ListDeliverables(ctx context.Context, project string) (model.DeliverableListResponse, []byte, error) {
	return doJSON[model.DeliverableListResponse](ctx, c, http.MethodGet,
		"/api/v1/projects/"+url.PathEscape(project)+"/deliverables", nil, "deliverable list")
}

// CreateDeliverable calls POST /api/v1/projects/{id}/deliverables, declaring
// a deliverable (spec 029 §3.1).
func (c *Client) CreateDeliverable(ctx context.Context, project string, in model.CreateDeliverableInput) (model.Deliverable, []byte, error) {
	return doJSON[model.Deliverable](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/deliverables", in, "deliverable")
}

// DeliverableTable prints deliverables with their reported state, artifact
// address and milestone attachment, if any.
func DeliverableTable(w io.Writer, ds []model.Deliverable) {
	tbl := newTable(
		column{header: "ID"},
		titleColumn("NAME"),
		column{header: "STATE"},
		column{header: "ARTIFACT"},
		column{header: "MILESTONE"},
		column{header: "REPORTED"},
	)
	for _, d := range ds {
		state := d.ReportedState
		if state == "" {
			state = "declared"
		}
		reported := "-"
		if d.ReportedAt != nil {
			reported = LocalTime(*d.ReportedAt)
		}
		tbl.add(d.ID, d.Name, state, dash(d.Artifact), dash(d.Milestone), reported)
	}
	tbl.flush(w)
}
