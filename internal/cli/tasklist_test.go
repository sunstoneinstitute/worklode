package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// tasklistStub records the request URI of the last call and answers with an
// empty task list — enough to pin how a filter is spelled on the wire.
func tasklistStub(t *testing.T) (*cli.Client, *string) {
	t.Helper()
	var uri string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tasks":[]}`))
	}))
	t.Cleanup(srv.Close)
	return cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "t"}), &uri
}

// TestListTasksDocFilters pins that both document filters reach the server
// under the parameter names the API parses (025 §9.2, §15.4): PlanDoc as
// plan_doc, AboutDoc as about_doc. A zero id must not leak the parameter at
// all — the server refuses a non-positive one.
func TestListTasksDocFilters(t *testing.T) {
	c, uri := tasklistStub(t)
	ctx := context.Background()

	if _, _, err := c.ListTasks(ctx, cli.TaskListFilter{AboutDoc: 12}); err != nil {
		t.Fatalf("ListTasks about_doc: %v", err)
	}
	if *uri != "/api/v1/tasks?about_doc=12" {
		t.Fatalf("ListTasks AboutDoc URI = %q, want /api/v1/tasks?about_doc=12", *uri)
	}

	if _, _, err := c.ListTasks(ctx, cli.TaskListFilter{PlanDoc: 7, AboutDoc: 12}); err != nil {
		t.Fatalf("ListTasks both doc filters: %v", err)
	}
	if *uri != "/api/v1/tasks?about_doc=12&plan_doc=7" {
		t.Fatalf("ListTasks both doc filters URI = %q", *uri)
	}

	if _, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "wl"}); err != nil {
		t.Fatalf("ListTasks unfiltered: %v", err)
	}
	if *uri != "/api/v1/tasks?project=wl" {
		t.Fatalf("ListTasks URI = %q, want no document parameters", *uri)
	}
}
