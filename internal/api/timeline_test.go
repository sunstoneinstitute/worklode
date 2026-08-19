package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedEvent runs apply inside a recorded test event.
func seedEvent(t *testing.T, st *store.Store, extID string, apply func(tx *sql.Tx, eventID int64) error) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github", extID, "test.seed", nil, apply)
	if err != nil {
		t.Fatalf("seed event %s: %v", extID, err)
	}
}

func TestTaskTimeline(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Add feature", "priority": "high", "kind": "feature",
	})

	// Claim: ready -> in_progress, writes the state_log entry.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	base := time.Now().UTC().Truncate(time.Second)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	const (
		repo     = "org/app"
		headSHA  = "headsha1"
		mergeSHA = "mergesha1"
	)

	// PR opened on the correlated branch.
	seedEvent(t, st, "pr-open", func(tx *sql.Tx, _ int64) error {
		_, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "open",
			HeadRef: "WL-1-add-feature", HeadSHA: headSHA,
			URL: "https://github.com/org/app/pull/7", OpenedAt: at(1),
		}, "")
		return err
	})
	// CI run on the PR's head sha, plus a decoy on an unrelated sha.
	seedEvent(t, st, "ci", func(tx *sql.Tx, _ int64) error {
		if err := store.UpsertCIRun(tx, store.CIRun{
			Repo: repo, HeadSHA: headSHA, Workflow: "ci", Status: "completed",
			URL: "https://ci/1", StartedAt: at(2),
		}); err != nil {
			return err
		}
		return store.UpsertCIRun(tx, store.CIRun{
			Repo: repo, HeadSHA: "othersha", Workflow: "ci", Status: "completed",
			URL: "https://ci/2", StartedAt: at(2),
		})
	})
	// Review on the PR, plus a decoy on another PR number.
	seedEvent(t, st, "review", func(tx *sql.Tx, _ int64) error {
		if err := store.UpsertReview(tx, store.Review{
			Repo: repo, PRNumber: 7, Reviewer: "bob", State: "approved", SubmittedAt: at(3),
		}); err != nil {
			return err
		}
		return store.UpsertReview(tx, store.Review{
			Repo: repo, PRNumber: 8, Reviewer: "bob", State: "approved", SubmittedAt: at(3),
		})
	})
	// PR merged with a merge sha.
	seedEvent(t, st, "pr-merge", func(tx *sql.Tx, _ int64) error {
		merged := at(4)
		ms := mergeSHA
		_, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "merged",
			HeadRef: "WL-1-add-feature", HeadSHA: headSHA, MergeSHA: &ms,
			URL: "https://github.com/org/app/pull/7", OpenedAt: at(1), MergedAt: &merged,
		}, "")
		return err
	})
	// Artifact built from the merge sha; a decoy built from the head sha must
	// not be linked (artifacts correlate via merge_sha only).
	var artifactID, decoyID int64
	seedEvent(t, st, "artifact", func(tx *sql.Tx, _ int64) error {
		var err error
		artifactID, err = store.CreateArtifact(tx, store.Artifact{
			Kind: "docker_image", Name: "reg/app", Version: "1.2.3",
			Repo: repo, SourceSHA: mergeSHA, BuiltAt: at(5),
		})
		if err != nil {
			return err
		}
		decoyID, err = store.CreateArtifact(tx, store.Artifact{
			Kind: "docker_image", Name: "reg/app", Version: "0.0.1-dev",
			Repo: repo, SourceSHA: headSHA, BuiltAt: at(5),
		})
		return err
	})
	// Deployment and runtime event on the merge-sha artifact; decoys on the
	// decoy artifact must not appear.
	seedEvent(t, st, "deploy", func(tx *sql.Tx, _ int64) error {
		if err := store.UpsertDeployment(tx, at(6), store.Deployment{
			ArtifactID: &artifactID, Environment: "prod",
			TargetKind: "flux_kustomization", TargetName: "app", Status: "deployed",
		}); err != nil {
			return err
		}
		return store.UpsertDeployment(tx, at(6), store.Deployment{
			ArtifactID: &decoyID, Environment: "dev",
			TargetKind: "flux_kustomization", TargetName: "app-dev", Status: "deployed",
		})
	})
	seedEvent(t, st, "runtime", func(tx *sql.Tx, _ int64) error {
		if _, err := store.InsertRuntimeEvent(tx, store.RuntimeEvent{
			Cluster: "prod-1", Kind: "crashloop", Workload: "app",
			ArtifactID: &artifactID, Message: "CrashLoopBackOff", OccurredAt: at(7),
		}); err != nil {
			return err
		}
		_, err := store.InsertRuntimeEvent(tx, store.RuntimeEvent{
			Cluster: "dev-1", Kind: "oom", Workload: "app-dev",
			ArtifactID: &decoyID, Message: "OOMKilled", OccurredAt: at(7),
		})
		return err
	})

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1/timeline", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)

	task, ok := got["task"].(map[string]any)
	if !ok || task["id"] != "WL-1" {
		t.Fatalf("task = %v, want WL-1", got["task"])
	}
	timeline, ok := got["timeline"].([]any)
	if !ok {
		t.Fatalf("timeline not an array: %s", rr.Body.String())
	}

	var types []string
	var prev time.Time
	for i, raw := range timeline {
		e := raw.(map[string]any)
		typ, _ := e["type"].(string)
		types = append(types, typ)
		ts := entryAt(t, i, e)
		if ts.Before(prev) {
			t.Fatalf("timeline not ascending at entry %d: %v < %v", i, ts, prev)
		}
		prev = ts
	}
	// Two "state" entries: CreateTask's own state_log row (see store.CreateTask),
	// then the claim's ready -> in_progress transition.
	want := []string{"state", "state", "pr", "ci", "review", "artifact", "deployment", "runtime"}
	if len(types) != len(want) {
		t.Fatalf("timeline types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("timeline types = %v, want %v", types, want)
		}
	}

	// byType keeps the last entry seen per type, so "state" ends up holding
	// the claim's transition (later in the ascending timeline), not the
	// create row.
	byType := map[string]map[string]any{}
	for _, raw := range timeline {
		e := raw.(map[string]any)
		byType[e["type"].(string)] = e
	}

	state := byType["state"]
	change, ok := state["change"].(map[string]any)
	if !ok || change["new"] != "in_progress" {
		t.Fatalf("state entry = %v, want change.new=in_progress", state)
	}
	if _, ok := state["event_id"].(float64); !ok {
		t.Fatalf("state entry missing event_id: %v", state)
	}

	pr := byType["pr"]
	if pr["repo"] != repo || pr["number"] != float64(7) || pr["state"] != "merged" ||
		pr["title"] != "Add feature" || pr["url"] != "https://github.com/org/app/pull/7" {
		t.Fatalf("pr entry = %v", pr)
	}
	if s, _ := pr["merged_at"].(string); s == "" {
		t.Fatalf("pr entry missing merged_at: %v", pr)
	}

	ci := byType["ci"]
	if ci["workflow"] != "ci" || ci["status"] != "completed" {
		t.Fatalf("ci entry = %v", ci)
	}
	review := byType["review"]
	if review["reviewer"] != "bob" || review["state"] != "approved" {
		t.Fatalf("review entry = %v", review)
	}
	artifact := byType["artifact"]
	if artifact["kind"] != "docker_image" || artifact["name"] != "reg/app" || artifact["version"] != "1.2.3" {
		t.Fatalf("artifact entry = %v", artifact)
	}
	deployment := byType["deployment"]
	if deployment["environment"] != "prod" || deployment["target_name"] != "app" || deployment["status"] != "deployed" {
		t.Fatalf("deployment entry = %v", deployment)
	}
	runtime := byType["runtime"]
	if runtime["kind"] != "crashloop" || runtime["cluster"] != "prod-1" ||
		runtime["workload"] != "app" || runtime["message"] != "CrashLoopBackOff" {
		t.Fatalf("runtime entry = %v", runtime)
	}
}

// TestTimelineDeliveryEntries covers the delivery facts a task picks up once
// its work lands: one "landed" entry per repo (not one per commit),
// "deployed" entries only for environments whose confirmed frontier covers
// the landed commit, and the earliest release covering it.
func TestTimelineDeliveryEntries(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ship it", "priority": "high", "kind": "feature",
	})

	base := time.Now().UTC().Truncate(time.Second)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	const (
		repoA = "org/app"
		repoB = "org/lib"
	)

	seedEvent(t, st, "delivery", func(tx *sql.Tx, _ int64) error {
		// repoA: the task landed a1 and a2; a3 is unrelated later work.
		a1, err := store.AppendMainCommit(tx, repoA, "a1", at(1))
		if err != nil {
			return err
		}
		a2, err := store.AppendMainCommit(tx, repoA, "a2", at(2))
		if err != nil {
			return err
		}
		a3, err := store.AppendMainCommit(tx, repoA, "a3", at(3))
		if err != nil {
			return err
		}
		for _, sha := range []string{"a1", "a2"} {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: "WL-1", Repo: repoA, SHA: sha, Source: "pr", SeenAt: at(1),
			}); err != nil {
				return err
			}
		}
		// dev is past the landed commit; prod is still behind it.
		if err := store.BumpEnvDeployGH(tx, at(5), repoA, "dev", a3); err != nil {
			return err
		}
		if err := store.BumpEnvDeployGH(tx, at(6), repoA, "prod", a1); err != nil {
			return err
		}
		// Three releases: one below the landed commit, two covering it. The
		// earliest covering one is the release that shipped the work.
		if err := store.SetReleaseFrontier(tx, repoA, "v0.9.0", a1, at(7)); err != nil {
			return err
		}
		if err := store.SetReleaseFrontier(tx, repoA, "v1.0.0", a2, at(8)); err != nil {
			return err
		}
		if err := store.SetReleaseFrontier(tx, repoA, "v2.0.0", a3, at(9)); err != nil {
			return err
		}

		// repoB: the task also landed b1. dev's GitHub watermark is past it but
		// Flux has only confirmed b0, so min(gh, flux) does not cover it.
		b0, err := store.AppendMainCommit(tx, repoB, "b0", at(1))
		if err != nil {
			return err
		}
		if _, err := store.AppendMainCommit(tx, repoB, "b1", at(2)); err != nil {
			return err
		}
		b2, err := store.AppendMainCommit(tx, repoB, "b2", at(3))
		if err != nil {
			return err
		}
		if err := store.InsertTaskCommit(tx, store.TaskCommit{
			TaskID: "WL-1", Repo: repoB, SHA: "b1", Source: "pr", SeenAt: at(1),
		}); err != nil {
			return err
		}
		if err := store.BumpEnvDeployGH(tx, at(5), repoB, "dev", b2); err != nil {
			return err
		}
		_, err = store.BumpEnvDeployFlux(tx, at(5), repoB, "dev", b0)
		return err
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/timeline", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	timeline, ok := got["timeline"].([]any)
	if !ok {
		t.Fatalf("timeline not an array: %s", rr.Body.String())
	}

	var entries []map[string]any
	var prev time.Time
	for i, raw := range timeline {
		e := raw.(map[string]any)
		ts := entryAt(t, i, e)
		if ts.Before(prev) {
			t.Fatalf("timeline not ascending at entry %d: %v < %v", i, ts, prev)
		}
		prev = ts
		entries = append(entries, e)
	}
	// CreateTask's own state_log row, plus exactly the four covered delivery
	// facts: no prod deploy (frontier behind), no repoB deploy (Flux behind),
	// no second landed entry for repoA's two commits, no extra release.
	if len(entries) != 5 {
		t.Fatalf("timeline = %v, want 1 state entry + 4 delivery entries", entries)
	}

	byType := map[string][]map[string]any{}
	for _, e := range entries {
		typ, _ := e["type"].(string)
		byType[typ] = append(byType[typ], e)
	}

	landed := byType["landed"]
	if len(landed) != 2 {
		t.Fatalf("landed entries = %v, want one per repo", landed)
	}
	landedByRepo := map[string]map[string]any{}
	for _, e := range landed {
		landedByRepo[e["repo"].(string)] = e
	}
	if e := landedByRepo[repoA]; e == nil || e["sha"] != "a2" {
		t.Fatalf("repoA landed entry = %v, want newest sha a2", e)
	} else if ts := entryAt(t, 0, e); !ts.Equal(at(2)) {
		t.Fatalf("repoA landed at = %v, want %v", ts, at(2))
	}
	if e := landedByRepo[repoB]; e == nil || e["sha"] != "b1" {
		t.Fatalf("repoB landed entry = %v, want sha b1", e)
	}

	deployed := byType["deployed"]
	if len(deployed) != 1 || deployed[0]["repo"] != repoA || deployed[0]["environment"] != "dev" {
		t.Fatalf("deployed entries = %v, want only %s dev", deployed, repoA)
	}
	if ts := entryAt(t, 0, deployed[0]); !ts.Equal(at(5)) {
		t.Fatalf("deployed at = %v, want %v", ts, at(5))
	}

	released := byType["released"]
	if len(released) != 1 || released[0]["repo"] != repoA || released[0]["tag"] != "v1.0.0" {
		t.Fatalf("released entries = %v, want only %s v1.0.0", released, repoA)
	}
	if ts := entryAt(t, 0, released[0]); !ts.Equal(at(8)) {
		t.Fatalf("released at = %v, want %v", ts, at(8))
	}
}

// entryAt parses a timeline entry's "at" field.
func entryAt(t *testing.T, i int, e map[string]any) time.Time {
	t.Helper()
	atStr, _ := e["at"].(string)
	ts, err := time.Parse(time.RFC3339, atStr)
	if err != nil {
		t.Fatalf("entry %d: bad at %q: %v", i, atStr, err)
	}
	return ts
}

func TestTaskTimelineNewTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Nothing yet", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/timeline", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	// A brand-new task's only entry is CreateTask's own state_log row (see
	// store.CreateTask); this also pins the "must be a JSON array, not null"
	// contract for a task with no other history.
	tl, ok := got["timeline"].([]any)
	if !ok || len(tl) != 1 {
		t.Fatalf("timeline = %v, want exactly one state entry", got["timeline"])
	}
	entry := tl[0].(map[string]any)
	if entry["type"] != "state" {
		t.Fatalf("timeline entry = %v, want type state", entry)
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-99/timeline", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task timeline status = %d, want 404", rr.Code)
	}
}
