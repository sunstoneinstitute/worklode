package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- inbox ------------------------------------------------------------

// ListIssues calls GET /api/v1/inbox. An empty state lists every triage
// state; an empty project lists every project's issues.
func (c *Client) ListIssues(ctx context.Context, state, project string) (model.IssueListResponse, []byte, error) {
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.IssueListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/inbox", q), nil, "issue list")
}

// PromoteIssue calls POST /api/v1/inbox/promote.
func (c *Client) PromoteIssue(ctx context.Context, in model.PromoteInput) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/inbox/promote", in, "task")
}

// DismissIssue calls POST /api/v1/inbox/dismiss (204, no body).
func (c *Client) DismissIssue(ctx context.Context, repo string, number int64) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/dismiss", model.DismissInput{Repo: repo, Number: number})
}

// LinkIssue calls POST /api/v1/inbox/link (204, no body): attach an inbox
// issue to a task that already exists.
func (c *Client) LinkIssue(ctx context.Context, repo string, number int64, taskID string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/link",
		model.LinkInput{Repo: repo, Number: number, TaskID: taskID})
}

// ImportInbox calls POST /api/v1/inbox/import.
func (c *Client) ImportInbox(ctx context.Context, in model.ImportInput) (model.ImportResult, []byte, error) {
	return doJSON[model.ImportResult](ctx, c, http.MethodPost, "/api/v1/inbox/import", in, "import response")
}

// IssueTable prints one row per inbox issue: repo, number, triage state,
// state, title.
func IssueTable(w io.Writer, issues []model.Issue) {
	tbl := newTable(
		column{header: "REPO"},
		column{header: "#"},
		column{header: "TRIAGE"},
		column{header: "STATE"},
		titleColumn("TITLE"),
	)
	for _, is := range issues {
		tbl.add(is.Repo, strconv.FormatInt(is.Number, 10), is.TriageState, is.State, is.Title)
	}
	tbl.flush(w)
}
