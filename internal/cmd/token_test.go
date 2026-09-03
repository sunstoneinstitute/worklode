package cmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestTokenAddTaskMintsTaskScopedToken covers `lode token add --task`, the
// verb `lode task token` folded into (WL-488, 061 §2). It must mint the same
// task-scoped token the old command did: a wl_ credential bound to the task,
// attributed to the named actor, with a TTL-derived expiry.
func TestTokenAddTaskMintsTaskScopedToken(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Needs a sandbox")
	if err := st.CreateActor(t.Context(), "sandbox2", "agent", "Sandbox 2", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	before := st.Now()
	out, err := runLode(t, "token", "add", "--task", task.ID, "--actor", "sandbox2", "--ttl", "2h", "--json")
	if err != nil {
		t.Fatalf("lode token add --task: %v\noutput: %s", err, out)
	}
	var resp model.TaskTokenResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if resp.Token == "" {
		t.Errorf("token = %q, want non-empty", resp.Token)
	}
	if resp.Actor != "sandbox2" {
		t.Errorf("actor = %q, want sandbox2", resp.Actor)
	}
	if resp.Task != task.ID {
		t.Errorf("task = %q, want %q", resp.Task, task.ID)
	}
	wantExpiry := before.Add(2 * time.Hour)
	if d := resp.ExpiresAt.Sub(wantExpiry); d < -time.Minute || d > time.Minute {
		t.Errorf("expires_at = %s, want close to %s", resp.ExpiresAt, wantExpiry)
	}
}

// TestTokenAddTaskDefaultsActorToSandbox matches the old `lode task token`'s
// documented default: an unset --actor mints as the auto-provisioned
// "sandbox" actor.
func TestTokenAddTaskDefaultsActorToSandbox(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Needs the default sandbox")

	out, err := runLode(t, "token", "add", "--task", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode token add --task: %v\noutput: %s", err, out)
	}
	var resp model.TaskTokenResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if resp.Actor != "sandbox" {
		t.Errorf("actor = %q, want sandbox", resp.Actor)
	}
}

// TestTokenAddRequiresActorWithoutTask keeps the pre-rename contract: an
// actor-scoped mint (no --task) still requires --actor.
func TestTokenAddRequiresActorWithoutTask(t *testing.T) {
	lifecycleTestServer(t)

	_, err := runLode(t, "token", "add")
	if err == nil {
		t.Fatalf("lode token add without --actor or --task: want error, got none")
	}
}
