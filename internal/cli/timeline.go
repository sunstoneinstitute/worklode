package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Timeline calls GET /api/v1/tasks/{id}/timeline.
func (c *Client) Timeline(ctx context.Context, taskID string) (model.TimelineResponse, []byte, error) {
	return doJSON[model.TimelineResponse](ctx, c, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(taskID)+"/timeline", nil, "timeline")
}

// TimelineRender prints one line per entry: timestamp, type, and a
// type-specific one-line summary.
func TimelineRender(w io.Writer, entries []model.TimelineEntry) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "TIME\tTYPE\tSUMMARY")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", LocalTime(e.At), e.Type, timelineSummary(e))
	}
	tw.Flush()
}

// timelineSummary builds a one-line, human-readable summary from a timeline
// entry's type-specific fields — see model.TimelineEntry for which fields
// each type populates.
func timelineSummary(e model.TimelineEntry) string {
	switch e.Type {
	case "state":
		// The change payload is a stored state_log row, not a shape this API
		// declares: "new" is a string for a field update and a list for the
		// secrets ones, so it is read key by key rather than decoded into a
		// struct (ADR 036 §3). Field "edge" (store.AddEdge/RemoveEdge) uses
		// op/type/from/to instead of old/new.
		var change map[string]any
		if json.Unmarshal(e.Change, &change) != nil {
			return ""
		}
		field, _ := change["field"].(string)
		if field == "edge" {
			op, _ := change["op"].(string)
			typ, _ := change["type"].(string)
			from, _ := change["from"].(string)
			to, _ := change["to"].(string)
			verb := "added"
			if op == "remove" {
				verb = "removed"
			}
			return fmt.Sprintf("edge %s: %s %s %s", verb, from, typ, to)
		}
		old, _ := change["old"].(string)
		nw, _ := change["new"].(string)
		if old != "" {
			return fmt.Sprintf("%s: %s -> %s", field, old, nw)
		}
		return fmt.Sprintf("%s: %s", field, nw)
	case "pr":
		return fmt.Sprintf("%s#%d %q (%s)", e.Repo, e.Number, e.Title, e.State)
	case "ci":
		conclusion := ""
		if e.Conclusion != nil {
			conclusion = *e.Conclusion
		}
		return fmt.Sprintf("%s %s: %s/%s", e.Repo, e.Workflow, e.Status, conclusion)
	case "review":
		return fmt.Sprintf("%s reviewed %s#%d: %s", e.Reviewer, e.Repo, e.Number, e.State)
	case "artifact":
		return fmt.Sprintf("%s %s %s", e.Kind, e.Name, e.Version)
	case "deployment":
		return fmt.Sprintf("%s/%s: %s", e.Environment, e.TargetName, e.Status)
	case "runtime":
		return fmt.Sprintf("%s on %s: %s", e.Kind, e.Workload, e.Message)
	case "landed":
		return fmt.Sprintf("%s %s on main", e.Repo, e.SHA)
	case "deployed":
		return fmt.Sprintf("%s confirmed in %s", e.Repo, e.Environment)
	case "released":
		return fmt.Sprintf("%s %s", e.Repo, e.Tag)
	default:
		return ""
	}
}
