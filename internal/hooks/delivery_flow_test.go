package hooks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// deployBranchSHA is the commit on last-deploy/dev in push_last_deploy.json.
// Its main-sha: trailer names mainMergeSHA, and deployment_status_prod.json
// reports it as the deployed sha — so the prod watermark only lands if the
// deploy_shas mapping resolved it back to the main commit.
const deployBranchSHA = "5555555555555555555555555555555555555555"

// TestDeliveryEndToEnd walks one task through the whole delivery lifecycle
// across both webhook handlers, asserting the recorded facts and the task
// state after every delivery:
//
//	claim → branch push → PR opened → merge to main → last-deploy push →
//	dev deployment_status → prod deployment_status → prod Flux success
//
// The two deploy legs cover both halves of the dual-signal rule. Dev stays
// GitHub-only (bootstrap fallback, flux_seen never latches). Prod takes the
// GitHub signal first and advances on it alone, then the Flux signal arrives
// and latches flux_seen without changing the state — bootstrap-then-latch in
// the order GitHub-before-Flux.
func TestDeliveryEndToEnd(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fh := fluxHandlerFor(e, map[string]string{"prod-cluster": "prod"})
	e.setDoneState(t, demoRepo, "deployed_prod")

	taskID := e.seedTask(t) // WL-1
	e.claimTask(t, taskID)
	if st := e.taskState(t, taskID); st != "in_progress" {
		t.Fatalf("task state after claim = %q, want in_progress", st)
	}

	// 1. Branch push: commits are attributed to the task but nothing landed.
	deliverPushOK(t, e, "d-1", "push_branch.json")
	for _, sha := range []string{
		"1111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222",
	} {
		if src := e.taskCommitSource(t, taskID, demoRepo, sha); src != "branch_push" {
			t.Fatalf("task_commit source for %s = %q, want branch_push", sha, src)
		}
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM main_commits`); n != 0 {
		t.Fatalf("main_commits rows after branch push = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "in_progress" {
		t.Fatalf("task state after branch push = %q, want in_progress", st)
	}

	// 2. PR opened: review, no new facts.
	deliverOK(t, e, "pull_request", "d-2", "pull_request_opened.json")
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 2 {
		t.Fatalf("task_commit rows after PR opened = %d, want 2 (branch push only)", n)
	}
	if st := e.taskState(t, taskID); st != "in_review" {
		t.Fatalf("task state after PR opened = %q, want in_review", st)
	}

	// 3. Merge to main: the work lands, the lease closes.
	deliverPushOK(t, e, "d-3", "push_main_merge.json")
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM main_commits WHERE repo = $1`, demoRepo); n != 3 {
		t.Fatalf("main_commits rows after merge = %d, want 3", n)
	}
	if src := e.taskCommitSource(t, taskID, demoRepo, mainMergeSHA); src != "merge_message" {
		t.Fatalf("merge task_commit source = %q, want merge_message", src)
	}
	if _, err := e.st.ActiveLease(ctx, taskID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active lease err = %v, want ErrNotFound (lease closed on landing)", err)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state after merge = %q, want merged", st)
	}
	head := e.mainCommitID(t, mainMergeSHA)

	// 4. last-deploy/dev push: the deploy-branch sha maps to the main commit
	//    its main-sha: trailer names. No frontier moves yet.
	deliverPushOK(t, e, "d-4", "push_last_deploy.json")
	if got := e.rawQueryInt(t,
		`SELECT main_id FROM deploy_shas WHERE repo = $1 AND sha = $2`,
		demoRepo, deployBranchSHA); got != head {
		t.Fatalf("deploy_shas main_id = %d, want %d", got, head)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows after last-deploy push = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state after last-deploy push = %q, want merged", st)
	}

	// 5. Dev deployment_status: no Flux revision has ever correlated for
	//    dev, so the GitHub signal alone confirms the frontier.
	deliverOK(t, e, "deployment_status", "d-5", "deployment_status_success.json")
	gh, flux, seen, ok := e.envDeploy(t, "dev")
	if !ok || !gh.Valid || gh.Int64 != int64(head) || flux.Valid || seen {
		t.Fatalf("dev env_deploy = gh %v flux %v seen %v ok %v, want gh %d, no flux, not seen",
			gh, flux, seen, ok, head)
	}
	if st := e.taskState(t, taskID); st != "deployed_dev" {
		t.Fatalf("task state after dev deploy = %q, want deployed_dev", st)
	}

	// 6. Prod deployment_status, reported against the deploy-branch sha: it
	//    resolves through deploy_shas, and with no Flux signal yet for prod
	//    the bootstrap fallback advances the task immediately.
	deliverOK(t, e, "deployment_status", "d-6", "deployment_status_prod.json")
	gh, flux, seen, ok = e.envDeploy(t, "prod")
	if !ok || !gh.Valid || gh.Int64 != int64(head) || flux.Valid || seen {
		t.Fatalf("prod env_deploy = gh %v flux %v seen %v ok %v, want gh %d, no flux, not seen",
			gh, flux, seen, ok, head)
	}
	if st := e.taskState(t, taskID); st != "deployed_prod" {
		t.Fatalf("task state after prod deploy = %q, want deployed_prod", st)
	}

	// 7. Flux confirms the same prod revision: flux_seen latches, switching
	//    prod to dual-signal gating from here on. The state is unchanged.
	fluxDeliverOK(t, fh, fluxBody("ReconciliationSucceeded", "info", "prod-cluster", mainMergeSHA))
	gh, flux, seen, ok = e.envDeploy(t, "prod")
	if !ok || !seen || !flux.Valid || flux.Int64 != int64(head) ||
		!gh.Valid || gh.Int64 != int64(head) {
		t.Fatalf("prod env_deploy after flux = gh %v flux %v seen %v ok %v, want both %d and seen",
			gh, flux, seen, ok, head)
	}
	if st := e.taskState(t, taskID); st != "deployed_prod" {
		t.Fatalf("task state after prod flux = %q, want deployed_prod (unchanged)", st)
	}
	// Dev never saw Flux, so it is still on the bootstrap fallback.
	if _, _, devSeen, _ := e.envDeploy(t, "dev"); devSeen {
		t.Fatal("dev flux_seen = true, want false (no Flux revision correlated for dev)")
	}
}
