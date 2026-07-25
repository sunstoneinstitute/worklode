package hooks_test

import (
	"net/http"
	"testing"
)

// TestDeploymentStatusAdvancesTask: a successful GitHub deployment of a main
// commit the task's work is part of moves the task from merged to
// deployed_dev and records the gh watermark for the environment.
func TestDeploymentStatusAdvancesTask(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t) // WL-1
	e.claimTask(t, taskID)

	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state before deploy = %q, want merged", st)
	}

	deliverOK(t, e, "deployment_status", "d-3", "deployment_status_success.json")

	if st := e.taskState(t, taskID); st != "deployed_dev" {
		t.Fatalf("task state = %q, want deployed_dev", st)
	}
	mainID := e.mainCommitID(t, "3333333333333333333333333333333333333333")
	got := e.rawQueryInt(t,
		`SELECT gh_main_id FROM env_deploys WHERE repo = $1 AND environment = 'dev'`, demoRepo)
	if got != mainID {
		t.Fatalf("env_deploys gh_main_id = %d, want %d", got, mainID)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM env_deploys WHERE flux_seen`); n != 0 {
		t.Fatalf("flux_seen rows = %d, want 0 (a GitHub deploy is not a Flux signal)", n)
	}
}

// TestDeploymentStatusUnknownSHAIgnored: a deploy of a sha that never
// appeared on main has nothing to anchor to. The fact is dropped (v1) and
// the delivery must still succeed.
func TestDeploymentStatusUnknownSHAIgnored(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	deliverOK(t, e, "deployment_status", "d-1", "deployment_status_success.json")

	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "in_progress" {
		t.Fatalf("task state = %q, want in_progress", st)
	}
}

// TestDeploymentStatusFailureIgnored: only successful deployments move the
// watermark.
func TestDeploymentStatusFailureIgnored(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")

	body := []byte(`{
		"action": "created",
		"deployment_status": {"state": "failure"},
		"deployment": {"environment": "dev", "sha": "3333333333333333333333333333333333333333"},
		"repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"}
	}`)
	rr := deliverBody(t, e.h, "deployment_status", "d-3", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
}

// TestDeploymentStatusIgnoredEnvironment: environments outside the delivery
// lifecycle (github-pages, copilot, ...) normalize to "" and are skipped.
func TestDeploymentStatusIgnoredEnvironment(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")

	body := []byte(`{
		"action": "created",
		"deployment_status": {"state": "success"},
		"deployment": {"environment": "github-pages", "sha": "3333333333333333333333333333333333333333"},
		"repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"}
	}`)
	rr := deliverBody(t, e.h, "deployment_status", "d-3", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
}

// TestDeploymentStatusProdAdvancesTask: a prod deploy takes a task all the
// way to deployed_prod, even though it never passed through deployed_dev.
func TestDeploymentStatusProdAdvancesTask(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")

	body := []byte(`{
		"action": "created",
		"deployment_status": {"state": "success"},
		"deployment": {"environment": "production", "sha": "3333333333333333333333333333333333333333"},
		"repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"}
	}`)
	rr := deliverBody(t, e.h, "deployment_status", "d-3", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if st := e.taskState(t, taskID); st != "deployed_prod" {
		t.Fatalf("task state = %q, want deployed_prod", st)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM env_deploys WHERE repo = $1 AND environment = 'prod'`,
		demoRepo); n != 1 {
		t.Fatalf("prod env_deploys rows = %d, want 1", n)
	}
}
