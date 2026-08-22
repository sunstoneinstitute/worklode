package cli_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestClientProjectsAndRepos(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()

	p, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "proj" || p.Name != "Project" || p.Key != "WL" {
		t.Fatalf("CreateProject result = %+v", p)
	}

	if _, _, err := c.AddRepo(ctx, "proj", "acme/widgets", ""); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	list, _, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	want := model.RepoMapping{Repo: "acme/widgets", DoneState: "merged"}
	if len(list.Projects) != 1 || len(list.Projects[0].Repos) != 1 || list.Projects[0].Repos[0] != want {
		t.Fatalf("ListProjects result = %+v", list.Projects)
	}

	if _, err := c.SetRepoDoneState(ctx, "acme/widgets", "released"); err != nil {
		t.Fatalf("SetRepoDoneState: %v", err)
	}
	list, _, err = c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects after SetRepoDoneState: %v", err)
	}
	if got := list.Projects[0].Repos[0].DoneState; got != "released" {
		t.Fatalf("done_state after SetRepoDoneState = %q, want released", got)
	}

	// Anything that is not two non-empty segments is rejected client-side and
	// never sent — one case per disjunct of the guard.
	for _, repo := range []string{"widgets", "acme/", "/widgets", ""} {
		t.Run("reject "+repo, func(t *testing.T) {
			_, err := c.SetRepoDoneState(ctx, repo, "released")
			if err == nil {
				t.Fatalf("SetRepoDoneState(%q): want error, got nil", repo)
			}
			var clientErr *cli.ClientError
			if errors.As(err, &clientErr) {
				t.Fatalf("SetRepoDoneState(%q) reached the server: %v", repo, err)
			}
			if !strings.Contains(err.Error(), "owner/name") {
				t.Fatalf("SetRepoDoneState(%q): error = %v, want it to mention owner/name", repo, err)
			}
		})
	}
}

func TestClientProjectFocus(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	set, _, err := c.SetProjectFocus(ctx, "proj", []string{"security", "completeness"})
	if err != nil {
		t.Fatalf("SetProjectFocus: %v", err)
	}
	if len(set.Focus) != 2 || set.Focus[0] != "security" || set.Focus[1] != "completeness" {
		t.Fatalf("SetProjectFocus result = %+v", set.Focus)
	}

	got, err := c.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Focus) != 2 || got.Focus[0] != "security" || got.Focus[1] != "completeness" {
		t.Fatalf("GetProject.Focus = %+v, want ordered [security completeness]", got.Focus)
	}

	cleared, _, err := c.SetProjectFocus(ctx, "proj", []string{})
	if err != nil {
		t.Fatalf("SetProjectFocus (clear): %v", err)
	}
	if len(cleared.Focus) != 0 {
		t.Fatalf("SetProjectFocus (clear) result = %+v, want empty", cleared.Focus)
	}

	got, err = c.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject after clear: %v", err)
	}
	if len(got.Focus) != 0 {
		t.Fatalf("GetProject.Focus after clear = %+v, want empty", got.Focus)
	}

	if _, err := c.GetProject(ctx, "nonexistent"); err == nil {
		t.Fatalf("GetProject unknown id: err = nil, want error")
	}
}

// TestClientProjectCuratedCards round-trips PinProjectFocus and
// SetProjectNextDecision through the real PATCH handler, verifying the values
// land in the store and that an empty value clears each card.
func TestClientProjectCuratedCards(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, _, err := c.PinProjectFocus(ctx, "proj", "Ship the cockpit", "stig"); err != nil {
		t.Fatalf("PinProjectFocus: %v", err)
	}
	if _, _, err := c.SetProjectNextDecision(ctx, "proj", "Pick a datastore", "stig", "blocked on benchmark"); err != nil {
		t.Fatalf("SetProjectNextDecision: %v", err)
	}

	p, err := st.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.FocusNote != "Ship the cockpit" || p.FocusPinnedBy != "stig" {
		t.Errorf("focus = {%q, %q}, want {Ship the cockpit, stig}", p.FocusNote, p.FocusPinnedBy)
	}
	if p.DecisionTitle != "Pick a datastore" || p.DecisionAccountable != "stig" ||
		p.DecisionReadiness != "blocked on benchmark" {
		t.Errorf("decision = {%q, %q, %q}, want {Pick a datastore, stig, blocked on benchmark}",
			p.DecisionTitle, p.DecisionAccountable, p.DecisionReadiness)
	}

	// Empty values clear each card.
	if _, _, err := c.PinProjectFocus(ctx, "proj", "", ""); err != nil {
		t.Fatalf("PinProjectFocus clear: %v", err)
	}
	if _, _, err := c.SetProjectNextDecision(ctx, "proj", "", "", ""); err != nil {
		t.Fatalf("SetProjectNextDecision clear: %v", err)
	}
	p, err = st.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject after clear: %v", err)
	}
	if p.FocusNote != "" || p.DecisionTitle != "" {
		t.Errorf("after clear focus_note=%q decision_title=%q, want both empty", p.FocusNote, p.DecisionTitle)
	}
}

func TestResolveRemoteSendsRawURL(t *testing.T) {
	var gotPath, gotRemote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRemote = r.URL.Query().Get("remote")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	p, err := c.ResolveRemote(context.Background(), "git@github.com:sunstoneinstitute/worklode.git")
	if err != nil {
		t.Fatalf("ResolveRemote: %v", err)
	}
	if gotPath != "/api/v1/projects/resolve" {
		t.Fatalf("path = %q; want /api/v1/projects/resolve", gotPath)
	}
	if gotRemote != "git@github.com:sunstoneinstitute/worklode.git" {
		t.Fatalf("remote = %q; want the raw URL unmodified", gotRemote)
	}
	if p.ID != "worklode" || p.Key != "WL" {
		t.Fatalf("project = %+v; want worklode/WL", p)
	}
}

func TestResolveRemoteNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"not found"}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	if _, err := c.ResolveRemote(context.Background(), "git@github.com:acme/nope.git"); err == nil {
		t.Fatal("ResolveRemote on an unmapped repo returned nil error")
	}
}
