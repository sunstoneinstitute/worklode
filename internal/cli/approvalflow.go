// approvalflow.go is the client and rendering side of applying an approval
// flow to a project (029 §7.2).

package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ApplyApprovalFlow calls POST /api/v1/projects/{id}/approval-flow: stamp the
// named flow on the project and backfill the requirements it demands of what
// the project already holds. reviewers is optional (lane -> actor id). A name
// the instance does not define comes back as a *ClientError with Status 404 —
// the flow set is server configuration, so the client never guesses at it.
func (c *Client) ApplyApprovalFlow(ctx context.Context, project, name string, reviewers map[string]string) (model.ApplyApprovalFlowResponse, []byte, error) {
	return doJSON[model.ApplyApprovalFlowResponse](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/approval-flow",
		model.ApplyApprovalFlowInput{Name: name, Reviewers: reviewers}, "approval flow")
}

// ApprovalFlowRender writes the one-line confirmation of an apply: which flow
// at which rev now governs the project, and how many requirements the
// backfill actually materialized (zero on a re-apply).
func ApprovalFlowRender(w io.Writer, resp model.ApplyApprovalFlowResponse) {
	noun := "requirements"
	if resp.Materialized == 1 {
		noun = "requirement"
	}
	fmt.Fprintf(w, "applied %s rev %s to %s%s: %d %s materialized\n",
		resp.Project.ApprovalFlowName, resp.Project.ApprovalFlowRev,
		resp.Project.ID, KeySuffix(resp.Project.Key), resp.Materialized, noun)
}
