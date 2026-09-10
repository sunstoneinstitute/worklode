// ladder.go is the client and the view for `lode task gap` and `lode task
// fix` (025 §15.5): the escalation ladder's non-escalating rungs.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// RecordGap calls POST /api/v1/tasks/{id}/gap: log that the plan or spec
// does not cover this case, without stopping to escalate it.
func (c *Client) RecordGap(ctx context.Context, id string, in model.GapTaskInput) (model.LadderEventResult, []byte, error) {
	return doJSON[model.LadderEventResult](ctx, c,
		http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/gap", in, "gap")
}

// RecordFix calls POST /api/v1/tasks/{id}/fix: log one phase (started or
// finished) of closing a design fix.
func (c *Client) RecordFix(ctx context.Context, id string, in model.FixTaskInput) (model.LadderEventResult, []byte, error) {
	return doJSON[model.LadderEventResult](ctx, c,
		http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/fix", in, "fix")
}

// GapRender writes the one-line confirmation for `lode task gap`.
func GapRender(w io.Writer, taskID string, res model.LadderEventResult) {
	if res.Recorded {
		fmt.Fprintf(w, "%s: gap recorded\n", taskID)
		return
	}
	fmt.Fprintf(w, "%s: gap already recorded\n", taskID)
}

// FixRender writes the one-line confirmation for `lode task fix`.
func FixRender(w io.Writer, taskID, phase string, res model.LadderEventResult) {
	if res.Recorded {
		fmt.Fprintf(w, "%s: fix %s recorded\n", taskID, phase)
		return
	}
	fmt.Fprintf(w, "%s: fix %s already recorded\n", taskID, phase)
}
