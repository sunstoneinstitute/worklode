// rally.go is the client and view for a project's active rally: the task
// naming what to finish now, and the transitive tree of open tasks it is
// waiting on.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ProjectRally calls GET /api/v1/projects/{id}/rally. A *ClientError with
// Status 404 means the project has no active rally.
func (c *Client) ProjectRally(ctx context.Context, id string) (model.Rally, []byte, error) {
	return doJSON[model.Rally](ctx, c,
		http.MethodGet, "/api/v1/projects/"+url.PathEscape(id)+"/rally", nil, "rally")
}

// RallyRender prints the rally's id and title, then its open members as a
// tree — reusing BlockerTreeRender, since a rally's membership is exactly
// the blocker tree rooted at it.
func RallyRender(w io.Writer, rally model.Rally) {
	fmt.Fprintf(w, "%s  %s\n", rally.Task.ID, rally.Task.Title)
	BlockerTreeRender(w, rally.Blockers)
}
