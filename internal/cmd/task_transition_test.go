package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// walkTaskStates moves a task through the state machine out of band, so a case
// can start from the state its verb requires without leaning on the commands
// under test. path lists the states to walk in order, starting with the one
// the task is already in.
func walkTaskStates(t *testing.T, st *store.Store, id string, path ...string) {
	t.Helper()
	for i := 0; i+1 < len(path); i++ {
		from, to := path[i], path[i+1]
		_, _, err := st.RecordEvent(context.Background(), "system",
			fmt.Sprintf("walk-%s-%d-%s", id, i, to), "task."+to, nil,
			func(tx *sql.Tx, eventID int64) error {
				return store.Transition(tx, st.Now(), id, from, to, eventID)
			})
		if err != nil {
			t.Fatalf("move %s %s -> %s: %v", id, from, to, err)
		}
	}
}

// createTaskInState creates a task and walks it to the state a case needs.
// path[0] is the state the task is created in — "draft" asks the server for a
// draft, anything else expects the default "ready".
func createTaskInState(t *testing.T, st *store.Store, c *cli.Client, title string, path ...string) model.Task {
	t.Helper()
	task, _, err := c.CreateTask(context.Background(), model.CreateTaskInput{
		Project:  "proj",
		Title:    title,
		Priority: "high",
		Kind:     "feature",
		Draft:    path[0] == "draft",
	})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	if task.State != path[0] {
		t.Fatalf("created %s in state %q, want %q", task.ID, task.State, path[0])
	}
	walkTaskStates(t, st, task.ID, path...)
	return task
}

// taskState reads a task's stored state back through `lode task show --json`,
// so the assertion goes over the same wire the command under test used.
func taskState(t *testing.T, id string) string {
	t.Helper()
	out, err := runLode(t, "task", "show", "--json", id)
	if err != nil {
		t.Fatalf("lode task show %s: %v\noutput: %s", id, err, out)
	}
	var task model.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return task.State
}

// taskBlocked reports a task's derived blocked flag from `lode task show`.
func taskBlocked(t *testing.T, id string) bool {
	t.Helper()
	out, err := runLode(t, "task", "show", "--json", id)
	if err != nil {
		t.Fatalf("lode task show %s: %v\noutput: %s", id, err, out)
	}
	var detail model.TaskDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return detail.Blocked
}

// taskRowState finds id's row in a cli.TaskTable rendering and returns its
// STATE column (ID PRIORITY KIND STATE PROJECT ASSIGNEE TITLE).
func taskRowState(t *testing.T, out, id string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == id {
			return fields[3]
		}
	}
	t.Fatalf("no table row for %s in:\n%s", id, out)
	return ""
}

// TestTaskTransitionCommands drives every `lode task <verb> <id>` state move
// through runLode, plain and --json, and asserts both what the command printed
// and the state the server ended up in.
func TestTaskTransitionCommands(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)

	cases := []struct {
		verb string
		from []string // states the task walks through before the verb runs
		want string
	}{
		{"publish", []string{"draft"}, "ready"},
		{"reopen", []string{"ready", "in_progress", "in_review", "merged"}, "ready"},
		{"rework", []string{"ready", "in_progress", "in_review"}, "in_progress"},
		{"done", []string{"ready", "in_progress", "in_review"}, "merged"},
		{"abandon", []string{"ready", "in_progress"}, "abandoned"},
	}
	for _, tc := range cases {
		t.Run(tc.verb+"/table", func(t *testing.T) {
			task := createTaskInState(t, st, c, "fixture", tc.from...)
			out, err := runLode(t, "task", tc.verb, task.ID)
			if err != nil {
				t.Fatalf("lode task %s: %v\noutput: %s", tc.verb, err, out)
			}
			if got := taskRowState(t, out, task.ID); got != tc.want {
				t.Errorf("printed state = %q, want %q\noutput: %s", got, tc.want, out)
			}
			if got := taskState(t, task.ID); got != tc.want {
				t.Errorf("stored state = %q, want %q", got, tc.want)
			}
		})

		t.Run(tc.verb+"/json", func(t *testing.T) {
			task := createTaskInState(t, st, c, "fixture", tc.from...)
			out, err := runLode(t, "task", tc.verb, "--json", task.ID)
			if err != nil {
				t.Fatalf("lode task %s --json: %v\noutput: %s", tc.verb, err, out)
			}
			var got model.Task
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("decode %q: %v", out, err)
			}
			if got.ID != task.ID || got.State != tc.want {
				t.Errorf("--json returned %s in state %q, want %s in %q",
					got.ID, got.State, task.ID, tc.want)
			}
			if stored := taskState(t, task.ID); stored != tc.want {
				t.Errorf("stored state = %q, want %q", stored, tc.want)
			}
		})
	}
}

// TestTaskTransitionRefusesIllegalMove pins that the server's 422 reaches the
// caller as an error and leaves the task where it was: `publish` publishes a
// draft, and has nothing to do with a task already published.
func TestTaskTransitionRefusesIllegalMove(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTaskInState(t, st, c, "already published", "ready")

	if out, err := runLode(t, "task", "publish", task.ID); err == nil {
		t.Fatalf("lode task publish on a ready task: want error, got nil\noutput: %s", out)
	}
	if got := taskState(t, task.ID); got != "ready" {
		t.Errorf("state after refused transition = %q, want %q", got, "ready")
	}
}

// TestTaskEdgeCommands drives `lode task block` and `lode task unblock`
// through runLode, asserting the confirmation line, the JSON body, and the
// blocked flag the edge produces.
func TestTaskEdgeCommands(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	t.Run("table", func(t *testing.T) {
		subject := createTestTask(t, c, "blocked task")
		blocker := createTestTask(t, c, "blocking task")

		out, err := runLode(t, "task", "block", subject.ID, "--by", blocker.ID)
		if err != nil {
			t.Fatalf("lode task block: %v\noutput: %s", err, out)
		}
		if want := fmt.Sprintf("%s is now blocked by %s\n", subject.ID, blocker.ID); out != want {
			t.Errorf("block printed %q, want %q", out, want)
		}
		if !taskBlocked(t, subject.ID) {
			t.Errorf("%s not blocked after `lode task block`", subject.ID)
		}

		out, err = runLode(t, "task", "unblock", subject.ID, "--by", blocker.ID)
		if err != nil {
			t.Fatalf("lode task unblock: %v\noutput: %s", err, out)
		}
		if want := fmt.Sprintf("%s is no longer blocked by %s\n", subject.ID, blocker.ID); out != want {
			t.Errorf("unblock printed %q, want %q", out, want)
		}
		if taskBlocked(t, subject.ID) {
			t.Errorf("%s still blocked after `lode task unblock`", subject.ID)
		}
	})

	t.Run("json", func(t *testing.T) {
		subject := createTestTask(t, c, "blocked task json")
		blocker := createTestTask(t, c, "blocking task json")

		out, err := runLode(t, "task", "block", "--json", subject.ID, "--by", blocker.ID)
		if err != nil {
			t.Fatalf("lode task block --json: %v\noutput: %s", err, out)
		}
		var edge model.Edge
		if err := json.Unmarshal([]byte(out), &edge); err != nil {
			t.Fatalf("decode %q: %v", out, err)
		}
		want := model.Edge{From: blocker.ID, To: subject.ID, Type: "blocks"}
		if edge != want {
			t.Errorf("block --json returned %+v, want %+v", edge, want)
		}
		if !taskBlocked(t, subject.ID) {
			t.Errorf("%s not blocked after `lode task block --json`", subject.ID)
		}

		// The delete answers 204, so there is no body to print: an empty
		// stdout is the contract here, not a missing result.
		out, err = runLode(t, "task", "unblock", "--json", subject.ID, "--by", blocker.ID)
		if err != nil {
			t.Fatalf("lode task unblock --json: %v\noutput: %s", err, out)
		}
		if out != "" {
			t.Errorf("unblock --json printed %q, want no output", out)
		}
		if taskBlocked(t, subject.ID) {
			t.Errorf("%s still blocked after `lode task unblock --json`", subject.ID)
		}
	})
}
