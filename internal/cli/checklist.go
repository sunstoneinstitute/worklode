package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// GetChecklist calls GET /api/v1/tasks/{id}/checklist: the checklist items
// parsed out of the task's body, in order of appearance.
func (c *Client) GetChecklist(ctx context.Context, id string) ([]model.ChecklistItem, []byte, error) {
	return doJSON[[]model.ChecklistItem](ctx, c, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/checklist", nil, "checklist")
}

// SetChecklistItem calls POST /api/v1/tasks/{id}/checklist, checking or
// unchecking one item identified by ordinal (canonical) or title.
func (c *Client) SetChecklistItem(ctx context.Context, id string, in model.SetChecklistItemInput) (model.ChecklistItem, []byte, error) {
	return doJSON[model.ChecklistItem](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/checklist", in, "checklist item")
}

// ChecklistRender writes one row per checklist item: ordinal, checkbox mark,
// title.
func ChecklistRender(w io.Writer, items []model.ChecklistItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "no checklist items")
		return
	}
	tw := newTabwriter(w)
	for _, it := range items {
		mark := " "
		if it.Checked {
			mark = "x"
		}
		fmt.Fprintf(tw, "%d\t[%s]\t%s\n", it.Ordinal, mark, it.Title)
	}
	tw.Flush()
}
