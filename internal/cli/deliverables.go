// deliverables.go is the client and the rendering for spec 029 §3's
// deliverable: a declared, checkable output of a project.
package cli

import (
	"context"
	"fmt"
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

// GetDeliverable calls GET /api/v1/deliverables/{id}.
func (c *Client) GetDeliverable(ctx context.Context, id string) (model.Deliverable, []byte, error) {
	return doJSON[model.Deliverable](ctx, c,
		http.MethodGet, "/api/v1/deliverables/"+url.PathEscape(id), nil, "deliverable")
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

// ReportDeliverable calls POST /api/v1/deliverables/{id}/report, filing the
// state the caller says they see as user-reported evidence (029 §3.2).
func (c *Client) ReportDeliverable(ctx context.Context, id string, in model.ReportDeliverableInput) (model.Deliverable, []byte, error) {
	return doJSON[model.Deliverable](ctx, c, http.MethodPost,
		"/api/v1/deliverables/"+url.PathEscape(id)+"/report", in, "deliverable")
}

// DeliverableReportRender prints the one-line confirmation of a user report.
// It names the provenance because that is the whole point of the write: the
// state now on the deliverable is a person's claim, not an observed fact.
func DeliverableReportRender(w io.Writer, d model.Deliverable) {
	fmt.Fprintf(w, "reported %s %s (user-reported)\n", d.ID, d.ReportedState)
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

// DeliverableRender prints one deliverable's detail: the `lode show
// <deliverable>` view. ReportedState is "declared" until something reports
// against Artifact (029 §3.2) — the deliverable itself carries no state of
// its own.
func DeliverableRender(w io.Writer, d model.Deliverable) {
	fmt.Fprintf(w, "%s  %s\n", d.ID, d.Name)
	fmt.Fprintf(w, "  project:   %s\n", d.Project)
	fmt.Fprintf(w, "  milestone: %s\n", dash(d.Milestone))
	fmt.Fprintf(w, "  artifact:  %s\n", dash(d.Artifact))
	fmt.Fprintf(w, "  url:       %s\n", dash(d.URL))
	state := d.ReportedState
	if state == "" {
		state = "declared"
	}
	if d.ReportedAt != nil {
		fmt.Fprintf(w, "  state:     %s (reported %s)\n", state, LocalTime(*d.ReportedAt))
	} else {
		fmt.Fprintf(w, "  state:     %s\n", state)
	}
	fmt.Fprintf(w, "  created:   %s by %s\n", LocalTime(d.CreatedAt), dash(d.CreatedBy))
	fmt.Fprintf(w, "  updated:   %s\n", LocalTime(d.UpdatedAt))
	if d.Description != "" {
		fmt.Fprintf(w, "\n%s\n", d.Description)
	}
}
