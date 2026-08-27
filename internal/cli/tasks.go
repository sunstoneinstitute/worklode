package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- tasks ----------------------------------------------------------------

// CreateTask calls POST /api/v1/tasks.
func (c *Client) CreateTask(ctx context.Context, in model.CreateTaskInput) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/tasks", in, "task")
}

// TaskListFilter narrows ListTasks. Zero-valued fields do not filter.
type TaskListFilter struct {
	Project  string
	States   []string
	Priority string
	Kind     string
	// Parent narrows to the direct children of this task id.
	Parent string
	// Assignee narrows to tasks assigned to this actor id.
	Assignee string
	// HasChildren narrows to containers — tasks with at least one child.
	HasChildren bool
	// Repo narrows to the project owning this repo. Any git remote URL form
	// works as well as owner/name; the server normalizes it.
	Repo string
	// PlanDoc narrows to the tasks minted from this plan document id (025
	// §9.2). 0 does not filter.
	PlanDoc int64
	// AboutDoc narrows to the tasks that reference this document id — the
	// review and planning tasks the doc-lifecycle watcher mints (025 §15.4).
	// 0 does not filter.
	AboutDoc int64
	// Deleted switches the list to tombstoned tasks (044 §5): they replace
	// the live ones rather than joining them, so a list never mixes the two.
	Deleted bool
}

// ListTasks calls GET /api/v1/tasks.
func (c *Client) ListTasks(ctx context.Context, f TaskListFilter) (model.TaskListResponse, []byte, error) {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	for _, s := range f.States {
		q.Add("state", s)
	}
	if f.Priority != "" {
		q.Set("priority", f.Priority)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Parent != "" {
		q.Set("parent", f.Parent)
	}
	if f.Assignee != "" {
		q.Set("assignee", f.Assignee)
	}
	if f.HasChildren {
		q.Set("has_children", "true")
	}
	if f.Repo != "" {
		q.Set("repo", f.Repo)
	}
	if f.PlanDoc != 0 {
		q.Set("plan_doc", strconv.FormatInt(f.PlanDoc, 10))
	}
	if f.AboutDoc != 0 {
		q.Set("about_doc", strconv.FormatInt(f.AboutDoc, 10))
	}
	if f.Deleted {
		q.Set("deleted", "true")
	}
	return doJSON[model.TaskListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/tasks", q), nil, "task list")
}

// TaskTreeFilter selects the hierarchy TaskTree returns. Root names a single
// container to report; Project and States narrow the whole-project form.
type TaskTreeFilter struct {
	Project string
	States  []string
	Root    string
}

// TaskTree calls GET /api/v1/tasks?tree=true: every container in scope with
// its progress and its direct children, in one request. The server assembles
// the tree so a client never fetches children per container.
func (c *Client) TaskTree(ctx context.Context, f TaskTreeFilter) (model.TaskTreeResponse, []byte, error) {
	q := url.Values{"tree": {"true"}}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	for _, s := range f.States {
		q.Add("state", s)
	}
	if f.Root != "" {
		q.Set("root", f.Root)
	}
	return doJSON[model.TaskTreeResponse](ctx, c, http.MethodGet, withQuery("/api/v1/tasks", q), nil, "task tree")
}

// GetTask calls GET /api/v1/tasks/{id}.
func (c *Client) GetTask(ctx context.Context, id string) (model.TaskDetail, []byte, error) {
	return doJSON[model.TaskDetail](ctx, c, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id), nil, "task")
}

// SetTaskSkills calls PUT /api/v1/tasks/{id}/skills, replacing the task's
// pinned skill names. A nil or empty skills clears existing pins.
func (c *Client) SetTaskSkills(ctx context.Context, id string, skills []string) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/api/v1/tasks/"+url.PathEscape(id)+"/skills",
		model.SetSkillsInput{Skills: skills})
}

// ClaimTask calls POST /api/v1/tasks/{id}/claim. worktree is the caller's
// worktree identity (required by the server); ttl <= 0 means the server
// default (2h).
func (c *Client) ClaimTask(ctx context.Context, id, worktree string, ttl time.Duration) (model.ClaimResponse, []byte, error) {
	in := model.ClaimInput{Worktree: worktree}
	if ttl > 0 {
		in.TTLSeconds = int(ttl.Seconds())
	}
	return doJSON[model.ClaimResponse](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/claim", in, "claim response")
}

// ClaimNext calls POST /api/v1/tasks/claim-next: rank the ready set
// server-side and atomically claim the top candidate. worktree is required
// unless DryRun is set. A "no ready task" or dry-run result is a normal
// (non-error) response — see model.ClaimNextResponse.
func (c *Client) ClaimNext(ctx context.Context, in model.ClaimNextInput) (model.ClaimNextResponse, []byte, error) {
	return doJSON[model.ClaimNextResponse](ctx, c, http.MethodPost, "/api/v1/tasks/claim-next", in, "claim-next response")
}

// Brief calls GET /api/v1/tasks/{id}/brief.
func (c *Client) Brief(ctx context.Context, id string) (model.Brief, []byte, error) {
	return c.brief(ctx, id, nil)
}

// BriefWithoutSkills is Brief with skills=false: the server skips pin
// resolution, the inlined pin bodies, and the embedding call. For callers
// that only read the task row or the lease, where a pinned brief is hundreds
// of kilobytes and up to a 2s round trip nobody reads.
func (c *Client) BriefWithoutSkills(ctx context.Context, id string) (model.Brief, []byte, error) {
	return c.brief(ctx, id, url.Values{"skills": {"false"}})
}

func (c *Client) brief(ctx context.Context, id string, q url.Values) (model.Brief, []byte, error) {
	return doJSON[model.Brief](ctx, c, http.MethodGet,
		withQuery("/api/v1/tasks/"+url.PathEscape(id)+"/brief", q), nil, "brief")
}

// RebindWorktree calls POST /api/v1/tasks/{id}/lease/worktree: move the
// caller's active lease on id to a new worktree identity. Returns the
// updated lease.
func (c *Client) RebindWorktree(ctx context.Context, id, worktree string) (model.Lease, []byte, error) {
	return doJSON[model.Lease](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/lease/worktree", model.RebindWorktreeInput{Worktree: worktree}, "lease")
}

// TouchAgentSession calls POST /api/v1/tasks/{id}/agent-session: report that
// this agent session is working id, or heartbeat an already-reported one.
//
// Usage is the session's spend so far; nil leaves whatever the server has
// recorded alone. Reporting it on a heartbeat is what gets a crashed or
// swept session's tokens onto the books at all, since only a clean end
// reports them otherwise.
func (c *Client) TouchAgentSession(ctx context.Context, id, agent, agentVersion, sessionID string, usage []model.SessionUsageBucket) (model.AgentSession, []byte, error) {
	in := model.AgentSessionInput{Agent: agent, AgentVersion: agentVersion, SessionID: sessionID, Usage: usage}
	return doJSON[model.AgentSession](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/agent-session", in, "agent session")
}

// EndAgentSession calls POST /api/v1/tasks/{id}/agent-session/end.
func (c *Client) EndAgentSession(ctx context.Context, id string, in model.EndAgentSessionInput) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session/end", in)
	return err
}

// Instruct calls POST /api/v1/tasks/{id}/instructions: queue a steering
// instruction against the task, delivered to whichever actor next holds its
// lease (migration 0056).
func (c *Client) Instruct(ctx context.Context, taskID, body string) (model.Instruction, []byte, error) {
	in := model.InstructionInput{Body: body}
	return doJSON[model.Instruction](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(taskID)+"/instructions", in, "instruction")
}

// ClaimInstructions calls POST /api/v1/instructions/claim: deliver every
// pending instruction queued against a task the caller currently leases.
func (c *Client) ClaimInstructions(ctx context.Context) (model.InstructionsResponse, []byte, error) {
	return doJSON[model.InstructionsResponse](ctx, c, http.MethodPost, "/api/v1/instructions/claim", nil, "instructions")
}

// EditTask calls PATCH /api/v1/tasks/{id}, sending only the fields set on in.
func (c *Client) EditTask(ctx context.Context, id string, in model.EditTaskInput) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), in, "task")
}

// RenewLease calls POST /api/v1/tasks/{id}/renew.
func (c *Client) RenewLease(ctx context.Context, id string, ttl time.Duration) (model.Lease, []byte, error) {
	in := model.RenewInput{}
	if ttl > 0 {
		in.TTLSeconds = int(ttl.Seconds())
	}
	return doJSON[model.Lease](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/renew", in, "lease")
}

// ReleaseLease calls POST /api/v1/tasks/{id}/release (204, no body).
func (c *Client) ReleaseLease(ctx context.Context, id string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/release", nil)
}

// ReacquireOrRenew re-acquires the lease on taskID for the worktree identity:
// renew when this worktree already holds it (including an expired lease still
// nominally ours), re-claim when no lease exists (the sweeper reclaimed it),
// and error when it is actively leased to a different worktree. lease is the
// current lease from a freshly-fetched brief (nil ⇒ none). This is the shared
// resume/auto-resume core used by both `lode resume` and the hook handlers.
func ReacquireOrRenew(ctx context.Context, c *Client, taskID, identity string, lease *model.Lease) error {
	switch {
	case lease == nil:
		if _, _, err := c.ClaimTask(ctx, taskID, identity, 0); err != nil {
			return fmt.Errorf("re-claim %s: %w", taskID, err)
		}
	case lease.Worktree == identity:
		if _, _, err := c.RenewLease(ctx, taskID, 0); err != nil {
			return fmt.Errorf("renew lease on %s: %w", taskID, err)
		}
	default:
		return fmt.Errorf("%s is actively leased to a different worktree (%s); refusing to resume", taskID, lease.Worktree)
	}
	return nil
}

// DoneTask calls POST /api/v1/tasks/{id}/done.
func (c *Client) DoneTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "done")
}

// ReportMerge calls POST /api/v1/merges: tell the backbone that sha landed on
// repo's default branch carrying the work of these tasks. repo may be any git
// remote URL form; the server normalizes it.
func (c *Client) ReportMerge(ctx context.Context, repo, sha string, tasks []string) (model.MergeReport, []byte, error) {
	return doJSON[model.MergeReport](ctx, c, http.MethodPost, "/api/v1/merges", model.MergeReportRequest{Repo: repo, SHA: sha, Tasks: tasks}, "merge report")
}

// AbandonTask calls POST /api/v1/tasks/{id}/abandon.
func (c *Client) AbandonTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "abandon")
}

// ReopenTask calls POST /api/v1/tasks/{id}/reopen: move a delivered or
// abandoned task back to ready (a fresh claim is then required).
func (c *Client) ReopenTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "reopen")
}

// DeleteTask calls DELETE /api/v1/tasks/{id}: tombstone the task (044 §2).
// The body is sent even when justification is empty — it marshals to `{}`,
// which the server reads as "none given". Whether that is acceptable depends
// on the instance environment and is the server's call alone (044 §3), so
// nothing is validated or prompted for here.
func (c *Client) DeleteTask(ctx context.Context, id, justification string) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id),
		model.DeleteInput{Justification: justification}, "task")
}

// UndeleteTask calls POST /api/v1/tasks/{id}/undelete: clear the tombstone.
// No justification on either instance environment (044 §3).
func (c *Client) UndeleteTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "undelete")
}

// ReadyTask calls PATCH /api/v1/tasks/{id} with state "ready": publish a
// draft task so it becomes claimable.
func (c *Client) ReadyTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.patchTaskState(ctx, id, "ready")
}

// ReworkTask calls PATCH /api/v1/tasks/{id} with state "in_progress": move a
// task under review back to in_progress after a review requested changes.
func (c *Client) ReworkTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.patchTaskState(ctx, id, "in_progress")
}

// SubmitTask calls PATCH /api/v1/tasks/{id} with state "in_review": move the
// caller's in_progress task to review.
func (c *Client) SubmitTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.patchTaskState(ctx, id, "in_review")
}

// AssignTask calls POST /api/v1/tasks/{id}/assign: sets the task's assignee.
// An empty assignee assigns the task to the calling actor.
func (c *Client) AssignTask(ctx context.Context, id, assignee string) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/assign", model.AssignInput{Assignee: assignee}, "task")
}

// UnassignTask calls POST /api/v1/tasks/{id}/unassign: clears the task's
// assignee.
func (c *Client) UnassignTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "unassign")
}

// StartTask calls POST /api/v1/tasks/{id}/start: moves the task to
// in_progress on behalf of the caller without taking a lease, assigning the
// caller when the task is unassigned.
func (c *Client) StartTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "start")
}

// StopTask calls POST /api/v1/tasks/{id}/stop: moves the caller's
// in_progress task back to ready, keeping the assignment.
func (c *Client) StopTask(ctx context.Context, id string) (model.Task, []byte, error) {
	return c.taskAction(ctx, id, "stop")
}

func (c *Client) patchTaskState(ctx context.Context, id, state string) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), model.EditTaskInput{State: &state}, "task")
}

func (c *Client) taskAction(ctx context.Context, id, action string) (model.Task, []byte, error) {
	return doJSON[model.Task](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/"+action, nil, "task")
}

// Block calls POST /api/v1/tasks/{id}/edges to record that by blocks id.
func (c *Client) Block(ctx context.Context, id, by string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges", model.EdgeInput{From: &by, Type: "blocks"})
}

// Unblock calls DELETE /api/v1/tasks/{id}/edges to remove the "by blocks id" edge.
func (c *Client) Unblock(ctx context.Context, id, by string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges", model.EdgeInput{From: &by, Type: "blocks"})
}

// Parent calls POST /api/v1/tasks/{id}/edges to file id under a parent.
func (c *Client) Parent(ctx context.Context, id, parent string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &parent, Type: "child_of"})
}

// Unparent calls DELETE /api/v1/tasks/{id}/edges to detach id from its parent.
func (c *Client) Unparent(ctx context.Context, id, parent string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &parent, Type: "child_of"})
}

// FollowUp calls POST /api/v1/tasks/{id}/edges to record that id was spun out
// of the work on origin.
func (c *Client) FollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &origin, Type: "follow_up_to"})
}

// UnfollowUp calls DELETE /api/v1/tasks/{id}/edges to drop the follow-up edge
// from id to origin.
func (c *Client) UnfollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &origin, Type: "follow_up_to"})
}

// Duplicate calls POST /api/v1/tasks/{id}/edges to record that id is the same
// request as canonical, which is the one to work.
func (c *Client) Duplicate(ctx context.Context, id, canonical string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &canonical, Type: "duplicate_of"})
}

// Unduplicate calls DELETE /api/v1/tasks/{id}/edges to drop the duplicate
// edge from id to canonical.
func (c *Client) Unduplicate(ctx context.Context, id, canonical string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		model.EdgeInput{To: &canonical, Type: "duplicate_of"})
}

// Decompose calls POST /api/v1/tasks/{id}/decompose: converts id into an
// parent and files titles as new children under it.
func (c *Client) Decompose(ctx context.Context, id string, titles []string) (model.DecomposeResponse, []byte, error) {
	return doJSON[model.DecomposeResponse](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/decompose", model.DecomposeInput{Into: titles}, "decompose response")
}

// TaskCost calls GET /api/v1/tasks/{id}/cost. A zero from or to leaves that
// end of the window unbounded.
func (c *Client) TaskCost(ctx context.Context, id string, children bool,
	from, to time.Time) (model.TaskCost, []byte, error) {

	q := url.Values{}
	if children {
		q.Set("children", "true")
	}
	if !from.IsZero() {
		q.Set("from", from.Format(time.DateOnly))
	}
	if !to.IsZero() {
		q.Set("to", to.Format(time.DateOnly))
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/tasks/"+url.PathEscape(id)+"/cost", q), nil)
	if err != nil {
		return model.TaskCost{}, nil, err
	}
	var tc model.TaskCost
	if err := json.Unmarshal(raw, &tc); err != nil {
		return model.TaskCost{}, nil, fmt.Errorf("decode task cost: %w", err)
	}
	return tc, raw, nil
}

// MintTaskToken calls POST /api/v1/tasks/{id}/tokens: a task-scoped token
// (001 §2.1). Zero values take the server defaults (actor "sandbox", the
// lease TTL).
func (c *Client) MintTaskToken(ctx context.Context, taskID string, in model.TaskTokenInput) (model.TaskTokenResponse, []byte, error) {
	return doJSON[model.TaskTokenResponse](ctx, c, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(taskID)+"/tokens", in, "task token")
}

// sessionStatus renders an agent session's lifecycle state for `task show`:
// "active" while running, "ended <ts>" once closed.
func sessionStatus(sess model.AgentSession) string {
	if sess.EndedAt != nil {
		return "ended " + LocalTime(*sess.EndedAt)
	}
	return "active"
}

// sessionTokens renders an agent session's input/output token counts, or
// "-" when neither has been reported yet (a session between claim and its
// first heartbeat).
func sessionTokens(sess model.AgentSession) string {
	if sess.InputTokens == nil && sess.OutputTokens == nil {
		return "-"
	}
	var in, out int64
	if sess.InputTokens != nil {
		in = *sess.InputTokens
	}
	if sess.OutputTokens != nil {
		out = *sess.OutputTokens
	}
	return fmt.Sprintf("%s in / %s out", HumanTokens(in), HumanTokens(out))
}

// sessionCost renders an agent session's recorded spend, or "-" before any
// cost has been reported.
func sessionCost(sess model.AgentSession) string {
	if sess.CostAmount == nil {
		return "-"
	}
	return fmt.Sprintf("%s %s", Money(*sess.CostAmount), sess.CostCurrency)
}

// TaskTable prints one row per task: id, priority, kind, state, project,
// assignee (- when unassigned), title.
func TaskTable(w io.Writer, tasks []model.Task) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "PRIORITY"},
		column{header: "KIND"},
		column{header: "STATE"},
		column{header: "PROJECT"},
		column{header: "ASSIGNEE"},
		titleColumn("TITLE"),
	)
	for _, t := range tasks {
		tbl.add(t.ID, t.Priority, t.Kind, t.State, t.Project, dash(t.Assignee), t.Title)
	}
	tbl.flush(w)
}

// TaskDetailRender prints one task with its edges, blocked status, and lease
// holder — worktree and agent sessions included when leased — the
// `lode task show` view. server is the API base URL, used to absolutize
// /blob/ references in the rendered body (MarkdownWithBase); pass "" when
// none is known.
func TaskDetailRender(w io.Writer, t model.TaskDetail, server string) {
	fmt.Fprintf(w, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(w, "  project:  %s\n", t.Project)
	fmt.Fprintf(w, "  priority: %s\n", t.Priority)
	fmt.Fprintf(w, "  kind:     %s\n", t.Kind)
	fmt.Fprintf(w, "  state:    %s\n", t.State)
	fmt.Fprintf(w, "  assignee: %s\n", dash(t.Assignee))
	if t.Hierarchy.Parent != nil {
		fmt.Fprintf(w, "  parent:   %s  %s (%s)\n",
			t.Hierarchy.Parent.ID, t.Hierarchy.Parent.Title, t.Hierarchy.Parent.State)
	}
	if t.Hierarchy.Progress.Total > 0 {
		fmt.Fprintf(w, "  progress: %d/%d children closed\n",
			t.Hierarchy.Progress.Closed, t.Hierarchy.Progress.Total)
	}
	if t.Concern != "" {
		fmt.Fprintf(w, "  concern:  %s\n", t.Concern)
	}
	if t.NeedsDecomposition {
		fmt.Fprintf(w, "  needs decomposition: yes\n")
	}
	if t.Blocked {
		fmt.Fprintf(w, "  blocked:  yes\n")
	}
	if t.Lease != nil {
		fmt.Fprintf(w, "  held by:  %s (expires %s)\n", t.Lease.ActorID, LocalTime(t.Lease.ExpiresAt))
		fmt.Fprintf(w, "  worktree: %s\n", t.Lease.Worktree)
	}
	if len(t.AgentSessions) > 0 {
		fmt.Fprintln(w, "\n  sessions:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "    AGENT\tSESSION\tSTARTED\tSTATUS\tTOKENS\tCOST")
		for _, sess := range t.AgentSessions {
			fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\t%s\t%s\n",
				sess.Agent, sess.SessionID, LocalTime(sess.StartedAt),
				sessionStatus(sess), sessionTokens(sess), sessionCost(sess))
		}
		tw.Flush()
	}
	if t.Body != "" {
		fmt.Fprintln(w)
		MarkdownWithBase(w, t.Body, server)
	}
	if len(t.Edges.Out) > 0 || len(t.Edges.In) > 0 {
		fmt.Fprintln(w, "\nedges:")
		for _, e := range t.Edges.Out {
			fmt.Fprintf(w, "  %s %s %s\n", t.ID, e.Type, e.To)
		}
		for _, e := range t.Edges.In {
			fmt.Fprintf(w, "  %s %s %s\n", e.From, e.Type, t.ID)
		}
	}
	if len(t.Blobs) > 0 {
		fmt.Fprintln(w, "\nattachments:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "  FILE\tTYPE\tSIZE\tWHERE\tURL")
		for _, b := range t.Blobs {
			where := "attached"
			if b.Embedded {
				where = "in body"
			}
			name := b.Filename
			if name == "" {
				name = b.Hash[:12]
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\n",
				name, b.MediaType, b.Size, where, b.URL)
		}
		tw.Flush()
	}
}

// BriefRender prints a task's brief as a readable summary — the `lode task
// brief` and `lode next` view.
func BriefRender(w io.Writer, b model.Brief) {
	fmt.Fprintf(w, "%s: %s\n", b.Task.ID, b.Task.Title)
	fmt.Fprintf(w, "state: %s   priority: %s\n", b.Task.State, b.Task.Priority)
	fmt.Fprintf(w, "branch: %s\n", b.Branch)
	if len(b.Task.Secrets) > 0 {
		fmt.Fprintf(w, "secrets: %s\n", strings.Join(b.Task.Secrets, ", "))
	}
	if b.Lease != nil {
		fmt.Fprintf(w, "lease: %s (expires %s)\n", b.Lease.Worktree, LocalTime(b.Lease.ExpiresAt))
	}
	BlockersRender(w, b.OpenBlockers, b.BlockingPlans)
	if b.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, b.Body)
	}
	// Warnings alone still print the section: a user who misspelled every pin
	// would otherwise see nothing at all, which is exactly the case the
	// warnings exist for.
	if len(b.Skills.Pinned) > 0 || len(b.Skills.Matches) > 0 || len(b.Skills.Warnings) > 0 {
		fmt.Fprintln(w, "\nSkills:")
		for _, p := range b.Skills.Pinned {
			fmt.Fprintf(w, "  pinned  %s — %s (content in brief)\n", p.Name, p.Description)
		}
		for _, m := range b.Skills.Matches {
			fmt.Fprintf(w, "  %.2f    %s — %s\n", m.Score, m.Name, m.Description)
		}
		for _, warn := range b.Skills.Warnings {
			fmt.Fprintf(w, "  warning: %s\n", warn)
		}
	}
}

// BlockersRender prints what is holding a task up, shared by `lode task
// brief`, `lode next` and `lode status`. Each section is omitted when empty.
func BlockersRender(w io.Writer, blockers []model.BriefBlocker, plans []model.DocRef) {
	if len(blockers) > 0 {
		fmt.Fprintln(w, "blocked by:")
		for _, blk := range blockers {
			fmt.Fprintf(w, "  - %s: %s (%s)\n", blk.ID, blk.Title, blk.State)
		}
	}
	if len(plans) > 0 {
		fmt.Fprintln(w, "blocked by plans:")
		for _, p := range plans {
			fmt.Fprintf(w, "  - %s: %s (%s)\n", p.Slug, p.Title, p.Status)
		}
	}
}

// PinnedSkillList prints a task's pinned skills, one per line, or a note when
// there are none — a bare blank line reads as a rendering bug, not "no pins".
func PinnedSkillList(w io.Writer, skills []string) {
	if len(skills) == 0 {
		fmt.Fprintln(w, "(no pinned skills)")
		return
	}
	fmt.Fprintln(w, strings.Join(skills, "\n"))
}

// TaskCostRender prints `lode task cost`: which task and scope, how many agent
// sessions billed usage, then the cost blocks CostRender renders. window is the
// human label for the requested period ("last 7 days", "all time").
func TaskCostRender(w io.Writer, tc model.TaskCost, window string) {
	if tc.IncludesChildren {
		fmt.Fprintf(w, "%s (including child tasks)\n", tc.Task)
	} else {
		fmt.Fprintf(w, "%s\n", tc.Task)
	}
	fmt.Fprintf(w, "sessions with recorded usage: %d\n", tc.Sessions)
	CostRender(w, tc.Cost, window)
}

// TreeRender prints each parent with its progress, then its children indented
// one level. Subtasks — the third tier the depth cap allows — are not
// expanded. The nodes come from the server as model.TaskTreeNode: the tree is
// one response, not a fetch per parent.
func TreeRender(w io.Writer, nodes []model.TaskTreeNode) {
	if len(nodes) == 0 {
		fmt.Fprintln(w, "no tasks with children")
		return
	}
	for i, n := range nodes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s  [%s]  %d/%d closed\n",
			n.Parent.ID, n.Parent.Title, n.Parent.State, n.Progress.Closed, n.Progress.Total)
		for _, c := range n.Children {
			fmt.Fprintf(w, "  %s  %s  (%s)\n", c.ID, c.Title, c.State)
		}
		if len(n.Children) == 0 {
			fmt.Fprintln(w, "  (no children)")
		}
	}
}

// InstructionTable prints one row per queued steering instruction: id, task,
// body, who queued it, when.
func InstructionTable(w io.Writer, ins []model.Instruction) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tTASK\tBODY\tCREATED_BY\tCREATED")
	for _, i := range ins {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", i.ID, i.Task, i.Body, dash(i.CreatedBy), LocalTime(i.CreatedAt))
	}
	tw.Flush()
}

// workerPickRowFmt lays out one `lode worker listen` row. Fixed widths for
// the same reason eventStreamRowFmt uses them: this is a stream, so there is
// no complete row set to measure and a tabwriter's columns would jitter as
// picks arrive.
const workerPickRowFmt = "%-12v  %-10v  %-12v  %-14v  %v\n"

// WorkerPickHeader prints the listen view's column header, once.
func WorkerPickHeader(w io.Writer) {
	fmt.Fprintf(w, workerPickRowFmt, "ID", "PRIORITY", "CONCERN", "PROJECT", "BRANCH")
}

// WorkerPickRow prints one dry-run claim-next pick — what `lode next` would
// take right now. Concern is dashed when the task carries none, matching how
// every other view renders an unset optional field.
func WorkerPickRow(w io.Writer, p model.ClaimNextPick) {
	fmt.Fprintf(w, workerPickRowFmt, p.ID, p.Priority, dash(p.Concern), p.Project, p.Branch)
}
