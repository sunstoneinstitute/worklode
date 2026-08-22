package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- graph --------------------------------------------------------------

// ProjectionFailures calls GET /api/v1/graph/projection/failures: the
// projects the knowledge-graph projector has quarantined, oldest failure
// first (006 §11).
func (c *Client) ProjectionFailures(ctx context.Context) (model.ProjectionFailureListResponse, []byte, error) {
	return doJSON[model.ProjectionFailureListResponse](ctx, c, http.MethodGet, "/api/v1/graph/projection/failures", nil, "projection failure list")
}

// ProjectionFailureTable lists the projects the knowledge-graph projector has
// quarantined. RETRY is the floor on the next attempt, not a schedule: fresh
// activity in the project makes it dirty and re-attempts it immediately, so a
// time in the past means "due, and the projector has not run since".
func ProjectionFailureTable(w io.Writer, failures []model.ProjectionFailure) {
	if len(failures) == 0 {
		fmt.Fprintln(w, "no projects are quarantined")
		return
	}
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "PROJECT\tATTEMPTS\tSTUCK SINCE\tLAST FAILED\tRETRY\tERROR")
	for _, f := range failures {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n",
			f.Project, f.Attempts, LocalTime(f.FirstFailedAt), LocalTime(f.LastFailedAt),
			LocalTime(f.NextAttemptAt), strings.Join(strings.Fields(f.LastError), " "))
	}
	tw.Flush()
}
