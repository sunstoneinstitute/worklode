// escalate.go is the client and the view for `lode task escalate` (025 §8.1).
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// EscalateTask calls POST /api/v1/tasks/{id}/escalate: hand the design defect
// up a tier, release the lease, and block this task on the fix.
func (c *Client) EscalateTask(ctx context.Context, id string, in model.EscalateTaskInput) (model.EscalateTaskResult, []byte, error) {
	return doJSON[model.EscalateTaskResult](ctx, c,
		http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/escalate", in, "escalation")
}

// EscalateRender writes the confirmation for one escalation of taskID: which
// design task now blocks it, whether that task was minted or already open, and
// who owes the fix.
func EscalateRender(w io.Writer, taskID string, res model.EscalateTaskResult) {
	if res.Minted == nil {
		fmt.Fprintf(w, "%s escalated; lease released\n", taskID)
		fmt.Fprintf(w, "  joined the open escalation %s, which now blocks %s\n", res.Joined, taskID)
		return
	}
	fmt.Fprintf(w, "%s escalated; lease released\n", taskID)
	fmt.Fprintf(w, "  %s: %s\n", res.Minted.ID, res.Minted.Title)
	if res.Minted.Assignee == "" {
		fmt.Fprintf(w, "  unassigned — the document's author is not on this project's crew\n")
	} else {
		fmt.Fprintf(w, "  assigned to %s\n", res.Minted.Assignee)
	}
	fmt.Fprintf(w, "  %s blocks %s until it closes\n", res.Minted.ID, taskID)
}
