package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- projects ---------------------------------------------------------

// CreateProject calls POST /api/v1/projects.
func (c *Client) CreateProject(ctx context.Context, in model.CreateProjectInput) (model.Project, []byte, error) {
	return doJSON[model.Project](ctx, c, http.MethodPost, "/api/v1/projects", in, "project")
}

// ListProjects calls GET /api/v1/projects.
func (c *Client) ListProjects(ctx context.Context) (model.ProjectListResponse, []byte, error) {
	return doJSON[model.ProjectListResponse](ctx, c, http.MethodGet, "/api/v1/projects", nil, "project list")
}

// SetProjectFocus calls PATCH /api/v1/projects/{id} with the ordered focus
// list and returns the updated project. focus is always sent non-nil (an
// empty slice clears the focus) since the server rejects a missing/null
// focus with 422.
func (c *Client) SetProjectFocus(ctx context.Context, id string, focus []string) (model.Project, []byte, error) {
	if focus == nil {
		focus = []string{}
	}
	return c.patchProject(ctx, id, model.PatchProjectInput{Focus: &focus})
}

// PinProjectFocus calls PATCH /api/v1/projects/{id} to set (or clear) the
// curated pinned-focus card and returns the updated project. An empty note
// clears the card; pinnedBy is an actor id or a plain display name. The fields
// are always sent, so the server reads note:"" as an explicit clear.
func (c *Client) PinProjectFocus(ctx context.Context, id, note, pinnedBy string) (model.Project, []byte, error) {
	return c.patchProject(ctx, id, model.PatchProjectInput{
		FocusNote:     &note,
		FocusPinnedBy: &pinnedBy,
	})
}

// SetProjectNextDecision calls PATCH /api/v1/projects/{id} to set (or clear)
// the curated next-decision card and returns the updated project. An empty
// title clears the card. The fields are always sent, so the server reads
// title:"" as an explicit clear.
func (c *Client) SetProjectNextDecision(ctx context.Context, id, title, accountable, readiness string) (model.Project, []byte, error) {
	return c.patchProject(ctx, id, model.PatchProjectInput{
		DecisionTitle:       &title,
		DecisionAccountable: &accountable,
		DecisionReadiness:   &readiness,
	})
}

// patchProject PATCHes in to /api/v1/projects/{id} and decodes the updated
// project it returns, shared by the project-mutation client methods.
func (c *Client) patchProject(ctx context.Context, id string, in model.PatchProjectInput) (model.Project, []byte, error) {
	return doJSON[model.Project](ctx, c, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(id), in, "project")
}

// ProjectDetail calls GET /api/v1/projects/{id}. A zero from or to leaves
// that end of the cost window unbounded.
func (c *Client) ProjectDetail(ctx context.Context, id string, from, to time.Time) (model.ProjectDetail, []byte, error) {
	q := url.Values{}
	if !from.IsZero() {
		q.Set("from", from.Format(time.DateOnly))
	}
	if !to.IsZero() {
		q.Set("to", to.Format(time.DateOnly))
	}
	return doJSON[model.ProjectDetail](ctx, c, http.MethodGet, withQuery("/api/v1/projects/"+url.PathEscape(id), q), nil, "project detail")
}

// ReportProjectSessionUsage calls POST /api/v1/projects/{id}/session-usage:
// report one session's complete usage across the project, every task it
// billed plus the remainder, replaced together (spec 052 §2).
func (c *Client) ReportProjectSessionUsage(ctx context.Context, projectID string, in model.ProjectSessionUsageInput) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/session-usage", in)
	return err
}

// GetProject returns one project by id, or a *ClientError with Status 404 if
// no such project exists. There is no single-project GET endpoint, so this
// filters the project list.
func (c *Client) GetProject(ctx context.Context, id string) (model.Project, error) {
	resp, _, err := c.ListProjects(ctx)
	if err != nil {
		return model.Project{}, err
	}
	for _, p := range resp.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Project{}, &ClientError{Status: http.StatusNotFound, Msg: "project not found: " + id}
}

// ResolveRemote calls GET /api/v1/projects/resolve, returning the project the
// given git remote URL maps to. The URL is sent exactly as git reported it —
// the server owns normalization — and a *ClientError with Status 404 means
// the repo is not mapped to any project.
func (c *Client) ResolveRemote(ctx context.Context, remote string) (model.Project, error) {
	q := url.Values{}
	q.Set("remote", remote)
	p, _, err := doJSON[model.Project](ctx, c, http.MethodGet, withQuery("/api/v1/projects/resolve", q), nil, "project")
	return p, err
}

// AddRepo calls POST /api/v1/projects/{id}/repos. An empty doneState leaves
// the mapping at the server's default terminal delivery state.
func (c *Client) AddRepo(ctx context.Context, projectID, repo, doneState string) (model.AddRepoResult, []byte, error) {
	return doJSON[model.AddRepoResult](ctx, c, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/repos", model.AddRepoInput{Repo: repo, DoneState: doneState}, "add-repo response")
}

// ReposDoctor calls GET /api/v1/repos/doctor. An empty repo reports every
// mapped repo. Admin-only on the server.
func (c *Client) ReposDoctor(ctx context.Context, repo string) (model.ReposDoctorResponse, []byte, error) {
	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	return doJSON[model.ReposDoctorResponse](ctx, c, http.MethodGet, withQuery("/api/v1/repos/doctor", q), nil, "repos doctor")
}

// Reconcile calls POST /api/v1/reconcile and returns the run report.
// Admin-only on the server; synchronous.
func (c *Client) Reconcile(ctx context.Context, in model.ReconcileInput) (model.ReconcileResponse, []byte, error) {
	return doJSON[model.ReconcileResponse](ctx, c, http.MethodPost, "/api/v1/reconcile", in, "reconcile")
}

// AddCrewMember calls POST /api/v1/projects/{id}/participants, adding one
// role-labelled Crew member (spec 029 §6.1). An empty role means "member";
// the returned member carries every role that actor holds on the project,
// not just the one just added. Deputy marks the member as the project's one
// deputy; it is mutually exclusive with lead.
func (c *Client) AddCrewMember(ctx context.Context, project, actor, role string, lead, deputy bool) (model.CrewMember, []byte, error) {
	return doJSON[model.CrewMember](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/participants",
		model.AddCrewMemberInput{Actor: actor, Role: role, Lead: lead, Deputy: deputy}, "crew member")
}

// ListCrew calls GET /api/v1/projects/{id}/participants: every member of a
// project's Crew (spec 029 §6.1), lead-first then by when they were added.
// An empty roster is an empty slice, not nil.
func (c *Client) ListCrew(ctx context.Context, project string) ([]model.CrewMember, []byte, error) {
	resp, raw, err := doJSON[model.ParticipantListResponse](ctx, c, http.MethodGet,
		"/api/v1/projects/"+url.PathEscape(project)+"/participants", nil, "participant list")
	if err != nil {
		return nil, nil, err
	}
	return resp.Participants, raw, nil
}

// RemoveCrewMember calls DELETE /api/v1/projects/{id}/participants/{actor},
// removing every role that actor holds on the project in one act (spec 029
// §6.1). The server answers 204 with no body, so the returned raw bytes are
// always empty; it is returned anyway to match AddCrewMember/ListCrew's
// shape, letting the caller's --json path print via printRaw the same way.
// A removal refused because the member still owns open work comes back as a
// *ClientError whose message names each item, so the caller can print the
// responsibility list as the server wrote it.
func (c *Client) RemoveCrewMember(ctx context.Context, project, actor string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete,
		"/api/v1/projects/"+url.PathEscape(project)+"/participants/"+url.PathEscape(actor), nil)
}

// SetRepoDoneState calls PATCH /api/v1/repos/{owner}/{name} (204, no body),
// setting the terminal delivery state for an already-mapped repo.
func (c *Client) SetRepoDoneState(ctx context.Context, repo, doneState string) ([]byte, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	return c.do(ctx, http.MethodPatch,
		"/api/v1/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name),
		model.SetRepoDoneStateInput{DoneState: doneState})
}

// ProjectTable prints one row per project: id, key, name, repos. Each repo is
// rendered as "owner/name (done_state)".
func ProjectTable(w io.Writer, projects []model.Project) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tKEY\tNAME\tREPOS")
	for _, p := range projects {
		repos := make([]string, 0, len(p.Repos))
		for _, m := range p.Repos {
			repos = append(repos, fmt.Sprintf("%s (%s)", m.Repo, m.DoneState))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Key, p.Name, strings.Join(repos, ", "))
	}
	tw.Flush()
}

// ProjectDetailRender prints `lode project show`: the project's identity,
// focus, and repos, then one cost block per currency. window is the human
// label for the cost period.
func ProjectDetailRender(w io.Writer, d model.ProjectDetail, window string) {
	fmt.Fprintf(w, "%s%s — %s\n", d.ID, KeySuffix(d.Key), d.Name)
	FocusLine(w, d.Focus)
	if len(d.Repos) > 0 {
		fmt.Fprintln(w, "repos:")
		tw := newTabwriter(w)
		for _, r := range d.Repos {
			fmt.Fprintf(tw, "  %s\tdone: %s\n", r.Repo, r.DoneState)
		}
		tw.Flush()
	}
	CostRender(w, d.Cost, window)
}

// FocusLine writes the "focus: a, b" (or "focus: (none)") line for a project's
// ranking focus.
func FocusLine(w io.Writer, focus []string) {
	if len(focus) == 0 {
		fmt.Fprintln(w, "focus: (none)")
		return
	}
	fmt.Fprintf(w, "focus: %s\n", strings.Join(focus, ", "))
}

// ReposDoctorRender prints `lode project doctor`: per mapped repo, whether the
// GitHub App check ran and what it found, when the last delivery arrived, and
// the reconcile hint for a repo that has never delivered. Senders that map to
// no project follow.
func ReposDoctorRender(w io.Writer, resp model.ReposDoctorResponse) {
	for _, r := range resp.Repos {
		// A nil app_installed means the check did not run; the reason is in
		// app_error when there is one, and its absence means no GitHub App is
		// configured at all.
		app := "unchecked (no GitHub App configured)"
		switch {
		case r.AppInstalled == nil && r.AppError != "":
			app = "unchecked (" + r.AppError + ")"
		case r.AppInstalled != nil && *r.AppInstalled:
			app = "installed"
		case r.AppInstalled != nil:
			app = "NOT INSTALLED (" + r.AppError + ")"
		}
		last := "never"
		if r.LastEventAt != nil {
			last = LocalTime(*r.LastEventAt)
		}
		fmt.Fprintf(w, "%s (project %s)\n", r.Repo, r.Project)
		fmt.Fprintf(w, "  app:        %s\n", app)
		fmt.Fprintf(w, "  last event: %s (types: %s)\n", last, strings.Join(r.EventTypes, ", "))
		fmt.Fprintf(w, "  unapplied:  %d\n", r.UnappliedEvents)
		if r.Stale {
			fmt.Fprintf(w, "  STALE: no delivery since mapping — run `lode task reconcile --repo %s`\n", r.Repo)
		}
	}
	for _, u := range resp.UnmappedSenders {
		fmt.Fprintf(w, "unmapped sender: %s (%d events, last %s)\n",
			u.Repo, u.Events, LocalTime(u.LastEventAt))
	}
}

// ReconcileRender prints `lode task reconcile`: the run id, what the replay pass
// repaired (or would repair, on a dry run), and what the poll pass did.
func ReconcileRender(w io.Writer, resp model.ReconcileResponse) {
	verb := "repaired"
	if resp.DryRun {
		verb = "would repair"
	}
	fmt.Fprintf(w, "run %s\n", resp.RunID)
	if resp.Replay != nil {
		fmt.Fprintf(w, "replay: %s %d of %d candidate event(s), %d still unmapped\n",
			verb, resp.Replay.Replayed, resp.Replay.Candidates, resp.Replay.StillUnmapped)
		for _, e := range resp.Replay.Errors {
			fmt.Fprintf(w, "  error: %s\n", e)
		}
		if n := resp.Replay.ErrorsOmitted; n > 0 {
			fmt.Fprintf(w, "  ... and %d more error(s), not reported\n", n)
		}
		if resp.Replay.Truncated {
			fmt.Fprintf(w, "  batch full: more candidates remain, run again\n")
		}
	}
	switch {
	case resp.PollSkipped != "":
		fmt.Fprintf(w, "poll: skipped (%s)\n", resp.PollSkipped)
	case resp.PollError != "":
		// Replay's report above still stands; only engine 2 failed.
		fmt.Fprintf(w, "poll: failed (%s)\n", resp.PollError)
	case resp.Poll != nil:
		// "observed" rather than verb: a candidate line lists the facts the
		// run found on GitHub for that task, not the subset that was new.
		fmt.Fprintf(w, "poll: %s %d candidate task(s)\n", pollVerb(resp.Poll.DryRun), resp.Poll.Candidates)
		for _, rep := range resp.Poll.Repaired {
			fmt.Fprintf(w, "  %s (%s, was %s): %d PR(s), %d landed commit(s)\n",
				rep.TaskID, rep.Repo, rep.State, len(rep.PRsUpdated), len(rep.CommitsLanded))
		}
		for _, e := range resp.Poll.Errors {
			fmt.Fprintf(w, "  error: %s\n", e)
		}
	}
}

// pollVerb keeps the poll line's dry-run marker on the poll's own DryRun,
// not the response's: they agree today, and a reader must not have to know
// that to trust the line.
func pollVerb(dryRun bool) string {
	if dryRun {
		return "examined (dry run)"
	}
	return "examined"
}

// CrewTable renders a project's Crew roster: name, roles comma-joined, and a
// "lead" marker on the row of the project's one lead, if any.
func CrewTable(w io.Writer, members []model.CrewMember) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ACTOR\tNAME\tROLES\tLEAD")
	for _, m := range members {
		lead := ""
		if m.Lead {
			lead = "lead"
		}
		// display_name is nullable; the web page falls back to the actor id
		// (internal/ui/crew.templ), so the CLI table matches it rather than
		// printing a blank NAME cell.
		name := m.DisplayName
		if name == "" {
			name = m.Actor
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Actor, name, strings.Join(m.Roles, ", "), lead)
	}
	tw.Flush()
}
